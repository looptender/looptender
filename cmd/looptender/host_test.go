package main

import (
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
