//go:build darwin

// fictional is an owned-process fixture, never a supervisor or provider adapter.
// It accepts the reviewed daemon-shaped argv only to exercise native argv binding.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/looptender/looptender/internal/daemoninfo"
	"golang.org/x/sys/unix"
)

func run() error {
	if len(os.Args) != 4 || os.Args[1] != "daemon" || os.Args[2] != "-c" {
		return fmt.Errorf("fixture arguments refused")
	}
	root := filepath.Dir(os.Args[3])
	state := filepath.Join(root, "state")
	if os.Getenv("OWNED_RETIRE_FIXTURE_ROOT") != root {
		return fmt.Errorf("fixture root refused")
	}
	mode := os.Getenv("OWNED_RETIRE_FIXTURE_MODE")
	if mode != "graceful" && mode != "ignore" {
		return fmt.Errorf("fixture mode refused")
	}
	// Every child has an independent finite lifetime, even if its driver dies.
	expiry := time.NewTimer(12 * time.Second)
	defer expiry.Stop()
	term := make(chan os.Signal, 4)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)
	fd, e := unix.Open(filepath.Join(state, "daemon.lock"), unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return e
	}
	server, e := daemoninfo.Start(state, "fictional-retirement-fixture", false)
	if e != nil {
		return e
	}
	defer server.Close()
	// Retained stdin ownership is cleanup authority, independent of token signalling.
	inputClosed := make(chan struct{})
	go func() { var b [1]byte; _, _ = os.Stdin.Read(b[:]); close(inputClosed) }()
	if e = json.NewEncoder(os.Stdout).Encode(struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"startedAt"`
	}{os.Getpid(), server.StartedAt().UTC().Format(time.RFC3339Nano)}); e != nil {
		return e
	}
	count := 0
	for {
		select {
		case <-expiry.C:
			return fmt.Errorf("fixture lifetime elapsed")
		case <-inputClosed:
			return nil
		case <-term:
			count++
			// Small durable evidence; no logs, endpoints, credentials or raw tokens printed.
			f, e := os.OpenFile(filepath.Join(root, "signals"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if e != nil {
				return e
			}
			_, e = fmt.Fprintln(f, count)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			if mode == "graceful" {
				return nil
			}
		}
	}
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
