//go:build darwin || linux

package legacyretire

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExitObservationNoSignalAndTerminalOutcomes(t *testing.T) {
	for _, kind := range []string{"exit", "pending-timeout", "exec", "invalid", "cancelled", "unbounded", "release-failure"} {
		t.Run(kind, func(t *testing.T) {
			polls, closes := 0, 0
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			if kind == "unbounded" {
				ctx = context.Background()
			}
			o := &exitObservation{poll: func(wait time.Duration) int {
				polls++
				if wait <= 0 || wait > 50*time.Millisecond {
					t.Fatal(wait)
				}
				switch kind {
				case "pending-timeout":
					cancel()
					return 0
				case "exec":
					return 2
				case "invalid":
					return -1
				}
				return 1
			}, release: func() error {
				closes++
				if kind == "release-failure" {
					return errors.New("fictional close failure")
				}
				return nil
			}}
			var observation ExitObservation = o
			if _, has := observation.(interface{ Terminate(context.Context) error }); has {
				t.Fatal("signal capability leaked")
			}
			dead, e := observation.WaitExit(ctx)
			if kind == "exit" {
				if !dead || e != nil {
					t.Fatal(dead, e)
				}
			} else if dead || e == nil {
				t.Fatal("unknown treated as death", dead, e)
			}
			if (kind == "cancelled" || kind == "unbounded") && polls != 0 {
				t.Fatal("polled invalid context")
			}
			before := polls
			ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
			defer cancel2()
			if dead, e = observation.WaitExit(ctx2); dead || e == nil || polls != before {
				t.Fatal("terminal handle reused", dead, e, polls)
			}
			if e = observation.Close(); e != nil || closes != 1 {
				t.Fatal(e, closes)
			}
		})
	}
}
func TestExitObservationCloseBeforeWait(t *testing.T) {
	calls := 0
	o := &exitObservation{poll: func(time.Duration) int { t.Fatal("poll after close"); return 1 }, release: func() error { calls++; return nil }}
	if e := o.Close(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if dead, e := o.WaitExit(ctx); dead || e == nil || calls != 1 {
		t.Fatal(dead, e, calls)
	}
}
