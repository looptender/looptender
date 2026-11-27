package control

import "time"

// Completion retains the latest explicit declaration independently of the hold.
// References remain in the immutable goal/assignment revisions, never copied
// into ordinary diagnostics. This is no verifier result or human acceptance.
type Completion struct {
	GoalID             string    `json:"goal_id,omitempty"`
	GoalRevision       uint64    `json:"goal_revision,omitempty"`
	DeclaredAt         time.Time `json:"declared_at"`
	Assignments        string    `json:"assignments,omitempty"`
	AssignmentRevision uint64    `json:"assignment_revision,omitempty"`
}

// RecordCompletion requires control.lock, which also serializes goal mutations.
// A missing goal preserves legacy completion; an invalid one cannot be bound.
func RecordCompletion(dir, loop string, at time.Time) (*Completion, error) {
	g, err := LoadGoal(dir)
	if err != nil {
		return nil, err
	}
	c := &Completion{DeclaredAt: at}
	if g != nil {
		if g.OwningLoop != loop {
			return nil, ErrGoal
		}
		c.GoalID, c.GoalRevision = g.ID, g.Summary().Revision
	}
	return c, nil
}

// CompareGoal compares identities only; it never infers acceptance from text.
func (c *Completion) CompareGoal(g *GoalSummary) string {
	if c == nil || c.GoalID == "" || c.GoalRevision == 0 {
		return "unbound"
	}
	if g == nil {
		return "unavailable"
	}
	if c.GoalID != g.ID {
		return "different_goal"
	}
	if c.GoalRevision != g.Revision {
		return "different_revision"
	}
	return "same_revision"
}
