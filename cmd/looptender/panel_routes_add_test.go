//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

type panelAddWorld struct {
	f   addFixture
	c   *panelChanges
	a   accounts.Entry
	key string
}

func newPanelAddWorld(t *testing.T, provider, mode string, existing bool) panelAddWorld {
	t.Helper()
	t.Setenv("BB_THREAD_ID", "")
	var args []string
	if existing {
		args = []string{"--fallback", "claude:pool-existing"}
	}
	f := newAddFixture(t, args...)
	marker := filepath.Join(f.root, "unexpected-call")
	t.Setenv("EXAMPLE_ROUTE_CALL", marker)
	bin := filepath.Join(f.root, "not-executable")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "bb"} {
		write(t, filepath.Join(bin, name), "#!/bin/sh\nprintf called >> \"$EXAMPLE_ROUTE_CALL\"\nexit 99\n", 0700)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if b, err := os.ReadFile(marker); !os.IsNotExist(err) {
			t.Errorf("unexpected provider/model call %q %v", b, err)
		}
	})
	regDir := filepath.Join(f.root, "registry")
	cfg := loadFixture(t, f.cfg)
	entry, err := registry.Add(regDir, cfg.Loop.Name, f.cfg, cfg.Loop.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(f.root, "catalog")
	var a accounts.Entry
	err = accounts.Update(catalog, func(all *accounts.File) error {
		cli := "codex"
		if provider == "claude-code" {
			cli = "claude"
		}
		a = accounts.Entry{Label: "Example selected context", Provider: provider, Mode: mode, CLI: filepath.Join(bin, cli), Workdir: f.project}
		if mode == "explicit_directory" {
			a.Directory = f.account("claude-b")
		}
		a = accounts.NewEntry(*all, a, "", time.Now().UTC(), os.Environ())
		all.Entries = append(all.Entries, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := newPanelChanges(context.Background(), regDir, &dockReader{}, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	c.routeCatalog = catalog
	c.caller = ""
	oldCatalog := setupAccountsDir
	setupAccountsDir = catalog
	t.Cleanup(func() { setupAccountsDir = oldCatalog })
	return panelAddWorld{f, c, a, dockKey(entry.ID)}
}
func (w panelAddWorld) input() panelRouteInput {
	return panelRouteInput{Op: "add", Loop: w.key, Account: w.a.ID, Model: "example-added-model", Limit: "pool-added", Behavior: config.EnterHeadless}
}
func (w panelAddWorld) prepare(t *testing.T) panelRouteReply {
	t.Helper()
	p := w.c.prepareRoutes("page", w.input())
	if p.Form == "" {
		t.Fatalf("preview %+v", p)
	}
	return p
}
func TestPanelLoopAddFrozenSelectionsAndPolicies(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		for _, mode := range []string{"inherited", "explicit_directory"} {
			for _, existing := range []bool{false, true} {
				t.Run(provider+"/"+mode+"/"+map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
					w := newPanelAddWorld(t, provider, mode, existing)
					cfg := loadFixture(t, w.f.cfg)
					ctl := control.File{Enabled: true, Paused: true, Stop: true, AwaitingUser: true, Completed: true, EpisodeHold: true}
					if err := control.Save(cfg.Loop.StateDir, ctl); err != nil {
						t.Fatal(err)
					}
					write(t, filepath.Join(cfg.Loop.StateDir, state.SendsName), "{\"version\":1,\"used\":7,\"seq\":7,\"settled\":7}\n", 0600)
					beforeCfg, _ := os.ReadFile(w.f.cfg)
					beforeCtl, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
					beforeBudget, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, state.SendsName))
					p := w.prepare(t)
					reviewed := w.c.forms[p.Form].route.add
					if reviewed.options.position < 1 || reviewed.options.id != p.Changes[0].Route || reviewed.options.set["probe-daily-cap"] || reviewed.options.accountEntry == nil {
						t.Fatal("options not frozen")
					}
					// Assert locks at the actual production planner read on confirmation.
					reads := 0
					originalRead := fallbackAddReadFile
					fallbackAddReadFile = func(path string) ([]byte, error) {
						reads++
						for dir, name := range map[string]string{w.c.registry: registry.LockName, w.c.routeCatalog: "accounts", cfg.Loop.StateDir: lock.Tick} {
							held, err := lock.Held(dir, name)
							if err != nil || !held {
								t.Errorf("binding lock %s absent: %v", name, err)
							}
						}
						return originalRead(path)
					}
					out := w.c.confirmRoutes("page", p.Form, "add", w.key)
					fallbackAddReadFile = originalRead
					if out.Outcome != "changed" || reads == 0 || out.Changes[0].Route != p.Changes[0].Route || out.Changes[0].To != p.Changes[0].To {
						t.Fatalf("save %+v reads %d", out, reads)
					}
					after := loadFixture(t, w.f.cfg)
					added := after.FallbackSet.Candidates[reviewed.options.position-1]
					if added.ID != reviewed.options.id || added.Account != w.a.ID || added.Model() != "example-added-model" {
						t.Fatal("saved another selection")
					}
					if after.FallbackSet.Probe.Disabled == existing {
						t.Fatal("creation-only probe policy wrong")
					}
					if existing {
						got, _ := os.ReadFile(w.f.cfg)
						if string(got) != string(beforeCfg) {
							t.Fatal("existing failover config changed")
						}
					} else if len(after.Loop.Errors.FailoverOn) != 0 {
						t.Fatal("first fallback enabled handoff")
					}
					for path, want := range map[string][]byte{filepath.Join(cfg.Loop.StateDir, control.FileName): beforeCtl, filepath.Join(cfg.Loop.StateDir, state.SendsName): beforeBudget} {
						got, _ := os.ReadFile(path)
						if string(got) != string(want) {
							t.Fatal("hold/allowance changed")
						}
					}
					if out := w.c.confirmRoutes("page", p.Form, "add", w.key); out.Outcome != "review_again" {
						t.Fatal("replayed", out)
					}
				})
			}
		}
	}
}
func TestPanelLoopAddAccountAndRequestRefusals(t *testing.T) {
	for _, problem := range []string{"effort", "to", "id", "provider", "conversation", "negative", "overflow", "context", "cli", "workdir", "missing", "standard", "no_project"} {
		t.Run(problem, func(t *testing.T) {
			w := newPanelAddWorld(t, "claude-code", "inherited", false)
			in := w.input()
			switch problem {
			case "effort":
				in.Effort = "high"
			case "to":
				in.To = 1
			case "id":
				in.ID = "browser-chosen"
			case "provider":
				in.Provider = "codex"
			case "conversation":
				in.NewConversation = true
			case "negative":
				in.At = -1
			case "overflow":
				in.At = int(^uint(0) >> 1)
			case "missing":
				in.Account = "missing"
			case "context", "cli", "workdir":
				if err := accounts.Update(w.c.routeCatalog, func(f *accounts.File) error {
					switch problem {
					case "context":
						f.Entries[0].Context = strings.Repeat("0", 64)
					case "cli":
						f.Entries[0].CLI = filepath.Join(w.f.root, "another-cli")
					case "workdir":
						f.Entries[0].Workdir = w.f.root
					}
					// Use valid revised entries to test exact invocation mismatch rather
					// than corrupt-file refusal for CLI/workdir.
					if problem != "context" {
						f.Entries[0] = accounts.Revise(*f, f.Entries[0], f.Entries[0], "", time.Now(), os.Environ())
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "standard":
				raw, _ := os.ReadFile(w.f.cfg)
				write(t, w.f.cfg, strings.Replace(string(raw), "  name: example-loop", "  routes: standard\n  name: example-loop", 1), 0600)
			case "no_project":
				if err := os.RemoveAll(w.f.project); err != nil {
					t.Fatal(err)
				}
			}
			before := w.f.files(t)
			out := w.c.prepareRoutes("page", in)
			if out.Outcome != "refused" || out.Form != "" || w.f.files(t) != before {
				t.Fatalf("accepted %s: %+v", problem, out)
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), w.f.root) {
				t.Fatal("private path leaked")
			}
		})
	}
	// Presence of unsupported, even zero-valued fields must not be silently
	// accepted by the selected-loop add wire path.
	w := newPanelAddWorld(t, "claude-code", "inherited", false)
	for _, extra := range []string{`,"to":0`, `,"id":""`, `,"provider":""`, `,"newConversation":false`, `,"permission":"full"`} {
		raw := `{"op":"add","loop":"` + w.key + `","account":"` + w.a.ID + `","model":"example-model","limit":"pool-new","behavior":"headless"` + extra + `}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
		req.Header.Set(panelFormHeader, w.c.prepareValue("page"))
		rec := httptest.NewRecorder()
		w.c.serveRoutes(rec, req, panelRoutesPrefix+"prepare", "page")
		var out panelRouteReply
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code == 200 && out.Outcome != "refused" {
			t.Fatal("wire accepted unsupported field", extra, rec.Body.String())
		}
	}
}
func TestPanelLoopAddConfirmBindingsAndObservationRefresh(t *testing.T) {
	for _, change := range []string{"revision", "context", "env", "observation", "registry", "tick_lock", "account_lock", "registry_lock", "append", "active", "session", "key", "expiry"} {
		t.Run(change, func(t *testing.T) {
			w := newPanelAddWorld(t, "claude-code", "inherited", false)
			p := w.prepare(t)
			cfg := loadFixture(t, w.f.cfg)
			page, key := "page", w.key
			switch change {
			case "revision", "context", "observation":
				if err := accounts.Update(w.c.routeCatalog, func(f *accounts.File) error {
					switch change {
					case "revision":
						f.Entries[0] = accounts.Revise(*f, f.Entries[0], f.Entries[0], "", time.Now(), os.Environ())
					case "context":
						f.Entries[0].Context = strings.Repeat("0", 64)
					case "observation":
						a := f.Entries[0]
						f.Entries[0].Last = &accounts.Observation{Revision: a.Revision, Context: a.Context, At: time.Now(), Auth: "reported_present"}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "env":
				t.Setenv("ANTHROPIC_BASE_URL", "https://fictional.example.com")
			case "registry":
				if err := registry.Remove(w.c.registry, "loop_"+strings.TrimPrefix(w.c.forms[p.Form].route.entry.ID, "loop_")); err != nil {
					t.Fatal(err)
				}
			case "tick_lock", "account_lock", "registry_lock":
				var held *lock.Lock
				var err error
				switch change {
				case "tick_lock":
					held, err = lock.Acquire(cfg.Loop.StateDir)
				case "account_lock":
					held, err = lock.AcquireNamed(w.c.routeCatalog, "accounts")
				case "registry_lock":
					held, err = lock.AcquireNamed(w.c.registry, registry.LockName)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer held.Release()
			case "append":
				write(t, w.f.fallbacks, "# unrelated destination\n", 0600)
			case "active":
				if err := state.Save(cfg.Loop.StateDir, state.File{ActiveTarget: "fictional-other"}); err != nil {
					t.Fatal(err)
				}
			case "session":
				page = "another-page"
			case "key":
				key = "l000000000000"
			case "expiry":
				w.c.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
			}
			before := w.f.files(t)
			out := w.c.confirmRoutes(page, p.Form, "add", key)
			if change == "observation" {
				if out.Outcome != "changed" {
					t.Fatalf("observation staled review: %+v", out)
				}
			} else if (out.Outcome != "review_again" && out.Outcome != "refused") || w.f.files(t) != before {
				t.Fatalf("stale save %s: %+v", change, out)
			}
		})
	}
}
func TestReviewedAddUnderLockRejectsSourceDrift(t *testing.T) {
	w := newPanelAddWorld(t, "claude-code", "inherited", false)
	p := w.prepare(t)
	original := fallbackAddTickLock
	t.Cleanup(func() { fallbackAddTickLock = original })
	fallbackAddTickLock = func(dir string) (*lock.Lock, error) {
		raw, err := os.ReadFile(w.f.cfg)
		if err != nil {
			return nil, err
		}
		write(t, w.f.cfg, string(raw)+"# changed after panel preflight\n", 0600)
		return original(dir)
	}
	out := w.c.confirmRoutes("page", p.Form, "add", w.key)
	if out.Outcome != "review_again" {
		t.Fatalf("under-lock stale plan saved: %+v", out)
	}
	if _, err := os.Stat(w.f.fallbacks); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale add published", err)
	}
}

func TestPanelLoopAddExistingOrderCollisionAndPublicationText(t *testing.T) {
	for _, change := range []string{"position", "append", "collision", "history", "cleanup"} {
		t.Run(change, func(t *testing.T) {
			existing := change != "cleanup"
			w := newPanelAddWorld(t, "claude-code", "inherited", existing)
			in := w.input()
			in.At = 1
			p := w.c.prepareRoutes("page", in)
			if p.Form == "" {
				t.Fatalf("preview %+v", p)
			}
			if p.Changes[0].To != 1 {
				t.Fatal("position not reviewed")
			}
			oldHistory, oldReplace, oldRemove := fallbackAddHistory, fallbackAddReplace, fallbackAddRemove
			t.Cleanup(func() {
				fallbackAddHistory = oldHistory
				fallbackAddReplace = oldReplace
				fallbackAddRemove = oldRemove
			})
			switch change {
			case "append", "collision":
				cfg := loadFixture(t, w.f.cfg)
				extra := cfg.FallbackSet.Candidates[0]
				extra.ID = "fictional-added"
				if change == "collision" {
					extra.ID = p.Changes[0].Route
				}
				raw, _ := os.ReadFile(w.f.fallbacks)
				next, err := insertCandidateYAML(raw, extra, len(cfg.FallbackSet.Candidates))
				if err != nil {
					t.Fatal(err)
				}
				write(t, w.f.fallbacks, string(next), 0600)
			case "history":
				fallbackAddHistory = func(string, string, string, string) error { return errors.New("fictional history failure") }
			case "cleanup":
				fallbackAddReplace = func(string, []byte) error { return errors.New("fictional config failure") }
				fallbackAddRemove = func(string) error { return errors.New("fictional removal failure") }
			}
			before := w.f.files(t)
			out := w.c.confirmRoutes("page", p.Form, "add", w.key)
			switch change {
			case "append", "collision":
				if out.Outcome != "review_again" || w.f.files(t) != before {
					t.Fatalf("stale order/ID saved: %+v", out)
				}
			case "position":
				cfg := loadFixture(t, w.f.cfg)
				if out.Outcome != "changed" || cfg.FallbackSet.Candidates[0].ID != p.Changes[0].Route {
					t.Fatal("position changed", out)
				}
			case "history":
				if out.Outcome != "changed" || !strings.Contains(out.Text, "History could not") {
					t.Fatalf("history misreported: %+v", out)
				}
			case "cleanup":
				if out.Outcome != "partial" || out.Stage != "fallback_file_retained" || len(out.Changes) != 0 || !strings.Contains(out.Text, "no rollback") || strings.Contains(out.Text, "nothing was changed") {
					t.Fatalf("partial misreported: %+v", out)
				}
			}
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), w.f.root) {
				t.Fatal("filesystem error leaked")
			}
		})
	}
}
func TestPanelLoopAddUnderLockContextDrift(t *testing.T) {
	w := newPanelAddWorld(t, "claude-code", "inherited", false)
	p := w.prepare(t)
	original := fallbackAddTickLock
	t.Cleanup(func() { fallbackAddTickLock = original })
	fallbackAddTickLock = func(dir string) (*lock.Lock, error) {
		t.Setenv("ANTHROPIC_BASE_URL", "https://changed.example.com")
		return original(dir)
	}
	out := w.c.confirmRoutes("page", p.Form, "add", w.key)
	if out.Outcome != "review_again" {
		t.Fatalf("under-lock context drift saved: %+v", out)
	}
	if _, err := os.Stat(w.f.fallbacks); !os.IsNotExist(err) {
		t.Fatal("stale context published", err)
	}
}

func TestPanelLoopAddResolvedCLITargetIsFrozen(t *testing.T) {
	w := newPanelAddWorld(t, "claude-code", "inherited", false)
	targets := []string{filepath.Join(w.f.root, "fictional-cli-one"), filepath.Join(w.f.root, "fictional-cli-two")}
	for _, target := range targets {
		if err := os.WriteFile(target, []byte("fictional image; never executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(w.a.CLI); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targets[0], w.a.CLI); err != nil {
		t.Fatal(err)
	}
	p := w.prepare(t)
	if err := os.Remove(w.a.CLI); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targets[1], w.a.CLI); err != nil {
		t.Fatal(err)
	}
	out := w.c.confirmRoutes("page", p.Form, "add", w.key)
	if out.Outcome != "review_again" {
		t.Fatalf("resolved CLI substitution saved: %+v", out)
	}
	if _, err := os.Stat(w.f.fallbacks); !os.IsNotExist(err) {
		t.Fatal("substitution published", err)
	}
}

func TestPanelLoopAddOwnerAndWireAuthority(t *testing.T) {
	for _, actor := range []string{"owner", "visitor", "unclaimed"} {
		t.Run(actor, func(t *testing.T) {
			w := newPanelAddWorld(t, "claude-code", "inherited", false)
			cfg := loadFixture(t, w.f.cfg)
			ctl := control.File{Enabled: true, Paused: true, Ownership: &control.Ownership{ThreadID: "thr_example_owner", Generation: 1}}
			if actor == "unclaimed" {
				ctl.Ownership = nil
			}
			if err := control.Save(cfg.Loop.StateDir, ctl); err != nil {
				t.Fatal(err)
			}
			caller := "thr_example_owner"
			if actor == "visitor" {
				caller = "thr_example_visitor"
			}
			t.Setenv("BB_THREAD_ID", caller)
			w.c.caller = caller
			p := w.c.prepareRoutes("page", w.input())
			if actor != "owner" {
				if p.Outcome != "refused" || p.Form != "" {
					t.Fatal("actor gained add authority", p)
				}
				return
			}
			if p.Form == "" {
				t.Fatal("owner refused", p)
			}
			if out := w.c.confirmRoutes("page", p.Form, "add", w.key); out.Outcome != "changed" {
				t.Fatal("owner save refused", out)
			}
		})
	}
	w := newPanelAddWorld(t, "claude-code", "inherited", false)
	wire := func(step, body, form string) (int, panelRouteReply) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(panelFormHeader, form)
		rec := httptest.NewRecorder()
		w.c.serveRoutes(rec, req, panelRoutesPrefix+step, "page")
		var reply panelRouteReply
		_ = json.Unmarshal(rec.Body.Bytes(), &reply)
		return rec.Code, reply
	}
	raw := `{"op":"add","loop":"` + w.key + `","account":"` + w.a.ID + `","model":"example-new","limit":"pool-new","behavior":"headless","at":1}`
	code, p := wire("prepare", raw, w.c.prepareValue("page"))
	if code != 200 || p.Form == "" {
		t.Fatal("wire preview", code, p)
	}
	code, _ = wire("confirm", `{"op":"add","loop":"`+w.key+`","account":"browser-substitution"}`, p.Form)
	if code != 400 {
		t.Fatal("confirm accepted unsupported account field", code)
	}
	code, out := wire("confirm", `{"op":"add","loop":"`+w.key+`"}`, p.Form)
	if code != 200 || out.Outcome != "changed" || out.Changes[0].Route != p.Changes[0].Route {
		t.Fatal("wire save", code, out)
	}
}
