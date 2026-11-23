package main

import (
	"context"

	"github.com/looptender/looptender/internal/hostcheck"
)

// Tests read a fixed computer: a session that is there and a light load, so
// a loaded test machine changes no decision. Tests about the host set their
// own readers.
func init() {
	hostReaders = hostcheck.Readers{
		Keychain:          func(context.Context) error { return nil },
		Resolve:           func(context.Context) error { return nil },
		Console:           func() (bool, bool) { return true, true },
		LoadAverage:       func() (float64, error) { return 1, nil },
		Cores:             func() int { return 8 },
		MemoryFreePercent: func() int { return 50 },
	}
}
