package main

import "github.com/looptender/looptender/internal/diskspace"

// The tests read a volume with room, whatever the disk of the computer that
// runs them holds: a low disk there must not add disk space reports to
// tests that count reports. Tests about a low volume set their own reading.
func init() {
	diskspace.Measure = func(string) (diskspace.Reading, error) {
		return diskspace.Reading{Available: 500 << 30, Total: 1000 << 30}, nil
	}
}
