//go:build darwin || linux

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/looptender/looptender/internal/legacyretire"
)

// The production path never injects native evidence. Tests of this command
// parser may provide a backend that has no process/filesystem operations.
type retirementCommandBackend interface {
	Supported() bool
	ReadSelection(string) (legacyretire.Selection, error)
	ReadTicket(string) (legacyretire.Ticket, error)
	WriteTicket(string, legacyretire.Ticket) error
	Prepare(context.Context, legacyretire.Selection) (legacyretire.Ticket, error)
	Execute(context.Context, legacyretire.Ticket, legacyretire.Selection) error
	Reconcile(context.Context, legacyretire.Ticket) (string, error)
}
type localRetirementBackend struct{}

func (localRetirementBackend) Supported() bool { return legacyretire.Supported() }
func (localRetirementBackend) ReadSelection(p string) (legacyretire.Selection, error) {
	return legacyretire.ReadSelection(p)
}
func (localRetirementBackend) ReadTicket(p string) (legacyretire.Ticket, error) {
	return legacyretire.ReadTicket(p)
}
func (localRetirementBackend) WriteTicket(p string, t legacyretire.Ticket) error {
	return legacyretire.WriteTicket(p, t)
}
func (localRetirementBackend) Prepare(ctx context.Context, s legacyretire.Selection) (legacyretire.Ticket, error) {
	return legacyretire.PrepareApplication(ctx, s)
}
func (localRetirementBackend) Execute(ctx context.Context, t legacyretire.Ticket, s legacyretire.Selection) error {
	return legacyretire.RetireApplication(ctx, t, s)
}
func (localRetirementBackend) Reconcile(ctx context.Context, t legacyretire.Ticket) (string, error) {
	return legacyretire.Reconcile(ctx, t)
}
func legacyRetireCommand(e env, args []string) int {
	return legacyRetireCommandWith(e, args, localRetirementBackend{})
}
func legacyRetireCommandWith(e env, args []string, backend retirementCommandBackend) int {
	if len(args) == 0 || (args[0] != "prepare" && args[0] != "execute" && args[0] != "reconcile") {
		e.errf("usage: legacy-retire prepare|execute|reconcile --ticket FILE [--selection FILE] [--timeout 20s]")
		return exitUsage
	}
	fs := flag.NewFlagSet("legacy-retire", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	selectionPath := fs.String("selection", "", "private reviewed manual-invocation selection")
	ticketPath := fs.String("ticket", "", "private retained ticket; prepare requires a new file")
	timeout := fs.Duration("timeout", 20*time.Second, "cooperative operation deadline (1s to 60s)")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *ticketPath == "" || *timeout < time.Second || *timeout > time.Minute {
		return exitUsage
	}
	action := args[0]
	if action == "reconcile" && *selectionPath != "" || action != "reconcile" && *selectionPath == "" {
		return exitUsage
	}
	if !backend.Supported() {
		e.errf("legacy-retire requires the reviewed Darwin native backend; no action taken")
		return exitFailed
	}
	ctx, cancel := context.WithTimeout(e.ctx, *timeout)
	defer cancel()
	fail := func(err error) int {
		e.errf("legacy retirement unavailable: %v; preserve ticket and evidence; no retry or replacement inferred", err)
		return exitFailed
	}
	if action == "reconcile" {
		ticket, err := backend.ReadTicket(*ticketPath)
		if err != nil {
			return fail(err)
		}
		status, err := backend.Reconcile(ctx, ticket)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.stdout, status)
		fmt.Fprintln(e.stdout, "No signal sent; this report does not authorize replacement.")
		return exitOK
	}
	selection, err := backend.ReadSelection(*selectionPath)
	if err != nil {
		return fail(err)
	}
	if action == "prepare" {
		ticket, err := backend.Prepare(ctx, selection)
		if err != nil {
			return fail(err)
		}
		if err = backend.WriteTicket(*ticketPath, ticket); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.stdout, "Prepared one private retirement ticket. No signal sent; review the exact selection and ticket before execute.")
		return exitOK
	}
	ticket, err := backend.ReadTicket(*ticketPath)
	if err != nil {
		return fail(err)
	}
	if err = backend.Execute(ctx, ticket, selection); err != nil {
		return fail(err)
	}
	fmt.Fprintln(e.stdout, "Selected supervisor retirement recorded; controls preserved. No replacement started.")
	return exitOK
}
