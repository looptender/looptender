package accounts

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type ingestionMemory struct {
	mu                 sync.Mutex
	data               []byte
	durable            bool
	fault              string
	confirms, replaces int
	phases             []string
}

func (b *ingestionMemory) Durable() bool { return b.durable }
func (b *ingestionMemory) WithLock(fn func() error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fault == "lock" {
		return errors.New("fixture lock")
	}
	err := fn()
	if err == nil && b.fault == "ack" {
		return errors.New("fixture lost acknowledgement")
	}
	return err
}
func (b *ingestionMemory) ReadBounded(n int) ([]byte, error) {
	if b.fault == "read" {
		return nil, errors.New("fixture read")
	}
	if b.data == nil {
		return nil, os.ErrNotExist
	}
	if len(b.data) > n {
		return append([]byte(nil), b.data[:n]...), nil
	}
	return append([]byte(nil), b.data...), nil
}
func (b *ingestionMemory) Replace(data []byte) error {
	b.replaces++
	for _, p := range []string{"temp", "write", "fileSync", "close", "beforeReplace", "afterReplace"} {
		b.phases = append(b.phases, p)
		if p == "afterReplace" {
			b.data = append([]byte(nil), data...)
		}
		if b.fault == p {
			return errors.New("fixture " + p)
		}
	}
	return nil
}
func (b *ingestionMemory) Confirm() error {
	b.confirms++
	b.phases = append(b.phases, "directorySync")
	if b.fault == "directorySync" {
		return errors.New("fixture barrier")
	}
	return nil
}

func TestIngestionEveryMutationDurabilityAndUnknownRecovery(t *testing.T) {
	for _, kind := range []string{"initialize", "begin", "commit", "fail", "retireEpoch", "compact"} {
		for _, fault := range []string{"temp", "write", "fileSync", "close", "beforeReplace", "afterReplace", "directorySync", "ack"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				s, b := ingestionTestStore(t)
				var ticket *ingestionTicket
				if kind == "commit" || kind == "fail" {
					_, ticket = ingestionTestBegin(t, s, 200)
				}
				f := ingestionRead(t, s)
				in := ingestionInput{Kind: kind, OperationID: ingestionTestID(300), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Epoch: f.Epochs[0].ID, IntentID: ingestionTestID(301)}
				switch kind {
				case "initialize":
					b.data = nil
					initial := ingestionTestInitial()
					in.Initial = &initial
					in.ExpectedRevision = 0
				case "commit":
					r := ingestionTestReceipt(f, ticket, 302)
					in.Receipt = &r
					in.At = r.Publication.AttemptAt
				case "fail":
					in.Reason = "failedRead"
				}
				op := s.prepare(in, ticket)
				before := append([]byte(nil), b.data...)
				b.fault = fault
				ack, err := s.apply(op)
				if err == nil || ack.Ticket != nil || ack.AppliedRevision != 0 || ack.ReceiptID != "" {
					t.Fatalf("uncertain mutation acknowledged: %+v %v", ack, err)
				}
				visible := fault == "afterReplace" || fault == "directorySync" || fault == "ack"
				if !visible && !bytes.Equal(before, b.data) {
					t.Fatal("pre-replace fault changed state")
				}
				if visible && bytes.Equal(before, b.data) {
					t.Fatal("fixture did not publish unknown whole record")
				}
				b.fault = ""
				// Same process/immutable operation: either retry absent mutation with same
				// CAS, or confirm its exact historical status; no new operation/RPC.
				ack, err = s.apply(op)
				if err != nil {
					t.Fatal("same operation recovery", err)
				}
				if visible && !ack.Historical {
					t.Fatal("retry reapplied operation")
				}
				if kind == "begin" && ack.Ticket == nil {
					t.Fatal("same-process exact pending ticket not recovered")
				}
				loaded := ingestionRead(t, s)
				if loaded.Revision != in.ExpectedRevision+1 {
					t.Fatal("duplicate mutation revision", loaded.Revision)
				}
				status, err := s.status(f.StoreID, in.OperationID, op.digest)
				if err != nil || !status.Historical || status.Ticket != nil {
					t.Fatal("status reconstructed ticket", status, err)
				}
				// Restart can only inspect historical status; it cannot apply a surviving
				// old process operation or transition its owned pending intent.
				other := &quotaIngestionStore{backend: b, session: ingestionTestID(999)}
				historical, err := other.status(f.StoreID, in.OperationID, op.digest)
				if err != nil || !historical.Historical || historical.Ticket != nil {
					t.Fatal("restart status", historical, err)
				}
				_, err = other.apply(op)
				ingestionAssertError(t, err, "associationUnavailable")
			})
		}
	}
}

func TestIngestionLostAcknowledgementAndHistoricalRevision(t *testing.T) {
	s, b := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	f := ingestionRead(t, s)
	r := ingestionTestReceipt(f, ticket, 203)
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: r.Publication.AttemptAt, Receipt: &r}, ticket)
	b.fault = "ack"
	a, err := s.apply(op)
	if err == nil || a.AppliedRevision != 0 {
		t.Fatal("ack loss exposed success", a, err)
	}
	b.fault = ""
	f = ingestionRead(t, s)
	retire := s.prepare(ingestionInput{Kind: "retireEpoch", OperationID: ingestionTestID(204), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Epoch: f.Epochs[0].ID}, nil)
	if _, err = s.apply(retire); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), b.data...)
	a, err = s.apply(op)
	if err != nil || !a.Historical || a.AppliedRevision != 3 || a.CurrentRevision != 4 || a.CurrentEpoch || a.Ticket != nil || !bytes.Equal(before, b.data) {
		t.Fatal("historical retry became current", a, err)
	}
	bad := s.prepare(ingestionInput{Kind: "compact", OperationID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: 4, At: f.WallHighWater}, nil)
	_, err = s.apply(bad)
	ingestionAssertError(t, err, "duplicateConflict")
	if !bytes.Equal(before, b.data) {
		t.Fatal("conflicting identity overwrote history")
	}
	// Lost Begin ack followed by unrelated revision: no recovered usable ticket.
	s, b = ingestionTestStore(t)
	f = ingestionRead(t, s)
	begin := s.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(300), IntentID: ingestionTestID(301), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Epoch: f.Epochs[0].ID}, nil)
	b.fault = "afterReplace"
	_, err = s.apply(begin)
	if err == nil {
		t.Fatal("expected uncertainty")
	}
	b.fault = ""
	f = ingestionRead(t, s)
	compact := s.prepare(ingestionInput{Kind: "compact", OperationID: ingestionTestID(302), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater}, nil)
	if _, err = s.apply(compact); err != nil {
		t.Fatal(err)
	}
	a, err = s.apply(begin)
	if err != nil || a.Ticket != nil || !a.Historical || a.AppliedRevision != 2 || a.CurrentRevision != 3 {
		t.Fatal("stale Begin ticket recovered", a, err)
	}
}

func TestIngestionUnsupportedAndCorruptNeverInitialize(t *testing.T) {
	f := ingestionTestInitial()
	for _, backend := range []ingestionBackend{nil, &ingestionMemory{durable: false}} {
		s := &quotaIngestionStore{backend: backend, session: ingestionTestID(100)}
		op := s.prepare(ingestionInput{Kind: "initialize", OperationID: ingestionTestID(101), StoreID: f.StoreID, Initial: &f, At: f.WallHighWater}, nil)
		_, err := s.apply(op)
		ingestionAssertError(t, err, "unsupportedDurability")
	}
	for _, data := range [][]byte{[]byte("partial"), {}, []byte(`{"version":1}`)} {
		b := &ingestionMemory{durable: true, data: data}
		s := &quotaIngestionStore{backend: b, session: ingestionTestID(100)}
		op := s.prepare(ingestionInput{Kind: "initialize", OperationID: ingestionTestID(101), StoreID: f.StoreID, Initial: &f, At: f.WallHighWater}, nil)
		if _, err := s.apply(op); err == nil || b.replaces != 0 {
			t.Fatal("corrupt store repaired")
		}
	}
	s, b := ingestionTestStore(t)
	for _, fault := range []string{"lock", "read", "directorySync", "ack"} {
		b.fault = fault
		out, err := s.load()
		if err == nil || out.StoreID != "" {
			t.Fatal("unconfirmed read exposed snapshot", fault, err)
		}
	}
}

func TestIngestionConcurrentBeginAndNoAliasing(t *testing.T) {
	s, b := ingestionTestStore(t)
	f := ingestionRead(t, s)
	var wg sync.WaitGroup
	success := make(chan ingestionAck, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := &quotaIngestionStore{backend: b, session: ingestionTestID(500 + i)}
			op := other.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(200 + i*2), IntentID: ingestionTestID(201 + i*2), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater}, nil)
			a, err := other.apply(op)
			if err == nil {
				success <- a
			} else if !strings.Contains(err.Error(), "storeConflict") {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(success)
	n := 0
	for range success {
		n++
	}
	if n != 1 {
		t.Fatal("duplicate concurrent Begin", n)
	}
	if len(ingestionRead(t, s).Requests) != 1 {
		t.Fatal("lost/duplicate exclusion")
	}
	// Input is frozen at prepare, including nested receipt values/publication.
	s, _ = ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 300)
	f = ingestionRead(t, s)
	r := ingestionTestReceipt(f, ticket, 303)
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(302), StoreID: f.StoreID, ExpectedRevision: f.Revision, Receipt: &r, At: r.Publication.AttemptAt}, ticket)
	original := append([]byte(nil), r.Parsed...)
	originalTime := r.Publication.AttemptAt
	r.Parsed[0] = '!'
	r.Publication.AttemptAt = r.Publication.AttemptAt.Add(time.Hour)
	if _, err := s.apply(op); err != nil {
		t.Fatal(err)
	}
	got := ingestionRead(t, s).Receipts[0]
	if !bytes.Equal(original, got.Parsed) || !got.Publication.AttemptAt.Equal(originalTime) {
		t.Fatal("prepared input mutated")
	}
}

func TestIngestionRestartSameSessionDoesNotReconstructBegin(t *testing.T) {
	s, b := ingestionTestStore(t)
	f := ingestionRead(t, s)
	in := ingestionInput{Kind: "begin", OperationID: ingestionTestID(200), IntentID: ingestionTestID(201), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater}
	op := s.prepare(in, nil)
	b.fault = "ack"
	_, err := s.apply(op)
	if err == nil {
		t.Fatal("ack loss missing")
	}
	b.fault = ""
	// Even copying the old session/operation IDs cannot recreate process-local
	// attempted-operation association in a new store instance.
	restarted := &quotaIngestionStore{backend: b, session: s.session}
	copied := restarted.prepare(in, nil)
	a, err := restarted.apply(copied)
	if err != nil || !a.Historical || a.Ticket != nil {
		t.Fatal("reconstructed Begin ticket", a, err)
	}
	original, err := s.apply(op)
	if err != nil || original.Ticket == nil {
		t.Fatal("original association lost", err)
	}
}

func TestIngestionOldProofSnapshotAndFailedHistory(t *testing.T) {
	s, _ := ingestionTestStore(t)
	_, tk := ingestionTestBegin(t, s, 200)
	_, r := ingestionTestCommit(t, s, tk, 202, nil)
	before := ingestionRead(t, s).Receipts[0]
	_, next := ingestionTestBegin(t, s, 204)
	f := ingestionRead(t, s)
	fail := s.prepare(ingestionInput{Kind: "fail", OperationID: ingestionTestID(206), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Reason: "failedRead"}, next)
	if _, err := s.apply(fail); err != nil {
		t.Fatal(err)
	}
	f = ingestionRead(t, s)
	if len(f.Receipts) != 1 || !bytes.Equal(ingestionJSON(before), ingestionJSON(f.Receipts[0])) || f.Receipts[0].RecordSHA256 != r.RecordSHA256 {
		t.Fatal("failure rewrote historical success")
	}
	// Explicitly assert the distinction: offline store validates a supplied hash,
	// not the genuine provider/cache event behind the original publication proof.
	if f.Requests[1].Reason != "failedRead" {
		t.Fatal("failure not separate")
	}
}
