//go:build unix

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

func panelRouteLoopKey(t *testing.T, r *routesFixture, name string) string {
	t.Helper()
	f, err := registry.Load(r.registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Entries {
		if e.Loop == name {
			return dockKey(e.ID)
		}
	}
	t.Fatal("missing fictional registration")
	return ""
}

// Every route read/preview/save must be independent of BB discovery or a
// provider executable, including model discovery and a route's probe CLI.
func panelRouteNoCalls(t *testing.T, r *routesFixture) {
	t.Helper()
	marker := filepath.Join(r.own.dir, "unexpected-provider-call")
	t.Setenv("EXAMPLE_ROUTE_CALL", marker)
	for _, name := range []string{"bb", "claude", "codex"} {
		write(t, filepath.Join(r.own.dir, "bin", name), "#!/bin/sh\nprintf called >> \"$EXAMPLE_ROUTE_CALL\"\nexit 99\n", 0700)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(marker); !os.IsNotExist(err) {
			t.Errorf("route operation invoked a provider/model lookup: %q %v", b, err)
		}
	})
}

func TestPanelRouteLoopEditsPreserveControlsAndFailover(t *testing.T) {
	for _, op := range []string{"move", "off", "on", "remove"} {
		t.Run(op, func(t *testing.T) {
			r, c, _ := routePanelFixture(t)
			panelRouteNoCalls(t, r)
			key := panelRouteLoopKey(t, r, "example-loop")
			cfg := loadFixture(t, r.own.cfg)
			if op == "on" {
				raw, _ := os.ReadFile(cfg.Loop.Fallbacks)
				write(t, cfg.Loop.Fallbacks, strings.Replace(string(raw), "  - id: codex-main", "  - disabled: true\n    id: codex-main", 1), 0600)
			}
			if err := control.Save(cfg.Loop.StateDir, control.File{Enabled: true, Paused: true, AwaitingUser: true, Completed: true, EpisodeHold: true, Stop: true}); err != nil {
				t.Fatal(err)
			}
			sends := filepath.Join(cfg.Loop.StateDir, "sends.json")
			write(t, sends, "{\"version\":1,\"used\":7,\"seq\":7,\"settled\":7}\n", 0600)
			before, _ := os.ReadFile(r.own.cfg)
			held, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
			budget, _ := os.ReadFile(sends)
			in := panelRouteInput{Op: op, Loop: key, ID: "codex-main"}
			if op == "move" {
				in.To = 1
			}
			p := c.prepareRoutes("page", in)
			if p.Form == "" || len(p.Changes) != 1 || p.Changes[0].List != "own" || p.Changes[0].Expect == "" {
				t.Fatalf("preview: %+v", p)
			}
			out := c.confirmRoutes("page", p.Form, op, key)
			if out.Outcome != "changed" {
				t.Fatalf("confirm: %+v", out)
			}
			after := loadFixture(t, r.own.cfg)
			ids := candidateIDs(after.FallbackSet)
			switch op {
			case "move":
				if ids[0] != "codex-main" {
					t.Fatal(ids)
				}
			case "off", "on":
				if after.FallbackSet.Candidates[1].Disabled != (op == "off") {
					t.Fatal("toggle not saved")
				}
			case "remove":
				if len(ids) != 1 || ids[0] != "claude-other-account" {
					t.Fatal(ids)
				}
			}
			for path, want := range map[string][]byte{r.own.cfg: before, filepath.Join(cfg.Loop.StateDir, control.FileName): held, sends: budget} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Fatalf("unreviewed config/hold/budget mutation: %s %v", filepath.Base(path), err)
				}
			}
			saved, _ := os.ReadFile(after.Loop.Fallbacks)
			if out := c.confirmRoutes("page", p.Form, op, key); out.Outcome != "review_again" {
				t.Fatal(out)
			}
			got, _ := os.ReadFile(after.Loop.Fallbacks)
			if string(got) != string(saved) {
				t.Fatal("replay wrote")
			}
		})
	}
}

func TestPanelRouteLoopReviewRejectsDrift(t *testing.T) {
	for _, drift := range []string{"config", "fallbacks", "active", "registration_path", "registration_state", "registration_id", "operation", "loop_key", "session", "expiry", "tick_lock", "registry_lock", "ownership"} {
		t.Run(drift, func(t *testing.T) {
			r, c, _ := routePanelFixture(t)
			panelRouteNoCalls(t, r)
			key := panelRouteLoopKey(t, r, "example-loop")
			cfg := loadFixture(t, r.own.cfg)
			if drift == "ownership" {
				t.Setenv("BB_THREAD_ID", "thr_example_owner")
				c.caller = "thr_example_owner"
				if err := control.Save(cfg.Loop.StateDir, control.File{Ownership: &control.Ownership{ThreadID: c.caller, Generation: 1}}); err != nil {
					t.Fatal(err)
				}
			}
			p := c.prepareRoutes("page", panelRouteInput{Op: "move", Loop: key, ID: "codex-main", To: 1})
			if p.Form == "" {
				t.Fatalf("preview %+v", p)
			}
			page, op, confirmKey := "page", "move", key
			switch drift {
			case "config", "fallbacks":
				path := r.own.cfg
				if drift == "fallbacks" {
					path = cfg.Loop.Fallbacks
				}
				raw, _ := os.ReadFile(path)
				write(t, path, string(raw)+"# fictional concurrent edit\n", 0600)
			case "active":
				if err := state.Save(cfg.Loop.StateDir, state.File{ActiveTarget: "claude-other-account"}); err != nil {
					t.Fatal(err)
				}
			case "registration_path", "registration_state", "registration_id":
				f, err := registry.Load(r.registry)
				if err != nil {
					t.Fatal(err)
				}
				for i := range f.Entries {
					if dockKey(f.Entries[i].ID) == key {
						switch drift {
						case "registration_path":
							replacement := filepath.Join(r.own.dir, "replacement.yaml")
							raw, _ := os.ReadFile(r.own.cfg)
							write(t, replacement, string(raw), 0600)
							f.Entries[i].ConfigPath = replacement
						case "registration_state":
							f.Entries[i].StateDir = filepath.Join(r.own.dir, "replacement-state")
						case "registration_id":
							f.Entries[i].ID = "loop_" + strings.Repeat("a", 32)
						}
					}
				}
				raw, _ := json.Marshal(f)
				write(t, filepath.Join(r.registry, registry.FileName), string(raw), 0600)
			case "ownership":
				if err := control.Save(cfg.Loop.StateDir, control.File{Ownership: &control.Ownership{ThreadID: "thr_example_successor", Generation: 2}}); err != nil {
					t.Fatal(err)
				}
			case "operation":
				op = "remove"
			case "loop_key":
				confirmKey = panelRouteLoopKey(t, r, "follower-loop")
			case "session":
				page = "other-page"
			case "expiry":
				c.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
			case "tick_lock", "registry_lock":
				var held *lock.Lock
				var err error
				if drift == "tick_lock" {
					held, err = lock.Acquire(cfg.Loop.StateDir)
				} else {
					held, err = lock.AcquireNamed(r.registry, registry.LockName)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer held.Release()
			}
			before, _ := os.ReadFile(cfg.Loop.Fallbacks)
			out := c.confirmRoutes(page, p.Form, op, confirmKey)
			if out.Outcome != "review_again" && out.Outcome != "refused" {
				t.Fatalf("drift %s: %+v", drift, out)
			}
			after, _ := os.ReadFile(cfg.Loop.Fallbacks)
			if string(before) != string(after) {
				t.Fatal("drift wrote")
			}
			if again := c.confirmRoutes("page", p.Form, "move", key); again.Outcome != "review_again" {
				t.Fatal("refused form not spent", again)
			}
		})
	}
}

func TestPanelRouteOwnIsExplicitAndPreservesLoopSettings(t *testing.T) {
	r, c, _ := routePanelFixture(t)
	panelRouteNoCalls(t, r)
	write(t, r.list, routesStandardList, 0600)
	key := panelRouteLoopKey(t, r, "follower-loop")
	cfg := loadFixture(t, r.follower)
	if err := control.Save(cfg.Loop.StateDir, control.File{Enabled: true, Paused: true, AwaitingUser: true, EpisodeHold: true}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.follower)
	held, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
	p := c.prepareRoutes("page", panelRouteInput{Op: "own", Loop: key})
	if p.Form == "" {
		t.Fatalf("preview %+v", p)
	}
	target := filepath.Join(filepath.Dir(r.follower), "fallbacks.yaml")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("preview created own list", err)
	}
	if out := c.confirmRoutes("page", p.Form, "own", key); out.Outcome != "changed" {
		t.Fatalf("own %+v", out)
	}
	after, _ := os.ReadFile(r.follower)
	if string(after) != strings.Replace(string(before), "  routes: standard\n", "  fallbacks: fallbacks.yaml\n", 1) {
		t.Fatalf("own changed unrelated settings: %s", after)
	}
	got, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
	if string(got) != string(held) {
		t.Fatal("own changed holds")
	}
	next := loadFixture(t, r.follower)
	if next.Loop.FollowsStandard() || len(next.FallbackSet.Candidates) == 0 {
		t.Fatal("own did not copy resolved routes")
	}
	for _, in := range []panelRouteInput{{Op: "own"}, {Op: "add", Loop: key}, {Op: "follow_standard", Loop: key}, {Op: "own", Loop: key, ID: "codex-main"}, {Op: "move", Loop: "l000000000000", ID: "codex-main", To: 1}} {
		if out := c.prepareRoutes("page", in); out.Outcome != "refused" {
			t.Fatalf("scope expanded: %+v %+v", in, out)
		}
	}
}

func TestPanelRouteLoopReadAndStrictKey(t *testing.T) {
	r, c, _ := routePanelFixture(t)
	panelRouteNoCalls(t, r)
	key := panelRouteLoopKey(t, r, "example-loop")
	for _, value := range []string{key, "l000000000000"} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"op":"read","loop":"`+value+`"}`))
		req.Header.Set(panelFormHeader, c.prepareValue("page"))
		w := httptest.NewRecorder()
		c.serveRoutes(w, req, panelRoutesPrefix+"read", "page")
		var out panelRouteReply
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if value == key {
			if out.Editor == nil || len(out.Editor.Loops) != 1 || out.Editor.Loops[0].Key != key {
				t.Fatalf("read: %+v", out)
			}
		} else if out.Outcome != "refused" {
			t.Fatal("unknown key fell back to all loops", out)
		}
		if strings.Contains(w.Body.String(), r.own.dir) {
			t.Fatal("private path leaked")
		}
	}
	wire := func(step, body, form string) panelRouteReply {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(panelFormHeader, form)
		w := httptest.NewRecorder()
		c.serveRoutes(w, req, panelRoutesPrefix+step, "page")
		var out panelRouteReply
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("wire %s: %d %s", step, w.Code, w.Body.String())
		}
		return out
	}
	prepared := wire("prepare", `{"op":"off","loop":"`+key+`","id":"codex-main"}`, c.prepareValue("page"))
	if prepared.Form == "" {
		t.Fatalf("wire preview: %+v", prepared)
	}
	if out := wire("confirm", `{"op":"off","loop":"`+key+`"}`, prepared.Form); out.Outcome != "changed" {
		t.Fatalf("wire confirm: %+v", out)
	}
	for _, raw := range []string{`{"op":"read","loop":"x","loop":"y"}`, `{"op":"read","Loop":"x"}`, `{"op":"read","loop":5}`, `{"op":"read","loop":null}`, `{"op":"read","loop":""}`} {
		if _, ok := panelReadRoute(strings.NewReader(raw)); ok {
			t.Fatal("accepted bad loop key", raw)
		}
	}
}

func TestPanelRouteLoopOwnershipAndUnsafeEdits(t *testing.T) {
	for _, who := range []string{"person", "owner", "visitor", "unclaimed", "unreadable", "active", "symlink"} {
		t.Run(who, func(t *testing.T) {
			r, c, _ := routePanelFixture(t)
			panelRouteNoCalls(t, r)
			key := panelRouteLoopKey(t, r, "example-loop")
			cfg := loadFixture(t, r.own.cfg)
			ctl := control.File{Enabled: true, Ownership: &control.Ownership{ThreadID: "thr_example_owner", Generation: 1}}
			if who == "unclaimed" {
				ctl.Ownership = nil
			}
			if err := control.Save(cfg.Loop.StateDir, ctl); err != nil {
				t.Fatal(err)
			}
			caller := ""
			switch who {
			case "owner", "unclaimed", "unreadable":
				caller = "thr_example_owner"
			case "visitor":
				caller = "thr_example_visitor"
			}
			t.Setenv("BB_THREAD_ID", caller)
			c.caller = caller
			if who == "unreadable" {
				write(t, filepath.Join(cfg.Loop.StateDir, control.FileName), "broken", 0600)
			}
			if who == "active" {
				if err := state.Save(cfg.Loop.StateDir, state.File{ActiveTarget: "codex-main"}); err != nil {
					t.Fatal(err)
				}
			}
			if who == "symlink" {
				target := cfg.Loop.Fallbacks + ".real"
				if err := os.Rename(cfg.Loop.Fallbacks, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, cfg.Loop.Fallbacks); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(cfg.Loop.Fallbacks)
			p := c.prepareRoutes("page", panelRouteInput{Op: "off", Loop: key, ID: "codex-main"})
			if who == "person" || who == "owner" {
				if p.Form == "" {
					t.Fatalf("authorized preview refused: %+v", p)
				}
				if out := c.confirmRoutes("page", p.Form, "off", key); out.Outcome != "changed" {
					t.Fatalf("authorized save refused: %+v", out)
				}
			} else {
				if p.Form != "" || p.Outcome != "refused" {
					t.Fatalf("unsafe preview: %+v", p)
				}
				after, _ := os.ReadFile(cfg.Loop.Fallbacks)
				if string(before) != string(after) {
					t.Fatal("refusal mutated routes")
				}
				b, _ := json.Marshal(p)
				if strings.Contains(string(b), r.own.dir) || strings.Contains(string(b), "thr_example_") {
					t.Fatal("refusal exposed private binding")
				}
			}
		})
	}
}

func TestPanelRouteLoopControlsModeRejectsEveryStep(t *testing.T) {
	r, c, _ := routePanelFixture(t)
	panelRouteNoCalls(t, r)
	key := panelRouteLoopKey(t, r, "example-loop")
	c.controlsOnly = true
	for _, step := range []string{"read", "prepare", "confirm"} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"op":"own","loop":"`+key+`"}`))
		req.Header.Set(panelFormHeader, c.prepareValue("page"))
		w := httptest.NewRecorder()
		c.serve(w, req, panelRoutesPrefix+step, "page")
		if w.Code != http.StatusForbidden {
			t.Fatalf("controls mode %s: %d", step, w.Code)
		}
	}
}

func TestPanelRouteLoopRefusesAmbiguousNamesAndStaleRegistration(t *testing.T) {
	for _, problem := range []string{"duplicate_name", "config_name", "state_binding"} {
		t.Run(problem, func(t *testing.T) {
			r, c, _ := routePanelFixture(t)
			panelRouteNoCalls(t, r)
			key := panelRouteLoopKey(t, r, "example-loop")
			p := c.prepareRoutes("page", panelRouteInput{Op: "off", Loop: key, ID: "codex-main"})
			if p.Form == "" {
				t.Fatalf("initial preview %+v", p)
			}
			f, err := registry.Load(r.registry)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(r.own.cfg)
			switch problem {
			case "duplicate_name":
				path := filepath.Join(r.own.dir, "duplicate.yaml")
				write(t, path, strings.Replace(string(raw), "state_dir: state", "state_dir: duplicate-state", 1), 0600)
				duplicateState, err := canonicalFuturePath(filepath.Join(r.own.dir, "duplicate-state"))
				if err != nil {
					t.Fatal(err)
				}
				f.Entries = append(f.Entries, registry.Entry{ID: "loop_" + strings.Repeat("b", 32), Loop: "example-loop", ConfigPath: path, StateDir: duplicateState})
				b, _ := json.Marshal(f)
				write(t, filepath.Join(r.registry, registry.FileName), string(b), 0600)
			case "config_name":
				write(t, r.own.cfg, strings.Replace(string(raw), "name: example-loop", "name: replacement-loop", 1), 0600)
			case "state_binding":
				write(t, r.own.cfg, strings.Replace(string(raw), "state_dir: state", "state_dir: replacement-state", 1), 0600)
			}
			routes, _ := os.ReadFile(filepath.Join(r.own.dir, "fallbacks.yaml"))
			if out := c.confirmRoutes("page", p.Form, "off", key); out.Outcome != "review_again" {
				t.Fatalf("stale confirmation %+v", out)
			}
			if out := c.prepareRoutes("page", panelRouteInput{Op: "off", Loop: key, ID: "codex-main"}); out.Outcome != "refused" || out.Form != "" {
				t.Fatalf("stale preview %+v", out)
			}
			if problem == "duplicate_name" {
				duplicate := dockKey(f.Entries[len(f.Entries)-1].ID)
				if out := c.prepareRoutes("page", panelRouteInput{Op: "off", Loop: duplicate, ID: "codex-main"}); out.Outcome != "refused" {
					t.Fatalf("ambiguous second key accepted %+v", out)
				}
			}
			after, _ := os.ReadFile(filepath.Join(r.own.dir, "fallbacks.yaml"))
			if string(after) != string(routes) {
				t.Fatal("ambiguous or stale binding wrote")
			}
		})
	}
}
