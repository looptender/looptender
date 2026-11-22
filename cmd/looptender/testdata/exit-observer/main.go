//go:build darwin && cgo

// Owned fixture only. No target PID argument and no signal dispatch/manager API.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/looptender/looptender/internal/legacyretire"
)

func child() int {
	expiry := time.NewTimer(8 * time.Second)
	defer expiry.Stop()
	// Fail on an unexpected termination request. The intended exit is pipe-only.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	commands := make(chan byte, 1)
	go func() {
		var b [1]byte
		for {
			_, e := io.ReadFull(os.Stdin, b[:])
			if e != nil {
				close(commands)
				return
			}
			commands <- b[0]
		}
	}()
	fmt.Println("ready")
	for {
		select {
		case <-expiry.C:
			fmt.Fprintln(os.Stderr, "owned child lifetime elapsed")
			return 1
		case <-signals:
			fmt.Fprintln(os.Stderr, "unexpected signal request")
			return 1
		case b, ok := <-commands:
			if !ok || b == 'e' {
				return 0
			}
			if b != 'a' {
				return 1
			}
			fmt.Println("accepted-alive")
		}
	}
}
func scenario(self string, alive bool) (result error) {
	cmd := exec.Command(self, "child")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
	cmd.Dir = os.Getenv("HOME")
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return e
	}
	cmd.Stderr = os.Stderr // fixed bounded child diagnostics, captured by outer recorder
	if e = cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return e
	}
	// Register exact owned pipe cleanup before any assertion. Never kill/poll or
	// reap until all native observations on this child have ended.
	defer func() {
		in.Close()
		if e := cmd.Wait(); e != nil && result == nil {
			result = e
		}
		out.Close()
	}()
	reader := bufio.NewReaderSize(out, 256)
	line, e := reader.ReadString('\n')
	if e != nil || line != "ready\n" {
		return fmt.Errorf("owned readiness unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	native, e := legacyretire.Inspect(cmd.Process.Pid)
	if e != nil {
		return e
	}
	wrong := native.Token
	wrong[7]++
	if mismatch, err := legacyretire.ArmExitObservation(ctx, cmd.Process.Pid, wrong); err == nil {
		mismatch.Close()
		return fmt.Errorf("wrong incarnation accepted")
	}
	observer, e := legacyretire.ArmExitObservation(ctx, cmd.Process.Pid, native.Token)
	if e != nil {
		return e
	}
	defer observer.Close()
	if _, has := observer.(interface{ Terminate(context.Context) error }); has {
		return fmt.Errorf("signal capability exposed")
	}
	if alive {
		if _, e = in.Write([]byte{'a'}); e != nil {
			return e
		}
		line, e = reader.ReadString('\n')
		if e != nil || line != "accepted-alive\n" {
			return fmt.Errorf("alive acknowledgement unavailable")
		}
		short, cancelShort := context.WithTimeout(context.Background(), 80*time.Millisecond)
		dead, waitErr := observer.WaitExit(short)
		cancelShort()
		if dead || waitErr == nil {
			return fmt.Errorf("acceptance confused with death")
		}
		if dead, waitErr = observer.WaitExit(ctx); dead || waitErr == nil {
			return fmt.Errorf("unknown observation reused")
		}
		// Child remains responsive after observation timeout; no signal was needed.
		if _, e = in.Write([]byte{'a'}); e != nil {
			return e
		}
		line, e = reader.ReadString('\n')
		if e != nil || line != "accepted-alive\n" {
			return fmt.Errorf("child lost after timeout")
		}
	} else {
		if _, e = in.Write([]byte{'e'}); e != nil {
			return e
		}
		if dead, e := observer.WaitExit(ctx); !dead || e != nil {
			return fmt.Errorf("external pipe exit unavailable: %v", e)
		}
	}
	return nil
}
func run() error {
	self, e := os.Executable()
	if e != nil {
		return e
	}
	if e = scenario(self, false); e != nil {
		return e
	}
	fmt.Println("external-pipe-exit: PASS")
	if e = scenario(self, true); e != nil {
		return e
	}
	fmt.Println("accepted-alive-unknown: PASS")
	return nil
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "child" {
		os.Exit(child())
	}
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	// Driver lifetime independent of pipe/native syscall progress; child timers
	// separately bound descendants if this exits before their normal pipe cleanup.
	time.AfterFunc(20*time.Second, func() { fmt.Fprintln(os.Stderr, "driver deadline"); os.Exit(1) })
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
