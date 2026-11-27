package accounts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func ingestionTestID(n int) string      { return fmt.Sprintf("%032x", n) }
func ingestionTestDigest(n int) string  { return fmt.Sprintf("%064x", n) }
func ingestionTestTime(n int) time.Time { return time.Date(2031, 1, 2, 3, 4, 5, n, time.UTC) }
func ingestionTestInitial() ingestionFile {
	e := ingestionEpoch{SelectionID: ingestionTestID(10), SelectionRevision: ingestionTestID(11), Context: ingestionTestDigest(12), Reader: "codex-account-rateLimits-read/v1", MappingRevision: ingestionTestDigest(13), PolicyRevision: ingestionTestDigest(14), Account: "fictional-account", Allowance: "fictional-allowance", Scope: "configured-meter", Canonical: ingestionTestID(10), CreatedAt: ingestionTestTime(0)}
	e.ID = ingestionEpochHash(e)
	return ingestionFile{Version: 1, StoreID: ingestionTestID(1), CatalogGeneration: ingestionTestDigest(2), WallHighWater: ingestionTestTime(0), NextSequence: 1, Epochs: []ingestionEpoch{e}, Requests: []ingestionRequest{}, Receipts: []ingestionReceipt{}, Fences: []ingestionFence{}, Seen: []ingestionSeen{}, Mutations: []ingestionMutation{}}
}
func ingestionTestStore(t *testing.T) (*quotaIngestionStore, *ingestionMemory) {
	t.Helper()
	b := &ingestionMemory{durable: true}
	s := &quotaIngestionStore{backend: b, session: ingestionTestID(100)}
	f := ingestionTestInitial()
	op := s.prepare(ingestionInput{Kind: "initialize", OperationID: ingestionTestID(101), StoreID: f.StoreID, Initial: &f, At: f.WallHighWater}, nil)
	if _, err := s.apply(op); err != nil {
		t.Fatal("initialize", err)
	}
	return s, b
}
func ingestionTestBegin(t *testing.T, s *quotaIngestionStore, n int) (*ingestionOperation, *ingestionTicket) {
	t.Helper()
	f, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	op := s.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(n), IntentID: ingestionTestID(n + 1), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater.Add(time.Millisecond)}, nil)
	a, err := s.apply(op)
	if err != nil || a.Ticket == nil {
		t.Fatal("begin", err)
	}
	return op, a.Ticket
}
func ingestionTestReceipt(f ingestionFile, ticket *ingestionTicket, n int) ingestionReceipt {
	q := ingestionFindRequest(&f, ticket.intentID)
	e, _ := ingestionCurrent(f, ticket.epoch)
	reset := ingestionTestTime(0).Add(time.Hour)
	details := QuotaDetails{Buckets: []QuotaBucket{{Key: "configured", Windows: []QuotaMeter{{Name: "primary", UsedPercent: 37.125, ResetsAt: &reset}}}}}
	data := ingestionJSON(details)
	sum := sha256.Sum256(data)
	r := ingestionReceipt{ID: ingestionTestID(n), Sequence: q.Sequence, SessionID: q.SessionID, Epoch: q.Epoch, Reader: e.Reader, StartedAt: q.CreatedAt.Add(time.Millisecond), CompletedAt: q.CreatedAt.Add(2 * time.Millisecond), ElapsedNS: int64(time.Millisecond), Parsed: data, ParsedSHA256: hex.EncodeToString(sum[:]), Publication: ingestionPublication{SelectionID: e.SelectionID, SelectionRevision: e.SelectionRevision, Context: e.Context, AttemptAt: q.CreatedAt.Add(3 * time.Millisecond), AttemptSHA256: ingestionTestDigest(n)}}
	r.RecordSHA256 = ingestionReceiptHash(r)
	return r
}
func ingestionTestCommit(t *testing.T, s *quotaIngestionStore, ticket *ingestionTicket, n int, fences []ingestionFence) (*ingestionOperation, ingestionReceipt) {
	t.Helper()
	f, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	r := ingestionTestReceipt(f, ticket, n+1)
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(n), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: r.Publication.AttemptAt.Add(time.Millisecond), Receipt: &r, Fences: fences}, ticket)
	if _, err = s.apply(op); err != nil {
		t.Fatal("commit", err)
	}
	return op, r
}
func ingestionAssertError(t *testing.T, err error, word string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), word) {
		t.Fatalf("want %s, got %v", word, err)
	}
}
func ingestionRead(t *testing.T, s *quotaIngestionStore) ingestionFile {
	t.Helper()
	f, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIngestionCanonicalSnapshotAndSyntheticScope(t *testing.T) {
	s, b := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	op, r := ingestionTestCommit(t, s, ticket, 202, nil)
	f := ingestionRead(t, s)
	wire := append([]byte(nil), b.data...)
	if len(f.Receipts) != 1 || f.Receipts[0].ID != r.ID || f.Receipts[0].ProviderObservedAt != nil || !bytes.Equal(f.Receipts[0].Parsed, r.Parsed) {
		t.Fatal("synthetic values lost")
	}
	// A structural publication digest is NOT proof of genuine transport/cache.
	if f.Receipts[0].Publication.AttemptSHA256 != ingestionTestDigest(203) {
		t.Fatal("fixture proof changed")
	}
	f.Receipts[0].Parsed[0] = '!'
	f.Epochs[0].PolicyRevision = "mutated"
	r.Parsed[0] = '?'
	again := ingestionRead(t, s)
	if !bytes.Equal(wire, b.data) || again.Receipts[0].Parsed[0] != '{' {
		t.Fatal("snapshot alias")
	}
	ack, err := s.apply(op)
	if err != nil || !ack.Historical || ack.AppliedRevision != 3 || ack.CurrentRevision != 3 || ack.ReceiptID != ingestionTestID(203) {
		t.Fatal("idempotence", ack, err)
	}
	if b.replaces != 3 {
		t.Fatal("retry rewrote state")
	}
}

func TestIngestionStrictDecodeAndImmutableHashes(t *testing.T) {
	s, b := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	_, r := ingestionTestCommit(t, s, ticket, 202, nil)
	good := append([]byte(nil), b.data...)
	base := ingestionRead(t, s)
	for _, tc := range []struct {
		name   string
		change func(*ingestionFile)
	}{
		{"version", func(f *ingestionFile) { f.Version = 2 }},
		{"provider time", func(f *ingestionFile) { v := ingestionTestTime(1); f.Receipts[0].ProviderObservedAt = &v }},
		{"parsed tamper", func(f *ingestionFile) { f.Receipts[0].Parsed[0] = '!' }},
		{"record tamper", func(f *ingestionFile) {
			f.Receipts[0].Publication.AttemptAt = f.Receipts[0].Publication.AttemptAt.Add(time.Nanosecond)
		}},
		{"nonfinite", func(f *ingestionFile) { f.Receipts[0].ElapsedNS = -1 }},
		{"future", func(f *ingestionFile) { f.Receipts[0].StartedAt = f.WallHighWater.Add(time.Second) }},
		{"missing seen", func(f *ingestionFile) { f.Seen = []ingestionSeen{} }},
		{"resurrection", func(f *ingestionFile) { f.Seen[0].Retired = true }},
		{"request reference", func(f *ingestionFile) { f.Requests[0].Epoch = ingestionTestDigest(999) }},
		{"orphan request", func(f *ingestionFile) { f.Mutations = f.Mutations[:1]; f.Revision = 1 }},
		{"partial", func(f *ingestionFile) {
			var d QuotaDetails
			_ = json.Unmarshal(f.Receipts[0].Parsed, &d)
			d.Partial = true
			f.Receipts[0].Parsed = ingestionJSON(d)
			sum := sha256.Sum256(f.Receipts[0].Parsed)
			f.Receipts[0].ParsedSHA256 = hex.EncodeToString(sum[:])
			f.Receipts[0].RecordSHA256 = ingestionReceiptHash(f.Receipts[0])
			f.Seen[0].RecordSHA256 = f.Receipts[0].RecordSHA256
		}},
		{"overflow sequence", func(f *ingestionFile) { f.NextSequence = 0 }},
		{"order", func(f *ingestionFile) { f.Mutations[0], f.Mutations[1] = f.Mutations[1], f.Mutations[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cloneIngestion(base)
			tc.change(&f)
			if _, err := decodeIngestion(ingestionJSON(f)); err == nil {
				t.Fatal("accepted invalid record")
			}
		})
	}
	for name, wire := range map[string][]byte{
		"duplicate equal":    bytes.Replace(good, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"duplicate conflict": bytes.Replace(good, []byte(`"version":1`), []byte(`"version":2,"version":1`), 1),
		"unknown":            bytes.Replace(good, []byte(`"version":1`), []byte(`"extra":1,"version":1`), 1),
		"missing null":       bytes.Replace(good, []byte(`"providerObservedAt":null,`), nil, 1),
		"missing array":      bytes.Replace(good, []byte(`"fences":[],`), nil, 1),
		"time offset":        bytes.Replace(good, []byte("Z\""), []byte("+00:00\""), 1),
		"whitespace":         append(good, '\n'),
		"trailing value":     append(append([]byte(nil), good...), []byte(`{}`)...),
		"truncated":          good[:len(good)-2],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeIngestion(wire); err == nil {
				t.Fatal("accepted ambiguous/noncanonical JSON")
			}
		})
	}
	if _, err := decodeIngestion(good); err != nil {
		t.Fatal("valid record rejected", err)
	}
	for _, v := range []float64{0, math.Copysign(0, -1), 0.1, 37.125, 99.99999999999999, 100, math.SmallestNonzeroFloat64} {
		f := cloneIngestion(base)
		var d QuotaDetails
		_ = json.Unmarshal(r.Parsed, &d)
		d.Buckets[0].Windows[0].UsedPercent = v
		f.Receipts[0].Parsed = ingestionJSON(d)
		sum := sha256.Sum256(f.Receipts[0].Parsed)
		f.Receipts[0].ParsedSHA256 = hex.EncodeToString(sum[:])
		f.Receipts[0].RecordSHA256 = ingestionReceiptHash(f.Receipts[0])
		f.Seen[0].RecordSHA256 = f.Receipts[0].RecordSHA256
		out, err := decodeIngestion(ingestionJSON(f))
		if err != nil {
			t.Fatal("float", v, err)
		}
		var result QuotaDetails
		_ = json.Unmarshal(out.Receipts[0].Parsed, &result)
		if math.Float64bits(result.Buckets[0].Windows[0].UsedPercent) != math.Float64bits(v) {
			t.Fatal("float bits lost")
		}
	}
}

func TestIngestionPendingSessionsCASAndRetirement(t *testing.T) {
	for _, crashed := range []bool{false, true} {
		t.Run(fmt.Sprint(crashed), func(t *testing.T) {
			a, b := ingestionTestStore(t)
			_, ticket := ingestionTestBegin(t, a, 200)
			before := append([]byte(nil), b.data...)
			f := ingestionRead(t, a)
			// New object/session cannot infer whether A is alive or crashed.
			other := &quotaIngestionStore{backend: b, session: ingestionTestID(999)}
			op := other.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(300), IntentID: ingestionTestID(301), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater}, nil)
			ack, err := other.apply(op)
			ingestionAssertError(t, err, "pendingOwnerUnknown")
			if ack.Ticket != nil || !bytes.Equal(before, b.data) {
				t.Fatal("foreign session changed pending")
			}
			fail := other.prepare(ingestionInput{Kind: "fail", OperationID: ingestionTestID(302), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Reason: "failedRead"}, ticket)
			_, err = other.apply(fail)
			ingestionAssertError(t, err, "associationUnavailable")
			retire := other.prepare(ingestionInput{Kind: "retireEpoch", OperationID: ingestionTestID(303), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater}, nil)
			_, err = other.apply(retire)
			ingestionAssertError(t, err, "pendingOwnerUnknown")
			if !bytes.Equal(before, b.data) {
				t.Fatal("pending released")
			}
			if !crashed {
				ingestionTestCommit(t, a, ticket, 202, nil)
			}
		})
	}
	a, _ := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, a, 200)
	f := ingestionRead(t, a)
	r := ingestionTestReceipt(f, ticket, 203)
	commit := a.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: r.Publication.AttemptAt, Receipt: &r}, ticket)
	unrelated := a.prepare(ingestionInput{Kind: "compact", OperationID: ingestionTestID(300), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater}, nil)
	if _, err := a.apply(unrelated); err != nil {
		t.Fatal(err)
	}
	_, err := a.apply(commit)
	ingestionAssertError(t, err, "storeConflict")
	f = ingestionRead(t, a)
	if f.Requests[0].Outcome != "pending" || len(f.Receipts) != 0 {
		t.Fatal("CAS mutated state")
	}
	fail := a.prepare(ingestionInput{Kind: "fail", OperationID: ingestionTestID(301), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Reason: "storeConflict"}, ticket)
	if _, err = a.apply(fail); err != nil {
		t.Fatal(err)
	}
	f = ingestionRead(t, a)
	if f.Requests[0].Outcome != "failed" || f.Requests[0].Reason != "storeConflict" {
		t.Fatal("explicit fail lost")
	}
}

func TestIngestionEpochAndStructuralFenceReferences(t *testing.T) {
	s, b := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	f := ingestionRead(t, s)
	r := ingestionTestReceipt(f, ticket, 203)
	var d QuotaDetails
	_ = json.Unmarshal(r.Parsed, &d)
	fence := ingestionFence{Lineage: f.Epochs[0].ID, Allowance: f.Epochs[0].Allowance, Scope: f.Epochs[0].Scope, Bucket: "configured", Meter: "primary", Revision: 1, State: "exhausted", CauseReceipt: r.ID, KnownBoundary: d.Buckets[0].Windows[0].ResetsAt}
	// This synthetic exhausted state deliberately has a positive quota value:
	// structural storage does not calculate/attest B exhaustion or reset clearing.
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: r.Publication.AttemptAt, Receipt: &r, Fences: []ingestionFence{fence}}, ticket)
	if _, err := s.apply(op); err != nil {
		t.Fatal(err)
	}
	base := ingestionRead(t, s)
	wire := append([]byte(nil), b.data...)
	for name, change := range map[string]func(*ingestionFile){
		"missing cause": func(f *ingestionFile) { f.Fences[0].CauseReceipt = ingestionTestID(999) },
		"missing clear": func(f *ingestionFile) { f.Fences[0].ClearedBy = ingestionPtr(ingestionTestID(999)) },
		"lost boundary": func(f *ingestionFile) { f.Fences[0].KnownBoundary = nil },
		"wrong scope":   func(f *ingestionFile) { f.Fences[0].Scope = "unmapped" },
		"bad prior":     func(f *ingestionFile) { f.Fences[0].PriorRevision = 1 },
		"epoch hash":    func(f *ingestionFile) { f.Epochs[0].PolicyRevision = ingestionTestDigest(333) },
		"retire pending history mismatch": func(f *ingestionFile) {
			f.Requests[0].Outcome = "pending"
			f.Requests[0].ReceiptID = nil
			f.Epochs[0].Retired = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := cloneIngestion(base)
			change(&f)
			if _, err := decodeIngestion(ingestionJSON(f)); err == nil {
				t.Fatal("accepted dangling/modified history")
			}
		})
	}
	if !bytes.Equal(wire, b.data) {
		t.Fatal("validation rewrote store")
	}
	// Valid predecessor chain; fork and duplicate current epoch are refused even
	// when each independently recomputed epoch digest has the right shape.
	original := ingestionTestInitial()
	root := original.Epochs[0]
	root.Retired = true
	child := root
	child.Retired = false
	child.Predecessor = ingestionPtr(root.ID)
	child.SelectionRevision = ingestionTestID(55)
	child.ID = ingestionEpochHash(child)
	original.Epochs = []ingestionEpoch{root, child}
	sortIngestion(&original)
	init := &quotaIngestionStore{backend: &ingestionMemory{durable: true}, session: ingestionTestID(800)}
	create := func(in ingestionFile, id int) error {
		op := init.prepare(ingestionInput{Kind: "initialize", OperationID: ingestionTestID(id), StoreID: in.StoreID, Initial: &in, At: in.WallHighWater}, nil)
		_, err := init.apply(op)
		return err
	}
	if err := create(original, 801); err != nil {
		t.Fatal("valid chain", err)
	}
	for _, fork := range []bool{false, true} {
		f := cloneIngestion(original)
		extra := child
		extra.SelectionRevision = ingestionTestID(56)
		if !fork {
			extra.Predecessor = nil
		}
		extra.ID = ingestionEpochHash(extra)
		f.Epochs = append(f.Epochs, extra)
		f.Revision = 1
		f.Mutations = []ingestionMutation{{OperationID: ingestionTestID(900), Kind: "initialize", InputSHA256: ingestionTestDigest(900), AppliedRevision: 1}}
		sortIngestion(&f)
		if validateIngestion(f) == nil {
			t.Fatal("accepted fork/independent history bootstrap", fork)
		}
	}
}

func TestIngestionRetentionTombstonesAndBounds(t *testing.T) {
	s, b := ingestionTestStore(t)
	var first ingestionReceipt
	for i := 0; i < 12; i++ {
		_, tk := ingestionTestBegin(t, s, 200+i*4)
		_, r := ingestionTestCommit(t, s, tk, 202+i*4, nil)
		if i == 0 {
			first = r
		}
	}
	f := ingestionRead(t, s)
	if len(f.Receipts) != 9 || len(f.Seen) != 12 || !f.Seen[0].Retired {
		t.Fatal("unbounded/history identities lost", len(f.Receipts), len(f.Seen))
	}
	_, tk := ingestionTestBegin(t, s, 300)
	f = ingestionRead(t, s)
	old := first
	old.Sequence = tk.sequence
	old.SessionID = tk.session
	old.StartedAt = f.WallHighWater
	old.CompletedAt = f.WallHighWater
	old.ElapsedNS = 0
	old.Publication.AttemptAt = f.WallHighWater
	old.RecordSHA256 = ingestionReceiptHash(old)
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(302), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Receipt: &old}, tk)
	before := append([]byte(nil), b.data...)
	_, err := s.apply(op)
	ingestionAssertError(t, err, "replayRetired")
	if !bytes.Equal(before, b.data) {
		t.Fatal("reattached retired ID")
	}
	for name, wire := range map[string][]byte{
		"total bytes": bytes.Repeat([]byte(" "), ingestionMaxBytes+1),
		"depth":       []byte(strings.Repeat("[", 17) + strings.Repeat("]", 17)),
		"token":       []byte(`"` + strings.Repeat("a", ingestionMaxToken+2) + `"`),
		"collection":  []byte(`{"epochs":[` + strings.Repeat("0,", 256) + `0]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if ingestionPreflight(wire) == nil {
				t.Fatal("preallocation limit missed")
			}
		})
	}
	// Saturation cannot evict pending/tombstones to make room. Fill failed request
	// history with valid begin/end pairs; the next Begin must leave bytes intact.
	full := ingestionTestInitial()
	full.Revision = 1
	full.Mutations = []ingestionMutation{{OperationID: ingestionTestID(10000), Kind: "initialize", InputSHA256: ingestionTestDigest(1), AppliedRevision: 1}}
	for i := 0; i < 4096; i++ {
		id := ingestionTestID(20000 + i)
		full.Requests = append(full.Requests, ingestionRequest{Sequence: uint64(i + 1), SessionID: s.session, IntentID: id, Epoch: full.Epochs[0].ID, CreatedAt: full.WallHighWater, Outcome: "failed", Reason: "failedRead"})
		for j, kind := range []string{"begin", "fail"} {
			full.Revision++
			full.Mutations = append(full.Mutations, ingestionMutation{OperationID: ingestionTestID(30000 + i*2 + j), Kind: kind, InputSHA256: ingestionTestDigest(1), IntentID: ingestionPtr(id), AppliedRevision: full.Revision})
		}
	}
	full.NextSequence = 4097
	sortIngestion(&full)
	wire, err := encodeIngestion(full)
	if err != nil {
		t.Fatal("saturation fixture", err)
	}
	b.data = wire
	overflow := s.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(50000), IntentID: ingestionTestID(50001), StoreID: full.StoreID, ExpectedRevision: full.Revision, Epoch: full.Epochs[0].ID, At: full.WallHighWater}, nil)
	_, err = s.apply(overflow)
	ingestionAssertError(t, err, "storageFull")
	if !bytes.Equal(wire, b.data) {
		t.Fatal("saturation changed state")
	}
	if !ingestionOverflow(math.MaxUint64) || ingestionOverflow(math.MaxUint64-1) {
		t.Fatal("overflow check")
	}
}

func TestIngestionPinnedFenceAndClearReferencesSurviveCompaction(t *testing.T) {
	s, _ := ingestionTestStore(t)
	_, tk := ingestionTestBegin(t, s, 200)
	f := ingestionRead(t, s)
	r := ingestionTestReceipt(f, tk, 203)
	var d QuotaDetails
	_ = json.Unmarshal(r.Parsed, &d)
	fence := ingestionFence{Lineage: f.Epochs[0].ID, Allowance: f.Epochs[0].Allowance, Scope: f.Epochs[0].Scope, Bucket: "configured", Meter: "primary", Revision: 1, State: "exhausted", CauseReceipt: r.ID, KnownBoundary: d.Buckets[0].Windows[0].ResetsAt}
	op := s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: r.Publication.AttemptAt, Receipt: &r, Fences: []ingestionFence{fence}}, tk)
	if _, err := s.apply(op); err != nil {
		t.Fatal(err)
	}
	_, tk = ingestionTestBegin(t, s, 204)
	f = ingestionRead(t, s)
	clear := ingestionTestReceipt(f, tk, 207)
	next := fence
	next.Revision = 2
	next.PriorRevision = 1
	next.ClearedBy = ingestionPtr(clear.ID)
	next.State = "baseline"
	op = s.prepare(ingestionInput{Kind: "commit", OperationID: ingestionTestID(206), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: clear.Publication.AttemptAt, Receipt: &clear, Fences: []ingestionFence{next}}, tk)
	if _, err := s.apply(op); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		_, tk = ingestionTestBegin(t, s, 300+i*4)
		ingestionTestCommit(t, s, tk, 302+i*4, nil)
	}
	f = ingestionRead(t, s)
	for _, id := range []string{r.ID, clear.ID} {
		present := false
		for _, body := range f.Receipts {
			if body.ID == id {
				present = true
			}
		}
		if !present {
			t.Fatal("pruned pinned cause/clear", id)
		}
	}
	if f.Fences[1].CauseReceipt != r.ID || *f.Fences[1].ClearedBy != clear.ID || !f.Fences[1].KnownBoundary.Equal(*fence.KnownBoundary) {
		t.Fatal("history rewritten")
	}
	// Structural append, NOT B policy: the fictional clearing request is before
	// the known boundary. Retention does not authenticate or admit that policy.
	if !clear.StartedAt.Before(*fence.KnownBoundary) {
		t.Fatal("fixture stopped testing structural-only boundary")
	}
}

func TestIngestionSequenceClockAndOperationIsolation(t *testing.T) {
	s, b := ingestionTestStore(t)
	f := ingestionRead(t, s)
	before := append([]byte(nil), b.data...)
	rollback := s.prepare(ingestionInput{Kind: "compact", OperationID: ingestionTestID(200), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater.Add(-time.Nanosecond)}, nil)
	_, err := s.apply(rollback)
	ingestionAssertError(t, err, "clockOrRevisionUnavailable")
	if !bytes.Equal(before, b.data) {
		t.Fatal("rollback changed high water")
	}
	f.NextSequence = math.MaxUint64
	wire, err := encodeIngestion(f)
	if err != nil {
		t.Fatal(err)
	}
	b.data = wire
	op := s.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(201), IntentID: ingestionTestID(202), StoreID: f.StoreID, ExpectedRevision: f.Revision, Epoch: f.Epochs[0].ID, At: f.WallHighWater}, nil)
	if _, err = s.apply(op); err == nil || !bytes.Equal(wire, b.data) {
		t.Fatal("sequence wrapped", err)
	}
	op.input[0] = '!'
	if _, err = s.apply(op); err == nil {
		t.Fatal("operation digest mutation accepted")
	}
	if ingestionPreflight([]byte(strings.Repeat("1", ingestionMaxToken+1))) == nil {
		t.Fatal("numeric token allocation unbounded")
	}
	if ingestionPreflight([]byte(`{"parsed":"`+strings.Repeat("A", ((ingestionMaxEnvelope+3)/3)*4)+`"}`)) == nil {
		t.Fatal("base64 decoding allocation not bounded")
	}
}

func TestIngestionAllCollectionCapsBeforeDecode(t *testing.T) {
	for key, limit := range map[string]int{"epochs": 256, "requests": 4096, "receipts": 512, "fences": 4096, "seen": 16384, "mutations": 16384} {
		t.Run(key, func(t *testing.T) {
			raw := []byte(`{"` + key + `":[` + strings.Repeat("0,", limit) + `0]}`)
			if ingestionPreflight(raw) == nil {
				t.Fatal("collection allocation cap missed")
			}
		})
	}
}

// Independent review regressions retained verbatim.
func TestIndependentIngestionRejectImpossibleHistory(t *testing.T) {
	s, _ := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	ingestionTestCommit(t, s, ticket, 202, nil)
	f := ingestionRead(t, s)
	if _, err := decodeIngestion(ingestionJSON(f)); err != nil {
		t.Fatal("valid control", err)
	}
	// A commit at revision 2 followed by its Begin at revision 3 is impossible.
	f.Mutations[1], f.Mutations[2] = f.Mutations[2], f.Mutations[1]
	f.Mutations[1].AppliedRevision = 2
	f.Mutations[2].AppliedRevision = 3
	if _, err := decodeIngestion(ingestionJSON(f)); err == nil {
		t.Fatal("accepted Commit before Begin with canonical contiguous revision history")
	}
}

func TestIndependentIngestionRetainsLatestBody(t *testing.T) {
	s, _ := ingestionTestStore(t)
	_, ticket := ingestionTestBegin(t, s, 200)
	ingestionTestCommit(t, s, ticket, 202, nil)
	f := ingestionRead(t, s)
	if _, err := decodeIngestion(ingestionJSON(f)); err != nil {
		t.Fatal("valid control", err)
	}
	// The sole and latest success cannot legitimately have been compacted.
	f.Receipts = []ingestionReceipt{}
	f.Seen[0].Retired = true
	if _, err := decodeIngestion(ingestionJSON(f)); err == nil {
		t.Fatal("accepted compacted-away sole/latest success body")
	}
}

func TestIndependentIngestionRejectHistoricalPendingOverlap(t *testing.T) {
	s, _ := ingestionTestStore(t)
	for _, n := range []int{200, 204} {
		_, ticket := ingestionTestBegin(t, s, n)
		ingestionTestCommit(t, s, ticket, n+2, nil)
	}
	f := ingestionRead(t, s)
	if _, err := decodeIngestion(ingestionJSON(f)); err != nil {
		t.Fatal("valid serialized control", err)
	}
	// init, Begin(A), Begin(B), Commit(A), Commit(B): both terminal now,
	// but B began while A still owned this same canonical selection.
	f.Mutations[2], f.Mutations[3] = f.Mutations[3], f.Mutations[2]
	f.Mutations[2].AppliedRevision = 3
	f.Mutations[3].AppliedRevision = 4
	if _, err := decodeIngestion(ingestionJSON(f)); err == nil {
		t.Fatal("accepted two overlapping historical pending owners on one canonical selection")
	}
}

func TestIngestionHistoricalFailureCausalityAndReadBoundary(t *testing.T) {
	for _, firstFails := range []bool{false, true} {
		t.Run(fmt.Sprint(firstFails), func(t *testing.T) {
			s, b := ingestionTestStore(t)
			for i, n := range []int{200, 204} {
				_, tk := ingestionTestBegin(t, s, n)
				if i == 0 && firstFails {
					f := ingestionRead(t, s)
					op := s.prepare(ingestionInput{Kind: "fail", OperationID: ingestionTestID(n + 2), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Reason: "failedRead"}, tk)
					if _, err := s.apply(op); err != nil {
						t.Fatal(err)
					}
				} else {
					ingestionTestCommit(t, s, tk, n+2, nil)
				}
			}
			good := ingestionRead(t, s)
			// Both serialized success and serialized failure release the original
			// pending owner. Their historical order must be preserved on every read.
			for _, terminalBeforeBegin := range []bool{false, true} {
				f := cloneIngestion(good)
				a, z := 2, 3
				if terminalBeforeBegin {
					a, z = 1, 2
				}
				f.Mutations[a], f.Mutations[z] = f.Mutations[z], f.Mutations[a]
				for i := range f.Mutations {
					f.Mutations[i].AppliedRevision = uint64(i + 1)
				}
				wire := ingestionJSON(f)
				if _, err := decodeIngestion(wire); err == nil {
					t.Fatal("accepted impossible historical owner order", firstFails, terminalBeforeBegin)
				}
				b.data = wire
				if _, err := s.load(); err == nil {
					t.Fatal("read boundary accepted impossible history")
				}
				b.data = ingestionJSON(good)
			}
		})
	}
}

func TestIngestionHistoricalPendingOnDifferentSelections(t *testing.T) {
	b := &ingestionMemory{durable: true}
	s := &quotaIngestionStore{backend: b, session: ingestionTestID(100)}
	initial := ingestionTestInitial()
	second := initial.Epochs[0]
	second.SelectionID = ingestionTestID(20)
	second.Canonical = second.SelectionID
	second.Account = "fictional-account-b"
	second.Allowance = "fictional-allowance-b"
	second.ID = ingestionEpochHash(second)
	firstID := initial.Epochs[0].ID
	secondID := second.ID
	initial.Epochs = append(initial.Epochs, second)
	sortIngestion(&initial)
	init := s.prepare(ingestionInput{Kind: "initialize", OperationID: ingestionTestID(101), StoreID: initial.StoreID, Initial: &initial, At: initial.WallHighWater}, nil)
	if _, err := s.apply(init); err != nil {
		t.Fatal(err)
	}
	tickets := []*ingestionTicket{}
	for i, epoch := range []string{firstID, secondID} {
		f := ingestionRead(t, s)
		op := s.prepare(ingestionInput{Kind: "begin", OperationID: ingestionTestID(200 + i*4), IntentID: ingestionTestID(201 + i*4), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater.Add(time.Millisecond), Epoch: epoch}, nil)
		a, err := s.apply(op)
		if err != nil || a.Ticket == nil {
			t.Fatal("different selection excluded", err)
		}
		tickets = append(tickets, a.Ticket)
	}
	for i, tk := range tickets {
		ingestionTestCommit(t, s, tk, 202+i*4, nil)
	}
	f := ingestionRead(t, s)
	if len(f.Receipts) != 2 {
		t.Fatal("lost independent selection history")
	}
	// Each selection needs its own latest body, even though the other has a
	// globally newer sequence and a fully retained receipt.
	for _, r := range f.Receipts {
		mutated := cloneIngestion(f)
		bodies := mutated.Receipts[:0]
		for _, body := range mutated.Receipts {
			if body.ID != r.ID {
				bodies = append(bodies, body)
			}
		}
		mutated.Receipts = bodies
		for i := range mutated.Seen {
			if mutated.Seen[i].ID == r.ID {
				mutated.Seen[i].Retired = true
			}
		}
		if _, err := decodeIngestion(ingestionJSON(mutated)); err == nil {
			t.Fatal("lost one selection's latest body")
		}
	}
}

func TestIngestionLatestBodyAfterFailureRetirementAndOlderTombstone(t *testing.T) {
	for _, laterFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(laterFailure), func(t *testing.T) {
			s, b := ingestionTestStore(t)
			_, tk := ingestionTestBegin(t, s, 200)
			_, first := ingestionTestCommit(t, s, tk, 202, nil)
			_, tk = ingestionTestBegin(t, s, 204)
			f := ingestionRead(t, s)
			latest := first.ID
			if laterFailure {
				fail := s.prepare(ingestionInput{Kind: "fail", OperationID: ingestionTestID(206), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Reason: "failedRead"}, tk)
				if _, err := s.apply(fail); err != nil {
					t.Fatal(err)
				}
			} else {
				_, last := ingestionTestCommit(t, s, tk, 206, nil)
				latest = last.ID
			}
			f = ingestionRead(t, s)
			if !laterFailure {
				// Legitimate older tombstone survives strict decode; latest remains intact.
				older := cloneIngestion(f)
				older.Receipts = older.Receipts[1:]
				older.Seen[0].Retired = true
				if _, err := decodeIngestion(ingestionJSON(older)); err != nil {
					t.Fatal("legitimate older tombstone refused", err)
				}
			}
			retire := s.prepare(ingestionInput{Kind: "retireEpoch", OperationID: ingestionTestID(208), StoreID: f.StoreID, ExpectedRevision: f.Revision, At: f.WallHighWater, Epoch: f.Epochs[0].ID}, nil)
			if _, err := s.apply(retire); err != nil {
				t.Fatal(err)
			}
			f = ingestionRead(t, s)
			bodies := f.Receipts[:0]
			for _, r := range f.Receipts {
				if r.ID != latest {
					bodies = append(bodies, r)
				}
			}
			f.Receipts = bodies
			for i := range f.Seen {
				if f.Seen[i].ID == latest {
					f.Seen[i].Retired = true
				}
			}
			b.data = ingestionJSON(f)
			if _, err := s.load(); err == nil {
				t.Fatal("read accepted lost latest success after failure/retirement")
			}
		})
	}
}
