package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func rankFixture(x usageExport) usageRankInput {
	b, _ := json.Marshal(x)
	return usageRankInput{raw: b, kind: "imported"}
}
func rankRow(t *testing.T, r usageRankReport, stream, metric string) usageRankRow {
	t.Helper()
	for _, row := range r.Rows {
		if row.Stream == stream && row.Metric == metric {
			return row
		}
	}
	t.Fatalf("missing %s/%s: %+v", stream, metric, r)
	return usageRankRow{}
}
func TestUsageRankComponentsDuplicatesAndTies(t *testing.T) {
	x := usageFixture()
	x.Samples = append(x.Samples, x.Samples[1])
	r := rankUsageEvidence([]usageRankInput{rankFixture(x)}, x.To)
	for metric, want := range map[string]int64{"input": 1000, "cached input": 600, "uncached input": 400, "output": 20, "reasoning output": 5, "total input plus output": 1020} {
		row := rankRow(t, r, "baseline", metric)
		if row.Tokens == nil || *row.Tokens != want || row.Rank == nil || *row.Rank != 1 || row.Peers != 2 || row.Intervals != 1 || row.Covered != 1 {
			t.Fatalf("%s: %+v", metric, row)
		}
	}
	if row := rankRow(t, r, "treatment", "input"); row.Rank == nil || *row.Rank != 2 {
		t.Fatal(row)
	}
	if row := rankRow(t, r, "treatment", "output"); row.Rank == nil || *row.Rank != 1 {
		t.Fatal(row)
	}
}
func TestUsageRankUnknownAndIncompatible(t *testing.T) {
	for _, mode := range []string{"missing", "reset", "observed-period", "provider", "unknown-provider", "baseline-only", "conflict", "path-loop", "path-provider", "future", "semantics"} {
		t.Run(mode, func(t *testing.T) {
			x := usageFixture()
			switch mode {
			case "missing":
				x.Samples[3].Counters.Input = nil
			case "reset":
				x.Samples[3].Counters.Input = usageNumber(1)
				x.Samples[3].Counters.Cached = usageNumber(0)
			case "observed-period":
				x.Samples[3].At = x.To
			case "provider":
				x.Samples[2].Provider = "other"
				x.Samples[3].Provider = "other"
			case "unknown-provider":
				x.Samples[2].Provider = "unavailable"
				x.Samples[3].Provider = "unavailable"
			case "baseline-only":
				x.Samples = x.Samples[:3]
			case "conflict":
				s := x.Samples[3]
				s.Counters.Input = usageNumber(900)
				x.Samples = append(x.Samples, s)
			case "path-loop":
				x.Loop = "/private/example"
			case "path-provider":
				x.Samples[0].Provider = "/private/example"
			case "future":
				x.To = x.To.Add(time.Hour)
			case "semantics":
				x.Semantics = "unknown"
			}
			r := rankUsageEvidence([]usageRankInput{rankFixture(x)}, usageFixture().To)
			for _, row := range r.Rows {
				if row.Metric == "input" && row.Rank != nil {
					t.Fatalf("unsupported rank: %+v", row)
				}
			}
			if mode == "missing" || mode == "reset" {
				row := rankRow(t, r, "treatment", "input")
				if row.Tokens != nil || row.Covered != 0 {
					t.Fatal(row)
				}
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "/private/example") {
				t.Fatal("private label leaked")
			}
		})
	}
}
func TestUsageRankPartialAndDeclaredPeriods(t *testing.T) {
	x := usageFixture()
	s := x.Samples[3]
	s.Seq = 3
	s.At = x.To
	s.Counters.Input = nil
	x.Samples = append(x.Samples, s)
	r := rankUsageEvidence([]usageRankInput{rankFixture(x)}, x.To)
	row := rankRow(t, r, "treatment", "input")
	if row.Tokens == nil || *row.Tokens != 800 || row.Covered != 1 || row.Intervals != 2 || row.Rank != nil {
		t.Fatal(row)
	}
	a, b := usageFixture(), usageFixture()
	a.Samples = a.Samples[:2]
	b.Samples = b.Samples[2:]
	b.Loop = "second"
	b.To = b.To.Add(time.Hour)
	r = rankUsageEvidence([]usageRankInput{rankFixture(a), rankFixture(b)}, b.To)
	for _, row := range r.Rows {
		if row.Rank != nil {
			t.Fatal("incompatible declared periods ranked")
		}
	}
	b.To = a.To
	r = rankUsageEvidence([]usageRankInput{rankFixture(a), rankFixture(b)}, a.To)
	if row := rankRow(t, r, "baseline", "input"); row.Rank == nil || row.Peers != 2 {
		t.Fatal(row)
	}
	r = rankUsageEvidence([]usageRankInput{rankFixture(a), rankFixture(a)}, a.To)
	if len(r.Rows) != 0 || len(r.Sources) != 2 || !strings.Contains(r.Sources[0].Status, "repeated") {
		t.Fatal(r)
	}
}
func TestUsageRankCLIReadOnlyAndPrivacy(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("safe reader unavailable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "private-example.json")
	b, _ := json.Marshal(usageFixture())
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	code := run(context.Background(), []string{"usage-rank", "--evidence", path}, &out, &errout)
	var report usageRankReport
	if code != 0 || json.Unmarshal(out.Bytes(), &report) != nil || len(report.Rows) != 12 || strings.Contains(out.String()+errout.String(), dir) {
		t.Fatalf("code %d %s %s", code, &out, &errout)
	}
	after, _ := os.ReadFile(path)
	files, _ := os.ReadDir(dir)
	if !bytes.Equal(b, after) || len(files) != 1 {
		t.Fatal("read mutated state")
	}
	for _, args := range [][]string{{"usage-rank", "--help"}, {"usage-rank", "--evidence", path + "missing"}, {"usage-rank", "--wrong", path}, {"usage-rank", "--evidence", path, "--evidence", path}} {
		out.Reset()
		errout.Reset()
		run(context.Background(), args, &out, &errout)
		if strings.Contains(out.String()+errout.String(), dir) {
			t.Fatal("path leaked")
		}
	}
	// Strict nested duplicate JSON keys cannot bypass the shared validator.
	raw := bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
	r := rankUsageEvidence([]usageRankInput{{raw: raw, kind: "imported"}}, usageFixture().To)
	if len(r.Rows) != 0 {
		t.Fatal("ambiguous evidence accepted")
	}
}
func TestUsageRankBounds(t *testing.T) {
	x := usageFixture()
	inputs := []usageRankInput{}
	for i := 0; i < 9; i++ {
		inputs = append(inputs, rankFixture(x))
	}
	if len(rankUsageEvidence(inputs, x.To).Rows) != 0 {
		t.Fatal("source bound exceeded")
	}
	x.Samples[3].Counters.Cached = usageNumber(900) // invalid snapshot subset
	if len(rankUsageEvidence([]usageRankInput{rankFixture(x)}, x.To).Rows) != 0 {
		t.Fatal("incompatible subset accepted")
	}
}

func TestUsageRankAutomaticAndUnavailableSources(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("safe reader unavailable")
	}
	x := usageFixture()
	x.Method = "observational"
	x.Evidence = "automatic-BB-token-metadata"
	x.Actions = nil
	x.Pairs = nil
	for i := range x.Samples {
		x.Samples[i].Provider = "unavailable"
		x.Samples[i].Model = "unavailable"
	}
	r := usageCollection{Version: 1, Cursor: 2, Success: x.To, Outcome: "recorded", Export: x}
	dir := t.TempDir()
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, usageCollectionFile), b, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if run(context.Background(), []string{"usage-rank", "--collection-dir", dir, "--evidence", filepath.Join(dir, "absent")}, &out, &errout) != 0 {
		t.Fatal(&errout)
	}
	var report usageRankReport
	if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Rows) != 12 || report.Sources[0].Kind != "automatic" || report.Sources[0].LastSuccess == nil || report.Sources[1].Status != "unavailable" {
		t.Fatal(&out)
	}
	for _, row := range report.Rows {
		if row.Rank != nil {
			t.Fatal("invented automatic attribution")
		}
	}
	if strings.Contains(out.String(), dir) {
		t.Fatal("private directory leaked")
	}
	// The safe reader refuses links rather than following another evidence source.
	path := filepath.Join(dir, "linked")
	if err := os.Symlink(filepath.Join(dir, usageCollectionFile), path); err != nil {
		t.Fatal(err)
	}
	if _, err := readUsageRankFile(path); err == nil {
		t.Fatal("followed symlink")
	}
}
func TestUsageRankZeroIsMeasuredAndBadDeltaSubsetUnavailable(t *testing.T) {
	x := usageFixture()
	x.Samples[1].Counters = x.Samples[0].Counters
	r := rankUsageEvidence([]usageRankInput{rankFixture(x)}, x.To)
	row := rankRow(t, r, "baseline", "input")
	if row.Tokens == nil || *row.Tokens != 0 || row.Rank == nil || *row.Rank != 2 {
		t.Fatal(row)
	}
	x = usageFixture()
	x.Samples[0].Counters.Input = usageNumber(1000)
	x.Samples[0].Counters.Cached = usageNumber(0)
	r = rankUsageEvidence([]usageRankInput{rankFixture(x)}, x.To)
	for _, metric := range []string{"cached input", "uncached input"} {
		row := rankRow(t, r, "baseline", metric)
		if row.Tokens != nil || row.Rank != nil {
			t.Fatal(row)
		}
	}
}
