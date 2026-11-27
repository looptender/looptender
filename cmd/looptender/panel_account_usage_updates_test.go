package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

func TestPanelAccountUsageUpdatesReviewedOptInAndRevocation(t *testing.T) {
	dir, e := accountFixture(t, "codex")
	reader := &dockReader{accountsDir: dir}
	c, err := newPanelChanges(context.Background(), "", reader, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	c.quotaRun = func(context.Context, string, string, []string, string) ([]byte, error) {
		t.Fatal("settings queried provider")
		return nil, nil
	}
	state, err := newPanelState(54321, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := state.newSession()
	handler := &panelHandler{state: state, guard: newDockGuard(54321), page: newDockServer(54321, state, reader), changes: c}
	request := func(step, action, form string, edit func(*http.Request)) (int, panelChangeReply) {
		r := httptest.NewRequest(http.MethodPost, state.base()+state.pagePath()+panelAccountUsagePrefix+e.ID+"/"+step, strings.NewReader(`{"action":"`+action+`"}`))
		r.Header.Set("Origin", state.base())
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set(panelFormHeader, form)
		r.AddCookie(&http.Cookie{Name: panelCookieName, Value: session})
		if edit != nil {
			edit(r)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var reply panelChangeReply
		_ = json.Unmarshal(w.Body.Bytes(), &reply)
		return w.Code, reply
	}
	for _, kind := range []string{"origin", "metadata", "csrf", "cookie", "controls_only", "read_only"} {
		c.controlsOnly = kind == "controls_only"
		if kind == "read_only" {
			handler.changes = nil
		}
		status, _ := request("prepare", panelAccountUsageEnable, c.prepareValue(session), func(r *http.Request) {
			switch kind {
			case "origin":
				r.Header.Set("Origin", "https://example.com")
			case "metadata":
				r.Header.Del("Sec-Fetch-Site")
			case "csrf":
				r.Header.Set(panelFormHeader, "wrong")
			case "cookie":
				r.Header.Del("Cookie")
			}
		})
		c.controlsOnly = false
		handler.changes = c
		if status == 200 {
			t.Fatal("guard accepted", kind)
		}
	}
	code, review := request("prepare", panelAccountUsageEnable, c.prepareValue(session), nil)
	if code != 200 || review.Preview == nil || review.Preview.Confirm != "Keep usage updated" {
		t.Fatal(code, review)
	}
	settings, _ := accounts.LoadUsagePolling(dir)
	if len(settings.Entries) != 0 {
		t.Fatal("prepare authorized polling")
	}
	_, result := request("confirm", panelAccountUsageEnable, review.Preview.Form, nil)
	if result.Result == nil || result.Result.Outcome != "done" {
		t.Fatal(result)
	}
	settings, _ = accounts.LoadUsagePolling(dir)
	if len(settings.Entries) != 1 || settings.Entries[0].AttemptAt != nil {
		t.Fatal("enable acquired or failed", settings)
	}
	_, replay := request("confirm", panelAccountUsageEnable, review.Preview.Form, nil)
	if replay.Result == nil || replay.Result.Outcome != "review_again" {
		t.Fatal("review replay accepted")
	}
	_, disable := request("prepare", panelAccountUsageDisable, c.prepareValue(session), nil)
	if disable.Preview == nil {
		t.Fatal(disable)
	}
	_, result = request("confirm", panelAccountUsageDisable, disable.Preview.Form, nil)
	settings, _ = accounts.LoadUsagePolling(dir)
	if result.Result.Outcome != "done" || len(settings.Entries) != 0 {
		t.Fatal("revoke failed")
	}
	_, review = request("prepare", panelAccountUsageEnable, c.prepareValue(session), nil)
	err = accounts.Update(dir, func(f *accounts.File) error { f.Entries[0].Revision = strings.Repeat("c", 32); return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, result = request("confirm", panelAccountUsageEnable, review.Preview.Form, nil)
	if result.Result.Outcome != "refused" {
		t.Fatal("changed selection enabled")
	}
	settings, _ = accounts.LoadUsagePolling(dir)
	if len(settings.Entries) != 0 {
		t.Fatal("changed selection authorized")
	}
}
