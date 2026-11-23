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

// SizesApart formats a, which is below b, and b so that a reads below b:
// with Size when that tells them apart, else with more precision or a
// smaller unit. 18.50 against 18.53 GiB reads "18.50 GiB" and "18.53 GiB",
// not "18 GiB" twice.
func SizesApart(a, b uint64) (string, string) {
	if x, y := Size(a), Size(b); x != y || a >= b {
		return x, y
	}
	for _, f := range []struct {
		unit  float64
		name  string
		least uint64
	}{{1 << 30, "GiB", 1 << 30}, {1 << 20, "MiB", 1 << 20}, {1 << 10, "KiB", 0}} {
		if a < f.least {
			continue
		}
		for decimals := 1; decimals <= 3; decimals++ {
			x := fmt.Sprintf("%.*f %s", decimals, float64(a)/f.unit, f.name)
			y := fmt.Sprintf("%.*f %s", decimals, float64(b)/f.unit, f.name)
			if x != y {
				return x, y
			}
		}
	}
	return fmt.Sprintf("%d bytes", a), fmt.Sprintf("%d bytes", b)
}

// Sentence says what the reading means for the loop, or "" when it is ok.
func (r Reading) Sentence() string {
	of := " free of " + Size(r.Total)
	switch r.Level() {
	case Critical:
		free, floor := SizesApart(r.Available, FloorBytes)
		return "the volume that holds the state directory is nearly full (" + free + of + ", below " + floor + "): any record may fail to save, and a check that cannot save the record of a send sends nothing. Free space on it now"
	case Low:
		free, low := SizesApart(r.Available, r.LowAt())
		return "the volume that holds the state directory is filling (" + free + of + ", below " + low + "): free space on it before records fail to save"
	case Unknown:
		return "the free space on the volume that holds the state directory could not be read"
	}
	return ""
}
