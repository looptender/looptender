package main

import (
	"context"
	"flag"
	"io"
	"os"
	"time"

	"github.com/looptender/looptender/internal/accounts"
)

const accountUsageOwnerHello = "looptender-menu-usage-v1\n"

// Internal foreground child protocol. A running menu owns stdin's write end;
// EOF (including abrupt app death), extra input or cancellation ends the worker.
// No detachment, service registration, implicit grant or output stream is added.
func accountsUsageWorkerCommand(e env, args []string, input io.ReadCloser) int {
	fs := flag.NewFlagSet("accounts usage-worker", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dir := fs.String("directory", "", "private catalog directory")
	owned := fs.Bool("owner-stdin", false, "require the menu lifetime pipe protocol")
	if fs.Parse(args) != nil || fs.NArg() != 0 || !*owned {
		return exitUsage
	}
	path, err := accountsDirectory(*dir)
	if err != nil {
		return exitFailed
	}
	return runAccountUsageOwnedWorker(e.ctx, path, os.Environ(), input, time.Now, accountQuotaRead, nil)
}
func accountUsageHasGrant(dir string, env []string) bool {
	settings, err := accounts.LoadUsagePolling(dir)
	if err != nil {
		return false
	}
	for _, p := range settings.Entries {
		s, err := accountQuotaCapture(dir, p.ID, env)
		if err == nil && panelQuotaProviderSupported(s.Entry.Provider) && accountUsageMatches(p, s) {
			return true
		}
	}
	return false
}
func runAccountUsageOwnedWorker(ctx context.Context, dir string, env []string, input io.ReadCloser, now func() time.Time, run accountQuotaRun, ticks <-chan time.Time) int {
	defer input.Close()
	if !accountUsageHasGrant(dir, env) {
		return exitOK
	}
	owned, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	hello := make(chan bool, 1)
	go func() {
		raw := make([]byte, len(accountUsageOwnerHello))
		_, err := io.ReadFull(input, raw)
		hello <- err == nil && string(raw) == accountUsageOwnerHello
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return exitOK
	case <-timer.C:
		return exitFailed
	case ok := <-hello:
		if !ok {
			return exitFailed
		}
	}
	go func() { var extra [1]byte; _, _ = input.Read(extra[:]); cancel() }()
	accountUsageSchedulerMode(owned, dir, env, now, run, ticks, true)
	return exitOK
}
