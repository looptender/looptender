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

const panelAccountRemovePrefix = "api/v1/account-remove/"
const panelAccountRemoveAction = "remove_account"

type panelAccountRemoveReview struct {
	id, label, provider, revision string
	references                    []string
	referencesComplete            bool
}

func (c *panelChanges) serveAccountRemove(w http.ResponseWriter, r *http.Request, rest, session string) {
	id, step, ok := strings.Cut(strings.TrimPrefix(rest, panelAccountRemovePrefix), "/")
	if !ok || !panelAccountID.MatchString(id) || (step != "prepare" && step != "confirm") {
		http.NotFound(w, r)
		return
	}
	req, ok := panelReadAction(http.MaxBytesReader(w, r.Body, panelMaxBody))
	if !ok || req.Action != panelAccountRemoveAction || req.Lanes || req.Immediately || req.DropQueued {
		http.Error(w, "refused", 400)
		return
	}
	var reply panelChangeReply
	if step == "prepare" {
		value, want := r.Header.Get(panelFormHeader), c.prepareValue(session)
		if len(value) != len(want) || subtle.ConstantTimeCompare([]byte(value), []byte(want)) != 1 {
			http.Error(w, "refused", 403)
			return
		}
		reply = c.prepareAccountRemove(session, id)
	} else {
		reply = c.confirmAccountRemove(session, id, r.Header.Get(panelFormHeader))
	}
	reply.SchemaVersion = 1
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func accountProviderLabel(provider string) string {
	switch provider {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "antigravity":
		return "Antigravity"
	default:
		return provider
	}
}

func (c *panelChanges) prepareAccountRemove(session, id string) panelChangeReply {
	refused := func(msg string) panelChangeReply {
		return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: msg}}
	}
	dir, err := accountsDirectory(c.reader.accountsDir)
	if err != nil {
		return refused("The private account catalog is unavailable. Nothing was removed.")
	}
	f, err := accounts.Load(dir)
	if err != nil {
		return refused("The private account catalog is unavailable. Nothing was removed.")
	}
	e := accounts.Find(&f, id)
	if e == nil {
		return refused("This account is no longer in the catalog. Nothing was removed.")
	}
	var refs []string
	refsComplete := false
	if regDir, rerr := registryDirectory(c.registry); rerr == nil {
		refs, refsComplete = accountUsesIn(regDir, dir, id)
	}
	value, err := panelRandom(32)
	if err != nil {
		return refused("A removal review could not be prepared. Nothing was removed.")
	}
	expires := c.now().UTC().Add(panelHoldFormTTL)
	c.keep(value, panelForm{
		session: session,
		key:     id,
		action:  panelAccountRemoveAction,
		expires: expires,
		remove: &panelAccountRemoveReview{
			id:                 e.ID,
			label:              e.Label,
			provider:           e.Provider,
			revision:           e.Revision,
			references:         refs,
			referencesComplete: refsComplete,
		},
	})
	lines := []dockText{
		txt("Removes only this entry from the private account catalog (accounts.json): " + accountProviderLabel(e.Provider) + " · " + e.Label + "."),
		txt("Provider credentials, sign-ins, config directories, and files on disk are untouched."),
	}
	switch {
	case len(refs) > 0:
		lines = append(lines, txt("Warning: registered loop routes still reference this account ("+strings.Join(refs, "; ")+"). Those loop configurations are not edited; update them or run looptender doctor afterward."))
	case !refsComplete:
		lines = append(lines, txt("Warning: not every registered loop could be read to verify route references. No loop configuration is edited."))
	default:
		lines = append(lines, txt("No registered loop on this device references this account."))
	}
	return panelChangeReply{Preview: &panelPreview{
		Action:    panelAccountRemoveAction,
		Key:       id,
		Title:     "Remove " + e.Label + " from catalog?",
		Confirm:   "Remove from catalog",
		Form:      value,
		ExpiresAt: expires,
		Lines:     lines,
		Lost:      "The account removal reply was lost. Read the saved state before reviewing again; nothing is retried automatically.",
		Refused:   "Account removal was refused before confirmation. Nothing was removed.",
	}}
}

func (c *panelChanges) confirmAccountRemove(session, id, value string) panelChangeReply {
	f, ok := c.take(value)
	if c.ctx.Err() != nil || !ok || f.session != session || f.key != id || f.action != panelAccountRemoveAction || f.remove == nil || !c.now().Before(f.expires) {
		return panelChangeReply{Result: &panelResult{Outcome: "review_again", Text: "This account removal review expired, changed, or was already used. Review again.", Retry: true}}
	}
	result := panelResult{Outcome: "refused", Text: "The account catalog changed since this review was prepared. Read the saved state and review again. Nothing was removed.", Retry: true}
	dir, err := accountsDirectory(c.reader.accountsDir)
	if err != nil {
		return panelChangeReply{Result: &result}
	}
	guard, err := accounts.CheckGuard(dir, id)
	if err != nil {
		if errors.Is(err, lock.ErrHeld) {
			result.Text = "A check or quota refresh is running for this account. Wait, then review again."
		}
		return panelChangeReply{Result: &result}
	}
	defer guard.Release()
	removed, err := accounts.Remove(dir, id, f.remove.revision)
	c.reader.forget()
	if err != nil {
		if errors.Is(err, lock.ErrHeld) {
			result.Text = "A check or quota refresh is running for this account. Wait, then review again."
		}
		return panelChangeReply{Result: &result}
	}
	msg := "Removed " + removed.Label + " from the account catalog. Provider logins and files on disk were left untouched."
	if len(f.remove.references) > 0 {
		msg += " Existing loop routes referencing this account were not edited."
	}
	return panelChangeReply{Result: &panelResult{Outcome: "done", Text: msg}}
}
