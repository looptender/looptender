package main

import "github.com/looptender/looptender/internal/telegram"

// This is local setup evidence, not a token validation, delivery test, or
// poller health check. Never load the token or expose pairing identities.
func doctorTelegram(r *doctorReport, selectedDirectory string) {
	dir, err := resolveTelegramDir(selectedDirectory)
	if err != nil {
		r.add("telegram.pairing", "unavailable", "The local Telegram pairing directory could not be resolved.", "Check the selected Telegram directory and user directory settings.")
		return
	}
	_, paired, err := telegram.LoadPairing(dir)
	if err != nil {
		r.add("telegram.pairing", "unavailable", "Saved Telegram pairing could not be read or validated.", "Inspect private Telegram state; no pairing or token was changed.")
	} else if !paired {
		r.add("telegram.pairing", "warning", "No saved Telegram pairing was found in the selected directory.", "Use telegram setup when you intend to pair this device.")
	} else {
		r.add("telegram.pairing", "ok", "Saved Telegram pairing validates locally; remote authorization is unverified.", "")
	}
	if telegram.TokenFileState(dir) == "not_private" {
		r.add("telegram.token_file", "error", "The saved Telegram token file can be read by others or is not owned by this user, so it is not used.", "Run chmod 600 on token.env in the Telegram state directory, or telegram setup again.")
	} else if telegram.HasTokenFile(dir) {
		r.add("telegram.token_file", "ok", "A regular saved Telegram token file is present; its contents and validity were not checked.", "")
	} else {
		r.add("telegram.token_file", "warning", "A regular saved Telegram token file is not available; environment credentials were not inspected.", "Check your intended token source. Do not paste tokens into diagnostic reports.")
	}
	if telegram.TokenFromEnvironment() {
		r.add("telegram.token_env", "warning", telegram.TokenEnv+" is set in this environment and overrides the saved token file.", "Unset it unless you mean every command run here to use that bot.")
	}
	doctorTelegramPoller(r, dir)
	r.add("telegram.delivery", "unavailable", "Telegram delivery and polling health were not checked; no network request was made.", "Use telegram test only when you intend to send a test alert. Polling must be run separately for incoming controls.")
}

// doctorTelegramPoller says whether a Telegram poller runs, which Resume,
// Pause and Stop from the phone need.
func doctorTelegramPoller(r *doctorReport, dir string) {
	if running, err := telegramPollerRunning(dir); err == nil && running {
		r.add("telegram.poller", "ok", "A Telegram poller is running, so Resume, Pause and Stop from the phone are read.", "")
		return
	}
	r.add("telegram.poller", "warning", "No Telegram poller is running: taps on the phone do nothing until one runs.", "Run looptender telegram service install once (yourself, in Terminal), or looptender telegram poll in a terminal.")
}
