package main

import (
	"fmt"

	"github.com/looptender/looptender/internal/config"
)

// Replacing blocked child lanes is on by default for a new-thread fallback,
// so status, doctor, init and fallbacks add say beforehand what a handoff to
// one would start, and how to turn it off.

// successorPlan is what a handoff to one candidate would start.
type successorPlan struct {
	Candidate  string `json:"candidate"`
	Provider   string `json:"provider"`
	Model      string `json:"model,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"`
	Permission string `json:"permission"`
	PerHandoff int    `json:"perHandoff"`
}

func successorPlanOf(c config.Candidate) (successorPlan, bool) {
	if c.Disabled || c.Adapter != "bb" || c.BB == nil || !c.BB.ReplacesChildren() {
		return successorPlan{}, false
	}
	model, reasoning := c.BB.SuccessorModel()
	perm := c.BB.SuccessorPermission()
	if config.PermissionRank(c.BB.NewThreadPermission()) < config.PermissionRank(perm) {
		perm = c.BB.NewThreadPermission()
	}
	return successorPlan{Candidate: c.ID, Provider: c.Provider, Model: model, Reasoning: reasoning, Permission: perm, PerHandoff: config.ChildSuccessorsPerRun}, true
}

func (p successorPlan) Text() string {
	model := p.Model
	if model == "" {
		model = "the provider's default model"
	}
	return fmt.Sprintf("A handoff to %s starts up to %d successor thread(s) on %s (%s, permission %s) for child lanes it brings over that are blocked on another provider's usage limit; each is a paid turn and a send against max_sends. Set bb.replace_blocked_children: false on that fallback to turn it off.",
		p.Candidate, p.PerHandoff, p.Provider, model, p.Permission)
}

// successorPlans lists the plans for a loop's enabled fallbacks.
func successorPlans(cfg *config.Config) []successorPlan {
	if cfg == nil || cfg.FallbackSet == nil {
		return nil
	}
	var out []successorPlan
	for _, c := range cfg.FallbackSet.Candidates {
		if p, ok := successorPlanOf(c); ok {
			out = append(out, p)
		}
	}
	return out
}
