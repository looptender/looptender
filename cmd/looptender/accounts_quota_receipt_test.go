package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/testexec"
)

const receiptQuotaResult = `{"rateLimits":{"primary":{"usedPercent":37.125,"windowDurationMins":300,"resetsAt":1898859600},"secondary":{"usedPercent":100}},"ordinaryUsageAllowed":false}`

func receiptFixture(t *testing.T, reply string) (string, accounts.Entry) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell RPC stand-in; portable clock/copy/carrier tests remain")
	}
	dir, e := accountFixture(t, "codex")
	testexec.Script(t, e.CLI, `#!/bin/sh
[ "$1" = app-server ] || exit 10
read initialize
case "$initialize" in *'"method":"initialize"'*) ;; *) exit 11;; esac
: > "$PWD/receipt-initialized"
printf '%s\n' '{"id":1,"result":{}}'
read initialized
case "$initialized" in *'"method":"initialized"'*) ;; *) exit 12;; esac
read request
: > "$PWD/receipt-request-read"
case "$request" in *'"method":"account/rateLimits/read"'*'"excludeResetCreditDetails":true'*) ;; *'"excludeResetCreditDetails":true'*'"method":"account/rateLimits/read"'*) ;; *) exit 13;; esac
printf '%s\n' '`+reply+`'
`)
	if err := accounts.Update(dir, func(f *accounts.File) error {
		e.Mode, e.Directory = "explicit_directory", e.Workdir
		e.Context = accounts.Context(*f, e, os.Environ())
		f.Entries[0] = e
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return dir, e
}
func receiptClock(ticks ...quotaReceiptTick) (quotaReceiptClock, *atomic.Int32) {
	count := &atomic.Int32{}
	return func() quotaReceiptTick {
		i := int(count.Add(1)) - 1
		if i >= len(ticks) {
			i = len(ticks) - 1
		}
		return ticks[i]
	}, count
}
func normalReceiptClock() quotaReceiptClock {
	at := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	clock, _ := receiptClock(quotaReceiptTick{at, 0}, quotaReceiptTick{at.Add(100 * time.Millisecond), 100 * time.Millisecond}, quotaReceiptTick{at.Add(time.Second), time.Second})
	return clock
}
func receiptPublicationTime() time.Time { return time.Date(2030, 3, 4, 12, 0, 0, 500000000, time.UTC) }

func newUnitReceiptTrace(clock quotaReceiptClock) *quotaReceiptTrace {
	t := newQuotaReceiptTrace(clock)
	t.expected = &quotaReceiptExecution{cli: "fixture-reader", workdir: "fixture-workdir"}
	return t
}
func beginUnitQuotaRequest(ctx context.Context, method string, params any) *quotaRequestTicket {
	return beginQuotaRequest(ctx, "fixture-reader", nil, "fixture-workdir", method, params)
}

func TestQuotaReceiptRealFakeTransportAndImmutableRetention(t *testing.T) {
	dir, entry := receiptFixture(t, `{"id":2,"result":`+receiptQuotaResult+`}`)
	baseClock := normalReceiptClock()
	calls := 0
	clock := func() quotaReceiptTick {
		calls++
		if calls == 1 {
			if _, err := os.Stat(filepath.Join(entry.Workdir, "receipt-initialized")); err != nil {
				t.Fatal("start stamped initialization", err)
			}
			if _, err := os.Stat(filepath.Join(entry.Workdir, "receipt-request-read")); !os.IsNotExist(err) {
				t.Fatal("start not before actual request write", err)
			}
		}
		return baseClock()
	}
	result, err := accountQuotaRefreshReceipt(context.Background(), dir, entry.ID, nil, accountQuotaRead, os.Environ(), receiptPublicationTime, clock)
	if err != nil || result.receipt == nil || calls != 3 {
		t.Fatalf("receipt missing: %v %s", err, result.receiptReason)
	}
	r := result.receipt
	start := receiptPublicationTime().Add(-500 * time.Millisecond)
	if !r.startedAt.Equal(start) || !r.completedAt.Equal(start.Add(100*time.Millisecond)) || r.elapsed != 100*time.Millisecond || r.providerObservedAt() != nil || r.id == [16]byte{} || r.reader != "codex-account-rateLimits-read/v1" {
		t.Fatal("incorrect request timing/source", r.startedAt, r.completedAt, r.elapsed)
	}
	selected, err := accountQuotaCapture(dir, entry.ID, os.Environ())
	if err != nil || !r.binding.same(selected) {
		t.Fatal("receipt not bound", err)
	}
	details := r.detailsCopy()
	if details == nil || len(details.Buckets) != 1 || details.Buckets[0].Windows[0].UsedPercent != 37.125 || details.Buckets[0].Windows[1].UsedPercent != 100 || details.Buckets[0].Windows[1].ResetsAt != nil || details.OrdinaryUsageAllowed == nil || *details.OrdinaryUsageAllowed {
		t.Fatal("parsed values/presence lost", details)
	}
	original := r.parsedCopy()
	if sha256.Sum256(original) != r.digest {
		t.Fatal("wrong immutable digest")
	}
	details.Buckets[0].Windows[0].UsedPercent = 99
	*details.Buckets[0].Windows[0].DurationMinutes = 1
	*details.Buckets[0].Windows[0].ResetsAt = time.Time{}
	*details.OrdinaryUsageAllowed = true
	changed := r.parsedCopy()
	changed[0] = '!'
	result.view.Details.Buckets[0].Windows[0].UsedPercent = 88
	if !bytes.Equal(r.parsedCopy(), original) || r.detailsCopy().Buckets[0].Windows[0].UsedPercent != 37.125 {
		t.Fatal("caller mutation changed receipt")
	}
	// Ordinary cache projection does not contain a receipt or mint another one.
	catalog, _ := accounts.Load(dir)
	view := accounts.QuotaViews(dir, catalog, os.Environ(), receiptPublicationTime())[entry.ID]
	wire, _ := json.Marshal(view)
	for _, private := range []string{"receipt", "startedAt", "elapsed", "readerContract"} {
		if bytes.Contains(wire, []byte(private)) {
			t.Fatal("public wire grew internal receipt", private)
		}
	}
	cacheBefore, _ := os.ReadFile(filepath.Join(dir, accounts.QuotaName))
	failed := func(context.Context, string, string, []string, string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	later := func() time.Time { return receiptPublicationTime().Add(time.Minute) }
	bad, err := accountQuotaRefreshReceipt(context.Background(), dir, entry.ID, nil, failed, os.Environ(), later, normalReceiptClock())
	if err != nil || bad.receipt != nil || bad.view.FailedRefreshAt == nil || bad.view.ObservedAt == nil || !bad.view.ObservedAt.Equal(receiptPublicationTime()) {
		t.Fatal("failed attempt changed retained success", err, bad.receiptReason)
	}
	cacheAfter, _ := accounts.LoadQuota(dir)
	var old accounts.QuotaFile
	if json.Unmarshal(cacheBefore, &old) != nil || !reflect.DeepEqual(old.Entries[0].Success, cacheAfter.Entries[0].Success) || !bytes.Equal(r.parsedCopy(), original) {
		t.Fatal("failure mutated historical receipt/success")
	}
}

func TestQuotaReceiptTransportParseAndBindingNegatives(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"wrong ID", `{"id":3,"result":` + receiptQuotaResult + `}`},
		{"duplicate ID", `{"id":2,"id":2,"result":` + receiptQuotaResult + `}`},
		{"duplicate payload", `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":1,"usedPercent":2}}}}`},
		{"partial", `{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":20},"unknown":true}}}`},
		{"missing meters", `{"id":2,"result":{"rateLimits":{"credits":{"hasCredits":true,"unlimited":false}}}}`},
		{"failed", `{"id":2,"error":{"code":-32601}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, e := receiptFixture(t, tc.reply)
			result, _ := accountQuotaRefreshReceipt(context.Background(), dir, e.ID, nil, accountQuotaRead, os.Environ(), receiptPublicationTime, normalReceiptClock())
			if result.receipt != nil {
				t.Fatal("invalid transport minted receipt", tc.name)
			}
			if tc.name == "partial" && result.receiptReason != "partial_response" {
				t.Fatal("partial not classified", result.receiptReason)
			}
			if tc.name == "missing meters" && result.receiptReason != "no_meters" {
				t.Fatal("missing meter not classified", result.receiptReason)
			}
		})
	}
	for _, kind := range []string{"selection", "executable", "persistence", "write_failed", "response_changed", "no_transport", "superseded_success", "superseded_failure", "transport_env", "transport_workdir", "transport_cli", "publication_busy"} {
		t.Run(kind, func(t *testing.T) {
			dir, e := receiptFixture(t, `{"id":2,"result":`+receiptQuotaResult+`}`)
			var savedBefore []byte
			if kind == "write_failed" {
				if os.Geteuid() == 0 {
					t.Skip("write-denial fixture needs ordinary user permissions")
				}
				fake := func(context.Context, string, string, []string, string) ([]byte, error) {
					return []byte(receiptQuotaResult), nil
				}
				_, err := accountQuotaRefresh(context.Background(), dir, e.ID, nil, fake, os.Environ(), func() time.Time { return receiptPublicationTime().Add(-time.Second) })
				if err != nil {
					t.Fatal(err)
				}
				savedBefore, err = os.ReadFile(filepath.Join(dir, accounts.QuotaName))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			}
			run := func(ctx context.Context, provider, cli string, env []string, wd string) ([]byte, error) {
				if kind == "no_transport" {
					return []byte(receiptQuotaResult), nil
				}
				switch kind {
				case "transport_env":
					env = append(append([]string(nil), env...), "OPENAI_API_KEY=fictional-override")
				case "transport_workdir":
					var err error
					wd, err = filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
				case "transport_cli":
					copy := cli + "-other"
					data, err := os.ReadFile(cli)
					if err != nil {
						t.Fatal(err)
					}
					testexec.Script(t, copy, string(data))
					cli = copy
				}
				raw, err := accountQuotaRead(ctx, provider, cli, env, wd)
				switch kind {
				case "selection":
					if err := accounts.Update(dir, func(f *accounts.File) error { f.Entries[0].Revision = strings.Repeat("a", 32); return nil }); err != nil {
						t.Fatal(err)
					}
				case "executable":
					if err := os.WriteFile(e.CLI, []byte("replaced fictional executable"), 0700); err != nil {
						t.Fatal(err)
					}
				case "write_failed":
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
				case "persistence":
					if err := os.Mkdir(filepath.Join(dir, accounts.QuotaName), 0700); err != nil {
						t.Fatal(err)
					}
				case "response_changed":
					raw = bytes.ReplaceAll(raw, []byte("37.125"), []byte("38.125"))
				}
				return raw, err
			}
			var held *lock.Lock
			defer func() {
				if held != nil {
					held.Release()
				}
			}()
			n := 0
			now := func() time.Time {
				n++
				if n == 2 && kind == "publication_busy" {
					var err error
					held, err = lock.AcquireNamed(dir, "accounts")
					if err != nil {
						t.Fatal(err)
					}
				}
				if n == 2 && strings.HasPrefix(kind, "superseded_") {
					selected, err := accountQuotaCapture(dir, e.ID, os.Environ())
					if err != nil {
						t.Fatal(err)
					}
					a := accounts.QuotaAttempt{At: receiptPublicationTime().Add(time.Millisecond)}
					if kind == "superseded_failure" {
						a.Reason = "timeout"
					} else {
						a.Details, _ = accountQuotaDetailsParse([]byte(receiptQuotaResult), nil)
						a.Details.Buckets[0].Windows[0].UsedPercent = 12
					}
					if err := accounts.PublishQuota(dir, e, selected.Bound, a, os.Environ(), func() error { return nil }); err != nil {
						t.Fatal(err)
					}
				}
				return receiptPublicationTime().Add(time.Duration(n-1) * time.Millisecond)
			}
			result, err := accountQuotaRefreshReceipt(context.Background(), dir, e.ID, nil, run, os.Environ(), now, normalReceiptClock())
			if result.receipt != nil {
				t.Fatal("unconfirmed publication minted receipt", kind)
			}
			if kind == "selection" || kind == "executable" || kind == "persistence" || kind == "write_failed" {
				if err == nil {
					t.Fatal("expected publication fence", kind)
				}
			}
			if kind == "write_failed" {
				after, readErr := os.ReadFile(filepath.Join(dir, accounts.QuotaName))
				if readErr != nil || !bytes.Equal(after, savedBefore) {
					t.Fatal("failed write changed saved success", readErr)
				}
			}
			if kind == "publication_busy" && (err != nil || result.view.State != "reported" || result.receiptReason != "publication_unconfirmed") {
				t.Fatal("contention changed view or minted receipt", err, result.receiptReason)
			}
			if strings.HasPrefix(kind, "transport_") && result.receiptReason != "transport_binding_mismatch" {
				t.Fatal("wrong transport binding result", result.receiptReason, err)
			}
			if strings.HasPrefix(kind, "superseded_") && result.receiptReason != "publication_superseded" {
				t.Fatal("wrong superseded result", result.receiptReason, err)
			}
		})
	}
}

func TestQuotaReceiptPortableClockAndCarrier(t *testing.T) {
	at := receiptPublicationTime()
	for _, tc := range []struct {
		name string
		end  quotaReceiptTick
	}{
		{"wall rollback", quotaReceiptTick{at.Add(-time.Nanosecond), time.Millisecond}},
		{"monotonic rollback", quotaReceiptTick{at.Add(time.Millisecond), -time.Nanosecond}},
		{"wall jump", quotaReceiptTick{at.Add(2 * time.Second), time.Millisecond}},
		{"over deadline", quotaReceiptTick{at.Add(5*time.Second + time.Nanosecond), 5*time.Second + time.Nanosecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock, _ := receiptClock(quotaReceiptTick{at, 0}, tc.end)
			trace := newUnitReceiptTrace(clock)
			ctx := context.WithValue(context.Background(), quotaReceiptContextKey{}, trace)
			ticket := beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true})
			ticket.wrote()
			ticket.received([]byte(receiptQuotaResult))
			details, _ := accountQuotaDetailsParse([]byte(receiptQuotaResult), nil)
			trace.parsedReply([]byte(receiptQuotaResult), details, "")
			if trace.reason != "clock_unavailable" || len(trace.parsed) != 0 {
				t.Fatal("bad clock accepted", trace.reason)
			}
		})
	}
	clock, count := receiptClock(quotaReceiptTick{at, 0})
	trace := newUnitReceiptTrace(clock)
	ctx := context.WithValue(context.Background(), quotaReceiptContextKey{}, trace)
	if beginUnitQuotaRequest(context.Background(), "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true}) != nil || beginUnitQuotaRequest(ctx, "initialize", nil) != nil || beginUnitQuotaRequest(ctx, "account/read", map[string]bool{"refreshToken": false}) != nil || beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": false}) != nil || count.Load() != 0 {
		t.Fatal("wrong method or absent carrier stamped")
	}
	var wg sync.WaitGroup
	var claims atomic.Int32
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true}) != nil {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 || trace.reason != "transport_reused" || count.Load() != 1 {
		t.Fatal("carrier reused or raced", claims.Load(), trace.reason)
	}
}

func TestQuotaReceiptFailedWriteCannotMint(t *testing.T) {
	trace := newUnitReceiptTrace(normalReceiptClock())
	ctx := context.WithValue(context.Background(), quotaReceiptContextKey{}, trace)
	ticket := beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true})
	// A start alone is provisional, even if a fabricated response arrives.
	ticket.received([]byte(receiptQuotaResult))
	details, _ := accountQuotaDetailsParse([]byte(receiptQuotaResult), nil)
	trace.parsedReply([]byte(receiptQuotaResult), details, "")
	if trace.reason != "transport_or_parse_unconfirmed" {
		t.Fatal("failed write accepted", trace.reason)
	}
}

func TestQuotaReceiptPortableDeepCopyAndPartialMeaning(t *testing.T) {
	raw := []byte(receiptQuotaResult)
	details, reason := accountQuotaDetailsParse(raw, nil)
	if reason != "" {
		t.Fatal(reason)
	}
	trace := newUnitReceiptTrace(normalReceiptClock())
	ctx := context.WithValue(context.Background(), quotaReceiptContextKey{}, trace)
	ticket := beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true})
	ticket.wrote()
	ticket.received(raw)
	trace.parsedReply(raw, details, "")
	if trace.reason != "" || len(trace.parsed) == 0 {
		t.Fatal(trace.reason)
	}
	saved := append([]byte(nil), trace.parsed...)
	// Neither later parser-buffer/value mutation nor returned accessors can change
	// the immutable parsed snapshot retained by a successful capture.
	raw[0] = '!'
	details.Buckets[0].Windows[0].UsedPercent = 77
	*details.Buckets[0].Windows[0].DurationMinutes = 7
	*details.Buckets[0].Windows[0].ResetsAt = time.Time{}
	*details.OrdinaryUsageAllowed = true
	receipt := &accountQuotaReceipt{parsed: append([]byte(nil), trace.parsed...), digest: sha256.Sum256(trace.parsed)}
	copied := receipt.detailsCopy()
	copied.Buckets[0].Windows[0].UsedPercent = 88
	b := receipt.parsedCopy()
	b[0] = '!'
	if !bytes.Equal(trace.parsed, saved) || !bytes.Equal(receipt.parsedCopy(), saved) || receipt.detailsCopy().Buckets[0].Windows[0].UsedPercent != 37.125 || receipt.digest != sha256.Sum256(saved) {
		t.Fatal("capture/accessor mutated immutable values")
	}
	// Missing optional meter/reset fields are retained as missing, not expanded
	// into a claim that every model/window is covered. No complete flag exists.
	one := []byte(`{"rateLimits":{"primary":{"usedPercent":0}}}`)
	d, _ := accountQuotaDetailsParse(one, nil)
	next := newUnitReceiptTrace(normalReceiptClock())
	c := context.WithValue(context.Background(), quotaReceiptContextKey{}, next)
	x := beginUnitQuotaRequest(c, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true})
	x.wrote()
	x.received(one)
	next.parsedReply(one, d, "")
	if next.reason != "" || len(d.Buckets[0].Windows) != 1 || d.Buckets[0].Windows[0].ResetsAt != nil {
		t.Fatal("missing meter/reset invented", next.reason)
	}
}

func TestQuotaReceiptDefaultClockAndPublicationClock(t *testing.T) {
	for _, kind := range []string{"real", "publication_rollback", "publication_monotonic_rollback", "legacy_future_at"} {
		t.Run(kind, func(t *testing.T) {
			dir, e := receiptFixture(t, `{"id":2,"result":`+receiptQuotaResult+`}`)
			clock := quotaReceiptClock(nil)
			now := time.Now
			if kind != "real" {
				at := receiptPublicationTime().Add(-500 * time.Millisecond)
				check := quotaReceiptTick{at.Add(time.Second), time.Second}
				if kind == "publication_rollback" {
					check.wall = at
				}
				if kind == "publication_monotonic_rollback" {
					check.monotonic = 0
				}
				clock, _ = receiptClock(quotaReceiptTick{at, 0}, quotaReceiptTick{at.Add(100 * time.Millisecond), 100 * time.Millisecond}, check)
				now = receiptPublicationTime
				if kind == "legacy_future_at" {
					now = func() time.Time { return at.Add(2 * time.Second) }
				}
			}
			got, err := accountQuotaRefreshReceipt(context.Background(), dir, e.ID, nil, accountQuotaRead, os.Environ(), now, clock)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "real" {
				if got.receipt == nil || got.receipt.elapsed < 0 || got.receipt.elapsed > 5*time.Second || got.receipt.startedAt.After(got.receipt.completedAt) {
					t.Fatal("default clock receipt missing", got.receiptReason)
				}
			} else if got.receipt != nil || got.receiptReason != "clock_unavailable" {
				t.Fatal("publication clock accepted", kind, got.receiptReason)
			}
		})
	}
}

func TestQuotaReceiptExactParsedFloatBits(t *testing.T) {
	for _, value := range []float64{0, math.Copysign(0, -1), 0.1, 37.125, 99.99999999999999, 100, math.SmallestNonzeroFloat64} {
		raw := []byte(`{"rateLimits":{"primary":{"usedPercent":` + strconv.FormatFloat(value, 'g', -1, 64) + `}}}`)
		details, reason := accountQuotaDetailsParse(raw, nil)
		if reason != "" {
			t.Fatal(reason)
		}
		trace := newUnitReceiptTrace(normalReceiptClock())
		ctx := context.WithValue(context.Background(), quotaReceiptContextKey{}, trace)
		ticket := beginUnitQuotaRequest(ctx, "account/rateLimits/read", map[string]bool{"excludeResetCreditDetails": true})
		ticket.wrote()
		ticket.received(raw)
		trace.parsedReply(raw, details, "")
		if trace.reason != "" {
			t.Fatal(trace.reason)
		}
		r := &accountQuotaReceipt{parsed: trace.parsed}
		if math.Float64bits(r.detailsCopy().Buckets[0].Windows[0].UsedPercent) != math.Float64bits(details.Buckets[0].Windows[0].UsedPercent) {
			t.Fatal("parsed bits changed", value)
		}
	}
}
