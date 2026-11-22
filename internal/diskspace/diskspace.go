// Package diskspace says how much room is left on the volume that holds a
// loop's state directory. A check writes small records, but every one of
// them, and every send's record before the send, needs a write to succeed;
// a full volume stops a loop's records, and other programs filling it (an
// install, a log, a build) do so faster than looptender itself ever could.
package diskspace

import "fmt"

// Thresholds. Below Low the volume is said to be filling, below Floor to be
// nearly full. Low is the larger of LowBytes and LowFraction of the volume:
// on a small volume a gigabyte is already a large share, on a large one the
// other programs that fill it write in larger amounts. Floor is fixed: at
// about 100 MiB free, installs and builds already fail for lack of room,
// and an operating system starts failing writes of its own.
const (
	LowBytes    = 1 << 30
	LowFraction = 0.02
	FloorBytes  = 100 << 20
)

// Levels of a reading.
const (
	OK       = "ok"
	Low      = "low"
	Critical = "critical"
	Unknown  = "unknown"
)

// Reading is the room on a volume, in bytes. Available is what this user
// may still write; Known is false where it cannot be read.
type Reading struct {
	Available, Total uint64
	Known            bool
}

// Measure reads the volume that holds dir. Tests replace it.
var Measure = measure

// Of reads the volume that holds dir; an error is an unknown reading.
func Of(dir string) Reading {
	r, err := Measure(dir)
	if err != nil || r.Total == 0 {
		return Reading{}
	}
	r.Known = true
	return r
}

// LowAt is the free space below which this volume is said to be filling.
func (r Reading) LowAt() uint64 {
	if f := uint64(float64(r.Total) * LowFraction); f > LowBytes {
		return f
	}
	return LowBytes
}

// Level is ok, low, critical or unknown.
func (r Reading) Level() string {
	switch {
	case !r.Known:
		return Unknown
	case r.Available < FloorBytes:
		return Critical
	case r.Available < r.LowAt():
		return Low
	}
	return OK
}

// Size formats a byte count for people: 117 MiB, 2.4 GiB.
func Size(n uint64) string {
	switch {
	case n >= 10<<30:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d KiB", n>>10)
}

// Sentence says what the reading means for the loop, or "" when it is ok.
func (r Reading) Sentence() string {
	free := fmt.Sprintf("%s free of %s", Size(r.Available), Size(r.Total))
	switch r.Level() {
	case Critical:
		return "the volume that holds the state directory is nearly full (" + free + ", below " + Size(FloorBytes) + "): any record may fail to save, and a check that cannot save the record of a send sends nothing. Free space on it now"
	case Low:
		return "the volume that holds the state directory is filling (" + free + ", below " + Size(r.LowAt()) + "): free space on it before records fail to save"
	case Unknown:
		return "the free space on the volume that holds the state directory could not be read"
	}
	return ""
}
