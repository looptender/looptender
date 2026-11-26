package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func usageNumber(n int64) *int64 { return &n }
func usageFixture() usageExport {
	from := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	x := usageExport{Version: 1, Loop: "example", From: from, To: from.Add(2 * time.Hour), Method: "controlled-replay", Evidence: "fixture-run-1", Semantics: "cumulative-inclusive-v1"}
	for _, stream := range []string{"baseline", "treatment"} {
		input, cached := int64(1000), int64(600)
		if stream == "treatment" {
			input, cached = 800, 500
		}
		x.Samples = append(x.Samples, usageSample{Stream: stream, Provider: "example-provider", Model: "example-model", Seq: 1, At: from, Counters: usageCounters{Input: usageNumber(50), Cached: usageNumber(10), Output: usageNumber(5), Reasoning: usageNumber(1)}}, usageSample{Stream: stream, Provider: "example-provider", Model: "example-model", Seq: 2, At: from.Add(time.Hour), Counters: usageCounters{Input: usageNumber(input + 50), Cached: usageNumber(cached + 10), Output: usageNumber(25), Reasoning: usageNumber(6)}})
	}
	x.Pairs = []usagePair{{Baseline: "baseline", Treatment: "treatment", Workload: "same-workload-v1", Acceptance: "same-checks-v1", BaselineWork: 2, TreatmentWork: 2, QualityAccepted: true, ContinuityAccepted: true, CoverageComplete: true, CacheComparable: true, OverheadIncluded: true}}
	x.Actions = []usageAvoided{{ID: "original", Kind: "wake", Disposition: "accepted", At: from}, {ID: "duplicate", Original: "original", Kind: "wake", Disposition: "suppressed_duplicate", At: from.Add(time.Hour)}}
	return x
}
func parsedUsage(t *testing.T, x usageExport) usageMeasurement {
	t.Helper()
	b, e := json.Marshal(x)
	if e != nil {
		t.Fatal(e)
	}
	return parseUsageEvidence(b, "example", x.To.Add(time.Hour))
}
func TestUsageEvidenceCounterDeltasAndComparableSavings(t *testing.T) {
	x := usageFixture()
	x.Samples = append(x.Samples, x.Samples[1])
	x.Actions = append(x.Actions, x.Actions[1])
	got := parsedUsage(t, x)
	text := strings.Join(got.Trend, " ")
	est := strings.Join(got.Estimate, " ")
	if got.Availability != "recorded" || !strings.Contains(text, "input 1,000") || !strings.Contains(text, "uncached input 400") || !strings.Contains(text, "reasoning output 5") || strings.Contains(text, "1,050") || !strings.Contains(est, "input: estimated mean 100.00") || !strings.Contains(est, "uncached input: estimated mean 50.00") || !strings.Contains(got.Avoided, "actions: 1 ") {
		t.Fatalf("%+v", got)
	}
}
func TestUsageEvidenceMissingResetAndQualityNeverBecomeSavings(t *testing.T) {
	for _, mode := range []string{"missing", "reset", "quality", "continuity", "coverage", "cache", "overhead", "model", "no-pairs", "observational"} {
		t.Run(mode, func(t *testing.T) {
			x := usageFixture()
			switch mode {
			case "missing":
				x.Samples[3].Counters.Input = nil
			case "reset":
				x.Samples[3].Counters.Input = usageNumber(1)
				x.Samples[3].Counters.Cached = usageNumber(0)
			case "quality":
				x.Pairs[0].QualityAccepted = false
			case "continuity":
				x.Pairs[0].ContinuityAccepted = false
			case "coverage":
				x.Pairs[0].CoverageComplete = false
			case "cache":
				x.Pairs[0].CacheComparable = false
			case "overhead":
				x.Pairs[0].OverheadIncluded = false
			case "model":
				x.Samples[2].Model = "other"
				x.Samples[3].Model = "other"
			case "no-pairs":
				x.Pairs = nil
			case "observational":
				x.Method = "observational"
			}
			g := parsedUsage(t, x)
			if strings.Contains("\n"+strings.Join(g.Estimate, "\n"), "\ninput: estimated mean") {
				t.Fatalf("unsupported estimate: %+v", g)
			}
		})
	}
}
func TestUsageEvidenceRejectsConflictsAndUnboundedOrWrongScope(t *testing.T) {
	for _, mode := range []string{"counter", "identity", "future", "scope", "semantics", "action", "time", "bound"} {
		t.Run(mode, func(t *testing.T) {
			x := usageFixture()
			switch mode {
			case "counter":
				x.Samples[0].Counters.Input = usageNumber(-1)
			case "identity":
				v := x.Samples[1]
				v.Counters.Input = usageNumber(9999)
				x.Samples = append(x.Samples, v)
			case "future":
				x.From = x.To
			case "scope":
				x.Loop = "other"
			case "semantics":
				x.Semantics = "unknown"
			case "action":
				x.Actions[1].Original = "missing"
			case "time":
				x.Actions[1].At = x.To.Add(time.Hour)
			case "bound":
				for len(x.Samples) < 513 {
					x.Samples = append(x.Samples, x.Samples[0])
				}
			}
			if g := parsedUsage(t, x); g.Availability != "unavailable" {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}
func TestUsageEvidenceNegativeSavingsAndAbsentActions(t *testing.T) {
	x := usageFixture()
	x.Pairs[0].Baseline, x.Pairs[0].Treatment = "treatment", "baseline"
	x.Actions = nil
	g := parsedUsage(t, x)
	if !strings.Contains(strings.Join(g.Estimate, " "), "mean -100.00") || !strings.Contains(g.Avoided, "unavailable") {
		t.Fatal(g)
	}
}
func TestUsageEvidenceReadIsBoundedAndPanelUsesIt(t *testing.T) {
	dir := t.TempDir()
	x := usageFixture()
	b, _ := json.Marshal(x)
	if g := loadUsageEvidence(dir, "example", x.To); g.Availability != "unavailable" {
		t.Fatal(g)
	}
	if e := os.WriteFile(filepath.Join(dir, "usage-evidence.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	g := loadUsageEvidence(dir, "example", x.To)
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if g.Availability != "unavailable" {
			t.Fatal(g)
		}
		return
	}
	p := dockUsageLoopOf(dockUsageInput{Key: "example", Status: &statusOut{UsageMeasurement: g}})
	if p.Measurement.Availability != "recorded" || len(p.Measurement.Trend) != 2 {
		t.Fatal(p.Measurement)
	}
	os.WriteFile(filepath.Join(dir, "usage-evidence.json"), make([]byte, usageEvidenceLimit+1), 0600)
	if g := loadUsageEvidence(dir, "example", x.To); g.Availability != "unavailable" {
		t.Fatal(g)
	}
}

func TestUsageEvidenceDocumentedExample(t *testing.T) {
	b, err := os.ReadFile("../../docs/examples/usage-evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	g := parseUsageEvidence(b, "example", time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC))
	if g.Availability != "recorded" || !strings.Contains(strings.Join(g.Estimate, " "), "mean 100.00") {
		t.Fatal(g)
	}
}
