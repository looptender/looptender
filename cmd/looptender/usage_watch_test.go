package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// usage-watch keeps its setting in the state directory, never in the
// configuration: the file is unchanged, so an older build's strict decoder
// still loads it. It is shown by the command, status and doctor, and
// turned off again.
func TestUsageWatchIsSetOutsideTheConfiguration(t *testing.T) {
	f := newDoctorFixture(t)
	before, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	cmd := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := usageWatchCommand(env{context.Background(), &out, &errs}, append([]string{"-c", f.config}, args...))
		return code, out.String(), errs.String()
	}
	if code, out, _ := cmd(); code != exitOK || out != "usage watch: off\n" {
		t.Fatalf("%d %q", code, out)
	}
	for _, bad := range [][]string{{"--account", "../x"}, {"--account", "a", "--every", "5m"}, {"--account", "a", "--warn-within", "200h"}, {"--account", "a", "--off"}} {
		if code, _, _ := cmd(bad...); code != exitUsage {
			t.Fatalf("accepted %v", bad)
		}
	}
	code, out, errs := cmd("--account", "example-claude-a", "--every", "20m")
	if code != exitOK || out != "usage watch: on for account example-claude-a, read every 20m0s; warns once a window when it would run out within 12h0m0s\n" || !strings.Contains(errs, "has no entry example-claude-a") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	after, err := os.ReadFile(f.config)
	if err != nil || !bytes.Equal(before, after) || strings.Contains(string(after), "usage") {
		t.Fatalf("the configuration changed: %s", after)
	}
	if _, err := config.Load(f.config); err != nil {
		t.Fatalf("strict load: %v", err)
	}
	s, err := state.LoadUsageWatchSetting(f.state)
	if err != nil || s == nil || s.Account != "example-claude-a" || s.Every != "20m0s" {
		t.Fatalf("%+v %v", s, err)
	}
	_, r, _ := f.run(t, successfulDoctorRun)
	if c := doctorCheckByID(t, r, "usage_watch"); c.Status != "warning" || !strings.Contains(c.Message, "no entry for its account") {
		t.Fatalf("doctor: %+v", c)
	}
	var b strings.Builder
	printUsageWatch(&b, describeUsageWatch(f.state))
	if !strings.HasPrefix(b.String(), "usage watch: on for account example-claude-a") {
		t.Fatal(b.String())
	}
	code, out, _ = cmd("--off", "--json")
	var v map[string]any
	if code != exitOK || json.Unmarshal([]byte(out), &v) != nil || v["usageWatch"] != nil {
		t.Fatalf("%d %s", code, out)
	}
	if s, err := state.LoadUsageWatchSetting(f.state); s != nil || err != nil {
		t.Fatalf("not off: %+v %v", s, err)
	}
}
