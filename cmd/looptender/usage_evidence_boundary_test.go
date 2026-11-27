package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageEvidenceRejectsAmbiguousFields(t *testing.T) {
	raw, _ := json.Marshal(usageFixture())
	for _, replacement := range []string{`"coverageComplete":false,"coverageComplete":true`, `"CoverageComplete":true`, `"coverageComplete":false,"CoverageComplete":true`, `"coverageComplete":true,"coverageComplete":true`, `"coverageComplete":false,"coverage\u0043omplete":true`} {
		t.Run(replacement, func(t *testing.T) {
			b := strings.Replace(string(raw), `"coverageComplete":true`, replacement, 1)
			g := parseUsageEvidence([]byte(b), "example", usageFixture().To)
			if g.Availability != "unavailable" {
				t.Fatalf("ambiguous declaration qualified: %+v", g.Estimate)
			}
		})
	}
}
func TestUsageEvidenceRejectsPathLabels(t *testing.T) {
	for _, label := range []string{"C:/fictional/private/account/usage.json", "https://example.com/private/usage", "relative/private/file", "C:\\fictional\\private", "../private/file"} {
		for _, field := range []string{"evidence", "stream", "provider", "model", "workload", "acceptance"} {
			t.Run(field+label, func(t *testing.T) {
				x := usageFixture()
				switch field {
				case "evidence":
					x.Evidence = label
				case "stream":
					x.Samples[0].Stream = label
				case "provider":
					x.Samples[0].Provider = label
				case "model":
					x.Samples[0].Model = label
				case "workload":
					x.Pairs[0].Workload = label
				case "acceptance":
					x.Pairs[0].Acceptance = label
				}
				g := parsedUsage(t, x)
				b, _ := json.Marshal(g)
				if strings.Contains(string(b), label) || strings.Contains(strings.Join(g.Estimate, " "), "estimated mean") {
					t.Fatalf("unsafe label disclosed or qualified: %s", b)
				}
			})
		}
	}
}
