package bb

import (
	"context"
	"encoding/json"
	"errors"
)

// Moving the direct children of a conversation the loop left to the thread
// keepgoing created for the handoff is the one re-parenting keepgoing does.
// It changes a child's parent and nothing else: no message, retry, stop,
// archive or model change.

// MaxListedChildren bounds the size of a listing that is read at all. It is
// far above what one move uses (state.MaxMovedChildren): archived and hidden
// children are listed too, and a long-lived coordinator gathers many.
const MaxListedChildren = maxListed

// ListedChild is a direct child as `bb thread list --parent-thread` gives it.
type ListedChild struct {
	ID       string
	Archived bool
}

// ChildRun runs one bb command and returns its standard output.
type ChildRun func(ctx context.Context, args ...string) ([]byte, error)

// Children lists and re-parents child threads through run. The supervisor
// uses it through the adapter; transfer apply gives it its own bb runner.
type Children struct{ Run ChildRun }

// ListChildren lists the direct children of parent, archived ones marked.
func (a *Adapter) ListChildren(ctx context.Context, parent string) ([]ListedChild, error) {
	return a.children().ListChildren(ctx, parent)
}

// SetParent makes parent the parent thread of child.
func (a *Adapter) SetParent(ctx context.Context, child, parent string) error {
	return a.children().SetParent(ctx, child, parent)
}

func (a *Adapter) children() Children {
	return Children{Run: func(ctx context.Context, args ...string) ([]byte, error) { return a.run(ctx, "", args...) }}
}

// ListChildren lists the direct children of parent, archived ones marked.
func (c Children) ListChildren(ctx context.Context, parent string) ([]ListedChild, error) {
	if !childIDPattern.MatchString(parent) {
		return nil, errors.New("bb thread list: invalid parent")
	}
	out, err := c.Run(ctx, "thread", "list", "--parent-thread", parent, "--include-hidden", "--json")
	if code, ok := envelopeError(out); ok {
		return nil, errors.New("bb thread list: " + code)
	}
	if err != nil {
		return nil, errors.New("bb thread list: failed")
	}
	var children []struct {
		ID         string `json:"id"`
		ArchivedAt *int64 `json:"archivedAt"`
		DeletedAt  *int64 `json:"deletedAt"`
	}
	// A bare array is required: null and envelopes are not an empty listing.
	if json.Unmarshal(out, &children) != nil || children == nil || len(children) > MaxListedChildren {
		return nil, errors.New("bb thread list: unexpected JSON")
	}
	listed := make([]ListedChild, 0, len(children))
	seen := map[string]bool{}
	for _, c := range children {
		if !childIDPattern.MatchString(c.ID) || c.ID == parent {
			return nil, errors.New("bb thread list: unexpected thread id")
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		listed = append(listed, ListedChild{ID: c.ID, Archived: c.ArchivedAt != nil || c.DeletedAt != nil})
	}
	return listed, nil
}

// SetParent makes parent the parent thread of child.
func (c Children) SetParent(ctx context.Context, child, parent string) error {
	if !childIDPattern.MatchString(child) || !childIDPattern.MatchString(parent) || child == parent {
		return errors.New("bb thread update: invalid thread")
	}
	out, err := c.Run(ctx, "thread", "update", child, "--parent-thread", parent, "--json")
	if code, ok := envelopeError(out); ok {
		return errors.New("bb thread update: " + code)
	}
	if err != nil {
		return errors.New("bb thread update: failed")
	}
	return nil
}
