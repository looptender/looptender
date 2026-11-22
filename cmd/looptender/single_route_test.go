package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// The incident this covers: a single-route loop with failover off and no
// report section waited out a limit days long, and only status said so.
// doctor now warns about the configuration and about the wait itself, and
// status says that nobody was told and what would let it continue.
func TestLongLimitWaitWithNoHandoffIsPlain(t *testing.T) {
	f := newDoctorFixture(t)
	f.loop["errors"] = map[string]any{"failover_on": []string{}}
	f.save(t)
	now := time.Now().UTC()
	rec := &state.LimitWaitReport{Target: "primary", ResetsAt: now.Add(132 * time.Hour), DetectedAt: now.Add(-time.Hour), Outcome: state.LimitWaitNotSubscribed, Reason: "limit_failover_disabled", ReportSection: "none"}
	if err := state.Save(f.state, state.File{LimitWaitReport: rec}); err != nil {
		t.Fatal(err)
	}
	control.Update(f.state, func(c *control.File) { c.Enabled = true })
	_, r, _ := f.run(t, successfulDoctorRun, "--offline")
	single := doctorCheckByID(t, r, "routes.single")
	if single.Status != "warning" || !strings.Contains(single.Message, "single route: it has no other route to hand off to") ||
		!strings.Contains(single.Message, "Any provider usage limit stops its work for the limit's full length") || !strings.Contains(single.Message, "nobody is told") || !strings.Contains(single.Fix, "fallbacks add") {
		t.Fatalf("%+v", single)
	}
	wait := doctorCheckByID(t, r, "limit_wait")
	for _, want := range []string{"Long limit wait: primary", "in about 5d ", "automatic handoff is off for this error", "Nobody was told: the loop had no report section when the wait was recorded", "not held"} {
		if !strings.Contains(wait.Message, want) {
			t.Fatalf("limit_wait lacks %q: %+v", want, wait)
		}
	}
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if status(env{context.Background(), &out, &out}, cfg, now, false, defaultStaleAfter) != exitOK {
		t.Fatal(out.String())
	}
	for _, want := range []string{"LONG LIMIT WAIT: no handoff is possible because automatic handoff is off for this error", "NOBODY WAS TOLD", "To continue before the reset"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status lacks %q:\n%s", want, out.String())
		}
	}
	s := describeLimitWait(rec, cfg.Loop, "", now)
	if s.Reason != "limit_failover_disabled" || s.ReportConfigured == nil || *s.ReportConfigured || s.ResetIn != "5d 12h" {
		t.Fatalf("%+v", s)
	}
	// Once the reset has passed, doctor no longer calls it a long wait.
	rec.ResetsAt = now.Add(-time.Minute)
	state.Save(f.state, state.File{LimitWaitReport: rec})
	_, r, _ = f.run(t, successfulDoctorRun, "--offline")
	for _, c := range r.Checks {
		if c.ID == "limit_wait" {
			t.Fatalf("passed reset still reported: %+v", c)
		}
	}
}

func TestSingleRouteWarning(t *testing.T) {
	base := func() *config.Config {
		return &config.Config{Loop: config.Loop{Errors: config.ErrorRules{FailoverOn: []string{"rate_limit"}}}}
	}
	cfg := base()
	if w := singleRouteWarning(cfg); !strings.Contains(w, "no other route") || !strings.Contains(w, "nobody is told") {
		t.Fatal(w)
	}
	cfg.FallbackSet = &config.Fallbacks{Candidates: []config.Candidate{{ID: "a", Disabled: true}}}
	if w := singleRouteWarning(cfg); !strings.Contains(w, "no other route") {
		t.Fatal("a disabled route counted:", w)
	}
	cfg.FallbackSet.Candidates = append(cfg.FallbackSet.Candidates, config.Candidate{ID: "b"})
	if w := singleRouteWarning(cfg); w != "" {
		t.Fatal(w)
	}
	cfg.Loop.Errors.FailoverOn = nil
	cfg.Loop.Report = &config.Report{}
	if w := singleRouteWarning(cfg); !strings.Contains(w, "errors.failover_on does not include rate_limit") || strings.Contains(w, "nobody is told") {
		t.Fatal(w)
	}
}

// The panel card says whether anyone was told about a long limit wait.
func TestDockSaysWhetherAnyoneWasToldOfALimitWait(t *testing.T) {
	reset := dockNow.Add(100 * time.Hour)
	for _, c := range []struct {
		state      string
		configured bool
		told, adds string
	}{
		{"not_subscribed", false, "no_report_section", "Nobody was told: the loop had no report section when the wait was recorded."},
		{"not_subscribed", true, "not_subscribed", "Nobody was told: limit_wait was not among the reported events."},
		{"report_failed", true, "report_failed", "Its limit_wait report failed; delivery is not confirmed."},
		{"reported", true, "reported", ""},
	} {
		in := dockEvidence{controls: &clientControls{Enabled: true}, status: func(s *statusOut) {
			configured := c.configured
			s.LimitWait = limitWaitStatus{State: c.state, Target: "primary", ResetsAt: &reset, Reason: "limit_failover_disabled", ReportConfigured: &configured}
		}}.input()
		l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false)
		if l.State != "waiting_on_limit" || l.LimitWait.Told != c.told || l.LimitWait.Reason != "limit_failover_disabled" ||
			(c.adds != "" && !strings.Contains(l.Detail, c.adds)) || (c.state != "not_subscribed" && strings.Contains(l.Detail, "Nobody was told")) {
			t.Errorf("%s/%t: %+v %q", c.state, c.configured, l.LimitWait, l.Detail)
		}
	}
}

// init warns, on stderr, that a single-route loop stops for any provider
// limit; with a fallback that can take over on a usage limit it does not.
func TestInitWarnsAboutASingleRoute(t *testing.T) {
	f := newSetupFixture(t)
	code, _, errOut := f.run(t, append(f.args(), "--dry-run"), "")
	if code != 0 || !strings.Contains(errOut, "warning: This loop has a single route") || !strings.Contains(errOut, "stops its work for the limit's full length") || !strings.Contains(errOut, "fallbacks add") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, _, errOut = f.run(t, append(f.args(), "--dry-run", "--fallback", "claude:another-pool"), "")
	if code != 0 || strings.Contains(errOut, "single route") {
		t.Fatalf("%d %q", code, errOut)
	}
}

// Review regressions: a held loop's recorded wait is not presented as
// current, and nothing claims the loop is not held; a failed report is
// never said to have told nobody.
func TestHeldLoopAndFailedReportAreWordedTruthfully(t *testing.T) {
	f := newDoctorFixture(t)
	f.loop["errors"] = map[string]any{"failover_on": []string{}}
	f.save(t)
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := &state.LimitWaitReport{Target: "primary", ResetsAt: now.Add(132 * time.Hour), DetectedAt: now.Add(-time.Hour), Outcome: state.LimitWaitNotSubscribed, Reason: "limit_failover_disabled", ReportSection: "none"}
	state.Save(f.state, state.File{LimitWaitReport: rec})
	control.Update(f.state, func(c *control.File) { c.Enabled = true })
	for _, hold := range []string{"complete", "pause", "stop"} {
		var out bytes.Buffer
		if code := run(context.Background(), []string{hold, "-c", f.config}, &out, &out); code != 0 {
			t.Fatalf("%s: %d %s", hold, code, out.String())
		}
		_, r, _ := f.run(t, successfulDoctorRun, "--offline")
		for _, c := range r.Checks {
			if c.ID == "limit_wait" {
				t.Fatalf("%s: held loop reported a current wait: %+v", hold, c)
			}
			if strings.Contains(c.Message, "not held") {
				t.Fatalf("%s: %s says the loop is not held: %s", hold, c.ID, c.Message)
			}
		}
		out.Reset()
		status(env{context.Background(), &out, &out}, cfg, now, false, defaultStaleAfter)
		text := limitWaitLines(out.String())
		if !strings.Contains(text, "the loop is held (") || strings.Contains(text, "not held") || strings.Contains(text, "NOBODY WAS TOLD") || strings.Contains(text, "LONG LIMIT WAIT") {
			t.Fatalf("%s: status:\n%s", hold, limitWaitLines(text))
		}
		out.Reset()
		run(context.Background(), []string{"resume", "-c", f.config}, &out, &out)
	}
	rec.Outcome = state.LimitWaitFailed
	state.Save(f.state, state.File{LimitWaitReport: rec})
	s := describeLimitWait(rec, cfg.Loop, "", now)
	if told := limitWaitTold(s); !strings.Contains(told, "delivery not confirmed") || strings.Contains(told, "Nobody") {
		t.Fatal(told)
	}
	for d, want := range map[time.Duration]string{132 * time.Hour: "5d 12h", 7*time.Hour + 5*time.Minute: "7h 5m", 12 * time.Minute: "12m"} {
		if got := humanDuration(d); got != want {
			t.Errorf("%s: %s", d, got)
		}
	}
}

func limitWaitLines(text string) string {
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "limit") || strings.Contains(l, "the loop is held") || strings.Contains(l, "NOBODY") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}
