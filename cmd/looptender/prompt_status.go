package main

import (
	"fmt"
	"io"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/prompt"
	"github.com/looptender/looptender/internal/state"
)

type promptSendStatus struct {
	Availability          string            `json:"availability"`
	Last                  *state.PromptSend `json:"last,omitempty"`
	CurrentGoalComparison string            `json:"currentGoalComparison"`
	Delivery              string            `json:"delivery"`
	// GoalCopy is the latest full goal copy that returned, which lets
	// prompt_goal_on_change send the short line. Diagnostic only.
	GoalCopy *state.GoalCopy `json:"goalCopy,omitempty"`
}

func describePromptSend(last *state.PromptSend, goal *control.GoalSummary) promptSendStatus {
	r := promptSendStatus{Availability: "unavailable", CurrentGoalComparison: "unavailable", Delivery: "unverified"}
	if last == nil || !last.Valid() {
		return r
	}
	r.Availability, r.Last = "recorded", last
	switch {
	case last.GoalID == "":
		r.CurrentGoalComparison = "not_included"
	case goal == nil:
	case goal.ID != last.GoalID:
		r.CurrentGoalComparison = "different_goal"
	case goal.Revision != last.GoalRevision:
		r.CurrentGoalComparison = "different_revision"
	default:
		r.CurrentGoalComparison = "same_revision"
	}
	return r
}

func printPromptSend(w io.Writer, r promptSendStatus) {
	if r.Last == nil {
		fmt.Fprintln(w, "last automatic prompt: unavailable; this does not prove nothing was sent")
		return
	}
	p := r.Last
	fmt.Fprintf(w, "last recorded automatic prompt: %s to %s (%s, %s); check %s\n", p.Action, p.Target, p.Mode, p.Outcome, when(p.CheckAt))
	if p.GoalID == "" {
		fmt.Fprintln(w, "  goal context: not included")
	} else {
		fmt.Fprintf(w, "  goal context: %s revision %d; current record: %s\n", p.GoalID, p.GoalRevision, r.CurrentGoalComparison)
	}
	if p.Bytes > 0 {
		goal := "no goal"
		switch p.GoalForm {
		case "full":
			goal = "goal in full"
		case "short":
			goal = "goal named as unchanged"
		}
		fmt.Fprintf(w, "  size: %d bytes, %s (bytes measured, tokens not)\n", p.Bytes, goal)
	}
	if g := r.GoalCopy; g != nil {
		fmt.Fprintf(w, "  last full goal copy: %s revision %d to %s; check %s; %d short send(s) since\n", g.GoalID, g.GoalRevision, g.Target, when(g.At), g.ShortSends)
	}
	fmt.Fprintln(w, "  saved after adapter return; delivery and execution unverified; queued messages may retain older revisions")
}

// wakePolicyStatus says where the loop's standing wake policy comes from:
// this build (the prompt file holds a wording setup wrote) or the loop's own
// text, which then receives no policy updates.
type wakePolicyStatus struct {
	prompt.PolicyState
	Message string `json:"message"`
}

const wakePolicyOptIn = "To receive policy updates, run `looptender prompt-policy apply -c CONFIG`: it rewrites the text above the line `Task:` to the current wording, keeps the task, and keeps the previous file beside it."

func describeWakePolicy(l config.Loop) *wakePolicyStatus {
	text, err := l.PromptText()
	if err != nil {
		return nil
	}
	s := &wakePolicyStatus{PolicyState: prompt.DescribePolicy(text, l.PromptFile != "")}
	switch {
	case s.Source == prompt.PolicyInline:
		s.Message = "The prompt is set in the configuration (loop.prompt) and sent exactly as written; it does not receive policy updates."
	case s.Source == prompt.PolicyCustom:
		s.Message = "The prompt file differs from every wording setup wrote, so it is sent exactly as written and does not receive policy updates. " + wakePolicyOptIn
	case s.FileVersion == s.CurrentVersion:
		s.Message = fmt.Sprintf("Built in: the prompt file holds the current wording (version %d), followed by the task.", s.CurrentVersion)
	default:
		s.Message = fmt.Sprintf("Built in: the prompt file holds version %d of the wording; each wake sends this build's version %d followed by the file's task. The file is not rewritten.", s.FileVersion, s.CurrentVersion)
	}
	return s
}

func printWakePolicy(w io.Writer, s *wakePolicyStatus) {
	if s != nil {
		fmt.Fprintf(w, "standing wake policy: %s\n", s.Message)
	}
}
