package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/lock"
)

// This is invocation-local transport evidence, never identity, provider freshness,
// durable consumption or admission authority. No receipt is encoded or persisted.
type accountQuotaReceipt struct {
	id                     [16]byte
	reader                 string
	binding                accountQuotaSelection
	startedAt, completedAt time.Time
	elapsed                time.Duration
	parsed                 []byte
	digest                 [32]byte
}

func (r *accountQuotaReceipt) detailsCopy() *accounts.QuotaDetails {
	if r == nil {
		return nil
	}
	var details accounts.QuotaDetails
	if json.Unmarshal(r.parsed, &details) != nil {
		return nil
	}
	return &details
}
func (r *accountQuotaReceipt) parsedCopy() []byte {
	if r == nil {
		return nil
	}
	return append([]byte(nil), r.parsed...)
}

// Current readers never supply a provider measurement timestamp.
func (r *accountQuotaReceipt) providerObservedAt() *time.Time { return nil }

type accountQuotaRefreshResult struct {
	view          accounts.QuotaView
	receipt       *accountQuotaReceipt
	receiptReason string // internal fixed diagnostic; never changes public QuotaView
}
type quotaReceiptTick struct {
	wall      time.Time
	monotonic time.Duration
}
type quotaReceiptClock func() quotaReceiptTick

func realQuotaReceiptClock() quotaReceiptClock {
	origin := time.Now()
	return func() quotaReceiptTick {
		at := time.Now()
		return quotaReceiptTick{at.UTC(), at.Sub(origin)}
	}
}

type quotaReceiptContextKey struct{}
type quotaReceiptExecution struct {
	cli, workdir string
	environment  []string
}
type quotaRequestTicket struct{ trace *quotaReceiptTrace }

// One carrier belongs to exactly one refresh. Mutex-protected single claim
// makes reuse/concurrent RPCs invalidate the carrier rather than mix evidence.
type quotaReceiptTrace struct {
	mu                                      sync.Mutex
	expected                                *quotaReceiptExecution
	clock                                   quotaReceiptClock
	id                                      [16]byte
	claimed, written, correlated, finalized bool
	reason                                  string
	start, finish                           quotaReceiptTick
	responseDigest                          [32]byte
	parsed                                  []byte
}

func newQuotaReceiptTrace(clock quotaReceiptClock) *quotaReceiptTrace {
	if clock == nil {
		clock = realQuotaReceiptClock()
	}
	t := &quotaReceiptTrace{clock: clock}
	if _, err := rand.Read(t.id[:]); err != nil {
		t.reason = "receipt_id_unavailable"
	}
	return t
}
func beginQuotaRequest(ctx context.Context, cli string, environment []string, workdir, method string, params any) *quotaRequestTicket {
	t, _ := ctx.Value(quotaReceiptContextKey{}).(*quotaReceiptTrace)
	p, ok := params.(map[string]bool)
	if t == nil || method != "account/rateLimits/read" || !ok || len(p) != 1 || !p["excludeResetCreditDetails"] {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.claimed {
		t.reason = "transport_reused"
		return nil
	}
	t.claimed = true
	if t.expected == nil || t.expected.cli != cli || t.expected.workdir != workdir || !slices.Equal(t.expected.environment, environment) {
		t.reason = "transport_binding_mismatch"
		return nil
	}
	// Immediately before the write. A successful write and correlated response
	// are both required before this provisional timestamp can yield a receipt.
	t.start = t.clock()
	return &quotaRequestTicket{trace: t}
}
func (ticket *quotaRequestTicket) wrote() {
	if ticket == nil {
		return
	}
	ticket.trace.mu.Lock()
	ticket.trace.written = true
	ticket.trace.mu.Unlock()
}
func (ticket *quotaRequestTicket) received(raw []byte) {
	if ticket == nil {
		return
	}
	t := ticket.trace
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.written {
		t.correlated = true
		t.responseDigest = sha256.Sum256(raw)
	}
}
func (t *quotaReceiptTrace) parsedReply(raw []byte, details *accounts.QuotaDetails, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reason != "" {
		return
	}
	if reason != "" || !t.claimed || !t.written || !t.correlated {
		t.reason = "transport_or_parse_unconfirmed"
		return
	}
	if sha256.Sum256(raw) != t.responseDigest {
		t.reason = "response_changed"
		return
	}
	if details == nil || !accounts.ValidQuotaDetails(details) {
		t.reason = "parsed_detail_unavailable"
		return
	}
	if details.Partial || details.Omitted != 0 {
		t.reason = "partial_response"
		return
	}
	meters := 0
	for _, bucket := range details.Buckets {
		if bucket.Partial {
			t.reason = "partial_response"
			return
		}
		meters += len(bucket.Windows)
		if bucket.Individual != nil {
			meters++
		}
	}
	if meters == 0 {
		t.reason = "no_meters"
		return
	}
	data, err := json.Marshal(details)
	if err != nil {
		t.reason = "parsed_detail_unavailable"
		return
	}
	// Completion includes the correlated response's strict parse and immutable
	// parsed-value copy. This says nothing about exhaustive model applicability.
	t.finish = t.clock()
	wall := t.finish.wall.Sub(t.start.wall)
	elapsed := t.finish.monotonic - t.start.monotonic
	if t.start.wall.IsZero() || t.finish.wall.IsZero() || t.start.wall.Year() < 2000 || t.finish.wall.Year() > 9999 || wall < 0 || t.start.monotonic < 0 || t.finish.monotonic < 0 || elapsed < 0 || elapsed > 5*time.Second || wall-elapsed > time.Second || elapsed-wall > time.Second {
		t.reason = "clock_unavailable"
		return
	}
	t.parsed = data // json.Marshal already made an owned deep snapshot before finish
}
func (t *quotaReceiptTrace) publish(dir string, selected accountQuotaSelection, attempt accounts.QuotaAttempt, env []string) (*accountQuotaReceipt, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finalized {
		return nil, "carrier_closed"
	}
	t.finalized = true
	if t.reason != "" {
		return nil, t.reason
	}
	if len(t.parsed) == 0 {
		return nil, "transport_or_parse_unconfirmed"
	}
	// Publication already succeeded. Recheck its exact retained success/attempt
	// under the same catalog lock as cooperating publishers, never provider IO.
	g, err := lock.AcquireNamed(dir, "accounts")
	if err != nil {
		return nil, "publication_unconfirmed"
	}
	defer g.Release()
	fresh, err := accountQuotaCapture(dir, selected.Entry.ID, env)
	if err != nil || !selected.same(fresh) {
		return nil, "selection_changed"
	}
	cache, err := accounts.LoadQuota(dir)
	if err != nil {
		return nil, "publication_unconfirmed"
	}
	confirmed := false
	for _, entry := range cache.Entries {
		if entry.ID == selected.Entry.ID && entry.Revision == selected.Entry.Revision && entry.Context == selected.Bound && entry.Success != nil && reflect.DeepEqual(*entry.Success, attempt) && reflect.DeepEqual(entry.Attempt, attempt) {
			confirmed = true
		}
	}
	if !confirmed {
		return nil, "publication_superseded"
	}
	check := t.clock()
	if check.wall.Before(t.finish.wall) || check.monotonic < t.finish.monotonic || attempt.At.Before(t.finish.wall) || attempt.At.After(check.wall) {
		return nil, "clock_unavailable"
	}
	return &accountQuotaReceipt{id: t.id, reader: "codex-account-rateLimits-read/v1", binding: selected, startedAt: t.start.wall.UTC(), completedAt: t.finish.wall.UTC(), elapsed: t.finish.monotonic - t.start.monotonic, parsed: append([]byte(nil), t.parsed...), digest: sha256.Sum256(t.parsed)}, ""
}
