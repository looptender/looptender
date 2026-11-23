package supervise

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
)

// A loop on an observed sessionpipe target, with no prompt, runs its checks
// and never sends to or stops the session, whatever the session's state.
func TestObservedTargetNeverSendsOrStops(t *testing.T) {
	for _, tc := range []struct {
		name string
		obs  decide.Observation
		stop bool
		want string
	}{
		{"idle", decide.Observation{State: decide.Idle}, false, "between turns"},
		{"errored", decide.Observation{State: decide.Error, ErrorClass: decide.ErrUnknown}, false, "ended in error"},
		{"rate limited", decide.Observation{State: decide.Error, ErrorClass: decide.ErrRateLimit}, false, "ended in error"},
		{"waiting on a person", decide.Observation{State: decide.Active, AwaitingInput: true}, false, "waiting on a person"},
		{"stop asked while active", decide.Observation{State: decide.Active}, true, "never stopped by looptender"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true, false)
			w.cfg.Loop.Prompt = ""
			w.cfg.Loop.Agent = config.Agent{Adapter: config.SessionpipeAdapter, Provider: config.SessionpipeAdapter, Group: config.SessionpipeAdapter,
				Sessionpipe: &config.Sessionpipe{Session: "claude-code:example", EventsFile: "/example/events.jsonl"}}
			if tc.stop {
				if _, err := control.Update(w.dir, func(f *control.File) { f.Stop = true }); err != nil {
					t.Fatal(err)
				}
			}
			p := w.primary(tc.obs)
			for i := range 4 {
				res, err := Tick(context.Background(), w.cfg, w.opts(t0.Add(time.Duration(i)*3*time.Hour)))
				if err != nil {
					t.Fatalf("check %d: %v", i, err)
				}
				if res.Action != string(decide.None) || !strings.Contains(res.Reason, tc.want) {
					t.Fatalf("check %d: %s (%q), want none saying %q", i, res.Action, res.Reason, tc.want)
				}
			}
			if len(p.sent) != 0 || p.stopped != 0 || p.observed == 0 {
				t.Fatalf("sent %d, stopped %d, observed %d", len(p.sent), p.stopped, p.observed)
			}
		})
	}
	// A gone session holds for a person, as any gone target does.
	w := newWorld(t, true, false)
	w.cfg.Loop.Prompt = ""
	w.cfg.Loop.Agent = config.Agent{Adapter: config.SessionpipeAdapter, Sessionpipe: &config.Sessionpipe{Session: "codex:example", EventsFile: "/example/events.jsonl"}}
	p := w.primary(decide.Observation{State: decide.Idle, Gone: true})
	if res := runTick(t, w, t0); res.Action != string(decide.AwaitUser) || len(p.sent) != 0 {
		t.Fatalf("gone: %+v", res)
	}
}

// Every decision in this package is taken from an input that says whether
// the target is observed: through t.decide, which sets it, or from the
// check's own input, built with it. A new call elsewhere has to be added
// here, where a reviewer sees it.
func TestEveryDecisionKnowsAnObservedTarget(t *testing.T) {
	allowed := map[string]bool{
		"incident.go:decide":        true, // sets ObservedOnly itself
		"wake.go:PollWake":          true, // passes ObservedOnly
		"hungturn.go:hungTurn":      true, // the check's input
		"hungturn.go:stallRuleActs": true, // the check's input
		"supervise.go:run":          true, // the check's input, for lane recovery
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Decide" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "decide" && !allowed[name+":"+fn.Name.Name] {
						t.Errorf("%s: %s calls decide.Decide; take the decision through t.decide or from the check's input", fset.Position(call.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	// The check's own input, which lane recovery and the hung-turn rule
	// decide from, is built with ObservedOnly from the target.
	f, err := parser.ParseFile(fset, "supervise.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	built := false
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Input" {
			return true
		}
		for _, e := range lit.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, _ := kv.Key.(*ast.Ident)
			call, _ := kv.Value.(*ast.CallExpr)
			if key != nil && key.Name == "ObservedOnly" && call != nil {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "observedOnly" {
					built = true
				}
			}
		}
		return true
	})
	if !built {
		t.Error("supervise.go builds the check's decide.Input without ObservedOnly: t.observedOnly()")
	}
	// And the hung-turn rule's stall check holds for an observed target.
	in := decide.Input{Obs: decide.Observation{State: decide.Active}, Control: decide.Control{Enabled: true}, InWindow: true,
		ProgressTracked: true, UnchangedFor: 24 * time.Hour, StallAfter: time.Hour, MaxNudges: 3}
	if !stallRuleActs(in) {
		t.Fatal("control: a stalled ordinary target should act")
	}
	in.ObservedOnly = true
	if stallRuleActs(in) {
		t.Fatal("the stall rule acts on an observed target")
	}
}
