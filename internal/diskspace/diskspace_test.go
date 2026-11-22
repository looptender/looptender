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
