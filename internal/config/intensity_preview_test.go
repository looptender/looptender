package config

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Parse/default/validation are fixture SETUP, outside the projector's pure boundary.
func intensityFixture(t *testing.T) *Config {
	t.Helper()
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func intensityProject(t *testing.T, c *Config) *IntensityConfigProjectionV1 {
	t.Helper()
	p, reason := ProjectIntensityConfig(c)
	if p == nil || reason != "" {
		t.Fatalf("projection refused: %s", reason)
	}
	return p
}
func intensityJSON(t *testing.T, p *IntensityConfigProjectionV1) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 4096 {
		t.Fatalf("oversized projection: %d", len(b))
	}
	return string(b)
}

func TestIntensityGolden(t *testing.T) {
	c := intensityFixture(t)
	c.Loop.Adapter = "claude"
	c.Loop.Claude = &Claude{Model: "example-model", Effort: "high", MaxBudgetUSD: 8.5}
	c.Loop.Limits.MaxSends = 240
	got := intensityJSON(t, intensityProject(t, c))
	want := `{"schemaVersion":1,"source":"supplied_config_declarations","configFormatVersion":1,"adapter":"claude","profile":{"state":"not_configured"},"limits":{"sendAllowance":{"state":"declared","count":"240"},"deadline":{"state":"unset","utc":null},"noProgressTimeout":{"state":"unset","nanoseconds":null},"creation":{"state":"not_applicable","newConversations":null,"childSuccessors":null}},"pacing":{"minimumWakeNanoseconds":"3600000000000","idleSpacing":{"state":"off","firstNanoseconds":null,"maximumNanoseconds":null}},"selection":{"model":"configured_redacted","effort":"configured_redacted"},"claudeBudget":{"state":"declared_option","usd":"8.5"},"coverage":{"sharedConcurrency":"not_implemented","allowanceReserve":"not_implemented","periodSpend":"not_implemented","effectivePolicy":"not_computed","dynamicState":"not_sampled","invocationOverrides":"not_evaluated"}}`
	if got != want {
		t.Fatalf("golden mismatch\ngot %s\nwant %s", got, want)
	}
	c = intensityFixture(t)
	p := intensityProject(t, c)
	if p.Adapter != "bb" || p.Limits.SendAllowance.State != "unset" || p.Limits.SendAllowance.Count != nil || p.ClaudeBudget.State != "not_applicable" || p.Selection.Model != "unset" || p.Selection.Effort != "unset" || p.Limits.Creation.State != "declared" || *p.Limits.Creation.NewConversations != c.Loop.Limits.MaxNewThreads || *p.Limits.Creation.ChildSuccessors != c.Loop.Limits.MaxChildSuccessors {
		t.Fatal("BB declaration/null contract")
	}
	c.Loop.Adapter, c.Loop.Codex = "codex", &Codex{Model: "example-model"}
	p = intensityProject(t, c)
	if p.Adapter != "codex" || p.Selection.Model != "configured_redacted" || p.Selection.Effort != "not_applicable" || p.ClaudeBudget.USD != nil || p.ClaudeBudget.State != "not_applicable" || p.Limits.Creation.NewConversations != nil || p.Limits.Creation.ChildSuccessors != nil {
		t.Fatal("Codex applicability contract")
	}
}

func TestIntensityRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
		reason ProjectionReason
	}{
		{"version", func(c *Config) { c.Version = 2 }, ProjectionInvalidConfig},
		{"missing-bb", func(c *Config) { c.Loop.BB = nil }, ProjectionInvalidConfig},
		{"missing-claude", func(c *Config) { c.Loop.Adapter = "claude" }, ProjectionInvalidConfig},
		{"missing-codex", func(c *Config) { c.Loop.Adapter = "codex" }, ProjectionInvalidConfig},
		{"unsupported", func(c *Config) { c.Loop.Adapter = "process" }, ProjectionUnsupportedAdapter},
		{"send-negative", func(c *Config) { c.Loop.Limits.MaxSends = -1 }, ProjectionInvalidConfig},
		{"wake-zero", func(c *Config) { c.Loop.Progress.MinWakeInterval.Duration = 0 }, ProjectionInvalidConfig},
		{"wake-short", func(c *Config) { c.Loop.Progress.MinWakeInterval.Duration = time.Minute - 1 }, ProjectionInvalidConfig},
		{"progress-negative", func(c *Config) { c.Loop.Limits.NoProgressTimeout.Duration = -1 }, ProjectionInvalidConfig},
		{"progress-short", func(c *Config) { c.Loop.Limits.NoProgressTimeout.Duration = time.Minute - 1 }, ProjectionInvalidConfig},
		{"idle-first", func(c *Config) {
			c.Loop.Progress.IdleSpacing = &IdleSpacing{First: Duration{time.Minute - 1}, Max: Duration{time.Hour}}
		}, ProjectionInvalidConfig},
		{"idle-order", func(c *Config) {
			c.Loop.Progress.IdleSpacing = &IdleSpacing{First: Duration{time.Hour}, Max: Duration{time.Minute}}
		}, ProjectionInvalidConfig},
		{"idle-over", func(c *Config) {
			c.Loop.Progress.IdleSpacing = &IdleSpacing{First: Duration{time.Minute}, Max: Duration{24*time.Hour + 1}}
		}, ProjectionInvalidConfig},
		{"creation-zero", func(c *Config) { c.Loop.Limits.MaxNewThreads = 0 }, ProjectionInvalidConfig},
		{"creation-negative", func(c *Config) { c.Loop.Limits.MaxNewThreads = -1 }, ProjectionInvalidConfig},
		{"creation-over", func(c *Config) { c.Loop.Limits.MaxNewThreads = 11 }, ProjectionInvalidConfig},
		{"successor-zero", func(c *Config) { c.Loop.Limits.MaxChildSuccessors = 0 }, ProjectionInvalidConfig},
		{"successor-negative", func(c *Config) { c.Loop.Limits.MaxChildSuccessors = -1 }, ProjectionInvalidConfig},
		{"successor-over", func(c *Config) { c.Loop.Limits.MaxChildSuccessors = 51 }, ProjectionInvalidConfig},
		{"bad-date", func(c *Config) { c.Loop.Limits.Deadline = "2030-02-30T00:00:00Z" }, ProjectionInvalidConfig},
		{"raw-deadline", func(c *Config) { c.Loop.Limits.Deadline = "private-example-sentinel" }, ProjectionInvalidConfig},
		{"old-date", func(c *Config) { c.Loop.Limits.Deadline = "1999-12-31T23:59:59Z" }, ProjectionUnrepresentableConfig},
		{"offset-low", func(c *Config) { c.Loop.Limits.Deadline = "2000-01-01T00:30:00+01:00" }, ProjectionUnrepresentableConfig},
		{"offset-high", func(c *Config) { c.Loop.Limits.Deadline = "9999-12-31T23:30:00-01:00" }, ProjectionUnrepresentableConfig},
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cases = append(cases, struct {
			name   string
			change func(*Config)
			reason ProjectionReason
		}{"budget-" + strconv.FormatFloat(v, 'g', -1, 64), func(c *Config) { c.Loop.Adapter = "claude"; c.Loop.Claude = &Claude{MaxBudgetUSD: v} }, ProjectionUnrepresentableConfig})
	}
	if p, r := ProjectIntensityConfig(nil); p != nil || r != ProjectionInvalidConfig {
		t.Fatal("nil input not refused")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := intensityFixture(t)
			tc.change(c)
			// A second projection must neither default nor mutate a refused input.
			before := *c
			loop := c.Loop
			for i := 0; i < 2; i++ {
				if p, r := ProjectIntensityConfig(c); p != nil || r != tc.reason {
					t.Fatalf("got %v/%q, want nil/%q", p, r, tc.reason)
				}
			}
			// NaN prevents DeepEqual; scalar/nested mutations are covered independently below.
			if c.Version != before.Version || c.Loop.Limits != loop.Limits || c.Loop.Progress.MinWakeInterval != loop.Progress.MinWakeInterval {
				t.Fatal("refusal mutated input")
			}
		})
	}
}

func TestIntensityNumericAndDeadlineBoundaries(t *testing.T) {
	c := intensityFixture(t)
	c.Loop.Limits.MaxSends = math.MaxInt64
	c.Loop.Limits.NoProgressTimeout.Duration = time.Duration(math.MaxInt64)
	c.Loop.Progress.MinWakeInterval.Duration = time.Duration(math.MaxInt64)
	c.Loop.Progress.IdleSpacing = &IdleSpacing{First: Duration{time.Minute + 1}, Max: Duration{24 * time.Hour}}
	c.Loop.Limits.MaxNewThreads = 10
	c.Loop.Limits.MaxChildSuccessors = 50
	p := intensityProject(t, c)
	if *p.Limits.SendAllowance.Count != "9223372036854775807" || *p.Limits.NoProgressTimeout.Nanoseconds != "9223372036854775807" || p.Pacing.MinimumWakeNanoseconds != "9223372036854775807" || *p.Pacing.IdleSpacing.FirstNanoseconds != "60000000001" || *p.Pacing.IdleSpacing.MaximumNanoseconds != "86400000000000" || *p.Limits.Creation.NewConversations != 10 || *p.Limits.Creation.ChildSuccessors != 50 {
		t.Fatal("exact scalar boundaries lost")
	}
	c.Loop.Limits.MaxNewThreads = 1
	c.Loop.Limits.MaxChildSuccessors = 1
	c.Loop.Progress.MinWakeInterval.Duration = time.Minute
	c.Loop.Limits.NoProgressTimeout.Duration = time.Minute
	p = intensityProject(t, c)
	if p.Pacing.MinimumWakeNanoseconds != "60000000000" || *p.Limits.NoProgressTimeout.Nanoseconds != "60000000000" || *p.Limits.Creation.NewConversations != 1 || *p.Limits.Creation.ChildSuccessors != 1 {
		t.Fatal("minimum boundaries")
	}
	for _, tc := range []struct{ input, want string }{
		{"2030-03-04T12:00:00.123456789Z", "2030-03-04T12:00:00.123456789Z"},
		{"2030-03-04T13:00:00.123456789+01:00", "2030-03-04T12:00:00.123456789Z"},
		{"2030-01-01T00:30:00+01:00", "2029-12-31T23:30:00Z"},
		{"1999-12-31T23:30:00-01:00", "2000-01-01T00:30:00Z"},
		{"2000-01-01T00:00:00Z", "2000-01-01T00:00:00Z"},
		{"9999-12-31T23:59:59.999999999Z", "9999-12-31T23:59:59.999999999Z"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			c.Loop.Limits.Deadline = tc.input
			p := intensityProject(t, c)
			if p.Limits.Deadline.State != "declared" || *p.Limits.Deadline.UTC != tc.want {
				t.Fatal("normalized deadline", p.Limits.Deadline)
			}
		})
	}
	// Existing Parse overflow rejection is caller-precondition evidence, not projector decoding.
	if _, err := Parse([]byte(valid + "  limits:\n    max_sends: 9223372036854775808\n")); err == nil {
		t.Fatal("Parse accepted overflowing int64")
	}
}

func TestIntensityBudgetRoundTrip(t *testing.T) {
	grammar := regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:e[+-]?[0-9]+)?$`)
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{1e-9, "1e-09"}, {1e6, "1e+06"}, {1e-10, "1e-10"}, {1e9, "1e+09"}, {8.5, "8.5"},
		{math.SmallestNonzeroFloat64, "5e-324"}, {math.MaxFloat64, "1.7976931348623157e+308"},
		{0.12345678901234566, "0.12345678901234566"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			c := intensityFixture(t)
			c.Loop.Adapter = "claude"
			c.Loop.Claude = &Claude{MaxBudgetUSD: tc.value}
			p := intensityProject(t, c)
			if p.ClaudeBudget.State != "declared_option" || p.ClaudeBudget.USD == nil {
				t.Fatal("budget missing")
			}
			s := *p.ClaudeBudget.USD
			v, err := strconv.ParseFloat(s, 64)
			if s != tc.want || len(s) > 32 || !grammar.MatchString(s) || err != nil || math.Float64bits(v) != math.Float64bits(tc.value) {
				t.Fatalf("budget failed exact roundtrip: %q", s)
			}
		})
	}
	for _, zero := range []float64{0, math.Copysign(0, -1)} {
		c := intensityFixture(t)
		c.Loop.Adapter = "claude"
		c.Loop.Claude = &Claude{MaxBudgetUSD: zero}
		p := intensityProject(t, c)
		if p.ClaudeBudget.State != "unset" || p.ClaudeBudget.USD != nil {
			t.Fatal("zero budget manufactured")
		}
	}
}

func TestIntensityInvalidUnprojectedEquivalence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"name-prompt", func(c *Config) { c.Loop.Name = ""; c.Loop.Prompt = "" }},
		{"window", func(c *Config) {
			c.Loop.Window = &Window{TZ: "example-invalid/no-file", Days: map[string][]string{"invalid": {"not-a-time"}}}
		}},
		{"fallback", func(c *Config) { c.FallbackSet = &Fallbacks{Version: -1} }},
		{"unselected", func(c *Config) { c.Loop.Claude = &Claude{MaxBudgetUSD: math.NaN()}; c.Loop.Codex = &Codex{Workdir: ""} }},
		{"missing-repositories", func(c *Config) { c.Loop.Progress.Repositories = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := intensityFixture(t)
			c.Loop.Limits.NoProgressTimeout.Duration = time.Minute
			c.Loop.Progress.Repositories = []string{"./example"}
			want := intensityJSON(t, intensityProject(t, c))
			tc.mutate(c)
			if got := intensityJSON(t, intensityProject(t, c)); got != want {
				t.Fatal("unprojected declarations affected DTO")
			}
		})
	}
}

func TestIntensityRedactionAndDetachment(t *testing.T) {
	c := intensityFixture(t)
	c.Loop.Adapter = "claude"
	c.Loop.Claude = &Claude{Model: "example-model", Effort: "high", MaxBudgetUSD: 8.5}
	c.Loop.Limits.MaxSends = 24
	c.Loop.Limits.Deadline = "2030-01-01T00:00:00Z"
	c.Loop.Limits.NoProgressTimeout.Duration = time.Minute
	c.Loop.Progress.IdleSpacing = &IdleSpacing{First: Duration{time.Minute}, Max: Duration{time.Hour}}
	p := intensityProject(t, c)
	want := intensityJSON(t, p)
	const secret = "private-example-sentinel"
	c.Loop.Name = secret
	c.Loop.StateDir = secret
	c.Loop.Prompt = secret
	c.Loop.PromptFile = secret
	c.Loop.Provider = secret
	c.Loop.Account = secret
	c.Loop.Group = secret
	c.Loop.Claude.Model = secret
	c.Loop.Claude.Effort = secret
	c.Loop.Claude.CLI = secret
	c.Loop.Claude.Workdir = secret
	c.Loop.Claude.PermissionMode = secret
	c.Loop.Claude.Env = map[string]string{secret: secret}
	c.Loop.Claude.ExtraArgs = []string{secret}
	c.Loop.BB = &BB{Thread: secret, PermissionMode: secret, Model: secret}
	c.Loop.Progress.Repositories = []string{secret}
	c.TimingStepsUnavailable = secret
	c.source = sourceSnapshot{path: secret, size: 123, sum: [32]byte{1}}
	c.routesSource = c.source
	got := intensityJSON(t, intensityProject(t, c))
	if strings.Contains(got, secret) || got != want {
		t.Fatal("private fields leaked/changed output")
	}
	// Results own every optional scalar, not pointers into input or another result.
	second := intensityProject(t, c)
	*p.Limits.SendAllowance.Count = "mutated"
	*p.Limits.Deadline.UTC = "mutated"
	*p.Limits.NoProgressTimeout.Nanoseconds = "mutated"
	*p.Pacing.IdleSpacing.FirstNanoseconds = "mutated"
	*p.Pacing.IdleSpacing.MaximumNanoseconds = "mutated"
	*p.ClaudeBudget.USD = "mutated"
	if intensityJSON(t, second) != want || c.Loop.Limits.MaxSends != 24 || c.Loop.Claude.MaxBudgetUSD != 8.5 {
		t.Fatal("result alias")
	}
	c.Loop.Limits.MaxSends = 99
	c.Loop.Claude.MaxBudgetUSD = 99
	c.Loop.Progress.IdleSpacing.First.Duration = 2 * time.Minute
	if intensityJSON(t, second) != want {
		t.Fatal("input alias")
	}
	bb := intensityFixture(t)
	bp := intensityProject(t, bb)
	old := bb.Loop.Limits
	*bp.Limits.Creation.NewConversations = 10
	*bp.Limits.Creation.ChildSuccessors = 50
	if bb.Loop.Limits != old {
		t.Fatal("creation pointer alias")
	}
	c.Loop.Claude.Model = ""
	c.Loop.Claude.Effort = ""
	empty := intensityProject(t, c)
	if empty.Selection.Model != "unset" || empty.Selection.Effort != "unset" {
		t.Fatal("presence not reflected")
	}
}

func TestIntensityDeterministicConcurrentReads(t *testing.T) {
	c := intensityFixture(t)
	before := *c
	bb := *c.Loop.BB
	want := intensityJSON(t, intensityProject(t, c))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 16; j++ {
				p, r := ProjectIntensityConfig(c)
				if p == nil || r != "" {
					t.Error("concurrent refusal")
					return
				}
				b, e := json.Marshal(p)
				if e != nil || string(b) != want {
					t.Error("nondeterministic projection")
				}
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(*c, before) || !reflect.DeepEqual(*c.Loop.BB, bb) {
		t.Fatal("projection mutated input")
	}
}

func TestIntensityPureDependencyGuard(t *testing.T) {
	// This test reads SOURCE as tooling; production never does. The call allowlist
	// complements behavioral tests and manual transitive review, not an IO sandbox.
	f, err := parser.ParseFile(token.NewFileSet(), "intensity_preview.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	imports := map[string]bool{`"encoding/json"`: true, `"math"`: true, `"strconv"`: true, `"time"`: true}
	for _, i := range f.Imports {
		if !imports[i.Path.Value] {
			t.Errorf("unreviewed import %s", i.Path.Value)
		}
	}
	allowed := map[string]bool{"intensityPresence": true, "len": true, "int64": true, "json.Marshal": true, "math.IsNaN": true, "math.IsInf": true, "strconv.FormatInt": true, "strconv.FormatFloat": true, "time.ParseInLocation": true, "instant.UTC": true, "instant.Year": true, "instant.Format": true}
	for _, declaration := range f.Decls {
		if d, ok := declaration.(*ast.GenDecl); ok && d.Tok == token.VAR {
			t.Error("package mutable state not allowed")
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				if x, ok := fn.X.(*ast.Ident); ok {
					name = x.Name + "." + fn.Sel.Name
				}
			}
			if !allowed[name] {
				t.Errorf("unreviewed call %q", name)
			}
		}
		if s, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == "time" && s.Sel.Name == "Local" {
				t.Error("time.Local forbidden")
			}
		}
		return true
	})
	// Keep the explicit UTC location, not a parse path that consults Local.
	source, err := os.ReadFile("intensity_preview.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "time.ParseInLocation(time.RFC3339Nano, l.Limits.Deadline, time.UTC)") {
		t.Error("deadline parser must use explicit UTC")
	}
}

func TestIntensityParsedAdapters(t *testing.T) {
	for _, adapter := range []string{"bb", "claude", "codex"} {
		t.Run(adapter, func(t *testing.T) {
			raw := valid
			if adapter != "bb" {
				raw = "version: 1\nloop:\n  name: example-loop\n  state_dir: state\n  prompt: Continue the loop.\n  adapter: " + adapter + "\n  " + adapter + ":\n    workdir: ./example\n    model: example-model\n"
			}
			c, err := Parse([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			before := intensityJSON(t, intensityProject(t, c))
			switch adapter {
			case "bb":
				c.Loop.BB.Model = "example-first"
				c.Loop.BB.Reasoning = "example-first"
				first := intensityJSON(t, intensityProject(t, c))
				c.Loop.BB.Model = "private-example-second"
				c.Loop.BB.Reasoning = "private-example-second"
				if intensityJSON(t, intensityProject(t, c)) != first || first == before {
					t.Fatal("BB presence/redaction contract")
				}
			case "claude":
				c.Loop.Limits.MaxNewThreads = -1
				c.Loop.Limits.MaxChildSuccessors = -1
				if intensityJSON(t, intensityProject(t, c)) != before {
					t.Fatal("non-BB creation fields inspected")
				}
			case "codex":
				c.Loop.Codex.Model = "private-example-second"
				c.Loop.Codex.Sandbox = "private-example-sandbox"
				c.Loop.Codex.Env = map[string]string{"private-example-key": "private-example-value"}
				c.Loop.Codex.ExtraArgs = []string{"private-example-argument"}
				if intensityJSON(t, intensityProject(t, c)) != before {
					t.Fatal("Codex raw fields escaped allowlist")
				}
			}
		})
	}
}
