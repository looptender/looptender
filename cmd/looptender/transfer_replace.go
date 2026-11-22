package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/supervise"
)

// transfer replace-children is the explicit, person-run way to replace child
// lanes blocked on another provider with successor threads on the loop's
// provider, typically after a transfer across providers moved them. It uses
// supervise.ReplaceChildren, as a fallback with bb.replace_blocked_children
// does, with the same caps and exactly-once rules.

// newSuccessorMaker builds bb for the command; fixtures swap it.
var newSuccessorMaker = func(cli, coordinator string) supervise.ChildSuccessorMaker {
	return bb.New(config.BB{CLI: cli, Thread: coordinator})
}

// replaceChildrenBudget bounds the time one run spends.
const replaceChildrenBudget = 5 * time.Minute

func transferReplaceChildren(e env, args []string) int {
	fs := flag.NewFlagSet("transfer replace-children", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("c", "", configFlagHelp("configuration path"))
	model := fs.String("model", "", "model of each successor thread (required unless --dry-run)")
	reasoning := fs.String("reasoning", "", "reasoning level of each successor thread")
	permission := fs.String("permission-mode", "", "accept-edits (default), auto or full")
	all := fs.Bool("all-old-provider", false, "replace every live child on another provider, not only those blocked on a usage limit")
	dryRun := fs.Bool("dry-run", false, "preview: count what would be created and create nothing")
	asJSON := fs.Bool("json", false, "machine-readable result")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(e.stdout)
			fs.PrintDefaults()
			return exitOK
		}
		e.errf("transfer replace-children: invalid arguments")
		return exitUsage
	}
	if fs.NArg() != 0 || (!*dryRun && *model == "") || (*permission != "" && *permission != "accept-edits" && *permission != "auto" && *permission != "full") ||
		(*model != "" && !transferLabel.MatchString(*model)) || (*reasoning != "" && !transferLabel.MatchString(*reasoning)) {
		e.errf("transfer replace-children: --model is required (unless --dry-run); --permission-mode is accept-edits, auto or full")
		return exitUsage
	}
	fail := func(err error) int {
		e.errf("transfer replace-children: %v", err)
		if errors.Is(err, lock.ErrHeld) {
			return exitSkipped
		}
		return exitFailed
	}
	if !configDefault(e, "transfer replace-children", fs, path) {
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		e.errf("transfer replace-children: configuration unavailable; inspect it privately")
		return exitUsage
	}
	if cfg.Loop.Adapter != "bb" || cfg.Loop.BB == nil || cfg.Loop.HasRouteList() {
		return fail(errors.New("supported only for a single BB primary without fallbacks; a fallback uses bb.replace_blocked_children"))
	}
	tick, err := lock.Acquire(cfg.Loop.StateDir)
	if err != nil {
		return fail(err)
	}
	defer tick.Release()
	ctl, err := control.Load(cfg.Loop.StateDir)
	if err != nil {
		return fail(errors.New("control record unavailable"))
	}
	switch {
	case ctl.SupersededBy != "":
		return fail(control.ErrSuperseded)
	case ctl.PendingTransfer != nil:
		return fail(errors.New("a transfer is pending; recover it first"))
	case ctl.BBTarget != "" && ctl.BBTarget != cfg.Loop.BB.Thread:
		return fail(errors.New("configuration does not match the loop's bound thread"))
	}
	if err := checkOwnership(cfg.Loop.StateDir, nil); err != nil {
		return fail(err)
	}
	st, err := state.Load(cfg.Loop.StateDir)
	if err != nil || st.Target() != state.Primary {
		return fail(errors.New("primary state unavailable"))
	}
	perm := *permission
	if perm == "" {
		perm = config.DefaultNewThreadPermission
	}
	deadline := time.Now().Add(replaceChildrenBudget)
	p := supervise.ReplacePlan{Loop: cfg.Loop.Name, Coordinator: cfg.Loop.BB.Thread, Provider: cfg.Loop.Provider, Model: *model, Reasoning: *reasoning,
		Permission: perm, AllOldProvider: *all, Lifetime: cfg.Loop.Limits.MaxChildSuccessors, DryRun: *dryRun}
	envm := supervise.ChildMoveEnv{
		Left: func() time.Duration { return time.Until(deadline) },
		// A person's control write while threads are being created ends it.
		Held: func() bool {
			c, err := control.Load(cfg.Loop.StateDir)
			return err != nil || !c.UpdatedAt.Equal(ctl.UpdatedAt)
		},
		Save: func() {
			if s, err := state.Load(cfg.Loop.StateDir); err == nil {
				s.ChildSuccessors, s.ChildSuccessorsCreated = st.ChildSuccessors, st.ChildSuccessorsCreated
				_ = state.Save(cfg.Loop.StateDir, s)
			}
		},
		Warn: func(s string) { e.errf("warning: %s", s) },
	}
	res, err := supervise.ReplaceChildren(e.ctx, newSuccessorMaker(cfg.Loop.BB.CLI, cfg.Loop.BB.Thread), &st.ChildSuccessors, &st.ChildSuccessorsCreated, p, envm)
	outcome := "applied"
	switch {
	case *dryRun:
		outcome = "preview"
	case errors.Is(err, supervise.ErrSuccessorUnsettled):
		outcome = "unsettled"
	case err != nil:
		return fail(err)
	}
	if *asJSON {
		out := struct {
			Outcome  string                  `json:"outcome"`
			Result   supervise.ReplaceResult `json:"result"`
			Lifetime int                     `json:"lifetimeCreated"`
			Max      int                     `json:"lifetimeMax"`
		}{outcome, res, st.ChildSuccessorsCreated, cfg.Loop.Limits.MaxChildSuccessors}
		if json.NewEncoder(e.stdout).Encode(out) != nil {
			return exitFailed
		}
	} else {
		printReplaceResult(e.stdout, outcome, res, st.ChildSuccessorsCreated, cfg.Loop.Limits.MaxChildSuccessors, *all)
	}
	if outcome == "unsettled" {
		e.errf("transfer replace-children: a successor titled %q may exist and could not be found; nothing else was created. Look for it in BB, then run this again to look again", res.Title)
		return exitFailed
	}
	return exitOK
}

func printReplaceResult(w io.Writer, outcome string, r supervise.ReplaceResult, created, max int, all bool) {
	what := "blocked on a usage limit of another provider"
	if all {
		what = "on another provider"
	}
	if !r.Listed && outcome != "unsettled" {
		fmt.Fprintln(w, "Child threads could not be listed; nothing was created.")
		return
	}
	switch outcome {
	case "preview":
		fmt.Fprintf(w, "Would create %d successor thread(s): %d child lane(s) are %s", r.WouldCreate, r.Eligible, what)
	case "unsettled":
		fmt.Fprintf(w, "Created %d successor thread(s) before one could not be confirmed", r.Created)
	default:
		fmt.Fprintf(w, "Created %d successor thread(s) for %d child lane(s) %s", r.Created, r.Eligible, what)
	}
	if r.CappedRun > 0 {
		fmt.Fprintf(w, "; %d over the %d per run", r.CappedRun, config.ChildSuccessorsPerRun)
	}
	if r.CappedLife > 0 {
		fmt.Fprintf(w, "; %d over the loop's limit (%d of %d used)", r.CappedLife, created, max)
	}
	if r.Unknown > 0 {
		fmt.Fprintf(w, "; %d could not be inspected", r.Unknown)
	}
	fmt.Fprintf(w, ". %d lane(s) have a successor on record.\n", r.Replaced)
	fmt.Fprintln(w, "The blocked lanes are left as they were. When their provider's limit resets, BB or a person may run them again; stopping or archiving them is your call.")
}
