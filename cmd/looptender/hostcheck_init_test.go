package main

import (
	"os"

	"github.com/looptender/looptender/internal/hostcheck"
)

// Every test in this package reads a fixed computer: its session available
// and a light load, so a loaded test machine changes no decision and no
// record. Programs the tests start inherit the same through FixedEnv. Tests
// about the host pin their own readings.
func init() {
	hostcheck.PinForTests(hostcheck.Fixed())
	os.Setenv(hostcheck.FixedEnv, "1")
}
