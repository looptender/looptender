package control

import (
	"errors"
	"regexp"
)

var ErrCompletionRenewal = errors.New("completion renewal evidence or conflicts unavailable")
var renewalAssignmentID = regexp.MustCompile(`^assignment_[0-9a-f]{32}$`)

// CompletionRenewalClass describes historical evidence, never present acceptance.
// The caller must strictly validate wire shape/presence before using this helper.
func CompletionRenewalClass(f File) (string, error) {
	if err := CheckOwner(f, "", false); err != nil {
		return "", ErrCompletionRenewal
	}
	c := f.Completion
	if c == nil {
		return "legacy_unbound", nil
	}
	if c.DeclaredAt.IsZero() || ((c.GoalID == "") != (c.GoalRevision == 0)) || ((c.Assignments == "") != (c.AssignmentRevision == 0)) {
		return "", ErrCompletionRenewal
	}
	if c.Assignments != "" && !renewalAssignmentID.MatchString(c.Assignments) {
		return "", ErrCompletionRenewal
	}
	if c.GoalID == "" {
		return "legacy_unbound", nil
	}
	if !goalID.MatchString(c.GoalID) {
		return "", ErrCompletionRenewal
	}
	return "bound", nil
}

// RenewCompletion projects exactly one flag. It grants no authority, changes no
// historical evidence and does not establish whether a later check can wake.
func RenewCompletion(f File) (File, error) {
	if _, err := CompletionRenewalClass(f); err != nil {
		return f, err
	}
	if !f.Enabled || !f.Completed || f.Paused || f.Stop || f.AwaitingUser || f.EpisodeHold || len(f.BriefHold) != 0 || f.Question != nil || f.StopLanesAt != nil || f.StopLanesDropQueued || f.PendingTransfer != nil || f.SupersededBy != "" {
		return f, ErrCompletionRenewal
	}
	if f.OpenQuestion != nil && f.OpenQuestion.At.IsZero() {
		return f, ErrCompletionRenewal
	}
	f.Completed = false
	return f, nil
}
