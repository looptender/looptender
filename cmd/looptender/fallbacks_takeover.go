package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/looptender/looptender/internal/config"
)

// fallbacks arm gives an existing BB loop the takeover a new BB loop gets
// from init: one new-thread fallback on another usable provider, with
// handoff on for rate limits. It is the same detection and defaults, then
// the fallbacks add path, which checks ownership and writes the fallbacks
// file and errors.failover_on. An update never does this on its own.

func fallbacksArmCommand(e env, args []string) int {
	return fallbacksArm(e, args, os.Stdin, exec.LookPath, runBBLookup)
}

func fallbacksArm(e env, args []string, input io.Reader, lookPath func(string) (string, error), bb bbLookup) int {
	fs := flag.NewFlagSet("fallbacks arm", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	path := fs.String("c", "", configFlagHelp("path to the config file"))
	var c newThreadChoice
	fs.StringVar(&c.Provider, "provider", "", "provider of the new conversation: claude-code or codex (default: the usable one bb reports)")
	fs.StringVar(&c.Model, "model", "", "its model (default: the provider's default model bb reports)")
	fs.StringVar(&c.Reasoning, "reasoning", "", "its reasoning level")
	fs.StringVar(&c.Permission, "permission-mode", "", "its permission mode: accept-edits (default), auto or full")
	dryRun := fs.Bool("dry-run", false, "preview only; write nothing")
	yes := fs.Bool("yes", false, "write without asking")
	asJSON := fs.Bool("json", false, "machine-readable result (needs --dry-run or --yes)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 0 || (*asJSON && !*dryRun && !*yes) {
		e.errf("fallbacks arm: unexpected arguments; --json needs --dry-run or --yes")
		return exitUsage
	}
	if !configDefault(e, "fallbacks arm", fs, path) {
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		e.errf("fallbacks arm: configuration unavailable; inspect it privately")
		return exitUsage
	}
	switch {
	case cfg.Loop.Adapter != "bb" || cfg.Loop.BB == nil:
		e.errf("fallbacks arm: this loop is not a BB loop; a takeover starts a new BB conversation")
		return exitUsage
	case cfg.Loop.FollowsStandard():
		e.errf("fallbacks arm: this loop follows your standard route list; add a new-conversation route there with keepgoing routes add")
		return exitUsage
	}
	if cfg.FallbackSet != nil {
		for _, cand := range cfg.FallbackSet.Candidates {
			if isNewThreadCandidate(cand) && !cand.Disabled {
				fmt.Fprintf(e.stdout, "Already armed: fallback %s starts a new conversation on %s at a usage limit. Nothing changed.\n", cand.ID, cand.Provider)
				return exitOK
			}
		}
	}
	if c.Provider == "" {
		offer := detectNewThreadOffer(e.ctx, cfg.Loop.Provider, lookPath)
		if offer == nil {
			e.errf("fallbacks arm: bb reports no other usable provider (claude-code or codex, available in BB, with its CLI on PATH); pass --provider and --model to name one")
			return exitFailed
		}
		c.Provider = offer.Provider
		if c.Model == "" {
			c.Model = offer.Model
		}
	}
	if err := validNewThreadChoice(c, cfg.Loop.Provider); err != nil {
		e.errf("fallbacks arm: %s", strings.NewReplacer("--new-thread-provider", "--provider", "--new-thread-model", "--model", "--new-thread-reasoning", "--reasoning", "--new-thread-permission-mode", "--permission-mode").Replace(err.Error()))
		return exitUsage
	}
	// The same id and quota label init gives this fallback.
	add := []string{"--non-interactive", "--agent", "bb", "--new-thread", "--provider", c.Provider, "--model", c.Model, "--enable-handoff",
		"--id", "new-conversation-" + c.Provider, "--group", c.Provider, "-c", *path}
	if c.Reasoning != "" {
		add = append(add, "--reasoning", c.Reasoning)
	}
	if c.Permission != "" {
		add = append(add, "--permission-mode", c.Permission)
	}
	if *asJSON {
		add = append(add, "--json")
	}
	preview := append(append([]string{}, add...), "--dry-run")
	if !*asJSON {
		probe := config.Candidate{Agent: config.Agent{Provider: c.Provider, BB: &config.BB{NewThread: true, Model: c.Model, Reasoning: c.Reasoning, PermissionMode: c.Permission}}}
		printNewThreadPlan(e.stdout, cfg.Loop.Provider, probe, "do not arm it; once armed, remove the fallback with keepgoing fallbacks remove, or turn handoff off with keepgoing fallbacks handoff off")
		if n := otherFallbacksGainingHandoff(cfg); n > 0 {
			fmt.Fprintf(e.stdout, "Arming turns handoff on for rate limits for the whole loop, so its %d other fallback(s) start taking handoffs too, in their current order, before this new conversation is tried.\n", n)
		}
		fmt.Fprintln(e.stdout)
	}
	if *dryRun {
		return fallbacksAdd(e, preview, input, lookPath, bb)
	}
	if !*yes {
		if code := fallbacksAdd(e, preview, input, lookPath, bb); code != exitOK {
			return code
		}
		fmt.Fprint(e.stdout, "\nArm this loop? (yes/no) [no]: ")
		line, _ := bufio.NewReader(input).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "yes" && a != "y" {
			fmt.Fprintln(e.stdout, "Cancelled: nothing written.")
			return exitOK
		}
	}
	return fallbacksAdd(e, add, input, lookPath, bb)
}

// otherFallbacksGainingHandoff counts the loop's enabled fallbacks that do
// not take handoffs now and would once arming turns rate-limit handoff on.
func otherFallbacksGainingHandoff(cfg *config.Config) int {
	if cfg.FallbackSet == nil {
		return 0
	}
	for _, class := range cfg.Loop.Errors.FailoverOn {
		if class == "rate_limit" {
			return 0 // they already take rate-limit handoffs
		}
	}
	n := 0
	for _, c := range cfg.FallbackSet.Candidates {
		if !c.Disabled && !isNewThreadCandidate(c) {
			n++
		}
	}
	return n
}
