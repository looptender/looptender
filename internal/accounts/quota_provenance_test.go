package accounts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaSummaryRetrievalNeverProviderObservation(t *testing.T) {
	at := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	for _, source := range []string{QuotaSource, ClaudeQuotaSource, AntigravityQuotaSource} {
		t.Run(source, func(t *testing.T) {
			v := QuotaView{State: "reported", Source: source, Freshness: "provider_unknown", RetrievedAt: &at}
			if source == QuotaSource {
				v.Freshness = "fresh"
				v.ObservedAt = &at
				v.RetrievedAt = nil
			}
			before, _ := json.Marshal(v)
			summary := SummarizeQuota(v, at.Add(time.Minute))
			p := summary.Provenance
			if p == nil || p.Version != 1 || !p.RetrievedAt.Equal(at) || p.ProviderObservedAt != nil {
				t.Fatalf("local retrieval became provider observation: %+v", summary)
			}
			if summary.Freshness != v.Freshness || summary.ObservedAt != v.ObservedAt || summary.RetrievedAt != v.RetrievedAt {
				t.Fatal("legacy contract changed")
			}
			raw, err := json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Provenance map[string]json.RawMessage `json:"provenance"`
			}
			if json.Unmarshal(raw, &wire) != nil || len(wire.Provenance) != 3 || string(wire.Provenance["providerObservedAt"]) != "null" {
				t.Fatalf("unknown provider time must be an explicit null: %s", raw)
			}
			after, _ := json.Marshal(v)
			if !bytes.Equal(before, after) {
				t.Fatal("projection mutated its input")
			}
		})
	}
	for _, v := range []QuotaView{
		{State: "unavailable", Freshness: "unavailable"},
		{State: "reported", Source: QuotaSource, Freshness: "fresh", RetrievedAt: &at},
		{State: "reported", Source: ClaudeQuotaSource, Freshness: "fresh", ObservedAt: &at},
		{State: "reported", Source: "unknown", Freshness: "fresh", ObservedAt: &at},
	} {
		summary := SummarizeQuota(v, at)
		raw, err := json.Marshal(summary)
		if err != nil || summary.Provenance != nil || bytes.Contains(raw, []byte(`"provenance"`)) {
			t.Fatalf("unavailable/invalid source manufactured provenance: %s %v", raw, err)
		}
	}
}

func TestQuotaProvenanceV1CannotPublishProviderTime(t *testing.T) {
	at := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	for _, p := range []QuotaProvenance{
		{Version: 1, RetrievedAt: at, ProviderObservedAt: &at},
		{Version: 2, RetrievedAt: at},
		{Version: 1},
	} {
		if raw, err := json.Marshal(p); err == nil {
			t.Fatalf("unreviewed provenance published: %s", raw)
		}
	}
}

func TestQuotaProvenanceLegacyStorageFailureResetAndBinding(t *testing.T) {
	for _, schema := range []int{1, 2} {
		t.Run(string(rune('0'+schema)), func(t *testing.T) {
			dir, e, env := quotaFixture(t)
			at := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
			reset := at.Add(time.Minute)
			if err := quotaPublish(t, dir, e, env, QuotaAttempt{At: at, Windows: []QuotaWindow{{Name: "primary", UsedPercent: 100, ResetsAt: &reset}}}); err != nil {
				t.Fatal(err)
			}
			q, err := LoadQuota(dir)
			if err != nil {
				t.Fatal(err)
			}
			q.SchemaVersion = schema
			raw, err := json.Marshal(q)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, QuotaName)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			f, _ := Load(dir)
			v := QuotaViews(dir, f, env, at.Add(time.Second))[e.ID]
			if v.Summary == nil || v.Summary.Provenance == nil || !v.Summary.Provenance.RetrievedAt.Equal(at) {
				t.Fatal("legacy successful time lost")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(raw, after) {
				t.Fatal("projection rewrote old storage")
			}
			// Crossing reset or refreshing a view cannot fabricate new availability/time.
			v = QuotaViews(dir, f, env, reset.Add(time.Second))[e.ID]
			if !v.Summary.Rows[0].Ended || *v.Summary.Rows[0].UsedPercent != 100 || !v.Summary.Provenance.RetrievedAt.Equal(at) || v.Summary.Provenance.ProviderObservedAt != nil {
				t.Fatalf("reset restored capacity: %+v", v)
			}
			if err := quotaPublish(t, dir, e, env, QuotaAttempt{At: reset.Add(time.Second), Reason: "timeout"}); err != nil {
				t.Fatal(err)
			}
			v = QuotaViews(dir, f, env, reset.Add(time.Minute))[e.ID]
			if v.FailedRefreshAt == nil || !v.Summary.Provenance.RetrievedAt.Equal(at) {
				t.Fatal("failure rewrote success")
			}
			// A collector clock behind the attempted read remains unavailable.
			v = QuotaViews(dir, f, env, at.Add(-time.Second))[e.ID]
			if v.State != "unavailable" || v.Summary.Provenance != nil {
				t.Fatal("future clock manufactured provenance")
			}
			f.Entries[0].Revision = token()
			v = QuotaViews(dir, f, env, reset.Add(time.Minute))[e.ID]
			if v.State != "unavailable" || v.Summary.Provenance != nil {
				t.Fatal("changed selection retained old provenance")
			}
			f, _ = Load(dir)
			v = QuotaViews(dir, f, []string{"HOME=/example-changed"}, reset.Add(time.Minute))[e.ID]
			if v.State != "unavailable" || v.Summary.Provenance != nil {
				t.Fatal("changed context retained old provenance")
			}
		})
	}
}
