package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/lock"
)

const panelAccountCheckPrefix = "api/v1/account-checks/"
const panelAccountCheckAction = "account_check"

func (c *panelChanges) serveAccountCheck(w http.ResponseWriter, r *http.Request, rest, session string) {
	id, step, ok := strings.Cut(strings.TrimPrefix(rest, panelAccountCheckPrefix), "/")
	if !ok || step != "run" || !panelAccountID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	value, want := r.Header.Get(panelFormHeader), c.prepareValue(session)
	if len(value) != len(want) || subtle.ConstantTimeCompare([]byte(value), []byte(want)) != 1 {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	req, ok := panelReadAction(http.MaxBytesReader(w, r.Body, panelMaxBody))
	if !ok || req.Action != panelAccountCheckAction || req.Lanes || req.Immediately || req.DropQueued {
		http.Error(w, "refused", http.StatusBadRequest)
		return
	}
	reply := c.runAccountCheck(id)
	reply.SchemaVersion = 1
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (c *panelChanges) runAccountCheck(id string) panelChangeReply {
	dir, err := accountsDirectory(c.reader.accountsDir)
	if err != nil {
		return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "The account catalog is unavailable."}}
	}
	runner := c.checkRun
	if runner == nil {
		runner = accountExec
	}
	err = accountCheckWithEnv(c.ctx, dir, id, c.caller, runner, func() []string { return c.quotaEnv }, c.now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, lock.ErrHeld):
			return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "Another check or refresh is already running for this selection."}}
		case errors.Is(err, accounts.ErrChanged):
			return panelChangeReply{Result: &panelResult{Outcome: "review_again", Text: "This account selection changed; read the saved state again."}}
		default:
			return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "Account check could not run for this selection."}}
		}
	}
	c.reader.forget()
	f, loadErr := accounts.Load(dir)
	if loadErr != nil {
		return panelChangeReply{Result: &panelResult{Outcome: "done", Text: "Account check completed."}}
	}
	e := accounts.Find(&f, id)
	if e != nil && e.Attempt != nil && e.Attempt.Reason == "context_changed" {
		return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "Selection context differs from this panel. Review the selection before checking again."}}
	}
	return panelChangeReply{Result: &panelResult{Outcome: "done", Text: "Account login check completed."}}
}
