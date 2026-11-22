//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/looptender/looptender/internal/legacyretire"
)

type fakeRetirementBackend struct {
	supported                                     bool
	reads, prepares, writes, executes, reconciles int
	executeErr                                    error
}

func (f *fakeRetirementBackend) Supported() bool { return f.supported }
func (f *fakeRetirementBackend) ReadSelection(string) (legacyretire.Selection, error) {
	f.reads++
	return legacyretire.Selection{}, nil
}
func (f *fakeRetirementBackend) ReadTicket(string) (legacyretire.Ticket, error) {
	f.reads++
	return legacyretire.Ticket{}, nil
}
func (f *fakeRetirementBackend) WriteTicket(string, legacyretire.Ticket) error {
	f.writes++
	return nil
}
func (f *fakeRetirementBackend) Prepare(ctx context.Context, _ legacyretire.Selection) (legacyretire.Ticket, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	f.prepares++
	return legacyretire.Ticket{}, nil
}
func (f *fakeRetirementBackend) Execute(context.Context, legacyretire.Ticket, legacyretire.Selection) error {
	f.executes++
	return f.executeErr
}
func (f *fakeRetirementBackend) Reconcile(context.Context, legacyretire.Ticket) (string, error) {
	f.reconciles++
	return "consumed-unknown", nil
}
func TestLegacyRetireCommandNeverPreparesOnExecuteOrUnknown(t *testing.T) {
	f := &fakeRetirementBackend{supported: true, executeErr: errors.New("lost reply")}
	var out, errs bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errs}
	code := legacyRetireCommandWith(e, []string{"execute", "--selection", "/fictional/selection", "--ticket", "/fictional/ticket"}, f)
	if code != exitFailed || f.executes != 1 || f.prepares != 0 || f.writes != 0 {
		t.Fatal(code, f)
	}
	code = legacyRetireCommandWith(e, []string{"reconcile", "--ticket", "/fictional/ticket"}, f)
	if code != exitOK || f.reconciles != 1 || f.executes != 1 || f.prepares != 0 {
		t.Fatal(code, f)
	}
}
func TestLegacyRetireCommandRefusesUnsupportedAndUnbounded(t *testing.T) {
	for _, args := range [][]string{{"execute", "--selection", "/fictional/s", "--ticket", "/fictional/t"}, {"prepare", "--selection", "/fictional/s", "--ticket", "/fictional/t", "--timeout", "2m"}, {"execute", "--ticket", "/fictional/t"}} {
		f := &fakeRetirementBackend{}
		var out, errs bytes.Buffer
		code := legacyRetireCommandWith(env{ctx: context.Background(), stdout: &out, stderr: &errs}, args, f)
		if code == exitOK || f.reads+f.prepares+f.writes+f.executes+f.reconciles != 0 {
			t.Fatal(code, f)
		}
	}
}
