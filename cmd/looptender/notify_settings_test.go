package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotifyStateDefaultsChoicesAndTheMenusHeartbeat(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := readNotifyState(dir, now)
	if !st.Kinds[noticeNeedsYou] || !st.Kinds[noticeLimit] || !st.Kinds[noticeSupervisor] || st.Kinds[noticeDisk] || st.Menu.Notifying || st.Menu.LastRunningAt != nil || st.Browser {
		t.Fatalf("defaults: %+v", st)
	}
	if err := setNotify(dir, noticeDisk, nil, true, now); err != nil {
		t.Fatal(err)
	}
	if err := setNotify(dir, noticeLimit, nil, false, now); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := setNotify(dir, "", &yes, false, now); err != nil {
		t.Fatal(err)
	}
	if err := setNotify(dir, "not_a_kind", nil, true, now); err == nil {
		t.Error("an unknown kind is refused")
	}
	st = readNotifyState(dir, now)
	if !st.Kinds[noticeDisk] || st.Kinds[noticeLimit] || !st.Kinds[noticeNeedsYou] || !st.Browser {
		t.Fatalf("choices: %+v", st)
	}
	// The menu's heartbeat: notifying while fresh, not after, and its last time is kept.
	if err := notifyBeat(dir, true, 0, now); err != nil {
		t.Fatal(err)
	}
	if st = readNotifyState(dir, now.Add(time.Minute)); !st.Menu.Notifying || st.Menu.LastRunningAt == nil || !st.Menu.LastRunningAt.Equal(now) {
		t.Errorf("fresh: %+v", st.Menu)
	}
	if st = readNotifyState(dir, now.Add(notifyMenuFresh+time.Second)); st.Menu.Notifying || st.Menu.LastRunningAt == nil {
		t.Errorf("stale: %+v", st.Menu)
	}
	if err := notifyBeat(dir, false, 0, now); err != nil {
		t.Fatal(err)
	}
	if st = readNotifyState(dir, now); st.Menu.Notifying {
		t.Error("a menu that says it is not notifying is not")
	}
	// A damaged or missing file is the defaults.
	if st = readNotifyState(t.TempDir(), now); !st.Kinds[noticeNeedsYou] {
		t.Error("defaults")
	}
}

func TestPanelNotifyRouteWorksOnAReadOnlyPanelAndGuardsItself(t *testing.T) {
	state, err := newPanelState(54321, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := state.newSession()
	dir := t.TempDir()
	h := &panelHandler{state: state, guard: newDockGuard(54321), page: newDockServer(54321, state, &dockReader{}), notifySecret: []byte("0123456789abcdef0123456789abcdef"), notifyDir: dir}
	form := notifyFormValue(h.notifySecret, session)
	post := func(body, value string, mutate func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:54321"+state.pagePath()+notifyPath, strings.NewReader(body))
		r.Host = "127.0.0.1:54321"
		r.Header.Set("Origin", "http://127.0.0.1:54321")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "same-origin")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(panelFormHeader, value)
		r.AddCookie(&http.Cookie{Name: panelCookieName, Value: session})
		if mutate != nil {
			mutate(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := post(`{"kind":"disk","on":true}`, form, nil)
	if w.Code != 200 {
		t.Fatalf("read-only panel: %d %s", w.Code, w.Body)
	}
	var st notifyState
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if !st.Kinds["disk"] {
		t.Errorf("not saved: %+v", st)
	}
	if !readNotifyState(dir, time.Now()).Kinds["disk"] {
		t.Error("not in the shared file")
	}
	if w = post(`{"browser":true}`, form, nil); w.Code != 200 || !readNotifyState(dir, time.Now()).Browser {
		t.Errorf("browser choice: %d", w.Code)
	}
	for name, c := range map[string]struct {
		body, value string
		mutate      func(*http.Request)
		want        int
	}{
		"wrong form value": {`{"kind":"disk","on":false}`, "x", nil, 403},
		"no form value":    {`{"kind":"disk","on":false}`, "", nil, 403},
		"unknown kind":     {`{"kind":"nope","on":true}`, form, nil, 400},
		"both choices":     {`{"kind":"disk","on":true,"browser":true}`, form, nil, 400},
		"neither":          {`{}`, form, nil, 400},
		"extra field":      {`{"kind":"disk","on":true,"x":1}`, form, nil, 400},
		"another origin":   {`{"kind":"disk","on":false}`, form, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
		"no session":       {`{"kind":"disk","on":false}`, form, func(r *http.Request) { r.Header.Del("Cookie") }, 403},
		"not json":         {`{"kind":"disk","on":false}`, form, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 403},
		"cross-site fetch": {`{"kind":"disk","on":false}`, form, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
	} {
		if w := post(c.body, c.value, c.mutate); w.Code != c.want {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if !readNotifyState(dir, time.Now()).Kinds["disk"] {
		t.Error("a refused request changed a setting")
	}
	// Any other write is still refused on a read-only panel.
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:54321"+state.pagePath()+"api/v1/changes/l000000000001/prepare", strings.NewReader(`{"action":"pause"}`))
	r.Host = "127.0.0.1:54321"
	r.Header.Set("Origin", "http://127.0.0.1:54321")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "same-origin")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(panelFormHeader, form)
	r.AddCookie(&http.Cookie{Name: panelCookieName, Value: session})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a loop change on a read-only panel: %d", rec.Code)
	}
	// The server's answer carries the value, on a read-only panel.
	sw := httptest.NewRecorder()
	h.served(sw)(session)
	var served dockServed
	_ = json.Unmarshal(sw.Body.Bytes(), &served)
	if served.NotifyForm != form || served.Form != "" {
		t.Errorf("served: %q %q", served.NotifyForm, served.Form)
	}
}

func TestPanelListAndTheDockReportCarryTheNotifyState(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	_ = notifyBeat(dir, true, 0, now)
	r := buildDockReport(nil, now, time.Minute, defaultStaleAfter, false, true, 0, true)
	r.Notify = readNotifyState(dir, now)
	b, _ := json.Marshal(buildPanelList(r))
	if !strings.Contains(string(b), `"notify":{"kinds":`) || !strings.Contains(string(b), `"notifying":true`) {
		t.Errorf("%s", b)
	}
}

func TestPanelNotifyCommandWritesTheSharedFilesAndRefusesBadArguments(t *testing.T) {
	base := t.TempDir()
	previous := panelStateBase
	panelStateBase = func() (string, error) { return base, nil }
	defer func() { panelStateBase = previous }()
	run := func(args ...string) (int, string) {
		var out, errs bytes.Buffer
		code := run(context.Background(), append([]string{"panel", "notify"}, args...), &out, &errs)
		return code, errs.String()
	}
	if code, e := run("set", "--kind", "hold", "--on"); code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	if code, _ := run("heartbeat", "--notifying=true"); code != 0 {
		t.Fatal(code)
	}
	st := readNotifyState(panelClientsDir(""), time.Now())
	if !st.Kinds["hold"] || !st.Menu.Notifying {
		t.Errorf("%+v", st)
	}
	for _, bad := range [][]string{{}, {"set"}, {"set", "--kind", "nope", "--on"}, {"set", "--kind", "hold"}, {"set", "--kind", "hold", "--on", "--off"}, {"heartbeat", "extra"}, {"unknown"}} {
		if code, _ := run(bad...); code != exitUsage {
			t.Errorf("%v: %d", bad, code)
		}
	}
}

func TestAQuittingMenuCannotRetractTheMenuThatReplacedIt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	_ = notifyBeat(dir, true, 100, now)
	// The new menu (200) has beaten; the old one (100) quits late with "not notifying".
	_ = notifyBeat(dir, true, 200, now.Add(2*time.Second))
	_ = notifyBeat(dir, false, 100, now.Add(3*time.Second))
	if st := readNotifyState(dir, now.Add(4*time.Second)); !st.Menu.Notifying {
		t.Error("the old menu's late quit retracted the new menu's beat")
	}
	// A menu quitting while its own beat is the file's says so.
	_ = notifyBeat(dir, false, 200, now.Add(5*time.Second))
	if st := readNotifyState(dir, now.Add(6*time.Second)); st.Menu.Notifying {
		t.Error("a menu that quits retracts its own beat")
	}
}

func TestHostIsAKindThatIsOnByDefaultAndCanBeSwitched(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	if st := readNotifyState(dir, now); !st.Kinds[noticeHost] {
		t.Fatalf("host is on by default: %+v", st.Kinds)
	}
	if err := setNotify(dir, noticeHost, nil, false, now); err != nil {
		t.Fatal(err)
	}
	if st := readNotifyState(dir, now); st.Kinds[noticeHost] {
		t.Error("host cannot be turned off")
	}
	if !notifyKindKnown("host") {
		t.Error("host is not a known kind")
	}
}
