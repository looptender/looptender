package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/looptender/looptender/internal/prready"
)

func prReady(e env, args []string, gh prready.Query) int {
	fs := flag.NewFlagSet("pr-ready", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	repo := fs.String("repo", "", "explicit github.com OWNER/NAME")
	pr := fs.Int("pr", 0, "pull request number")
	path := fs.String("gh", "gh", "path to gh CLI")
	asJSON := fs.Bool("json", false, "print structured evidence")
	if fs.Parse(args) != nil || fs.NArg() != 0 || !prready.Valid(*repo, *pr) {
		e.errf("usage: looptender pr-ready --repo OWNER/NAME --pr NUMBER [--json]")
		return exitUsage
	}
	if gh == nil {
		gh = prready.ExecGH(*path)
	}
	r := prready.Inspect(e.ctx, gh, *repo, *pr)
	if *asJSON {
		if json.NewEncoder(e.stdout).Encode(r) != nil {
			return exitFailed
		}
	} else if r.Ready {
		if _, err := fmt.Fprintln(e.stdout, r.Head); err != nil {
			return exitFailed
		}
	} else {
		fmt.Fprintf(e.stderr, "pr-ready: %s\n", r.Reason)
	}
	if r.Ready {
		return exitOK
	}
	return exitFailed
}
