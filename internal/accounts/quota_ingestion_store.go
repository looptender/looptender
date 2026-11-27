package accounts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// No implementation exists outside fixtures. A future backend must serialize
// with accounts.lock, bound snapshot reads, flush the replacement and establish
// the supported filesystem's durability barrier. Injection is not certification.
type ingestionBackend interface {
	WithLock(func() error) error
	ReadBounded(int) ([]byte, error)
	Replace([]byte) error
	Confirm() error
	Durable() bool
}
type quotaIngestionStore struct {
	mu      sync.Mutex
	backend ingestionBackend
	session string
	begun   map[string]*ingestionOperation
}

// Ticket association is process-local, not reconstructed from a disk/session ID.
// These tickets authorize only synthetic store transitions, never acquisition.
type ingestionTicket struct {
	owner                             *quotaIngestionStore
	storeID, intentID, epoch, session string
	sequence                          uint64
}
type ingestionInput struct {
	Kind             string            `json:"kind"`
	OperationID      string            `json:"operationId"`
	StoreID          string            `json:"storeId"`
	ExpectedRevision uint64            `json:"expectedRevision"`
	At               time.Time         `json:"at"`
	SessionID        string            `json:"sessionId"`
	IntentID         string            `json:"intentId"`
	Epoch            string            `json:"epoch"`
	Reason           string            `json:"reason"`
	Initial          *ingestionFile    `json:"initial"`
	Receipt          *ingestionReceipt `json:"receipt"`
	Fences           []ingestionFence  `json:"fences"`
}
type ingestionOperation struct {
	owner  *quotaIngestionStore
	input  []byte
	digest string
	ticket *ingestionTicket
}
type ingestionAck struct {
	OperationID                      string
	AppliedRevision, CurrentRevision uint64
	Historical, CurrentEpoch, Pruned bool
	Ticket                           *ingestionTicket
	ReceiptID                        string
}

func (s *quotaIngestionStore) prepare(in ingestionInput, ticket *ingestionTicket) *ingestionOperation {
	// Marshal now owns all nested pointers/bytes; later caller mutation cannot
	// alter either the request identity or a retry's original publication tuple.
	in.SessionID = s.session
	return &ingestionOperation{owner: s, input: ingestionJSON(in), digest: ingestionHash("quota-ingestion/input/v1", ingestionJSON(in)), ticket: ticket}
}
func (s *quotaIngestionStore) withLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend == nil || !s.backend.Durable() {
		return ingestionError("unsupportedDurability")
	}
	var entered bool
	var own error
	err := s.backend.WithLock(func() error { entered = true; own = fn(); return own })
	if own != nil {
		return own
	}
	if err != nil {
		if entered {
			return ingestionError("mutationUnknown")
		}
		return ingestionError("storageUnavailable")
	}
	return nil
}
func (s *quotaIngestionStore) read() (ingestionFile, error) {
	b, err := s.backend.ReadBounded(ingestionMaxBytes + 1)
	if errors.Is(err, os.ErrNotExist) {
		return ingestionFile{}, errIngestionUninitialized
	}
	if err != nil {
		return ingestionFile{}, ingestionError("storageUnavailable")
	}
	return decodeIngestion(b)
}
func (s *quotaIngestionStore) load() (out ingestionFile, err error) {
	err = s.withLock(func() error {
		f, e := s.read()
		if e != nil {
			return e
		}
		if s.backend.Confirm() != nil {
			return ingestionError("mutationUnknown")
		}
		out = f
		return nil
	})
	if err != nil {
		out = ingestionFile{}
	}
	return
}
func ingestionStatus(f ingestionFile, m ingestionMutation) ingestionAck {
	a := ingestionAck{OperationID: m.OperationID, AppliedRevision: m.AppliedRevision, CurrentRevision: f.Revision, Historical: true}
	if m.IntentID != nil {
		q := ingestionFindRequest(&f, *m.IntentID)
		if q != nil {
			_, a.CurrentEpoch = ingestionCurrent(f, q.Epoch)
		}
	}
	if m.ReceiptID != nil {
		a.ReceiptID = *m.ReceiptID
		for _, v := range f.Seen {
			if v.ID == a.ReceiptID {
				a.Pruned = v.Retired
			}
		}
	}
	return a
}
func (s *quotaIngestionStore) status(storeID, opID, digest string) (ack ingestionAck, err error) {
	err = s.withLock(func() error {
		f, e := s.read()
		if e != nil {
			return e
		}
		if f.StoreID != storeID {
			return ingestionError("storeConflict")
		}
		for _, m := range f.Mutations {
			if m.OperationID == opID {
				if m.InputSHA256 != digest {
					return ingestionError("duplicateConflict")
				}
				if s.backend.Confirm() != nil {
					return ingestionError("mutationUnknown")
				}
				ack = ingestionStatus(f, m)
				return nil
			}
		}
		return ingestionError("operationUnknown")
	})
	if err != nil {
		ack = ingestionAck{}
	}
	return
}
func (s *quotaIngestionStore) apply(op *ingestionOperation) (ack ingestionAck, err error) {
	if op == nil || op.owner != s || ingestionHash("quota-ingestion/input/v1", op.input) != op.digest || ingestionPreflight(op.input) != nil {
		return ack, ingestionError("associationUnavailable")
	}
	var in ingestionInput
	d := json.NewDecoder(bytesReaderIngestion(op.input))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || !ingestionID(in.OperationID) || !ingestionID(in.StoreID) || in.SessionID != s.session || !ingestionID(s.session) || !ingestionTime(in.At) || !ingestionKind(in.Kind) {
		return ack, errIngestionInvalid
	}
	err = s.withLock(func() error {
		f, e := s.read()
		absent := errors.Is(e, errIngestionUninitialized)
		if in.Kind == "initialize" && absent {
			if in.Initial == nil || in.ExpectedRevision != 0 || in.Initial.Revision != 0 || len(in.Initial.Mutations) != 0 || in.StoreID != in.Initial.StoreID {
				return errIngestionInvalid
			}
			f = cloneIngestion(*in.Initial)
		} else if e != nil {
			return e
		}

		if f.StoreID != in.StoreID {
			return ingestionError("storeConflict")
		}
		// Exact applied retries are historical, never a new mutation/CAS rebase.
		for _, m := range f.Mutations {
			if m.OperationID == in.OperationID {
				if m.InputSHA256 != op.digest {
					return ingestionError("duplicateConflict")
				}
				if s.backend.Confirm() != nil {
					return ingestionError("mutationUnknown")
				}
				ack = ingestionStatus(f, m)
				if in.Kind == "begin" && s.begun[in.OperationID] == op && f.Revision == m.AppliedRevision && m.IntentID != nil {
					q := ingestionFindRequest(&f, *m.IntentID)
					if q != nil && q.Outcome == "pending" && q.SessionID == s.session && q.Epoch == in.Epoch && ack.CurrentEpoch {
						ack.Ticket = &ingestionTicket{s, f.StoreID, q.IntentID, q.Epoch, q.SessionID, q.Sequence}
					}
				}
				return nil
			}
		}
		if f.Revision != in.ExpectedRevision {
			return ingestionError("storeConflict")
		}
		if ingestionOverflow(f.Revision) || in.At.Before(f.WallHighWater) {
			return ingestionError("clockOrRevisionUnavailable")
		}
		m := ingestionMutation{OperationID: in.OperationID, Kind: in.Kind, InputSHA256: op.digest, AppliedRevision: f.Revision + 1}
		switch in.Kind {
		case "initialize":
			if !absent {
				return ingestionError("alreadyInitialized")
			}
			if len(f.Requests) != 0 || len(f.Receipts) != 0 || len(f.Fences) != 0 || len(f.Seen) != 0 {
				return errIngestionInvalid
			}
		case "begin":
			ep, ok := ingestionCurrent(f, in.Epoch)
			if !ok {
				return ingestionError("epochChanged")
			}
			if !ingestionID(in.IntentID) || ingestionFindRequest(&f, in.IntentID) != nil || ingestionOverflow(f.NextSequence) {
				return errIngestionInvalid
			}
			for _, q := range f.Requests {
				if q.Outcome == "pending" {
					for _, e := range f.Epochs {
						if e.ID == q.Epoch && e.Canonical == ep.Canonical {
							return ingestionError("pendingOwnerUnknown")
						}
					}
				}
			}
			q := ingestionRequest{Sequence: f.NextSequence, SessionID: s.session, IntentID: in.IntentID, Epoch: in.Epoch, CreatedAt: in.At, Outcome: "pending"}
			f.NextSequence++
			f.Requests = append(f.Requests, q)
			m.IntentID = ingestionPtr(q.IntentID)
		case "commit", "fail":
			t := op.ticket
			if t == nil || t.owner != s || t.storeID != f.StoreID || t.session != s.session {
				return ingestionError("associationUnavailable")
			}
			q := ingestionFindRequest(&f, t.intentID)
			if q == nil || q.Outcome != "pending" || q.Sequence != t.sequence || q.SessionID != t.session || q.Epoch != t.epoch {
				return ingestionError("intentChanged")
			}
			if _, ok := ingestionCurrent(f, q.Epoch); !ok {
				return ingestionError("epochChanged")
			}
			m.IntentID = ingestionPtr(q.IntentID)
			if in.Kind == "fail" {
				if !ingestionReason(in.Reason) {
					return errIngestionInvalid
				}
				q.Outcome = "failed"
				q.Reason = in.Reason
			} else {
				r := in.Receipt
				if r == nil || r.Sequence != q.Sequence || r.Epoch != q.Epoch || r.SessionID != q.SessionID {
					return errIngestionInvalid
				}
				for _, v := range f.Seen {
					if v.ID == r.ID {
						if v.Retired {
							return ingestionError("replayRetired")
						}
						return ingestionError("duplicateConflict")
					}
				}
				q.Outcome = "retained"
				q.ReceiptID = ingestionPtr(r.ID)
				f.Receipts = append(f.Receipts, *r)
				f.Seen = append(f.Seen, ingestionSeen{r.ID, r.RecordSHA256, false})
				f.Fences = append(f.Fences, in.Fences...)
				m.ReceiptID = ingestionPtr(r.ID)
			}
		case "retireEpoch":
			ep, ok := ingestionCurrent(f, in.Epoch)
			if !ok {
				return ingestionError("epochChanged")
			}
			for _, q := range f.Requests {
				for _, v := range f.Epochs {
					if q.Outcome == "pending" && q.Epoch == v.ID && v.Canonical == ep.Canonical {
						return ingestionError("pendingOwnerUnknown")
					}
				}
			}
			for i := range f.Epochs {
				if f.Epochs[i].ID == in.Epoch {
					f.Epochs[i].Retired = true
				}
			}
		case "compact":
		}
		f.Revision++
		f.WallHighWater = in.At
		f.Mutations = append(f.Mutations, m)
		compactIngestion(&f)
		sortIngestion(&f)
		if len(f.Epochs) > 256 || len(f.Requests) > 4096 || len(f.Receipts) > 512 || len(f.Seen) > 16384 || len(f.Fences) > 4096 || len(f.Mutations) > 16384 {
			return ingestionError("storageFull")
		}
		if len(ingestionJSON(f)) > ingestionMaxBytes {
			return ingestionError("storageFull")
		}
		b, e := encodeIngestion(f)
		if e != nil {
			return e
		}
		// Replace may have made the entire new state visible even when it errors.
		// No ticket/ref/failure-release acknowledgement escapes either uncertainty.
		if in.Kind == "begin" {
			if s.begun == nil {
				s.begun = map[string]*ingestionOperation{}
			}
			s.begun[in.OperationID] = op
		}
		if s.backend.Replace(b) != nil || s.backend.Confirm() != nil {
			return ingestionError("mutationUnknown")
		}
		ack = ingestionStatus(f, m)
		ack.Historical = false
		if in.Kind == "begin" {
			q := ingestionFindRequest(&f, in.IntentID)
			ack.Ticket = &ingestionTicket{s, f.StoreID, q.IntentID, q.Epoch, q.SessionID, q.Sequence}
		}
		return nil
	})
	if err != nil {
		ack = ingestionAck{}
	}
	return
}

// Backend-independent copies only; no disk/transport work in this helper.
func bytesReaderIngestion(b []byte) *bytes.Reader { return bytes.NewReader(b) }
func ingestionRetentionKeep(f ingestionFile) map[string]bool {
	pinned := map[string]bool{}
	for _, v := range f.Fences {
		pinned[v.CauseReceipt] = true
		if v.ClearedBy != nil {
			pinned[*v.ClearedBy] = true
		}
	}
	canonical := map[string]string{}
	for _, e := range f.Epochs {
		canonical[e.ID] = e.Canonical
	}
	bySelection := map[string][]ingestionReceipt{}
	for _, r := range f.Receipts {
		bySelection[canonical[r.Epoch]] = append(bySelection[canonical[r.Epoch]], r)
	}
	keep := map[string]bool{}
	for _, rs := range bySelection {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Sequence > rs[j].Sequence })
		historical := 0
		for i, r := range rs {
			if i == 0 || pinned[r.ID] {
				keep[r.ID] = true
				continue
			}
			if historical < 8 {
				keep[r.ID] = true
				historical++
			}
		}
	}
	return keep
}
func compactIngestion(f *ingestionFile) {
	keep := ingestionRetentionKeep(*f)
	kept := f.Receipts[:0]
	for _, r := range f.Receipts {
		if keep[r.ID] {
			kept = append(kept, r)
		}
	}
	f.Receipts = kept
	for i := range f.Seen {
		if !keep[f.Seen[i].ID] {
			f.Seen[i].Retired = true
		}
	}
}
