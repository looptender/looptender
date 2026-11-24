package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

// A login check keeps a keyed fingerprint of the login, never the identity.
// Two entries logged in to the same account say so; an entry logged in to
// another account since its last check says so, with that check's date.
func TestAccountLoginFingerprint(t *testing.T) {
	dir, a := accountFixture(t, "claude-code")
	var b accounts.Entry
	if err := accounts.Update(dir, func(f *accounts.File) error {
		b = accounts.NewEntry(*f, accounts.Entry{Label: "Example studio", Provider: "claude-code", Mode: "inherited", CLI: a.CLI, Workdir: a.Workdir}, "", time.Now().UTC(), os.Environ())
		f.Entries = append(f.Entries, b)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	email := "private-one@example.com"
	run := func(_ context.Context, _ string, args, _ []string, _ string) ([]byte, error) {
		if args[0] == "--version" {
			return []byte("2.1.318 (Claude Code)"), nil
		}
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","email":"` + email + `","orgId":"example-org"}`), nil
	}
	day1 := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{a.ID, b.ID} {
		if err := accountCheck(context.Background(), dir, id, "", run, day1); err != nil {
			t.Fatal(err)
		}
	}
	view := accounts.ReadView(dir, day1)
	if c := view.Cards[0]; c.Login == nil || c.Login.State != "same_as" || c.Login.Text != "Same login as Example studio." {
		t.Fatalf("%+v", c.Login)
	}
	email = "Private-Two@example.com"
	day2 := day1.Add(48 * time.Hour)
	if err := accountCheck(context.Background(), dir, a.ID, "", run, day2); err != nil {
		t.Fatal(err)
	}
	view = accounts.ReadView(dir, day2)
	if c := view.Cards[0]; c.Login == nil || c.Login.State != "changed" || c.Login.Text != "A different login than when checked on 2026-03-02." {
		t.Fatalf("%+v", c.Login)
	}
	if c := view.Cards[1]; c.Login == nil || c.Login.State != "checked" {
		t.Fatalf("the other entry: %+v", c.Login)
	}
	// The identity is nowhere: not in the catalog, the fingerprint record or
	// the view.
	raw, _ := json.Marshal(view)
	for _, name := range []string{accounts.FileName, accounts.LoginsName} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, b...)
	}
	for _, s := range []string{"private-one", "private-two", "Private-Two", "example-org"} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("identity %q stored or shown", s)
		}
	}
}

// At a usage limit, a check asks for the login check of the account its
// route states, at most once an hour.
func TestLimitAccountCheckIsSpaced(t *testing.T) {
	dir, a := accountFixture(t, "claude-code")
	saved, savedRun := limitAccountsDir, limitAccountRun
	t.Cleanup(func() { limitAccountsDir, limitAccountRun = saved, savedRun })
	limitAccountsDir = dir
	calls := 0
	limitAccountRun = func(_ context.Context, _ string, args, _ []string, _ string) ([]byte, error) {
		calls++
		if args[0] == "--version" {
			return []byte("2.1.318 (Claude Code)"), nil
		}
		return []byte(`{"loggedIn":true,"email":"private@example.com"}`), nil
	}
	tickAccountCheck(context.Background(), a.ID)
	tickAccountCheck(context.Background(), a.ID)
	if calls != 2 {
		t.Fatalf("%d commands for two checks within the hour", calls)
	}
	tickAccountCheck(context.Background(), "not-in-the-catalog")
	if calls != 2 {
		t.Fatal("checked an entry the catalog does not have")
	}
}
