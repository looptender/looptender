package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// usage-watch turns the usage watch on or off for a loop, or shows it. The
// setting is a record in the loop's state directory, never a key of its
// configuration: an older build, including the rollback copy after an
// install, loads the configuration as before and never reads the record.
func usageWatchCommand(e env, args []string) int {
	fs := flag.NewFlagSet("usage-watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("c", "", configFlagHelp("configuration"))
	account := fs.String("account", "", "accounts catalog ID of the login the loop's own conversation uses")
	every := fs.Duration("every", 30*time.Minute, "how often its quota is read (10m to 24h)")
	warnWithin := fs.Duration("warn-within", 12*time.Hour, "warn once when, at the week's rate so far, it runs out this soon (1h to 168h)")
	off := fs.Bool("off", false, "turn the watch off")
	asJSON := fs.Bool("json", false, "print the setting as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		e.errf("usage: usage-watch [-c CONFIG] [--account ID [--every 30m] [--warn-within 12h] | --off] [--json]")
		return exitUsage
	}
	if *off && *account != "" {
		e.errf("usage-watch: --off and --account do not go together")
		return exitUsage
	}
	if !configDefault(e, "usage-watch", fs, path) {
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		e.errf("usage-watch: configuration cannot be read or validated; run doctor for diagnostics")
		return exitUsage
	}
	dir := cfg.Loop.StateDir
	if (*off || *account != "") && os.MkdirAll(dir, 0o700) != nil {
		e.errf("usage-watch: the state directory cannot be created")
		return exitFailed
	}
	switch {
	case *off:
		if err := state.SaveUsageWatchSetting(dir, nil); err != nil {
			e.errf("usage-watch: %v", err)
			return exitFailed
		}
	case *account != "":
		s := state.UsageWatchSetting{Account: *account, Every: every.String(), WarnWithin: warnWithin.String(), SetAt: time.Now().UTC()}
		if _, _, ok := s.Durations(); !ok {
			e.errf("usage-watch: --account must be an accounts catalog ID, --every between 10m and 24h, and --warn-within between 1h and 168h")
			return exitUsage
		}
		if err := state.SaveUsageWatchSetting(dir, &s); err != nil {
			e.errf("usage-watch: %v", err)
			return exitFailed
		}
		if known, _ := usageWatchAccountKnown(*account); !known {
			e.errf("usage-watch: warning: the accounts catalog has no entry %s; nothing is read until it does (see accounts list)", *account)
		}
	}
	v := describeUsageWatch(dir)
	if *asJSON {
		_ = json.NewEncoder(e.stdout).Encode(struct {
			SchemaVersion int             `json:"schemaVersion"`
			UsageWatch    *usageWatchView `json:"usageWatch"`
		}{1, v})
		return exitOK
	}
	if v == nil {
		fmt.Fprintln(e.stdout, "usage watch: off")
		return exitOK
	}
	printUsageWatch(e.stdout, v)
	return exitOK
}

// usageWatchView is the setting as status and doctor say it.
type usageWatchView struct {
	Account    string    `json:"account"`
	Every      string    `json:"every"`
	WarnWithin string    `json:"warnWithin"`
	SetAt      time.Time `json:"setAt"`
	// Unreadable: a setting is there and cannot be used; the watch is off.
	Unreadable bool `json:"unreadable,omitempty"`
}

func describeUsageWatch(stateDir string) *usageWatchView {
	s, err := state.LoadUsageWatchSetting(stateDir)
	if err != nil {
		return &usageWatchView{Unreadable: true}
	}
	if s == nil {
		return nil
	}
	return &usageWatchView{Account: s.Account, Every: s.Every, WarnWithin: s.WarnWithin, SetAt: s.SetAt}
}

func printUsageWatch(w io.Writer, v *usageWatchView) {
	switch {
	case v == nil:
	case v.Unreadable:
		fmt.Fprintf(w, "usage watch: off: %s cannot be read; set it again with usage-watch\n", state.UsageWatchSettingName)
	default:
		fmt.Fprintf(w, "usage watch: on for account %s, read every %s; warns once a window when it would run out within %s\n", v.Account, v.Every, v.WarnWithin)
	}
}

// usageWatchAccountKnown says whether the catalog the watch reads has the
// entry.
func usageWatchAccountKnown(id string) (bool, error) {
	dir, err := accountsDirectory(usageWatchAccountsDir)
	if err != nil {
		return false, err
	}
	f, err := accounts.Load(dir)
	if err != nil {
		return false, err
	}
	if accounts.Find(&f, id) == nil {
		return false, errors.New("not in the catalog")
	}
	return true, nil
}
