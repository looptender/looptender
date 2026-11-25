//go:build unix

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
)

// The default panel can pause, stop and resume, each after its review, and
// nothing more: no build replacement, route or send-allowance edits,
// account selection review, sign-in or quota reads, which need
// --allow-changes. The page is told which.
func TestTheDefaultPanelPausesStopsAndResumesOnly(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	w := newPanelControlsWorld(t)
	if !w.rec.Controls || w.rec.Changes {
		t.Fatalf("%+v", w.rec)
	}
	_, body := dockRequest(t, http.MethodGet, w.page+dockServerPath, withCookie(w.cookie))
	var served dockServed
	if err := json.Unmarshal([]byte(body), &served); err != nil {
		t.Fatal(err)
	}
	if served.Changes != "on" || served.Form == "" || served.RouteChanges != "" || served.AccountQuota || served.AccountSelectionReview || served.AccountCheck || served.AccountRemove || served.SendAllowanceEdit || len(served.AccountConnectProviders) != 0 ||
		!strings.HasPrefix(served.ChangesText.render(dockNow), "Pause, stop and resume allowed for") || !strings.Contains(served.ChangesText.render(dockNow), "need looptender panel --allow-changes") {
		t.Fatalf("served: %s", body)
	}
	e := w.entry(t, "docs-site-refresh")
	key := dockKey(e.ID)
	// Pause, then resume, each through its review.
	for _, action := range []string{"pause", "resume"} {
		_, p := w.post(t, key, "prepare", w.form, `{"action":"`+action+`"}`, nil)
		if p.Preview == nil {
			t.Fatalf("%s: no review: %+v", action, p)
		}
		_, r := w.post(t, key, "confirm", p.Preview.Form, `{"action":"`+action+`"}`, nil)
		if r.Result == nil || r.Result.Outcome != "done" {
			t.Fatalf("%s: %+v", action, r.Result)
		}
	}
	if c := controlOf(t, e); c.Paused {
		t.Fatalf("not resumed: %+v", c)
	}
	// A build replacement is refused before any review.
	if resp, p := w.post(t, key, "prepare", w.form, `{"action":"replace_build"}`, nil); resp.StatusCode == http.StatusOK || p.Preview != nil {
		t.Fatalf("build replacement offered: %d %+v", resp.StatusCode, p)
	}
	// The other changes' endpoints answer nothing.
	for _, prefix := range []string{panelRoutesPrefix, panelAccountConnectPrefix, panelAccountSelectionPrefix, panelAccountQuotaPrefix, panelAccountCheckPrefix, panelAccountRemovePrefix, panelSendAllowancePrefix} {
		req, _ := http.NewRequest(http.MethodPost, w.page+prefix+"x/prepare", strings.NewReader(`{}`))
		req.Header.Set("Origin", w.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(panelFormHeader, w.form)
		req.AddCookie(w.cookie)
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: %d", prefix, resp.StatusCode)
		}
	}
	// The page is never offered the build change.
	_, page := dockRequest(t, http.MethodGet, w.page+dockAPIPath, withCookie(w.cookie))
	if strings.Contains(page, `"action":"replace_build"`) {
		t.Fatal("the default panel offered a build replacement")
	}
}

// The supervised conversation, and anything its supervisor started, cannot
// use the default panel to lift its own loop's hold: the resume is refused
// before any review, as on the command line.
func TestTheDefaultPanelRefusesTheSupervisedSessionsOwnResume(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	w := newPanelControlsWorld(t)
	e := w.entry(t, "docs-site-refresh")
	if _, err := control.Update(e.StateDir, func(f *control.File) { f.Paused = true }); err != nil {
		t.Fatal(err)
	}
	t.Setenv(control.SupervisedLoopVar, e.StateDir)
	_, p := w.post(t, dockKey(e.ID), "prepare", w.form, `{"action":"resume"}`, nil)
	if p.Preview != nil || p.Result == nil || p.Result.Text != panelSupervisedSession+" Nothing was changed." {
		t.Fatalf("%+v", p)
	}
	if !controlOf(t, e).Paused {
		t.Fatal("the hold was lifted")
	}
	// A process its supervisor started is an agent, never a person, for the
	// panel: it does not share a person's panel.
	if panelCallerKind() != "agent" || panelCallerHash() == "" {
		t.Fatalf("%s %q", panelCallerKind(), panelCallerHash())
	}
	if address, err := panelRequestOpening(context.Background(), w.dir, w.rec, "", false); err == nil || address != "" {
		t.Fatalf("a supervised process opened a person's panel: %s %v", address, err)
	}
	// The background panel keeps the marker, so it refuses as the command does.
	found := false
	for _, kv := range panelServerEnvironment() {
		found = found || kv == control.SupervisedLoopVar+"="+e.StateDir
	}
	if !found {
		t.Fatal("the panel's environment drops the supervised-loop marker")
	}
}

// Opening the panel again without a flag turns a running read-only panel
// into the default one, by stopping it and starting one (never two): a page
// open on it ends. --allow-changes strengthens the default one the same way;
// the default reuses a panel that allows every change; --read-only against
// a panel that changes loops is refused.
func TestOpeningThePanelAgainStrengthensItsMode(t *testing.T) {
	w := newPanelProcessWorld(t)
	w.env = append(w.env, "BB_THREAD_ID=")
	if code, out, errs := w.open("--read-only", "--no-open"); code != exitOK || strings.Contains(out, "can pause") {
		t.Fatalf("read-only: %d %s %s", code, out, errs)
	}
	ro := w.record()
	if ro.mode() != panelReadOnly {
		t.Fatalf("%+v", ro)
	}
	code, out, errs := w.open("--no-open")
	if code != exitOK || !strings.Contains(out, "Restarted the running panel") || !strings.Contains(out, "The page can pause, stop and resume loops") {
		t.Fatalf("default: %d %s %s", code, out, errs)
	}
	ctl := w.record()
	if ctl.mode() != panelControlsMode || ctl.Instance == ro.Instance {
		t.Fatalf("%+v", ctl)
	}
	if code, out, errs := w.open("--allow-changes", "--no-open"); code != exitOK || !strings.Contains(out, "Restarted the running panel") {
		t.Fatalf("all changes: %d %s %s", code, out, errs)
	}
	full := w.record()
	if full.mode() != panelFullMode || full.Instance == ctl.Instance {
		t.Fatalf("%+v", full)
	}
	if code, out, errs := w.open("--no-open"); code != exitOK || strings.Contains(out, "Restarted") {
		t.Fatalf("default on a full panel: %d %s %s", code, out, errs)
	}
	if w.record() != full {
		t.Fatal("the default replaced a panel that allows every change")
	}
	if code, _, errs := w.open("--read-only", "--no-open"); code != exitFailed || !strings.Contains(errs, "stop first to open a read-only one") {
		t.Fatalf("read-only on a full panel: %d %s", code, errs)
	}
	if code, _, errs := w.open("--read-only", "--allow-changes", "--no-open"); code != exitUsage {
		t.Fatalf("both flags: %d %s", code, errs)
	}
	if code, out, _ := w.run("panel", "status", "--json", "--panel-directory", w.dir); code != exitOK || !strings.Contains(out, `"mode":"all"`) {
		t.Fatalf("status: %d %s", code, out)
	}
}

func TestDefaultPanelAndClientArchiveAndUnarchiveAfterReview(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	w := newPanelControlsWorld(t)
	e := w.entry(t, "docs-site-refresh")
	key := dockKey(e.ID)

	// Supervising loop can be reviewed & archived from the default panel without deleting files.
	_, archPrep := w.post(t, key, "prepare", w.form, `{"action":"archive"}`, nil)
	if archPrep.Preview == nil || archPrep.Preview.Action != "archive" || archPrep.Preview.Confirm != "Archive docs-site-refresh" {
		t.Fatalf("archive prepare: %+v", archPrep)
	}
	var previewText strings.Builder
	for _, line := range archPrep.Preview.Lines {
		previewText.WriteString(line.render(dockNow))
		previewText.WriteByte('\n')
	}
	if !strings.Contains(previewText.String(), "No files or records are deleted") || !strings.Contains(previewText.String(), "Unarchive") {
		t.Fatalf("archive preview text missing explanation: %s", previewText.String())
	}
	// Group archive must still be refused.
	if resp, gp := w.post(t, panelGroupKey, "prepare", w.form, `{"action":"archive"}`, nil); resp.StatusCode == http.StatusOK && (gp.Preview != nil || gp.Result == nil || gp.Result.Outcome != "refused") {
		t.Fatalf("group archive not refused: %d %+v", resp.StatusCode, gp)
	}
	_, archDone := w.post(t, key, "confirm", archPrep.Preview.Form, `{"action":"archive"}`, nil)
	if archDone.Result == nil || archDone.Result.Outcome != "done" || !strings.Contains(archDone.Result.Text, "Archived") {
		t.Fatalf("archive confirm: %+v", archDone.Result)
	}
	if arch, err := control.LoadArchive(e.StateDir); err != nil || arch == nil {
		t.Fatalf("expected archive record on disk: arch=%+v err=%v", arch, err)
	}
	if !controlOf(t, e).Paused {
		t.Fatalf("expected supervising loop to be paused when archived: %+v", controlOf(t, e))
	}

	// Once archived, panel offers unarchive, and client review/apply unarchives cleanly without resuming.
	regDir := filepath.Join(w.root, "registry")
	var listJSON panelList
	code, listOut, listErr := runWith("panel", "list", "--json", "--directory", regDir, "--offline")
	if code != exitOK || json.Unmarshal([]byte(listOut), &listJSON) != nil {
		t.Fatalf("panel list: code=%d err=%s out=%s", code, listErr, listOut)
	}
	var found *panelListLoop
	for i := range listJSON.Loops {
		if listJSON.Loops[i].Key == key {
			found = &listJSON.Loops[i]
		}
	}
	if found == nil || found.State != "archived" || strings.Join(found.Can, ",") != "unarchive" {
		t.Fatalf("archived loop row: %+v", found)
	}

	// Client review + apply unarchive (path shared with macOS menu).
	var rev clientReviewReply
	code, revOut, revErr := runWith("client", "review", "unarchive", "--loop", key, "--directory", regDir, "--json")
	if code != exitOK || json.Unmarshal([]byte(revOut), &rev) != nil || rev.Preview == nil || rev.Preview.Action != "unarchive" {
		t.Fatalf("client review unarchive: code=%d err=%s out=%s", code, revErr, revOut)
	}
	var app clientReviewReply
	code, appOut, appErr := runWith("client", "apply", "unarchive", "--loop", key, "--expect", rev.Preview.Expect, "--directory", regDir, "--json")
	if code != exitOK || json.Unmarshal([]byte(appOut), &app) != nil || app.Result == nil || app.Result.Outcome != "done" {
		t.Fatalf("client apply unarchive: code=%d err=%s out=%s", code, appErr, appOut)
	}
	if arch, err := control.LoadArchive(e.StateDir); err != nil || arch != nil {
		t.Fatalf("expected archive record removed: arch=%+v err=%v", arch, err)
	}
	if !controlOf(t, e).Paused {
		t.Fatalf("unarchive should leave holds intact without resuming: %+v", controlOf(t, e))
	}
}
