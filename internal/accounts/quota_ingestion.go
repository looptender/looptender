package accounts

// Offline, synthetic evidence storage only. There is no acquisition bridge,
// policy evaluator, real persistence backend, receipt consumer or caller.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	ingestionMaxBytes    = 8 << 20
	ingestionMaxEnvelope = 64 << 10
	ingestionMaxDepth    = 16
	ingestionMaxToken    = 96 << 10
)

var errIngestionInvalid = errors.New("quota ingestion invalid")
var errIngestionUninitialized = errors.New("quota ingestion: uninitialized")

type ingestionEpoch struct {
	ID                string    `json:"id"`
	SelectionID       string    `json:"selectionId"`
	SelectionRevision string    `json:"selectionRevision"`
	Context           string    `json:"context"`
	Reader            string    `json:"reader"`
	MappingRevision   string    `json:"mappingRevision"`
	PolicyRevision    string    `json:"policyRevision"`
	Account           string    `json:"account"`
	Allowance         string    `json:"allowance"`
	Scope             string    `json:"scope"`
	Canonical         string    `json:"canonical"`
	CreatedAt         time.Time `json:"createdAt"`
	Retired           bool      `json:"retired"`
	Predecessor       *string   `json:"predecessor"`
}
type ingestionRequest struct {
	Sequence  uint64    `json:"sequence"`
	SessionID string    `json:"sessionId"`
	IntentID  string    `json:"intentId"`
	Epoch     string    `json:"epoch"`
	CreatedAt time.Time `json:"createdAt"`
	Outcome   string    `json:"outcome"`
	ReceiptID *string   `json:"receiptId"`
	Reason    string    `json:"reason"`
}
type ingestionPublication struct {
	SelectionID       string    `json:"selectionId"`
	SelectionRevision string    `json:"selectionRevision"`
	Context           string    `json:"context"`
	AttemptAt         time.Time `json:"attemptAt"`
	AttemptSHA256     string    `json:"attemptSha256"`
}
type ingestionReceipt struct {
	ID                 string               `json:"id"`
	Sequence           uint64               `json:"sequence"`
	SessionID          string               `json:"sessionId"`
	Epoch              string               `json:"epoch"`
	Reader             string               `json:"reader"`
	StartedAt          time.Time            `json:"startedAt"`
	CompletedAt        time.Time            `json:"completedAt"`
	ElapsedNS          int64                `json:"elapsedNs"`
	ProviderObservedAt *time.Time           `json:"providerObservedAt"`
	Parsed             []byte               `json:"parsed"`
	ParsedSHA256       string               `json:"parsedSha256"`
	RecordSHA256       string               `json:"recordSha256"`
	Publication        ingestionPublication `json:"publication"`
}
type ingestionSeen struct {
	ID           string `json:"id"`
	RecordSHA256 string `json:"recordSha256"`
	Retired      bool   `json:"retired"`
}
type ingestionMutation struct {
	OperationID     string  `json:"operationId"`
	Kind            string  `json:"kind"`
	InputSHA256     string  `json:"inputSha256"`
	IntentID        *string `json:"intentId"`
	AppliedRevision uint64  `json:"appliedRevision"`
	ReceiptID       *string `json:"receiptId"`
}
type ingestionFence struct {
	Lineage       string     `json:"lineage"`
	Allowance     string     `json:"allowance"`
	Scope         string     `json:"scope"`
	Bucket        string     `json:"bucket"`
	Meter         string     `json:"meter"`
	Revision      uint64     `json:"revision"`
	State         string     `json:"state"`
	CauseReceipt  string     `json:"causeReceipt"`
	KnownBoundary *time.Time `json:"knownBoundary"`
	PriorRevision uint64     `json:"priorRevision"`
	ClearedBy     *string    `json:"clearedBy"`
}
type ingestionFile struct {
	Version           int                 `json:"version"`
	StoreID           string              `json:"storeId"`
	Revision          uint64              `json:"revision"`
	CatalogGeneration string              `json:"catalogGeneration"`
	WallHighWater     time.Time           `json:"wallHighWater"`
	NextSequence      uint64              `json:"nextSequence"`
	Epochs            []ingestionEpoch    `json:"epochs"`
	Requests          []ingestionRequest  `json:"requests"`
	Receipts          []ingestionReceipt  `json:"receipts"`
	Fences            []ingestionFence    `json:"fences"`
	Seen              []ingestionSeen     `json:"seen"`
	Mutations         []ingestionMutation `json:"mutations"`
}

func ingestionHash(domain string, b []byte) string {
	h := sha256.New()
	h.Write([]byte(domain + "\x00"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func ingestionJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func ingestionEpochHash(e ingestionEpoch) string {
	e.ID = ""
	e.Retired = false
	return ingestionHash("quota-ingestion/epoch/v1", ingestionJSON(e))
}
func ingestionReceiptHash(r ingestionReceipt) string {
	r.RecordSHA256 = ""
	return ingestionHash("quota-ingestion/receipt/v1", ingestionJSON(r))
}
func ingestionID(s string) bool     { return len(s) == 32 && ingestionHex(s) }
func ingestionDigest(s string) bool { return len(s) == 64 && ingestionHex(s) }
func ingestionHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return s != ""
}
func ingestionText(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func ingestionTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 2000 && t.Year() <= 9999 && t.Location() == time.UTC
}
func ingestionReader(s string) bool { return s == "codex-account-rateLimits-read/v1" }
func ingestionReason(s string) bool {
	switch s {
	case "failedRead", "storeConflict", "selectionChanged", "publicationSuperseded", "clockUnavailable", "mappingUnknown", "reconciliationRequired":
		return true
	}
	return false
}
func ingestionKind(s string) bool {
	switch s {
	case "initialize", "begin", "commit", "fail", "retireEpoch", "compact":
		return true
	}
	return false
}
func ingestionFenceKey(f ingestionFence) string {
	return f.Lineage + "\x00" + f.Scope + "\x00" + f.Bucket + "\x00" + f.Meter
}

// Lexically bound strings/nesting before encoding/json allocates tokens. JSON
// syntax/duplicates and per-collection counts are then checked before decoding.
func ingestionPreflight(b []byte) error {
	if len(b) == 0 || len(b) > ingestionMaxBytes {
		return errIngestionInvalid
	}
	depth := 0
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '"':
			start := i
			i++
			for ; i < len(b); i++ {
				if i-start > ingestionMaxToken {
					return errIngestionInvalid
				}
				if b[i] == '\\' {
					i++
					continue
				}
				if b[i] == '"' {
					break
				}
			}
			if i == len(b) {
				return errIngestionInvalid
			}
		case 'n', 't', 'f', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			start := i
			for i+1 < len(b) && b[i+1] != ',' && b[i+1] != ']' && b[i+1] != '}' && b[i+1] != ' ' && b[i+1] != '\n' && b[i+1] != '\r' && b[i+1] != '\t' {
				i++
				if i-start >= ingestionMaxToken {
					return errIngestionInvalid
				}
			}
		case '{', '[':
			depth++
			if depth > ingestionMaxDepth {
				return errIngestionInvalid
			}
		case '}', ']':
			depth--
			if depth < 0 {
				return errIngestionInvalid
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(string, int) error
	walk = func(key string, level int) error {
		if level > ingestionMaxDepth {
			return errIngestionInvalid
		}
		v, err := d.Token()
		if err != nil {
			return errIngestionInvalid
		}
		delim, ok := v.(json.Delim)
		if !ok {
			if key == "parsed" {
				str, yes := v.(string)
				if !yes || len(str)%4 != 0 {
					return errIngestionInvalid
				}
				size := len(str) / 4 * 3
				if strings.HasSuffix(str, "==") {
					size -= 2
				} else if strings.HasSuffix(str, "=") {
					size--
				}
				if size < 0 || size > ingestionMaxEnvelope {
					return errIngestionInvalid
				}
			}
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				s, ok := k.(string)
				if e != nil || !ok || seen[s] || len(seen) >= 32 {
					return errIngestionInvalid
				}
				seen[s] = true
				if e = walk(s, level+1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return errIngestionInvalid
			}
		case '[':
			limit := 16384
			switch key {
			case "epochs":
				limit = 256
			case "requests", "fences":
				limit = 4096
			case "receipts":
				limit = 512
			}
			n := 0
			for d.More() {
				n++
				if n > limit {
					return errIngestionInvalid
				}
				if e := walk("", level+1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return errIngestionInvalid
			}
		default:
			return errIngestionInvalid
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errIngestionInvalid
	}
	return nil
}
func decodeIngestion(b []byte) (ingestionFile, error) {
	var f ingestionFile
	if ingestionPreflight(b) != nil {
		return f, errIngestionInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil {
		return ingestionFile{}, errIngestionInvalid
	}
	if err := validateIngestion(f); err != nil {
		return ingestionFile{}, err
	}
	// Required keys/nulls and canonical field/array/time/numeric encoding are exact.
	if !bytes.Equal(b, ingestionJSON(f)) {
		return ingestionFile{}, errIngestionInvalid
	}
	return f, nil
}
func encodeIngestion(f ingestionFile) ([]byte, error) {
	if err := validateIngestion(f); err != nil {
		return nil, err
	}
	b := ingestionJSON(f)
	if ingestionPreflight(b) != nil {
		return nil, errIngestionInvalid
	}
	return b, nil
}
func ingestionSorted[T any](v []T, key func(T) string) bool {
	for i := 1; i < len(v); i++ {
		if key(v[i-1]) >= key(v[i]) {
			return false
		}
	}
	return true
}
func sortIngestion(f *ingestionFile) {
	sort.Slice(f.Epochs, func(i, j int) bool { return f.Epochs[i].ID < f.Epochs[j].ID })
	sort.Slice(f.Requests, func(i, j int) bool { return f.Requests[i].Sequence < f.Requests[j].Sequence })
	sort.Slice(f.Receipts, func(i, j int) bool { return f.Receipts[i].ID < f.Receipts[j].ID })
	sort.Slice(f.Seen, func(i, j int) bool { return f.Seen[i].ID < f.Seen[j].ID })
	sort.Slice(f.Fences, func(i, j int) bool {
		a, b := f.Fences[i], f.Fences[j]
		if ingestionFenceKey(a) != ingestionFenceKey(b) {
			return ingestionFenceKey(a) < ingestionFenceKey(b)
		}
		return a.Revision < b.Revision
	})
	sort.Slice(f.Mutations, func(i, j int) bool { return f.Mutations[i].AppliedRevision < f.Mutations[j].AppliedRevision })
}

func validateIngestion(f ingestionFile) error {
	bad := func() error { return errIngestionInvalid }
	if f.Version != 1 || !ingestionID(f.StoreID) || !ingestionDigest(f.CatalogGeneration) || f.Revision == 0 || f.NextSequence == 0 || !ingestionTime(f.WallHighWater) || f.Epochs == nil || f.Requests == nil || f.Receipts == nil || f.Fences == nil || f.Seen == nil || f.Mutations == nil || len(f.Epochs) > 256 || len(f.Requests) > 4096 || len(f.Receipts) > 512 || len(f.Fences) > 4096 || len(f.Seen) > 16384 || len(f.Mutations) > 16384 {
		return bad()
	}
	if !ingestionSorted(f.Epochs, func(e ingestionEpoch) string { return e.ID }) || !ingestionSorted(f.Receipts, func(r ingestionReceipt) string { return r.ID }) || !ingestionSorted(f.Seen, func(s ingestionSeen) string { return s.ID }) {
		return bad()
	}
	epochs := map[string]ingestionEpoch{}
	successors := map[string]string{}
	selections := map[string]bool{}
	for _, e := range f.Epochs {
		if e.ID != ingestionEpochHash(e) || !ingestionID(e.SelectionID) || !ingestionID(e.SelectionRevision) || !ingestionDigest(e.Context) || !ingestionReader(e.Reader) || !ingestionDigest(e.MappingRevision) || !ingestionDigest(e.PolicyRevision) || !ingestionText(e.Account) || !ingestionText(e.Allowance) || !ingestionText(e.Scope) || e.Canonical != e.SelectionID || !ingestionTime(e.CreatedAt) || e.CreatedAt.After(f.WallHighWater) {
			return bad()
		}
		epochs[e.ID] = e
		selections[e.Canonical] = true
		if e.Predecessor != nil {
			if !ingestionDigest(*e.Predecessor) || successors[*e.Predecessor] != "" {
				return bad()
			}
			successors[*e.Predecessor] = e.ID
		}
	}
	if len(selections) > MaxEntries {
		return bad()
	}
	roots := map[string]string{}
	lineage := map[string]string{}
	current := map[string]string{}
	allowances := map[string]string{}
	for _, e := range f.Epochs {
		at := e
		seen := map[string]bool{}
		for at.Predecessor != nil {
			if seen[at.ID] {
				return bad()
			}
			seen[at.ID] = true
			p, ok := epochs[*at.Predecessor]
			if !ok || !p.Retired || p.Canonical != e.Canonical || p.Account != e.Account || p.Allowance != e.Allowance || p.Scope != e.Scope || p.CreatedAt.After(at.CreatedAt) {
				return bad()
			}
			at = p
		}
		if roots[e.Canonical] != "" && roots[e.Canonical] != at.ID {
			return bad()
		}
		roots[e.Canonical] = at.ID
		lineage[e.ID] = at.ID
		if !e.Retired {
			if current[e.Canonical] != "" || successors[e.ID] != "" {
				return bad()
			}
			current[e.Canonical] = e.ID
			key := e.Allowance + "\x00" + e.Scope
			if allowances[key] != "" && allowances[key] != e.Canonical {
				return bad()
			}
			allowances[key] = e.Canonical
		}
	}
	requests := map[string]ingestionRequest{}
	seqs := map[uint64]bool{}
	pending := map[string]bool{}
	for i, r := range f.Requests {
		e, ok := epochs[r.Epoch]
		if !ok || !ingestionID(r.SessionID) || !ingestionID(r.IntentID) || requests[r.IntentID].IntentID != "" || r.Sequence == 0 || r.Sequence >= f.NextSequence || seqs[r.Sequence] || i > 0 && f.Requests[i-1].Sequence >= r.Sequence || !ingestionTime(r.CreatedAt) || r.CreatedAt.Before(e.CreatedAt) || r.CreatedAt.After(f.WallHighWater) {
			return bad()
		}
		switch r.Outcome {
		case "pending":
			if e.Retired || pending[e.Canonical] || r.ReceiptID != nil || r.Reason != "" {
				return bad()
			}
			pending[e.Canonical] = true
		case "retained":
			if r.ReceiptID == nil || !ingestionID(*r.ReceiptID) || r.Reason != "" {
				return bad()
			}
		case "failed", "rejected":
			if r.ReceiptID != nil || !ingestionReason(r.Reason) {
				return bad()
			}
		default:
			return bad()
		}
		requests[r.IntentID] = r
		seqs[r.Sequence] = true
	}
	receipts := map[string]ingestionReceipt{}
	receiptRequests := map[string]ingestionRequest{}
	for _, r := range f.Requests {
		if r.ReceiptID != nil {
			if _, ok := receiptRequests[*r.ReceiptID]; ok {
				return bad()
			}
			receiptRequests[*r.ReceiptID] = r
		}
	}
	for _, r := range f.Receipts {
		e, ok := epochs[r.Epoch]
		q, qok := receiptRequests[r.ID]
		if !ok || !qok || !ingestionID(r.ID) || r.Sequence != q.Sequence || r.SessionID != q.SessionID || r.Epoch != q.Epoch || !ingestionReader(r.Reader) || r.Reader != e.Reader || r.ProviderObservedAt != nil || !ingestionTime(r.StartedAt) || !ingestionTime(r.CompletedAt) || r.StartedAt.Before(q.CreatedAt) || r.CompletedAt.Before(r.StartedAt) || r.CompletedAt.After(f.WallHighWater) || r.ElapsedNS < 0 || r.ElapsedNS > int64(5*time.Second) || len(r.Parsed) == 0 || len(r.Parsed) > ingestionMaxEnvelope {
			return bad()
		}
		wall := r.CompletedAt.Sub(r.StartedAt)
		elapsed := time.Duration(r.ElapsedNS)
		if wall-elapsed > time.Second || elapsed-wall > time.Second {
			return bad()
		}
		sum := sha256.Sum256(r.Parsed)
		if r.ParsedSHA256 != hex.EncodeToString(sum[:]) || r.RecordSHA256 != ingestionReceiptHash(r) || len(ingestionJSON(r)) > ingestionMaxEnvelope || ingestionPreflight(r.Parsed) != nil {
			return bad()
		}
		var details QuotaDetails
		d := json.NewDecoder(bytes.NewReader(r.Parsed))
		d.DisallowUnknownFields()
		if d.Decode(&details) != nil || !ValidQuotaDetails(&details) || !bytes.Equal(ingestionJSON(details), r.Parsed) || details.Partial || details.Omitted != 0 {
			return bad()
		}
		meters := 0
		for _, b := range details.Buckets {
			if b.Partial {
				return bad()
			}
			meters += len(b.Windows)
			for _, meter := range b.Windows {
				if meter.ResetsAt != nil && !ingestionTime(*meter.ResetsAt) {
					return bad()
				}
			}
			if b.Individual != nil {
				if !ingestionTime(b.Individual.ResetsAt) {
					return bad()
				}
				meters++
			}
		}
		if meters == 0 {
			return bad()
		}
		p := r.Publication
		if p.SelectionID != e.SelectionID || p.SelectionRevision != e.SelectionRevision || p.Context != e.Context || !ingestionTime(p.AttemptAt) || p.AttemptAt.Before(r.CompletedAt) || p.AttemptAt.After(f.WallHighWater) || !ingestionDigest(p.AttemptSHA256) {
			return bad()
		}
		receipts[r.ID] = r
	}
	keep := ingestionRetentionKeep(f)
	for id := range receipts {
		if !keep[id] {
			return bad()
		}
	}
	seenIDs := map[string]ingestionSeen{}
	for _, s := range f.Seen {
		if !ingestionID(s.ID) || !ingestionDigest(s.RecordSHA256) {
			return bad()
		}
		r, ok := receipts[s.ID]
		if ok && (s.Retired || s.RecordSHA256 != r.RecordSHA256) || !ok && !s.Retired {
			return bad()
		}
		seenIDs[s.ID] = s
	}
	if len(seenIDs) != len(receiptRequests) {
		return bad()
	}
	for id := range receiptRequests {
		if _, ok := seenIDs[id]; !ok {
			return bad()
		}
	}
	lastFences := map[string]ingestionFence{}
	for i, v := range f.Fences {
		key := ingestionFenceKey(v)
		prev := lastFences[key]
		cause, ok := receipts[v.CauseReceipt]
		e := epochs[cause.Epoch]
		if !ok || lineage[cause.Epoch] != v.Lineage || v.Allowance != e.Allowance || v.Scope != e.Scope || !quotaKey.MatchString(v.Bucket) || (v.Meter != "primary" && v.Meter != "secondary" && v.Meter != "individual") || v.Revision == 0 || v.Revision != prev.Revision+1 || v.PriorRevision != prev.Revision || i > 0 && (ingestionFenceKey(f.Fences[i-1]) > key || ingestionFenceKey(f.Fences[i-1]) == key && f.Fences[i-1].Revision >= v.Revision) {
			return bad()
		}
		switch v.State {
		case "baseline", "exhausted", "ended", "boundaryUnknown", "reconciliationRequired":
		default:
			return bad()
		}
		if prev.Revision > 0 && (v.CauseReceipt != prev.CauseReceipt || !reflectIngestionTime(v.KnownBoundary, prev.KnownBoundary)) {
			return bad()
		}
		var details QuotaDetails
		_ = json.Unmarshal(cause.Parsed, &details)
		found := false
		var reset *time.Time
		for _, b := range details.Buckets {
			if b.Key != v.Bucket {
				continue
			}
			for _, m := range b.Windows {
				if m.Name == v.Meter {
					found = true
					reset = m.ResetsAt
				}
			}
			if v.Meter == "individual" && b.Individual != nil {
				found = true
				reset = &b.Individual.ResetsAt
			}
		}
		if !found || !reflectIngestionTime(reset, v.KnownBoundary) {
			return bad()
		}
		if v.ClearedBy != nil {
			r, ok := receipts[*v.ClearedBy]
			if !ok || lineage[r.Epoch] != v.Lineage || epochs[r.Epoch].Scope != v.Scope || epochs[r.Epoch].Allowance != v.Allowance {
				return bad()
			}
		}
		lastFences[key] = v
	}
	begins := map[string]int{}
	ends := map[string]int{}
	ops := map[string]bool{}
	var prior uint64
	for _, m := range f.Mutations {
		if !ingestionID(m.OperationID) || ops[m.OperationID] || !ingestionKind(m.Kind) || !ingestionDigest(m.InputSHA256) || m.AppliedRevision == 0 || m.AppliedRevision > f.Revision || m.AppliedRevision != prior+1 {
			return bad()
		}
		if (m.Kind == "initialize") != (m.AppliedRevision == 1) {
			return bad()
		}
		switch m.Kind {
		case "begin", "commit", "fail":
			if m.IntentID == nil {
				return bad()
			}
			q, ok := requests[*m.IntentID]
			if !ok {
				return bad()
			}
			if m.Kind == "begin" {
				begins[*m.IntentID]++
			} else {
				ends[*m.IntentID]++
			}
			if m.Kind == "fail" && q.Outcome != "failed" {
				return bad()
			}
			if m.Kind == "commit" {
				if m.ReceiptID == nil || q.ReceiptID == nil || *m.ReceiptID != *q.ReceiptID {
					return bad()
				}
			} else if m.ReceiptID != nil {
				return bad()
			}
		default:
			if m.IntentID != nil || m.ReceiptID != nil {
				return bad()
			}
		}
		ops[m.OperationID] = true
		prior = m.AppliedRevision
	}
	for id, q := range requests {
		if begins[id] != 1 || q.Outcome == "pending" && ends[id] != 0 || q.Outcome != "pending" && ends[id] != 1 {
			return bad()
		}
	}
	if len(f.Mutations) == 0 || prior != f.Revision {
		return bad()
	}
	return nil
}
func reflectIngestionTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b) && ingestionTime(*a) && ingestionTime(*b)
}

func cloneIngestion(f ingestionFile) ingestionFile {
	var out ingestionFile
	_ = json.Unmarshal(ingestionJSON(f), &out)
	return out
}
func ingestionPtr(s string) *string { return &s }
func ingestionFindRequest(f *ingestionFile, id string) *ingestionRequest {
	for i := range f.Requests {
		if f.Requests[i].IntentID == id {
			return &f.Requests[i]
		}
	}
	return nil
}
func ingestionCurrent(f ingestionFile, id string) (ingestionEpoch, bool) {
	for _, e := range f.Epochs {
		if e.ID == id && !e.Retired {
			return e, true
		}
	}
	return ingestionEpoch{}, false
}
func ingestionOverflow(n uint64) bool    { return n == math.MaxUint64 }
func ingestionError(reason string) error { return fmt.Errorf("quota ingestion: %s", reason) }
