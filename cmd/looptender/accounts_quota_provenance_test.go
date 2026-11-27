package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/looptender/looptender/internal/accounts"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAccountQuotaProvenanceUsesPostReadClockNotProviderTime(t *testing.T) {
	dir, e := accountFixture(t, "codex")
	started := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	at := started
	run := func(context.Context, string, string, []string, string) ([]byte, error) {
		at = started.Add(2 * time.Second)
		// Unconsumed metadata cannot manufacture a provider measurement timestamp.
		return []byte(strings.Replace(quotaResponse, "{", `{"providerObservedAt":"2030-03-04T11:00:00Z",`, 1)), nil
	}
	v, err := accountQuotaRefresh(context.Background(), dir, e.ID, nil, run, os.Environ(), func() time.Time { return at })
	if err != nil || v.State != "reported" || v.Freshness != "fresh" || v.ObservedAt == nil || !v.ObservedAt.Equal(at) || v.RetrievedAt != nil {
		t.Fatalf("legacy local recency changed: %+v %v", v, err)
	}
	p := v.Summary.Provenance
	if p == nil || !p.RetrievedAt.Equal(started.Add(2*time.Second)) || p.ProviderObservedAt != nil {
		t.Fatalf("local post-read time became provider proof: %+v", p)
	}
}

// Exercise the actual unchanged JavaScript summary guard with producer output.
// The optional file is only a fictional native contract fixture, never runtime state.
func TestQuotaProvenanceOldConsumersAndNativeFixture(t *testing.T) {
	at := dockNow
	quotaList := &panelQuotaList{State: "available", Complete: true, Accounts: []panelQuotaAccount{}}
	for n, source := range []string{accounts.QuotaSource, accounts.ClaudeQuotaSource, accounts.AntigravityQuotaSource} {
		v := accounts.QuotaView{State: "reported", Source: source, Freshness: "provider_unknown", RetrievedAt: &at}
		provider := []string{"codex", "claude-code", "antigravity"}[n]
		if n == 0 {
			v.Freshness = "fresh"
			v.ObservedAt = &at
			v.RetrievedAt = nil
		}
		quotaList.Accounts = append(quotaList.Accounts, panelQuotaAccount{Label: "Example " + provider, Provider: provider, Summary: accounts.SummarizeQuota(v, at)})
	}
	payload := panelList{SchemaVersion: 1, ReadAt: at, Registry: "available", Complete: true, AllClear: true, Loops: []panelListLoop{}, AccountQuotas: quotaList}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("LOOPTENDER_QUOTA_PROVENANCE_FIXTURE"); path != "" {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: old JS acceptance not executed")
	}
	js := strings.ReplaceAll(dockUIText(t, "dock.js"), "\r\n", "\n")
	source := "const assert=require('node:assert/strict');\n" + jsFunction(t, js, "function accountQuotaSummary(c) {") + "\n}\nconst input=" + string(raw) + ";\n"
	source += `for(const item of input.accountQuotas.accounts){
  const s=item.summary,c={provider:item.provider,quota:{summary:s}};
  assert.equal(s.provenance.version,1);
  assert.equal(s.provenance.providerObservedAt,null);
  assert.equal(s.provenance.retrievedAt,s.retrievedAt||s.observedAt);
  assert.equal(accountQuotaSummary(c),s); // new producer -> old consumer
  const old={...s};delete old.provenance;c.quota.summary=old;
  assert.equal(accountQuotaSummary(c),old); // old producer -> old consumer
  const conflict={...s,observedAt:s.provenance.retrievedAt,retrievedAt:s.provenance.retrievedAt};
  c.quota.summary=conflict;assert.equal(accountQuotaSummary(c),null);
  c.quota.summary={...s,source:'unsupported'};assert.equal(accountQuotaSummary(c),null);
 }
 console.log('Unchanged JS decoder PASS: all source additive output, old summary fallback, legacy source/time conflicts');`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=commonjs", "-")
	cmd.Stdin = bytes.NewBufferString(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("old web consumer: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func TestQuotaProvenanceIndependentOfCollectionMode(t *testing.T) {
	for _, age := range []time.Duration{time.Minute, 72 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			dir, e := accountFixture(t, "codex")
			now := dockNow
			success := now.Add(-age)
			run := func(context.Context, string, string, []string, string) ([]byte, error) {
				return []byte(quotaResponse), nil
			}
			if _, err := accountQuotaRefresh(context.Background(), dir, e.ID, nil, run, os.Environ(), func() time.Time { return success }); err != nil {
				t.Fatal(err)
			}
			catalog, _ := accounts.Load(dir)
			check := func(want string) {
				v := accounts.Project(catalog, now)
				accounts.AttachQuota(&v, accounts.QuotaViews(dir, catalog, os.Environ(), now))
				attachAccountUsageUpdating(&v, dir, os.Environ(), now)
				card := v.Cards[0]
				if card.UsageUpdating.State != want {
					t.Fatalf("want %s: %+v", want, card.UsageUpdating)
				}
				p := card.Quota.Summary.Provenance
				if p == nil || !p.RetrievedAt.Equal(success) || p.ProviderObservedAt != nil {
					t.Fatalf("%s changed retrieval/provider provenance: %+v", want, p)
				}
				expectedFreshness := "fresh"
				if age > 30*time.Minute {
					expectedFreshness = "stale"
				}
				if card.Quota.Freshness != expectedFreshness {
					t.Fatalf("%s changed legacy evidence age: %+v", want, card.Quota)
				}
			}
			check("off")
			enableUsageFixture(t, dir, e, now)
			check("waiting")
			if err := accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.SchedulerAt = &now; return nil }); err != nil {
				t.Fatal(err)
			}
			check("enabled")
			if err := accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error { f.Entries[0].Failures = 1; return nil }); err != nil {
				t.Fatal(err)
			}
			check("backoff")
		})
	}
}
