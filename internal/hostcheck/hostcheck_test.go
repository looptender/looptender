package hostcheck

import (
	"context"
	"errors"
	"testing"
	"time"
)

func readers() Readers {
	return Readers{
		Keychain:          func(context.Context) error { return nil },
		Resolve:           func(context.Context) error { return nil },
		Console:           func() (bool, bool) { return true, true },
		LoadAverage:       func() (float64, error) { return 4, nil },
		Cores:             func() int { return 8 },
		MemoryFreePercent: func() int { return 50 },
	}
}

func TestSessionIsUnavailableOnlyWhenAReadingSaysSo(t *testing.T) {
	r := readers()
	if s := r.ReadSession(context.Background()); !s.Available || s.Keychain != OK || s.DNS != OK || s.Console != OK {
		t.Fatalf("%+v", s)
	}
	r.Keychain = func(context.Context) error { return errors.New("fictional: no security daemon") }
	if s := r.ReadSession(context.Background()); s.Available || s.Keychain != Unavailable {
		t.Fatalf("keychain: %+v", s)
	}
	r = readers()
	// A console that belongs to root is reported, not taken as an outage:
	// headless computers and the login window look like that all the time.
	r.Console = func() (bool, bool) { return false, true }
	if s := r.ReadSession(context.Background()); !s.Available || s.Console != Unavailable {
		t.Fatalf("console: %+v", s)
	}
	// Names that do not resolve are the network's: the session is there.
	r = readers()
	r.Resolve = func(context.Context) error { return errors.New("fictional: no such host") }
	if s := r.ReadSession(context.Background()); !s.Available || s.DNS != Unavailable {
		t.Fatalf("dns: %+v", s)
	}
	// A reading that runs out of time is unknown, not unavailable.
	r = readers()
	r.Keychain = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	start := time.Now()
	if s := r.ReadSession(context.Background()); !s.Available || s.Keychain != Unknown {
		t.Fatalf("timeout: %+v", s)
	}
	if time.Since(start) > Bound+time.Second {
		t.Fatal("a reading was not bounded")
	}
	// A system without the readings says so and is available.
	if s := (Readers{}).ReadSession(context.Background()); !s.Available || s.Keychain != NotHere || s.Console != NotHere {
		t.Fatalf("none: %+v", s)
	}
}

func TestLoadLevels(t *testing.T) {
	for _, c := range []struct {
		load  float64
		mem   int
		level string
	}{{4, 50, LoadOK}, {16, 50, LoadHigh}, {32, 50, LoadOverloaded}, {100, 50, LoadOverloaded}, {4, 5, LoadHigh}, {4, -1, LoadOK}} {
		r := readers()
		r.LoadAverage = func() (float64, error) { return c.load, nil }
		r.MemoryFreePercent = func() int { return c.mem }
		if l := r.ReadLoad(); l.Level != c.level || l.Cores != 8 {
			t.Errorf("load %v mem %d: %+v, want %s", c.load, c.mem, l, c.level)
		}
	}
	r := readers()
	r.LoadAverage = func() (float64, error) { return 0, errors.New("fictional") }
	if l := r.ReadLoad(); l.Level != LoadUnknown {
		t.Fatalf("%+v", l)
	}
}

// The system's own readers answer here, within the bound, and never fail
// the session on a computer someone is logged in to.
func TestSystemReadersAnswer(t *testing.T) {
	l := System.ReadLoad()
	if l.Cores <= 0 {
		t.Fatalf("%+v", l)
	}
	start := time.Now()
	_ = System.ReadSession(context.Background())
	if time.Since(start) > 3*Bound+time.Second {
		t.Fatal("the session reading was not bounded")
	}
}

// A program started by tests with FixedEnv=1 reads the fixed computer, and
// no other value or an unset variable changes what it reads.
func TestTheFixedEnvironmentPinsAProgramsReadings(t *testing.T) {
	sys := Readers{LoadAverage: func() (float64, error) { return 999, nil }, Cores: func() int { return 1 }}
	for v, overloaded := range map[string]bool{"1": false, "": true, "0": true, "yes": true} {
		r := fromEnv(func(string) string { return v }, sys)
		if got := r.ReadLoad().Level == LoadOverloaded; got != overloaded {
			t.Errorf("%s=%q: overloaded %v", FixedEnv, v, got)
		}
	}
	if s := Fixed().ReadSession(context.Background()); !s.Available || Fixed().ReadLoad().Level != LoadOK {
		t.Fatal("the fixed computer is not available and light")
	}
}
