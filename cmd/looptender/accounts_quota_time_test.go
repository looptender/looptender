package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/testexec"
)

func TestAccountQuotaTextUsesOnlyRetainedLocalRetrieval(t *testing.T) {
	at := time.Date(2030, 3, 4, 12, 0, 0, 0, time.UTC)
	failed := at.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		view   accounts.QuotaView
		cached bool
	}{
		{"legacy Codex fresh", accounts.QuotaView{ObservedAt: &at, Freshness: "fresh", Source: "codex account/rateLimits/read"}, false},
		{"legacy Codex stale", accounts.QuotaView{ObservedAt: &at, Freshness: "stale", Source: "codex account/rateLimits/read"}, false},
		{"retained after failure", accounts.QuotaView{ObservedAt: &at, FailedRefreshAt: &failed, Freshness: "stale", Source: "codex account/rateLimits/read"}, false},
		{"Claude", accounts.QuotaView{RetrievedAt: &at, Freshness: "provider_unknown", Source: "claude:get_usage"}, true},
		{"Antigravity", accounts.QuotaView{RetrievedAt: &at, Freshness: "provider_unknown", Source: "agy:/usage"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			writeAccountQuotaRetrieval(&out, tc.view)
			got := out.String()
			want := "Last successful fetch 2030-03-04T12:00:00Z (local retrieval time); provider observation time and freshness unknown; source: " + tc.view.Source + "\n"
			if tc.cached {
				want += "Usage may be cached or seeded.\n"
			}
			if got != want {
				t.Fatalf("got %q; want %q", got, want)
			}
			if strings.Contains(got, "(fresh)") || strings.Contains(got, "(stale)") || strings.Contains(got, failed.Format(time.RFC3339)) {
				t.Fatal("invented freshness or replaced success time", got)
			}
		})
	}
	var out bytes.Buffer
	writeAccountQuotaRetrieval(&out, accounts.QuotaView{State: "unavailable", Freshness: "unavailable", FailedRefreshAt: &failed})
	if out.Len() != 0 {
		t.Fatal("invented successful fetch", out.String())
	}
}

// Exercise the actual command with a local RPC stand-in, never a provider.
func TestAccountQuotaUsageTextAndLegacyJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell RPC stand-in")
	}
	dir, entry := accountFixture(t, "codex")
	testexec.Script(t, entry.CLI, `#!/bin/sh
read initialize
printf '%s\n' '{"id":1,"result":{}}'
read initialized
read request
printf '%s\n' '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":100}}}}'
`)
	if err := accounts.Update(dir, func(f *accounts.File) error {
		entry.Mode = "explicit_directory"
		entry.Directory = entry.Workdir
		entry.Context = accounts.Context(*f, entry, os.Environ())
		f.Entries[0] = entry
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &stderr}
	if code := accountsUsage(e, dir, entry.ID, false); code != exitOK {
		t.Fatalf("text exit %d: %s", code, stderr.String())
	}
	text := out.String()
	if !strings.Contains(text, "Last successful fetch ") || !strings.Contains(text, "(local retrieval time); provider observation time and freshness unknown") || strings.Contains(text, "(fresh)") || !strings.Contains(text, "100% used") {
		t.Fatal(text)
	}
	out.Reset()
	if code := accountsUsage(e, dir, entry.ID, true); code != exitOK {
		t.Fatalf("JSON exit %d: %s", code, stderr.String())
	}
	var wire struct {
		SchemaVersion int                        `json:"schemaVersion"`
		ID            string                     `json:"id"`
		Quota         map[string]json.RawMessage `json:"quota"`
	}
	if err := json.Unmarshal(out.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.SchemaVersion != 1 || wire.ID != entry.ID || string(wire.Quota["freshness"]) != `"fresh"` || len(wire.Quota["observedAt"]) == 0 || wire.Quota["retrievedAt"] != nil || wire.Quota["providerObservedAt"] != nil {
		t.Fatalf("Legacy machine contract changed: %s", out.String())
	}
	var stamp string
	if err := json.Unmarshal(wire.Quota["observedAt"], &stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Fatal(err)
	}
}
