//go:build darwin || linux

package legacyretire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateDocumentsRejectUnknownDuplicateAliasAndNull(t *testing.T) {
	for _, kind := range []string{"valid", "duplicate", "unknown", "alias", "null", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := canonicalTemp(t)
			p := filepath.Join(dir, "selection")
			s := Selection{ConfigPath: "/fictional/loop.yaml", StatePath: "/fictional/state", PID: 123, Build: "example", StartedAt: "2026-01-01T00:00:00Z", Executable: "/fictional/looptender", ExecutableDigest: strings.Repeat("a", 64), Arguments: []string{"/fictional/looptender", "daemon", "-c", "/fictional/loop.yaml"}}
			b, _ := json.Marshal(s)
			raw := string(b)
			switch kind {
			case "duplicate":
				raw = strings.Replace(raw, `"pid":123`, `"pid":123,"pid":123`, 1)
			case "unknown":
				raw = strings.Replace(raw, `"pid":123`, `"pid":123,"future":true`, 1)
			case "alias":
				raw = strings.Replace(raw, `"pid":123`, `"PID":123`, 1)
			case "null":
				raw = strings.Replace(raw, `"pid":123`, `"pid":null`, 1)
			}
			if e := os.WriteFile(p, []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if kind == "symlink" {
				if e := os.Rename(p, p+".original"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(p+".original", p); e != nil {
					t.Fatal(e)
				}
			}
			got, e := ReadSelection(p)
			if kind == "valid" {
				if e != nil || got.PID != 123 {
					t.Fatal(got, e)
				}
			} else if e == nil {
				t.Fatal("ambiguous document accepted")
			}
		})
	}
}
func TestTicketPublicationIsExclusiveAndRoundTrips(t *testing.T) {
	ticket, _ := ready(t)
	dir := canonicalTemp(t)
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "ticket")
	if e := WriteTicket(p, ticket); e != nil {
		t.Fatal(e)
	}
	got, e := ReadTicket(p)
	if e != nil || got != ticket {
		t.Fatal(got, e)
	}
	if e = WriteTicket(p, ticket); e == nil {
		t.Fatal("overwrote ticket")
	}
}

func TestTicketRefusesAttemptDescendantsAndAliasParents(t *testing.T) {
	ticket, _ := ready(t)
	nested := filepath.Join(ticket.Root, "nested")
	if e := os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	outside := canonicalTemp(t)
	if e := os.Chmod(outside, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(outside, "alias")
	if e := os.Symlink(nested, alias); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{filepath.Join(ticket.Root, "ticket"), filepath.Join(nested, "ticket"), filepath.Join(alias, "ticket")} {
		if e := WriteTicket(p, ticket); e == nil {
			t.Fatal("published inside or through alias", p)
		}
		if _, e := os.Lstat(p); !os.IsNotExist(e) {
			t.Fatal("publication occurred", e)
		}
	}
}
