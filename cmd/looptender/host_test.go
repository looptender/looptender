package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/hostcheck"
)

func TestDoctorAndNoticesSayTheSessionIsGoneNotSignedOut(t *testing.T) {
	gone := hostStatus{Session: hostcheck.Session{Available: false, Keychain: hostcheck.Unavailable, DNS: hostcheck.Unavailable, Console: hostcheck.Unavailable},
		Load: hostcheck.Load{Level: hostcheck.LoadOverloaded, Load1: 120, Cores: 12, PerCore: 10, MemoryFreePercent: 4}}
	var r doctorReport
	doctorHost(&r, gone)
	byID := map[string]doctorCheck{}
	for _, c := range r.Checks {
		byID[c.ID] = c
	}
	s, l := byID["host.session"], byID["host.load"]
	if s.Status != "error" || !strings.Contains(s.Message, "signed nothing out") || strings.Contains(strings.ToLower(s.Fix), "sign in to") || !strings.Contains(s.Fix, "does not help") {
		t.Fatalf("session: %+v", s)
	}
	if l.Status != "warning" || !strings.Contains(l.Message, "overloaded") || !strings.Contains(l.Message, "120.0 on 12 cores") {
		t.Fatalf("load: %+v", l)
	}
	n := hostNotices(&gone, time.Unix(0, 0))
	if len(n) != 2 || n[0].Kind != noticeHost || n[0].Loop != "" || !strings.Contains(n[0].Text, "signed nothing out") || n[1].ID == n[0].ID {
		t.Fatalf("notices: %+v", n)
	}
	fine := hostStatus{Session: hostcheck.Session{Available: true, Keychain: hostcheck.OK, DNS: hostcheck.OK, Console: hostcheck.OK}, Load: hostcheck.Load{Level: hostcheck.LoadOK, Load1: 1, Cores: 8}}
	if n := hostNotices(&fine, time.Unix(0, 0)); len(n) != 0 {
		t.Fatalf("no notices when fine: %+v", n)
	}
	var out strings.Builder
	printHost(&out, &fine)
	if out.Len() != 0 {
		t.Fatalf("status says nothing when fine: %q", out.String())
	}
	printHost(&out, &gone)
	if !strings.Contains(out.String(), "host:      "+hostcheck.Message) || !strings.Contains(out.String(), "load:      overloaded") {
		t.Fatalf("status: %q", out.String())
	}
}

// The test-only variable that fixes the readings, set outside a test, is
// said wherever the readings are: doctor warns on both checks, status
// prints it on its host line and in JSON, and the daemon records it.
func TestFixedHostReadingsAreSaid(t *testing.T) {
	saved := hostFixedByEnv
	hostFixedByEnv = func() bool { return true }
	hostCache.Lock()
	hostCache.at = time.Time{}
	hostCache.Unlock()
	t.Cleanup(func() {
		hostFixedByEnv = saved
		hostCache.Lock()
		hostCache.at = time.Time{}
		hostCache.Unlock()
	})
	h := readHost(context.Background())
	if h.Fixed != hostcheck.FixedMessage {
		t.Fatalf("%+v", h)
	}
	var r doctorReport
	doctorHost(&r, h)
	if len(r.Checks) != 2 || r.Checks[0].ID != "host.session" || r.Checks[1].ID != "host.load" || r.Checks[0].Status != "warning" || r.Checks[1].Status != "warning" ||
		!strings.Contains(r.Checks[0].Message, "detection is off for this process") {
		t.Fatalf("%+v", r.Checks)
	}
	var out strings.Builder
	printHost(&out, &h)
	if out.String() != "host:      "+hostcheck.FixedMessage+"\n" {
		t.Fatalf("%q", out.String())
	}
	b, _ := json.Marshal(h)
	if !strings.Contains(string(b), `"fixed":"host readings are fixed by LOOPTENDER_TEST_FIXED_HOST`) {
		t.Fatalf("%s", b)
	}
}
