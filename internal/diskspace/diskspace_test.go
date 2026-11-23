package diskspace

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestLevels(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct {
		avail, total uint64
		want         string
	}{
		{50 * gib, 100 * gib, OK},
		{1500 << 20, 20 * gib, OK},      // above 1 GiB on a small volume
		{900 << 20, 20 * gib, Low},      // below 1 GiB
		{15 * gib, 1000 * gib, Low},     // above 1 GiB, below 2% of a large volume
		{25 * gib, 1000 * gib, OK},      // above 2%
		{117 << 20, 500 * gib, Low},     // the incident: above the floor
		{99 << 20, 500 * gib, Critical}, // below the floor
		{0, 500 * gib, Critical},
	} {
		if got := (Reading{Available: c.avail, Total: c.total, Known: true}).Level(); got != c.want {
			t.Errorf("%s of %s: %s, want %s", Size(c.avail), Size(c.total), got, c.want)
		}
	}
	if (Reading{}).Level() != Unknown || !strings.Contains((Reading{}).Sentence(), "could not be read") {
		t.Fatal("an unread volume is not unknown")
	}
	if s := (Reading{Available: 50 << 20, Total: 100 << 30, Known: true}).Sentence(); !strings.Contains(s, "nearly full (50 MiB free of 100 GiB, below 100 MiB)") {
		t.Fatal(s)
	}
}

func TestOfReadsThisVolumeOrSaysUnknown(t *testing.T) {
	r := Of(t.TempDir())
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		if !r.Known || r.Total == 0 || r.Available > r.Total {
			t.Fatalf("%+v", r)
		}
	}
	saved := Measure
	defer func() { Measure = saved }()
	Measure = func(string) (Reading, error) { return Reading{}, errors.New("fictional") }
	if r := Of(t.TempDir()); r.Known || r.Level() != Unknown {
		t.Fatalf("%+v", r)
	}
}

// Whenever the volume is low, the free figure reads below the threshold.
func TestFreeReadsBelowTheThreshold(t *testing.T) {
	const gib = uint64(1) << 30
	// The live case: 18.50 GiB free against a threshold of 18.53 GiB.
	r := Reading{Available: gib * 185 / 10, Total: 926 * gib, Known: true}
	if r.Level() != Low || !strings.Contains(r.Sentence(), "(18.50 GiB free of 926 GiB, below 18.52 GiB)") {
		t.Fatalf("%s: %s", Size(r.LowAt()), r.Sentence())
	}
	for _, c := range [][2]uint64{
		{gib * 185 / 10, gib * 1853 / 100},
		{1000 << 20, 1001 << 20},
		{99<<20 + 900<<10, 100 << 20},
		{5 << 30, 6 << 30},
		{100<<20 - 1, 100 << 20},
	} {
		a, b := SizesApart(c[0], c[1])
		if a == b {
			t.Errorf("%d and %d both read %q", c[0], c[1], a)
		}
	}
	if a, b := SizesApart(5<<30, 6<<30); a != "5.0 GiB" || b != "6.0 GiB" {
		t.Errorf("plain sizes changed: %s %s", a, b)
	}
}
