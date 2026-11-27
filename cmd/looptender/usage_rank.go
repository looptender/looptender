package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

const usageRankHelp = `usage-rank --evidence FILE [--evidence FILE ...] [--collection-dir DIR ...]
Read at most eight explicitly supplied local exports/collection records; JSON only.
No configuration, provider query, collection or runtime write. Paths are never output.
Ranks compare complete recorded metrics within identical provider and declared/observed
periods. They do not establish complete history, account attribution or quota savings.
`

type usageRankRow struct {
	Loop         string    `json:"loop"`
	Stream       string    `json:"stream"`
	Provider     string    `json:"provider"`
	Source       string    `json:"source"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	ObservedFrom time.Time `json:"observedFrom"`
	ObservedTo   time.Time `json:"observedTo"`
	Metric       string    `json:"metric"`
	Tokens       *int64    `json:"observedTokens"`
	Covered      int       `json:"coveredIntervals"`
	Intervals    int       `json:"intervals"`
	Rank         *int      `json:"rank"`
	Peers        int       `json:"comparableStreams"`
	Reason       string    `json:"rankingStatus"`
}
type usageRankSource struct {
	Index       int        `json:"index"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	LastSuccess *time.Time `json:"lastSuccessfulCollection"`
}
type usageRankReport struct {
	Version int               `json:"version"`
	AsOf    time.Time         `json:"asOf"`
	Sources []usageRankSource `json:"sources"`
	Rows    []usageRankRow    `json:"rows"`
	Limits  string            `json:"limits"`
}
type usageRankInput struct {
	raw     []byte
	kind    string
	success *time.Time
}

// Explicit regular files only, with the same nonblocking/no-follow open boundary
// as Panel evidence. Errors never include the path or raw evidence.
func readUsageRankFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > usageEvidenceLimit {
		return nil, fmt.Errorf("unavailable evidence")
	}
	f, err := openUsageEvidence(path)
	if err != nil {
		return nil, fmt.Errorf("unavailable evidence")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > usageEvidenceLimit {
		return nil, fmt.Errorf("unavailable evidence")
	}
	b, err := io.ReadAll(io.LimitReader(f, usageEvidenceLimit+1))
	if err != nil || len(b) > usageEvidenceLimit {
		return nil, fmt.Errorf("unavailable evidence")
	}
	return b, nil
}
func usageRankCommand(e env, args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(e.stdout, usageRankHelp)
		return 0
	}
	if len(args) == 0 || len(args)%2 != 0 || len(args) > 16 {
		fmt.Fprint(e.stderr, usageRankHelp)
		return exitUsage
	}
	// Validate every argument before any read; no generic flag errors echo paths.
	for i := 0; i < len(args); i += 2 {
		if (args[i] != "--evidence" && args[i] != "--collection-dir") || args[i+1] == "" {
			fmt.Fprint(e.stderr, usageRankHelp)
			return exitUsage
		}
	}
	inputs := []usageRankInput{}
	for i := 0; i < len(args); i += 2 {
		in := usageRankInput{kind: "imported"}
		if args[i] == "--evidence" {
			in.raw, _ = readUsageRankFile(args[i+1])
		} else {
			in.kind = "automatic"
			r, err := readUsageCollection(args[i+1])
			if err == nil {
				in.raw, _ = json.Marshal(r.Export)
				if !r.Success.IsZero() {
					in.success = &r.Success
				}
			}
		}
		inputs = append(inputs, in)
	}
	report := rankUsageEvidence(inputs, time.Now().UTC())
	if json.NewEncoder(e.stdout).Encode(report) != nil {
		return 1
	}
	return 0
}

func rankUsageEvidence(inputs []usageRankInput, now time.Time) usageRankReport {
	out := usageRankReport{Version: 1, AsOf: now, Sources: []usageRankSource{}, Rows: []usageRankRow{}, Limits: "Recorded token deltas only; cached input and reasoning are subsets, not additional tokens. Account/model attribution, unobserved history, lane completeness, useful work and subscription savings unavailable. Period end is evidence freshness, not proof of current collection. Supplied labels are author-attested; automatic provider attribution may be unavailable. No cross-source loop aggregation."}
	if len(inputs) > 8 {
		return out
	}
	seen := map[string]bool{}
	// Refuse all repeated loop exports, including identical files, rather than
	// guessing stream identity or merging overlapping windows across sources.
	counts := map[string]int{}
	for _, in := range inputs {
		var x usageExport
		if json.Unmarshal(in.raw, &x) == nil {
			counts[x.Loop]++
		}
	}
	for idx, in := range inputs {
		source := usageRankSource{Index: idx + 1, Kind: in.kind, Status: "unavailable"}
		var x usageExport
		if json.Unmarshal(in.raw, &x) != nil || !usageEvidenceLabel.MatchString(x.Loop) {
			out.Sources = append(out.Sources, source)
			continue
		}
		if counts[x.Loop] > 1 || seen[x.Loop] {
			source.Status = "unavailable: repeated loop sources"
			out.Sources = append(out.Sources, source)
			continue
		}
		seen[x.Loop] = true
		projectUsageEvidence(in.raw, x.Loop, now, func(x usageExport, totals map[string]*usageAccum, streams map[string][]usageSample) {
			source.Status = "recorded"
			if in.success != nil && !in.success.After(now) {
				source.LastSuccess = in.success
			}
			keys := []string{}
			for k := range totals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				a := totals[k]
				samples := streams[k]
				names := append(append([]string{}, usageMetricNames...), "total input plus output")
				for j, name := range names {
					row := usageRankRow{Loop: x.Loop, Stream: k, Provider: a.provider, Source: in.kind, From: x.From, To: x.To, ObservedFrom: samples[0].At, ObservedTo: samples[len(samples)-1].At, Metric: name, Intervals: a.intervals, Reason: "unavailable: incomplete metric coverage"}
					if j < 5 {
						row.Covered = a.covered[j]
						if row.Covered > 0 {
							n := a.totals[j]
							row.Tokens = &n
						}
					} else if a.intervals > 0 && a.covered[0] == a.intervals && a.covered[2] == a.intervals {
						n := a.totals[0] + a.totals[2]
						row.Tokens = &n
						row.Covered = a.intervals
					}
					if row.Tokens != nil && row.Covered == row.Intervals && row.ObservedTo.After(row.ObservedFrom) {
						row.Reason = "unavailable: provider attribution"
						if row.Provider != "unavailable" && row.Provider != "unknown" {
							row.Reason = "unavailable: no comparable peer"
						}
					}
					out.Rows = append(out.Rows, row)
				}
			}
		})
		out.Sources = append(out.Sources, source)
	}
	groups := map[string][]int{}
	for i, r := range out.Rows {
		if r.Reason != "unavailable: no comparable peer" {
			continue
		}
		key := fmt.Sprintf("%s|%s|%s|%s|%s|%s", r.Provider, r.Metric, r.From.UTC().Format(time.RFC3339Nano), r.To.UTC().Format(time.RFC3339Nano), r.ObservedFrom.UTC().Format(time.RFC3339Nano), r.ObservedTo.UTC().Format(time.RFC3339Nano))
		groups[key] = append(groups[key], i)
	}
	for _, indices := range groups {
		sort.SliceStable(indices, func(i, j int) bool { return *out.Rows[indices[i]].Tokens > *out.Rows[indices[j]].Tokens })
		rank := 1
		for pos, idx := range indices {
			r := &out.Rows[idx]
			r.Peers = len(indices)
			if len(indices) < 2 {
				continue
			}
			if pos > 0 && *r.Tokens != *out.Rows[indices[pos-1]].Tokens {
				rank = pos + 1
			}
			n := rank
			r.Rank = &n
			r.Reason = "ranked recorded deltas only"
		}
	}
	// Stable presentation by metric, provider, period and rank; unknowns last.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if !a.From.Equal(b.From) {
			return a.From.Before(b.From)
		}
		if !a.To.Equal(b.To) {
			return a.To.Before(b.To)
		}
		if !a.ObservedFrom.Equal(b.ObservedFrom) {
			return a.ObservedFrom.Before(b.ObservedFrom)
		}
		if !a.ObservedTo.Equal(b.ObservedTo) {
			return a.ObservedTo.Before(b.ObservedTo)
		}
		if (a.Rank == nil) != (b.Rank == nil) {
			return a.Rank != nil
		}
		if a.Rank != nil && b.Rank != nil && *a.Rank != *b.Rank {
			return *a.Rank < *b.Rank
		}
		if a.Loop != b.Loop {
			return a.Loop < b.Loop
		}
		return a.Stream < b.Stream
	})
	return out
}
