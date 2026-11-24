package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/looptender/looptender/internal/hostcheck"
)

// hostStatus is what status, doctor and the panel say about the computer
// the supervisor runs on.
type hostStatus struct {
	Session hostcheck.Session `json:"session"`
	Load    hostcheck.Load    `json:"load"`
	// Fixed: the readings are fixed by the test-only environment variable,
	// so they say nothing about this computer.
	Fixed string `json:"fixed,omitempty"`
}

// readHost reads the computer, at most once in hostCacheFor within one
// process: status and the panel read it for every loop.
func readHost(ctx context.Context) hostStatus {
	hostCache.Lock()
	defer hostCache.Unlock()
	if hostCache.at.IsZero() || time.Since(hostCache.at) > hostCacheFor {
		hostCache.h = hostStatus{Session: hostcheck.System.ReadSession(ctx), Load: hostcheck.System.ReadLoad()}
		if hostFixedByEnv() {
			hostCache.h.Fixed = hostcheck.FixedMessage
		}
		hostCache.at = time.Now()
	}
	return hostCache.h
}

const hostCacheFor = 15 * time.Second

var hostCache struct {
	sync.Mutex
	at time.Time
	h  hostStatus
}

// hostLoadSentence is the load in one sentence.
func hostLoadSentence(l hostcheck.Load) string {
	s := fmt.Sprintf("load %.1f on %d cores (%.1f per core)", l.Load1, l.Cores, l.PerCore)
	if l.MemoryFreePercent >= 0 {
		s += fmt.Sprintf(", memory pressure level %d of 100 (lower is more pressure)", l.MemoryFreePercent)
	}
	return s
}

// hostFixedByEnv: the test-only variable fixed this program's readings. A
// test binary pins them on purpose (PinForTests clears FixedByEnv) and is
// not told. No call into the testing package: the built binary must not
// link it.
var hostFixedByEnv = func() bool { return hostcheck.FixedByEnv }

// doctorHost adds host.session and host.load.
func doctorHost(r *doctorReport, h hostStatus) {
	if h.Fixed != "" {
		r.add("host.session", "warning", capitalize(h.Fixed)+".", "Unset "+hostcheck.FixedEnv+" in this program's environment.")
		r.add("host.load", "warning", capitalize(h.Fixed)+".", "Unset "+hostcheck.FixedEnv+" in this program's environment.")
		return
	}
	s := h.Session
	parts := fmt.Sprintf("keychain %s, name service %s, console user %s", s.Keychain, s.DNS, s.Console)
	switch {
	case !s.Available:
		r.add("host.session", "error", "This computer's login session or network is unavailable to background processes ("+parts+"). looptender signed nothing out and read no credentials; while a provider's sign-in fails in this state, checks send and hand off nothing, for up to 45 minutes.",
			"Log in at the computer's screen again if no one is; signing in to a provider again does not help while this lasts.")
	case s.DNS == hostcheck.Unavailable:
		r.add("host.session", "warning", "Names do not resolve ("+parts+"). The connectivity check decides whether the network is down.", "")
	default:
		r.add("host.session", "ok", "The login session is available ("+parts+").", "")
	}
	l := h.Load
	switch l.Level {
	case hostcheck.LoadOverloaded:
		r.add("host.load", "warning", "This computer is overloaded: "+hostLoadSentence(l)+". Turns and checks slow down, and tools time out.", "Pause new heavy work, such as test suites and browsers, until the load falls.")
	case hostcheck.LoadHigh:
		r.add("host.load", "warning", "This computer is busy: "+hostLoadSentence(l)+".", "Avoid starting more heavy work until the load falls.")
	case hostcheck.LoadOK:
		r.add("host.load", "ok", "This computer's "+hostLoadSentence(l)+".", "")
	default:
		r.add("host.load", "unavailable", "The load cannot be read here.", "")
	}
}

// printHost writes status's host lines, only when there is something to say.
func printHost(w interface{ Write([]byte) (int, error) }, h *hostStatus) {
	if h == nil {
		return
	}
	if h.Fixed != "" {
		fmt.Fprintf(w, "host:      %s\n", h.Fixed)
		return
	}
	if !h.Session.Available {
		fmt.Fprintf(w, "host:      %s; wakes and handoffs are held until it is back\n", hostcheck.Message)
	}
	if h.Load.Level == hostcheck.LoadHigh || h.Load.Level == hostcheck.LoadOverloaded {
		fmt.Fprintf(w, "load:      %s: %s\n", h.Load.Level, hostLoadSentence(h.Load))
	}
}

// Notice kind for the computer itself.
const noticeHost = "host"

// hostNotices are the computer's notices: one for a session unavailable to
// background processes, one for an overload. They belong to no loop.
func hostNotices(h *hostStatus, now time.Time) []panelNotice {
	if h == nil {
		return nil
	}
	var out []panelNotice
	if !h.Session.Available {
		out = append(out, panelNotice{ID: noticeID(noticeHost, "computer", time.Time{}, "session"), Kind: noticeHost, Severity: sevHigh, Since: now,
			Text: "This computer's login session or network is unavailable to background processes; looptender signed nothing out, and checks wait for it.", Group: noticeHost + ".session",
			GroupText: "This computer's login session or network is unavailable to background processes."})
	}
	if h.Load.Level == hostcheck.LoadOverloaded {
		out = append(out, panelNotice{ID: noticeID(noticeHost, "computer", time.Time{}, "load"), Kind: noticeHost, Severity: sevNormal, Since: now,
			Text: "This computer is overloaded; turns and checks slow down.", Group: noticeHost + ".load", GroupText: "This computer is overloaded."})
	}
	return out
}
