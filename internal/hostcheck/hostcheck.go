// Package hostcheck reads two facts about the computer a supervisor runs
// on, each cheaply and within a short bound: whether its login session is
// available to background processes (Session), and how loaded it is (Load).
//
// A supervisor started from a login session keeps running after that
// session ends (a logout, or the window server restarting). It then cannot
// reach the login keychain, and on some systems not the name service
// either, so a provider CLI it runs says "not logged in" and a token reads
// as invalid although nothing was signed out. Session tells that state
// apart, so it is reported as what it is and nothing advises signing in.
package hostcheck

import (
	"context"
	"runtime"
	"time"
)

// Bound is the longest any one reading may take.
const Bound = 2 * time.Second

// Reading values of one part of Session.
const (
	OK          = "ok"
	Unavailable = "unavailable"
	Unknown     = "unknown"        // could not be read
	NotHere     = "not_applicable" // this system has no such thing
)

// Session is whether this computer's login session is available to a
// background process.
type Session struct {
	// Available is false only when a reading says it is not: the keychain
	// cannot be reached, or no one is logged in at the console.
	Available bool `json:"available"`
	// Keychain: the user's keychain can be reached.
	Keychain string `json:"keychain"`
	// DNS: a name resolves. On its own a failure is the network's, which
	// the connectivity check reports; it is here to say which part failed.
	DNS string `json:"dns"`
	// Console: someone is logged in at the console.
	Console string `json:"console"`
}

// Message is the one sentence for an unavailable session.
const Message = "this computer's login session or network is unavailable to background processes (credentials were not read; nothing was signed out)"

// Readers are the system calls Session and Load make; tests replace them.
type Readers struct {
	Keychain func(ctx context.Context) error
	Resolve  func(ctx context.Context) error
	// Console returns whether someone is logged in at the console, and
	// whether that could be told.
	Console func() (bool, bool)
	// LoadAverage is the one-minute load average.
	LoadAverage func() (float64, error)
	Cores       func() int
	// MemoryFreePercent is the share of memory free, or -1 when unknown.
	MemoryFreePercent func() int
}

// System is the readers of this computer.
var System = systemReaders()

// ReadSession reads the session, each part within Bound.
func (r Readers) ReadSession(ctx context.Context) Session {
	s := Session{Keychain: NotHere, DNS: Unknown, Console: NotHere}
	if r.Keychain != nil {
		s.Keychain = bounded(ctx, r.Keychain)
	}
	if r.Resolve != nil {
		s.DNS = bounded(ctx, r.Resolve)
	}
	if r.Console != nil {
		if present, known := r.Console(); !known {
			s.Console = Unknown
		} else if present {
			s.Console = OK
		} else {
			s.Console = Unavailable
		}
	}
	s.Available = s.Keychain != Unavailable && s.Console != Unavailable
	return s
}

func bounded(ctx context.Context, f func(context.Context) error) string {
	c, cancel := context.WithTimeout(ctx, Bound)
	defer cancel()
	if err := f(c); err != nil {
		if c.Err() != nil {
			return Unknown // timed out: not known to be unavailable
		}
		return Unavailable
	}
	return OK
}

// Load levels.
const (
	LoadOK         = "ok"
	LoadHigh       = "high"       // more work waiting than the cores can run
	LoadOverloaded = "overloaded" // far more: everything on it slows down
	LoadUnknown    = "unknown"
)

// Thresholds of the one-minute load average per core.
const (
	HighPerCore       = 2.0
	OverloadedPerCore = 4.0
	// LowMemoryPercent of free memory raises the level to high.
	LowMemoryPercent = 10
)

// Load is how loaded this computer is.
type Load struct {
	Level   string  `json:"level"`
	Load1   float64 `json:"load1"`
	Cores   int     `json:"cores"`
	PerCore float64 `json:"perCore"`
	// MemoryFreePercent is -1 when it cannot be read cheaply here.
	MemoryFreePercent int `json:"memoryFreePercent"`
}

// ReadLoad reads the load.
func (r Readers) ReadLoad() Load {
	l := Load{Level: LoadUnknown, MemoryFreePercent: -1}
	if r.Cores != nil {
		l.Cores = r.Cores()
	}
	if l.Cores <= 0 {
		l.Cores = runtime.NumCPU()
	}
	if r.MemoryFreePercent != nil {
		l.MemoryFreePercent = r.MemoryFreePercent()
	}
	if r.LoadAverage == nil {
		return l
	}
	avg, err := r.LoadAverage()
	if err != nil || avg < 0 {
		return l
	}
	l.Load1 = avg
	l.PerCore = avg / float64(l.Cores)
	switch {
	case l.PerCore >= OverloadedPerCore:
		l.Level = LoadOverloaded
	case l.PerCore >= HighPerCore || (l.MemoryFreePercent >= 0 && l.MemoryFreePercent < LowMemoryPercent):
		l.Level = LoadHigh
	default:
		l.Level = LoadOK
	}
	return l
}

// ResolveName is the name Session resolves: one whose lookup needs the name
// service but no particular site to be up.
const ResolveName = "github.com"
