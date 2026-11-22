package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/diskspace"
	"github.com/looptender/looptender/internal/snapshot"
)

// volume makes every reading of the state directory's volume say avail
// bytes free of total.
func volume(t *testing.T, avail, total uint64) {
	t.Helper()
	saved := diskspace.Measure
	diskspace.Measure = func(string) (diskspace.Reading, error) {
		return diskspace.Reading{Available: avail, Total: total}, nil
	}
	t.Cleanup(func() { diskspace.Measure = saved })
}

func TestDoctorAndStatusWarnOfAFillingVolume(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct {
		avail, total uint64
		doctor, line string
		exit         int
	}{
		{50 * gib, 500 * gib, "ok", "", exitOK},
		{5 * gib, 500 * gib, "warning", "disk:      the volume that holds the state directory is filling (5.0 GiB free of 500 GiB, below 10 GiB)", exitOK},
		{117 << 20, 500 * gib, "warning", "is filling (117 MiB free", exitOK},
		{60 << 20, 500 * gib, "error", "disk:      the volume that holds the state directory is nearly full (60 MiB free of 500 GiB, below 100 MiB)", exitFailed},
	} {
		volume(t, c.avail, c.total)
		f := newDoctorFixture(t)
		var out bytes.Buffer
		code := run(context.Background(), []string{"doctor", "-c", f.config, "--json", "--offline"}, &out, &out)
		var r doctorReport
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(out.String())
		}
		got := doctorFinding(r, "state.disk_space")
		if got == nil || got.Status != c.doctor || (c.exit == exitFailed && code != exitFailed) {
			t.Fatalf("%s: doctor %d %+v", diskspace.Size(c.avail), code, got)
		}
		st, text := statusOf(t, f, time.Now())
		if st.DiskSpace.AvailableBytes != c.avail || (c.line == "") != !strings.Contains(text, "disk:") || (c.line != "" && !strings.Contains(text, c.line)) {
			t.Fatalf("%s: %+v\n%s", diskspace.Size(c.avail), st.DiskSpace, text)
		}
	}
	// Unknown where it cannot be read: said, not judged.
	saved := diskspace.Measure
	diskspace.Measure = func(string) (diskspace.Reading, error) { return diskspace.Reading{}, os.ErrPermission }
	defer func() { diskspace.Measure = saved }()
	f := newDoctorFixture(t)
	var out bytes.Buffer
	run(context.Background(), []string{"doctor", "-c", f.config, "--json", "--offline"}, &out, &out)
	var r doctorReport
	json.Unmarshal(out.Bytes(), &r)
	if got := doctorFinding(r, "state.disk_space"); got == nil || got.Status != "unavailable" {
		t.Fatalf("%+v", got)
	}
}

// A hold a person saves on a full disk fails, says so, and leaves the old
// control record: nothing looks held that is not.
func TestPauseOnAFullDiskFailsAndSaysSo(t *testing.T) {
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := control.Save(f.state, control.File{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(f.state, control.FileName))
	snapshot.InjectWriteFaults(func(path string) (int, error) {
		if strings.HasPrefix(filepath.Base(path), "."+control.FileName+"-") {
			return 10, snapshot.ENOSPC
		}
		return 0, nil
	})
	t.Cleanup(func() { snapshot.InjectWriteFaults(nil) })
	for _, cmd := range []string{"pause", "stop", "await-user"} {
		var out bytes.Buffer
		if code := run(context.Background(), []string{cmd, "-c", f.config}, &out, &out); code == exitOK || !strings.Contains(out.String(), "no space") {
			t.Fatalf("%s on a full disk: %d %s", cmd, code, out.String())
		}
		if after, _ := os.ReadFile(filepath.Join(f.state, control.FileName)); !bytes.Equal(before, after) {
			t.Fatalf("%s changed control.json", cmd)
		}
	}
}
