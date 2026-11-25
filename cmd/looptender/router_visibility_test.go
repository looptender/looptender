package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockReportOmitsRouterWhenUnconfigured(t *testing.T) {
	d, _ := dockFixtureReader(t)
	raw, err := d.json(false)
	if err != nil {
		t.Fatalf("dock json: %v", err)
	}
	if strings.Contains(string(raw), `"router"`) {
		t.Fatalf("expected unconfigured router field to be omitted, got %s", raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["router"]; ok {
		t.Fatalf("expected no router key in unconfigured dock JSON")
	}
}

func TestDoctorRouterAbsentLeavesReportClean(t *testing.T) {
	r := doctorReport{SchemaVersion: 1, Checks: []doctorCheck{}}
	doctorRouter(context.Background(), &r, filepath.Join(t.TempDir(), "missing-router"), true)
	if r.Router != nil {
		t.Fatalf("expected nil Router when dir is missing, got %+v", r.Router)
	}
	if len(r.Checks) != 0 {
		t.Fatalf("expected zero doctor checks for missing router, got %+v", r.Checks)
	}
}
