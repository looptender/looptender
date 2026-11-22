//go:build darwin || linux

package legacyretire

import (
	"context"
	"golang.org/x/sys/unix"
)

// Reconcile reports retained operation evidence only, never dispatches, resets
// or reconstructs anything. Recorded completion is NOT current process absence
// or authority to launch: another invocation may now exist. Unknown cannot be
// upgraded using PID absence, endpoint disappearance or daemon-lock availability.
func Reconcile(ctx context.Context, t Ticket) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	dir, id, e := openRoot(t.Root)
	if e != nil {
		return "", e
	}
	defer unix.Close(dir)
	if id != t.Directory {
		return "", ErrUnavailable
	}
	fd, lid, e := openRecord(dir, "reservation", unix.O_RDWR)
	if e != nil {
		return "", e
	}
	defer unix.Close(fd)
	if lid != t.Lock || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return "", ErrUnavailable
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	b, e := load(dir, t)
	if e != nil {
		return "", e
	}
	if t.Anchor.Inode != 0 {
		if e = validateApplicationTicket(t, b); e != nil {
			return "", e
		}
	}
	lockRaw, e := read(dir, "reservation", t.Lock)
	if e != nil || string(lockRaw) != t.Digest {
		return "", ErrUnavailable
	}
	report := func(status string) (string, error) {
		current, cid, e := openRoot(t.Root)
		if e != nil {
			return "", e
		}
		unix.Close(current)
		if cid != t.Directory || !sameName(dir, "reservation", t.Lock) || !sameName(dir, "intent", t.Intent) {
			return "", ErrUnavailable
		}
		raw, e := read(dir, "reservation", t.Lock)
		if e != nil || string(raw) != t.Digest {
			return "", ErrUnavailable
		}
		if t.Anchor.Inode != 0 {
			if e = validateApplicationTicket(t, b); e != nil {
				return "", e
			}
		}
		return status, nil
	}
	if absent(dir, "spent") {
		raw, e := read(dir, "ready", t.Ready)
		if e != nil || string(raw) != t.Digest || !absent(dir, "completed") {
			return "", ErrUnavailable
		}
		return report("prepared-no-dispatch-recorded")
	}
	// Partial consumption can leave two hard links; do not normalize or repair.
	// A valid completed record can only have been written after observed death.
	if !absent(dir, "ready") {
		return report("consumed-unknown")
	}
	raw, e := read(dir, "spent", t.Ready)
	if e != nil || string(raw) != t.Digest {
		return "", ErrUnavailable
	}
	c, cid, e := openRecord(dir, "completed", unix.O_RDONLY)
	if e != nil {
		if absent(dir, "completed") {
			return report("consumed-unknown")
		}
		return "", ErrUnavailable
	}
	unix.Close(c)
	raw, e = read(dir, "completed", cid)
	if e != nil || string(raw) != t.Digest {
		return "", ErrUnavailable
	}
	return report("recorded-completion-not-launch-authority")
}
