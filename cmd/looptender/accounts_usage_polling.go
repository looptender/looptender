package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

const accountUsageCadence = 5 * time.Minute

func accountUsageStamp(s accountQuotaSelection) string {
	h := sha256.New()
	for _, item := range []struct {
		path string
		info os.FileInfo
	}{{s.Entry.CLI, s.CLI}, {s.Entry.Workdir, s.Workdir}, {s.Entry.Directory, s.Directory}} {
		if item.info == nil && item.path == "" {
			continue
		}
		id, err := accountUsageIdentity(item.path, item.info)
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s\x00", id)
	}
	if s.CLI != nil {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%d", s.Entry.CLI, s.CLI.Size(), s.CLI.ModTime().UnixNano(), s.CLI.Mode())
	}
	return hex.EncodeToString(h.Sum(nil))
}
func accountUsageMatches(p accounts.UsagePoll, s accountQuotaSelection) bool {
	stamp := accountUsageStamp(s)
	return stamp != "" && p.ID == s.Entry.ID && p.Revision == s.Entry.Revision && p.Binding == s.Bound && p.Executable == stamp
}
func accountUsagePollingCommand(e env, dir, id string, enable, disable, asJSON bool) int {
	if enable && disable || !panelAccountID.MatchString(id) {
		e.errf("accounts usage-polling: select --id and at most one of --enable/--disable")
		return exitUsage
	}
	if enable || disable {
		if err := setAccountUsagePolling(dir, id, enable, nil, os.Environ(), time.Now); err != nil {
			e.errf("accounts usage-polling: selection or private settings unavailable; nothing enabled")
			return exitFailed
		}
	}

	f, err := accounts.LoadUsagePolling(dir)
	if err != nil {
		e.errf("accounts usage-polling: settings unavailable")
		return exitFailed
	}
	var selected *accounts.UsagePoll
	for i := range f.Entries {
		if f.Entries[i].ID == id {
			selected = &f.Entries[i]
		}
	}
	state := "off"
	if selected != nil {
		state = "on"
		s, err := accountQuotaCapture(dir, id, os.Environ())
		if err != nil || !accountUsageMatches(*selected, s) {
			state = "selection_changed"
		}
	}
	// Public output omits binding/executable digests and the private grant.
	out := struct {
		SchemaVersion int        `json:"schemaVersion"`
		State         string     `json:"state"`
		NextAt        *time.Time `json:"nextAttemptAt,omitempty"`
		AttemptAt     *time.Time `json:"lastAttemptAt,omitempty"`
	}{SchemaVersion: 1, State: state}
	if selected != nil {
		out.NextAt = &selected.NextAt
		out.AttemptAt = selected.AttemptAt
	}
	if asJSON {
		_ = json.NewEncoder(e.stdout).Encode(out)
	} else {
		fmt.Fprintf(e.stdout, "Automatic selected-context usage: %s\n", state)
		if state == "on" {
			fmt.Fprintln(e.stdout, "Keep a non-offline panel or dock process running to fetch model-free usage at least 5 minutes apart, with bounded failure backoff. The menu alone cannot schedule reads. No login or model request is started.")
		}
	}
	return exitOK
}

// accountUsagePollDue is called only by the elected background worker. Ordinary catalog
// reads remain saved-only. No grant means no locks, writes or provider calls.
func accountUsagePollDue(ctx context.Context, dir string, env []string, now func() time.Time, run accountQuotaRun) {
	f, err := accounts.LoadUsagePolling(dir)
	if err != nil || len(f.Entries) == 0 {
		return
	}
	guard, err := accounts.UsagePollGuard(dir)
	if err != nil {
		return
	}
	defer guard.Release()
	f, err = accounts.LoadUsagePolling(dir)
	if err != nil {
		return
	}
	sort.SliceStable(f.Entries, func(i, j int) bool { return f.Entries[i].NextAt.Before(f.Entries[j].NextAt) })
	batch, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	count := 0
	for _, p := range f.Entries {
		at := now().UTC()
		if batch.Err() != nil || count >= 2 {
			return
		}
		if p.NextAt.After(at) {
			continue
		}
		// Future attempt clocks are unknown, never an invitation to retry.
		if p.AttemptAt != nil && p.AttemptAt.After(at) {
			continue
		}
		s, err := accountQuotaCapture(dir, p.ID, env)
		if err != nil || !accountUsageMatches(p, s) || !panelQuotaProviderSupported(s.Entry.Provider) {
			continue
		}
		claimed := false
		// Reserve cadence durably before the external read. A crash or lost outcome
		// waits the same five minutes instead of immediately repeating provider work.
		err = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error {
			for i := range f.Entries {
				q := &f.Entries[i]
				if q.ID == p.ID && q.Grant == p.Grant && !q.NextAt.After(at) {
					q.AttemptAt = &at
					q.NextAt = at.Add(accountUsageCadence)
					claimed = true
				}
			}
			return nil
		})
		if err != nil || !claimed {
			continue
		}
		count++
		v, err := accountQuotaRefresh(batch, dir, p.ID, &s, run, env, now)
		failed := err != nil || v.State != "reported" || v.FailedRefreshAt != nil
		_ = accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error {
			for i := range f.Entries {
				q := &f.Entries[i]
				if q.ID != p.ID || q.Grant != p.Grant {
					continue
				}
				q.Failures = 0
				if failed {
					q.Failures = p.Failures + 1
					if q.Failures > 10 {
						q.Failures = 10
					}
				}
				delay := accountUsageCadence
				for n := 0; n < q.Failures && delay < time.Hour; n++ {
					delay *= 2
				}
				if delay > time.Hour {
					delay = time.Hour
				}
				q.NextAt = now().UTC().Add(delay)
			}
			return nil
		})
	}
}

func setAccountUsagePolling(dir, id string, enable bool, expected *accountQuotaSelection, env []string, now func() time.Time) error {
	var s accountQuotaSelection
	var err error
	if enable {
		s, err = accountQuotaCapture(dir, id, env)
		if err != nil || !panelQuotaProviderSupported(s.Entry.Provider) || accountUsageStamp(s) == "" {
			return accounts.ErrChanged
		}
		if expected != nil && (!expected.same(s) || accountUsageStamp(*expected) != accountUsageStamp(s)) {
			return accounts.ErrChanged
		}
	}
	grant, err := panelRandom(16)
	if err != nil {
		return err
	}
	return accounts.UpdateUsagePolling(dir, func(f *accounts.UsagePolling) error {
		if enable {
			for _, p := range f.Entries {
				if accountUsageMatches(p, s) {
					return nil
				}
			}
		}
		entries := f.Entries[:0]
		for _, p := range f.Entries {
			if p.ID != id {
				entries = append(entries, p)
			}
		}
		f.Entries = entries
		if enable {
			f.Entries = append(f.Entries, accounts.UsagePoll{ID: id, Revision: s.Entry.Revision, Binding: s.Bound, Executable: accountUsageStamp(s), Grant: grant, NextAt: now().UTC()})
		}
		return nil
	})
}
