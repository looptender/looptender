package main

// This diagnostic reads an explicit local export. It never queries a provider,
// reads transcripts, writes runtime state, or participates in supervision.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"time"
)

const usageEvidenceLimit = 128 << 10

var usageEvidenceLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,79}$`)

type usageCounters struct {
	Input     *int64 `json:"input"`
	Cached    *int64 `json:"cached"`
	Output    *int64 `json:"output"`
	Reasoning *int64 `json:"reasoning"`
}
type usageSample struct {
	Stream   string        `json:"stream"` // opaque lane/session key; not a transcript or path
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Seq      int64         `json:"seq"`
	At       time.Time     `json:"at"`
	Counters usageCounters `json:"counters"` // cumulative; input includes cached, output includes reasoning
}
type usageAvoided struct {
	At          time.Time `json:"at"`
	ID          string    `json:"id"`
	Original    string    `json:"original"`
	Kind        string    `json:"kind"`
	Disposition string    `json:"disposition"` // accepted original or suppressed duplicate
}
type usagePair struct {
	CoverageComplete   bool   `json:"coverageComplete"`
	CacheComparable    bool   `json:"cacheComparable"`
	OverheadIncluded   bool   `json:"overheadIncluded"`
	Baseline           string `json:"baseline"`
	Treatment          string `json:"treatment"`
	Workload           string `json:"workload"`
	Acceptance         string `json:"acceptance"`
	BaselineWork       int    `json:"baselineWork"`
	TreatmentWork      int    `json:"treatmentWork"`
	QualityAccepted    bool   `json:"qualityAccepted"`
	ContinuityAccepted bool   `json:"continuityAccepted"`
}
type usageExport struct {
	Version   int            `json:"version"`
	Loop      string         `json:"loop"`
	From      time.Time      `json:"from"`
	To        time.Time      `json:"to"`
	Method    string         `json:"method"`
	Evidence  string         `json:"evidence"` // local evidence label, never a URL/path
	Semantics string         `json:"semantics"`
	Samples   []usageSample  `json:"samples"`
	Actions   []usageAvoided `json:"actions"`
	Pairs     []usagePair    `json:"pairs"`
}
type usageMeasurement struct {
	Availability string   `json:"availability"`
	Details      []string `json:"details"`
	Trend        []string `json:"trend"`
	Avoided      string   `json:"avoided"`
	Estimate     []string `json:"estimate"`
}

func unavailableUsage(reason string) usageMeasurement {
	return usageMeasurement{Availability: "unavailable", Details: []string{reason}, Trend: []string{}, Avoided: "Observed avoided redundant actions: unavailable; no identity-based evidence.", Estimate: []string{"Estimated token savings: unavailable; no accepted comparable baseline. Subscription quota savings: unknown."}}
}
func loadUsageEvidence(dir, loop string, now time.Time) usageMeasurement {
	return loadUsageEvidenceWithOpen(dir, loop, now, openUsageEvidence)
}

// The opener seam permits deterministic replacement-race fixtures.
func loadUsageEvidenceWithOpen(dir, loop string, now time.Time, open func(string) (*os.File, error)) usageMeasurement {
	p := filepath.Join(dir, "usage-evidence.json")
	info, err := os.Lstat(p)
	if err != nil {
		return unavailableUsage("Usage trend unavailable; no local usage-evidence.json export was read.")
	}
	if !info.Mode().IsRegular() || info.Size() > usageEvidenceLimit {
		return unavailableUsage("Usage evidence unavailable; export exceeds bounds or is not a regular file.")
	}
	f, err := open(p)
	if err != nil {
		return unavailableUsage("Usage evidence unavailable; export could not be read.")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > usageEvidenceLimit || !os.SameFile(info, opened) {
		return unavailableUsage("Usage evidence unavailable; export changed or opened file is unsafe.")
	}
	b, err := io.ReadAll(io.LimitReader(f, usageEvidenceLimit+1))
	if err != nil || len(b) > usageEvidenceLimit {
		return unavailableUsage("Usage evidence unavailable; export exceeds bounds.")
	}
	return parseUsageEvidence(b, loop, now)
}
func counterValues(c usageCounters) []*int64 {
	return []*int64{c.Input, c.Cached, c.Output, c.Reasoning}
}
func validCounters(c usageCounters) bool {
	for _, n := range counterValues(c) {
		if n != nil && (*n < 0 || *n > 1e12) {
			return false
		}
	}
	return !(c.Input != nil && c.Cached != nil && *c.Cached > *c.Input || c.Output != nil && c.Reasoning != nil && *c.Reasoning > *c.Output)
}

type usageAccum struct {
	totals          [5]int64
	covered         [5]int
	intervals       int
	provider, model string
}

var usageMetricNames = []string{"input", "cached input", "output", "reasoning output", "uncached input"}

func parseUsageEvidence(raw []byte, loop string, now time.Time) usageMeasurement {
	bad := unavailableUsage("Usage evidence unavailable; invalid, conflicting or unsupported export.")
	if len(raw) > usageEvidenceLimit || !usageEvidenceFields(raw) {
		return bad
	}
	var x usageExport
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&x) != nil || d.Decode(new(any)) != io.EOF {
		return bad
	}
	if x.Version != 1 || x.Loop != loop || x.Semantics != "cumulative-inclusive-v1" || x.From.IsZero() || !x.To.After(x.From) || x.To.After(now) || x.To.Sub(x.From) > 31*24*time.Hour || !usageEvidenceLabel.MatchString(x.Evidence) || len(x.Samples) > 512 || len(x.Actions) > 128 || len(x.Pairs) > 8 {
		return bad
	}
	if x.Method != "observational" && x.Method != "controlled-replay" {
		return bad
	}
	streams := map[string][]usageSample{}
	identities := map[string]usageSample{}
	for _, s := range x.Samples {
		if !usageEvidenceLabel.MatchString(s.Stream) || !usageEvidenceLabel.MatchString(s.Provider) || !usageEvidenceLabel.MatchString(s.Model) || s.Seq < 0 || s.At.Before(x.From) || s.At.After(x.To) || !validCounters(s.Counters) {
			return bad
		}
		id := fmt.Sprintf("%s/%d", s.Stream, s.Seq)
		if old, ok := identities[id]; ok {
			if !reflect.DeepEqual(old, s) {
				return bad
			}
			continue
		}
		identities[id] = s
		streams[s.Stream] = append(streams[s.Stream], s)
	}
	if len(streams) > 16 {
		return bad
	}
	out := unavailableUsage("")
	out.Availability = "recorded"
	out.Details = []string{fmt.Sprintf("Measured provider counters from local export %s: %s to %s (UTC). Method: %s; %d unique snapshots, %d streams.", x.Evidence, x.From.UTC().Format(time.RFC3339), x.To.UTC().Format(time.RFC3339), x.Method, len(identities), len(streams)), "Export coverage and quality are author-attested, not independently verified by the Panel. Only consecutive cumulative deltas are counted. First snapshots, missing fields and counter resets do not become zero usage. Cached input and reasoning are components, not extra tokens. This export does not measure subscription charge."}
	totals := map[string]*usageAccum{}
	days := map[string]*usageAccum{}
	excluded := 0
	keys := make([]string, 0, len(streams))
	for k := range streams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rows := streams[key]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
		a := &usageAccum{provider: rows[0].Provider, model: rows[0].Model}
		totals[key] = a
		for i, s := range rows {
			if s.Provider != a.provider || s.Model != a.model {
				return bad
			}
			if i == 0 {
				continue
			}
			prev := rows[i-1]
			if s.At.Before(prev.At) {
				return bad
			}
			values, prior := counterValues(s.Counters), counterValues(prev.Counters)
			reset := false
			for j, n := range values {
				if n != nil && prior[j] != nil && *n < *prior[j] {
					reset = true
				}
			}
			if reset {
				excluded++
				a.intervals++ // withheld from complete comparison coverage
				continue
			}
			day := s.At.UTC().Format("2006-01-02") + " / " + key + " / " + s.Provider + " / " + s.Model
			b := days[day]
			if b == nil {
				b = &usageAccum{}
				days[day] = b
			}
			if len(days) > 32 {
				return bad
			}
			delta := [5]*int64{}
			for j, n := range values {
				if n != nil && prior[j] != nil {
					v := *n - *prior[j]
					delta[j] = &v
				}
			}
			if delta[0] != nil && delta[1] != nil && *delta[1] > *delta[0] {
				delta[1] = nil
			}
			if delta[2] != nil && delta[3] != nil && *delta[3] > *delta[2] {
				delta[3] = nil
			}
			if delta[0] != nil && delta[1] != nil && *delta[0] >= *delta[1] {
				v := *delta[0] - *delta[1]
				delta[4] = &v
			}
			for _, sum := range []*usageAccum{a, b} {
				sum.intervals++
				for j, n := range delta {
					if n != nil {
						sum.totals[j] += *n
						sum.covered[j]++
					}
				}
			}
		}
	}
	out.Details = append(out.Details, fmt.Sprintf("%d counter-reset intervals excluded. Daily trend assigns each observed interval to its ending day; it is not exact midnight attribution. Collection outside this export and unobserved gaps remain unknown.", excluded))
	dayKeys := make([]string, 0, len(days))
	for k := range days {
		dayKeys = append(dayKeys, k)
	}
	sort.Strings(dayKeys)
	for _, k := range dayKeys {
		a := days[k]
		line := k + fmt.Sprintf(" — %d observed intervals", a.intervals)
		for j, name := range usageMetricNames {
			if a.covered[j] > 0 {
				line += fmt.Sprintf("; %s %s (%d/%d intervals)", name, groupDigits(a.totals[j]), a.covered[j], a.intervals)
			} else {
				line += "; " + name + " unavailable"
			}
		}
		out.Trend = append(out.Trend, line)
	}
	if len(out.Trend) == 0 {
		out.Trend = []string{"Usage trend unavailable; no consecutive measured intervals."}
	}
	actions := map[string]usageAvoided{}
	for _, a := range x.Actions {
		if a.At.Before(x.From) || a.At.After(x.To) || !usageEvidenceLabel.MatchString(a.ID) || (a.Kind != "wake" && a.Kind != "retry") || (a.Disposition != "accepted" && a.Disposition != "suppressed_duplicate") {
			return bad
		}
		if old, ok := actions[a.ID]; ok && old != a {
			return bad
		}
		actions[a.ID] = a
	}
	if x.Actions != nil {
		count := 0
		for _, a := range actions {
			if a.Disposition == "suppressed_duplicate" {
				original, ok := actions[a.Original]
				if !ok || original.Disposition != "accepted" || original.Kind != a.Kind || original.At.After(a.At) {
					return bad
				}
				count++
			}
		}
		out.Avoided = fmt.Sprintf("Observed avoided redundant actions: %d identity-linked duplicate wakes/retries suppressed in this export. These are actions, not saved tokens or subscription quota; coverage is limited to supplied records.", count)
	}
	if len(x.Pairs) > 0 && x.Method == "controlled-replay" {
		seen := map[string]bool{}
		deltas := [5][]float64{}
		valid := true
		provider, model := "", ""
		for _, p := range x.Pairs {
			a, b := totals[p.Baseline], totals[p.Treatment]
			if a == nil || b == nil || p.Baseline == p.Treatment || seen[p.Baseline] || seen[p.Treatment] || a.provider != b.provider || a.model != b.model || a.intervals == 0 || b.intervals == 0 || !usageEvidenceLabel.MatchString(p.Workload) || !usageEvidenceLabel.MatchString(p.Acceptance) || p.BaselineWork < 1 || p.TreatmentWork < 1 || p.BaselineWork > 1000000 || p.TreatmentWork > 1000000 || !p.QualityAccepted || !p.ContinuityAccepted || !p.CoverageComplete || !p.CacheComparable || !p.OverheadIncluded {
				valid = false
				break
			}
			if provider != "" && (provider != a.provider || model != a.model) {
				valid = false
				break
			}
			provider, model = a.provider, a.model
			seen[p.Baseline] = true
			seen[p.Treatment] = true
			for j := range deltas {
				if a.covered[j] == a.intervals && b.covered[j] == b.intervals {
					deltas[j] = append(deltas[j], float64(a.totals[j])/float64(p.BaselineWork)-float64(b.totals[j])/float64(p.TreatmentWork))
				}
			}
		}
		if valid {
			out.Estimate = []string{fmt.Sprintf("Estimated token difference per accepted work unit, baseline minus treatment; %d controlled pairs. Positive means fewer tokens; negative means more. Quality, continuity, workload and work-unit equivalence are author-attested. Range is observed pair variation, not a confidence interval or subscription saving. Small samples and unmatched overhead limit inference.", len(x.Pairs))}
			for _, p := range x.Pairs {
				out.Estimate = append(out.Estimate, fmt.Sprintf("Baseline %s (%d accepted units) vs %s (%d); workload %s; acceptance %s.", p.Baseline, p.BaselineWork, p.Treatment, p.TreatmentWork, p.Workload, p.Acceptance))
			}
			for j, v := range deltas {
				if len(v) != len(x.Pairs) {
					out.Estimate = append(out.Estimate, usageMetricNames[j]+": unavailable; incomplete comparable coverage.")
					continue
				}
				sum, lo, hi := 0.0, v[0], v[0]
				for _, n := range v {
					sum += n
					lo = min(lo, n)
					hi = max(hi, n)
				}
				out.Estimate = append(out.Estimate, fmt.Sprintf("%s: estimated mean %.2f tokens per accepted unit; observed range %.2f to %.2f; n=%d.", usageMetricNames[j], sum/float64(len(v)), lo, hi, len(v)))
			}
		}
	}
	encoded, _ := json.Marshal(out)
	if len(encoded) > 6<<10 {
		return unavailableUsage("Usage evidence unavailable; projected details exceed the 6 KiB display bound. Export a narrower period.")
	}
	return out
}
