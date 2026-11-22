package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/health"
	"github.com/looptender/looptender/internal/probe"
)

// A BB loop's takeover on a usage limit is a new conversation on another
// provider (bb.new_thread). init and fallbacks arm set one up when another
// provider is known to be usable, which is decided from metadata only:
//
//   - `bb provider list --json` reports the provider as available, it is not
//     the loop's own provider, and it is one looptender can create a
//     conversation on and check (claude-code or codex);
//   - the local CLI that checks it before any handoff (claude or codex) is on
//     PATH, as fallbacks add requires; and
//   - `bb provider models <provider> --json` names a default model.
//
// No login is checked and no model is called. Any failure or unexpected
// answer means no provider is known, and setup is as before.

// newThreadProviders are the providers a takeover may start a conversation
// on, in the order they are offered.
var newThreadProviders = []string{"claude-code", "codex"}

// newThreadOffer is a provider a new conversation could take over on, with
// the model BB names as its default.
type newThreadOffer struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// newThreadQueryTime bounds each bb metadata query.
const newThreadQueryTime = 5 * time.Second

// detectNewThreadOffer finds a usable provider other than primary. It is a
// variable so fixtures can stand in for bb.
var detectNewThreadOffer = func(ctx context.Context, primary string, lookPath func(string) (string, error)) *newThreadOffer {
	cli, err := lookPath("bb")
	if err != nil {
		return nil
	}
	query := func(args ...string) []byte {
		qctx, cancel := context.WithTimeout(ctx, newThreadQueryTime)
		defer cancel()
		b, err := doctorExecBounded(qctx, cli, args, nil, "", 1<<20)
		if err != nil || qctx.Err() != nil {
			return nil
		}
		return b
	}
	var providers []struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
	}
	if json.Unmarshal(query("provider", "list", "--json"), &providers) != nil {
		return nil
	}
	available := map[string]bool{}
	for _, p := range providers {
		available[p.ID] = p.Available
	}
	for _, p := range newThreadProviders {
		if p == primary || !available[p] {
			continue
		}
		kind, err := bbProbeKind(p)
		if err != nil {
			continue
		}
		if _, err := lookPath(kind); err != nil {
			continue // it could not be checked before a handoff
		}
		var models []struct {
			ID        string `json:"id"`
			Model     string `json:"model"`
			IsDefault bool   `json:"isDefault"`
		}
		if json.Unmarshal(query("provider", "models", p, "--json"), &models) != nil {
			continue
		}
		for _, m := range models {
			name := m.Model
			if name == "" {
				name = m.ID
			}
			if m.IsDefault && transferLabel.MatchString(name) {
				return &newThreadOffer{Provider: p, Model: name}
			}
		}
	}
	return nil
}

// newThreadChoice is a new-thread fallback asked for by flags or answers.
type newThreadChoice struct {
	Provider, Model, Reasoning, Permission string
}

// validNewThreadChoice checks a choice against the loop's own provider.
func validNewThreadChoice(c newThreadChoice, primary string) error {
	known := false
	for _, p := range newThreadProviders {
		known = known || c.Provider == p
	}
	switch {
	case !known:
		return errors.New("--new-thread-provider must be claude-code or codex (or none)")
	case c.Provider == primary:
		return fmt.Errorf("--new-thread-provider %s is the loop's own provider; a takeover needs another one", c.Provider)
	case c.Model == "" || !transferLabel.MatchString(c.Model):
		return errors.New("--new-thread-model is required with --new-thread-provider")
	case c.Reasoning != "" && !transferLabel.MatchString(c.Reasoning):
		return errors.New("--new-thread-reasoning has an unsupported shape")
	case c.Permission != "" && config.PermissionRank(c.Permission) < 0:
		return errors.New("--new-thread-permission-mode must be accept-edits, auto or full")
	}
	return nil
}

// newThreadCandidate is the fallback for a choice: a new conversation on the
// provider, checked with its local CLI, with the loop's bb CLI. Children
// follow and blocked lanes get successors by default (bb.move_children and
// bb.replace_blocked_children unset).
func newThreadCandidate(c newThreadChoice, bbCLI string, lookPath func(string) (string, error)) (config.Candidate, error) {
	kind, err := bbProbeKind(c.Provider)
	if err != nil {
		return config.Candidate{}, err
	}
	if _, err := lookPath(kind); err != nil {
		return config.Candidate{}, fmt.Errorf("the %s CLI was not found on PATH; a new-thread fallback is checked with it before any handoff", kind)
	}
	perm := c.Permission
	if perm == config.DefaultNewThreadPermission {
		perm = "" // the default; written only when it differs
	}
	agent := config.Agent{Adapter: "bb", Provider: c.Provider, Group: c.Provider,
		BB: &config.BB{NewThread: true, Model: c.Model, Reasoning: c.Reasoning, PermissionMode: perm, CLI: bbCLI}}
	return config.Candidate{ID: "new-conversation-" + c.Provider, Agent: agent, Probe: config.Probe{Kind: kind}}, nil
}

// printNewThreadPlan says exactly what a loop with this fallback does at a
// usage limit, and how to turn it off.
func printNewThreadPlan(w io.Writer, primaryProvider string, c config.Candidate, off string) {
	perm := c.BB.NewThreadPermission()
	model := c.BB.Model
	if c.BB.Reasoning != "" {
		model += ", reasoning " + c.BB.Reasoning
	}
	fmt.Fprintf(w, "On a usage limit on %s: a new conversation on %s (%s, permission %s) is started with a handoff brief and owns the loop from then on; "+
		"this conversation's child lanes move to it; lanes blocked by the limit get successor threads on %s (at most %d per handoff). "+
		"At most %d new conversations over the loop's life, never two within an hour. Each new conversation and each successor is a paid turn and a send against max_sends. "+
		"With permission %s, an unattended new conversation or successor may stop at a permission question and wait for a person. Turn it off: %s.\n",
		primaryProvider, c.Provider, model, perm, c.Provider, config.ChildSuccessorsPerRun, config.DefaultMaxNewThreads, perm, off)
}

// offerCandidate is the fallback an offer would write, for saying what it
// does before anyone answers.
func offerCandidate(o newThreadOffer, permission string) config.Candidate {
	return config.Candidate{Agent: config.Agent{Provider: o.Provider, BB: &config.BB{NewThread: true, Model: o.Model, PermissionMode: permission}}}
}

// initNewThreadOff is how init's person turns the takeover off.
const initNewThreadOff = "answer no, or pass --new-thread-provider none; later, remove the fallback with keepgoing fallbacks remove, or set bb.replace_blocked_children: false to keep only the takeover"

// isNewThreadCandidate reports a candidate that starts a new BB conversation.
func isNewThreadCandidate(c config.Candidate) bool {
	return c.Adapter == "bb" && c.BB != nil && c.BB.NewThread
}

// takeoverReadiness says whether the offered provider can take over now, as
// far as the loop's own records show without a model call: the latest
// recorded probe of a fallback on that provider in the same quota group as
// the new conversation (group), so a check of another account or quota pool
// on that provider is not taken for this one. A usage limit it recorded with
// a reset still ahead means it cannot take over until then. With no record
// it says so, and that the fallback is checked with a probe only when a
// handoff happens.
func takeoverReadiness(cfg *config.Config, provider, group string, now time.Time) string {
	var last *probe.Result
	if cfg != nil && cfg.Loop.StateDir != "" {
		if reg, err := health.Load(cfg.Loop.StateDir); err == nil && reg != nil {
			ids := map[string]bool{"new-conversation-" + provider: true}
			if cfg.FallbackSet != nil {
				for _, c := range cfg.FallbackSet.Candidates {
					if c.Provider == provider && c.Group == group {
						ids[c.ID] = true
					}
				}
			}
			for id, e := range reg.Candidates {
				if ids[id] && e != nil && e.Last != nil && (last == nil || e.Last.At.After(last.At)) {
					last = e.Last
				}
			}
		}
	}
	at := func(t time.Time) string {
		ago := "just now"
		if d := now.Sub(t); d >= time.Minute {
			ago = humanDuration(d) + " ago"
		}
		return t.UTC().Format(time.RFC3339) + ", " + ago
	}
	check := fmt.Sprintf("the last recorded check of a fallback on %s in quota group %s", provider, group)
	switch {
	case last == nil:
		return "Whether " + provider + " can take over now is not known: nothing in this loop's records has checked a fallback on it in quota group " + group + ". A new-conversation fallback is checked with a probe only when a handoff happens; if that probe fails, no conversation is started."
	case !last.OK && last.ResetsAt != nil && last.ResetsAt.After(now):
		return fmt.Sprintf("%s cannot take over now: %s (%s) found a usage limit until %s (in %s). A handoff before then finds no usable route and the loop waits.", provider, check, at(last.At), last.ResetsAt.UTC().Format(time.RFC3339), humanDuration(last.ResetsAt.Sub(now)))
	case !last.OK:
		class := last.ErrorClass
		if class == "" {
			class = "no answer"
		}
		return fmt.Sprintf("%s (%s) failed (%s). It is checked again with a probe when a handoff happens.", upperFirst(check), at(last.At), class)
	}
	return fmt.Sprintf("%s (%s) passed. It is checked again with a probe when a handoff happens.", upperFirst(check), at(last.At))
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
