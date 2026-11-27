package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

const panelAccountUsagePrefix = "api/v1/account-usage-updates/"
const panelAccountUsageEnable = "enable_usage_updates"
const panelAccountUsageDisable = "disable_usage_updates"

func (c *panelChanges) serveAccountUsageUpdates(w http.ResponseWriter, r *http.Request, rest, session string) {
	id, step, ok := strings.Cut(strings.TrimPrefix(rest, panelAccountUsagePrefix), "/")
	if !ok || !panelAccountID.MatchString(id) || (step != "prepare" && step != "confirm") {
		http.NotFound(w, r)
		return
	}
	req, ok := panelReadAction(http.MaxBytesReader(w, r.Body, panelMaxBody))
	if !ok || (req.Action != panelAccountUsageEnable && req.Action != panelAccountUsageDisable) {
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
		reply = c.prepareAccountUsageUpdates(session, id, req.Action)
	} else {
		reply = c.confirmAccountUsageUpdates(session, id, req.Action, r.Header.Get(panelFormHeader))
	}
	reply.SchemaVersion = 1
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}
func (c *panelChanges) prepareAccountUsageUpdates(session, id, action string) panelChangeReply {
	refuse := func() panelChangeReply {
		return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "This selection is unavailable. Usage update settings were not changed."}}
	}
	dir, err := accountsDirectory(c.reader.accountsDir)
	if err != nil {
		return refuse()
	}
	s, err := accountQuotaCapture(dir, id, c.quotaEnv)
	if action == panelAccountUsageEnable && (err != nil || !panelQuotaProviderSupported(s.Entry.Provider) || accountUsageStamp(s) == "") {
		return refuse()
	}
	value, err := panelRandom(32)
	if err != nil {
		return refuse()
	}
	expires := c.now().UTC().Add(panelHoldFormTTL)
	form := panelForm{session: session, key: id, action: action, expires: expires}
	if action == panelAccountUsageEnable {
		form.quota = &s
	}
	c.keep(value, form)
	title, confirm := "Keep usage updated for "+s.Entry.Label+"?", "Keep usage updated"
	lines := []dockText{txt("Authorizes ongoing model-free usage reads for this exact selection, at least five minutes apart, with bounded failure backoff. No confirmation is needed for each read. No login or ordinary model turn is started."), txt("Keep the macOS menu running, or a non-offline panel/dock process. The menu owns a bounded child while open or closed; it stops on menu exit, sleep or Low Power Mode. Panel polling continues with a hidden tab until process or idle shutdown. No persistent service is installed."), txt("Provider freshness may remain unknown. Selection, environment, executable or directory changes invalidate this authorization. Pause usage updates to revoke it; an already reserved read may finish.")}
	if action == panelAccountUsageDisable {
		title, confirm = "Pause automatic usage updates?", "Pause usage updates"
		lines = []dockText{txt("Revokes automatic usage reads for this selection. Saved evidence retains its original timestamps. An already reserved read may finish; it cannot restore authorization.")}
	}
	return panelChangeReply{Preview: &panelPreview{Action: action, Key: id, Title: title, Confirm: confirm, Form: value, ExpiresAt: expires, Lines: lines, Lost: "The settings reply was lost. Read the saved state before trying again.", Refused: "Usage update settings were not changed."}}
}
func (c *panelChanges) confirmAccountUsageUpdates(session, id, action, value string) panelChangeReply {
	f, ok := c.take(value)
	if !ok || f.session != session || f.key != id || f.action != action || !c.now().Before(f.expires) {
		return panelChangeReply{Result: &panelResult{Outcome: "review_again", Text: "This review expired or changed. Review again.", Retry: true}}
	}
	dir, err := accountsDirectory(c.reader.accountsDir)
	if err == nil {
		err = setAccountUsagePolling(dir, id, action == panelAccountUsageEnable, f.quota, c.quotaEnv, c.now)
	}
	if err != nil {
		return panelChangeReply{Result: &panelResult{Outcome: "refused", Text: "The selection changed or private settings are unavailable. Nothing was enabled."}}
	}
	c.reader.forget()
	text := "Keep usage updated is enabled for this selection. A running menu/panel/dock scheduler checks due work without further confirmation."
	if action == panelAccountUsageDisable {
		text = "Automatic usage updates paused. Saved evidence retains its time."
	}
	return panelChangeReply{Result: &panelResult{Outcome: "done", Text: text}}
}
