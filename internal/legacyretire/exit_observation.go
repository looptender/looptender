//go:build darwin || linux

package legacyretire

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ExitObservation observes one prearmed process incarnation. It has no method
// that requests termination. A token proves incarnation, not application/manager
// ownership. The caller must establish that separately before any external action.
// The handle is in-memory only: losing it cannot be repaired by PID polling.
type ExitObservation interface {
	WaitExit(context.Context) (bool, error)
	Close() error
}

// No sender callback or nativeObserver is stored here. Even a dynamic assertion
// cannot recover the manual retirement Observer's Terminate capability.
type exitObservation struct {
	mu       sync.Mutex
	poll     func(time.Duration) int // 0 pending, 1 exact exit, other invalid/exec
	release  func() error
	terminal bool
}

func (o *exitObservation) closeLocked() error {
	if o.terminal {
		return nil
	}
	o.terminal = true
	return o.release()
}
func (o *exitObservation) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closeLocked()
}

// WaitExit is single-use, including timeout/cancellation/error. No-event is not
// death. Exec invalidates application identity, even if exit is also reported.
// Concurrent Close waits for this bounded wait; calls never reuse a closed fd.
func (o *exitObservation) WaitExit(ctx context.Context) (dead bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal {
		return false, ErrUnavailable
	}
	defer func() {
		if e := o.closeLocked(); e != nil {
			dead = false
			err = errors.Join(err, e)
		}
	}()
	deadline, ok := ctx.Deadline()
	if !ok {
		return false, ErrUnavailable
	}
	for {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return false, context.DeadlineExceeded
		}
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		switch o.poll(wait) {
		case 0:
			continue
		case 1:
			if e := ctx.Err(); e != nil {
				return false, e
			}
			return true, nil
		default:
			return false, ErrUnavailable
		}
	}
}
