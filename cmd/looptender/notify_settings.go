package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"github.com/looptender/looptender/internal/lock"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The notification settings the menu, the panel and (later) BB push share, and
// the menu's heartbeat, which lets the panel say when the menu already
// notifies on this computer. Both are small private files in the panel
// directory; the panel server and the `panel notify` commands are what write
// them, so there is one writer and one set of rules. Nothing here is a loop
// control: it changes what is shown to a person, never what a loop does.

const (
	notifySettingsFile = "notify-settings.json"
	notifyMenuFile     = "menu-notifying.json"
	// notifyMenuFresh is how long a heartbeat counts: the menu reads every
	// minute (five when reads fail) and beats after each.
	notifyMenuFresh = 6 * time.Minute
	notifyPath      = "api/v1/notify"
)

type notifySettings struct {
	Version int `json:"version"`
	// Kinds holds only the kinds somebody chose; the rest follow noticeDefaultOn.
	Kinds     map[string]bool `json:"kinds,omitempty"`
	Browser   bool            `json:"browser,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt,omitempty"`
}

type notifyMenuBeat struct {
	Version   int       `json:"version"`
	At        time.Time `json:"at"`
	Notifying bool      `json:"notifying"`
	// PID is the menu process that wrote it. A menu that is quitting says it is
	// not notifying only if the file is still its own, so a late write from the
	// old menu never overwrites the first beat of the menu that replaced it.
	PID int `json:"pid,omitempty"`
}

// notifyState is what a reader needs: the resolved settings and whether the
// menu is notifying now. LastRunningAt is when the menu last beat, which a
// menu starting up reads to find what began while it was down.
type notifyState struct {
	Kinds   map[string]bool `json:"kinds"`
	Browser bool            `json:"browser"`
	Menu    notifyMenuState `json:"menu"`
}

type notifyMenuState struct {
	Notifying     bool       `json:"notifying"`
	LastRunningAt *time.Time `json:"lastRunningAt,omitempty"`
}

func notifyKindKnown(kind string) bool {
	for _, k := range noticeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func notifyRead(dir, name string, into any) bool {
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && len(b) <= 64<<10 && json.Unmarshal(b, into) == nil
}

func notifyWrite(dir, name string, v any) error {
	if dir == "" {
		return errors.New("no panel directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// readNotifyState resolves the settings (a missing or unreadable file is the
// defaults) and the menu's heartbeat against now.
func readNotifyState(dir string, now time.Time) *notifyState {
	var s notifySettings
	notifyRead(dir, notifySettingsFile, &s)
	st := &notifyState{Kinds: map[string]bool{}, Browser: s.Browser}
	for _, k := range noticeKinds {
		on, chosen := s.Kinds[k]
		if !chosen {
			on = noticeDefaultOn[k]
		}
		st.Kinds[k] = on
	}
	var beat notifyMenuBeat
	if notifyRead(dir, notifyMenuFile, &beat) && !beat.At.IsZero() {
		at := beat.At
		st.Menu.LastRunningAt = &at
		st.Menu.Notifying = beat.Notifying && now.Sub(at) >= 0 && now.Sub(at) < notifyMenuFresh
	}
	return st
}

// setNotifyKind records one kind on or off, or the browser choice.
func setNotify(dir, kind string, browser *bool, on bool, now time.Time) error {
	var s notifySettings
	notifyRead(dir, notifySettingsFile, &s)
	s.Version, s.UpdatedAt = 1, now.UTC()
	if browser != nil {
		s.Browser = *browser
	} else {
		if !notifyKindKnown(kind) {
			return errors.New("unknown kind")
		}
		if s.Kinds == nil {
			s.Kinds = map[string]bool{}
		}
		s.Kinds[kind] = on
	}
	return notifyWrite(dir, notifySettingsFile, s)
}

// notifyBeatLock is held around reading the heartbeat's writer and writing it,
// so a new menu's first beat cannot land between a quitting menu's ownership
// check and its write. It waits briefly for another writer, and gives up (the
// beat is not written, and the caller is told) rather than write unguarded.
func notifyBeatLock(dir string) (*lock.Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var err error
	for i := 0; i < 100; i++ {
		var l *lock.Lock
		if l, err = lock.AcquireNamed(dir, "menu-notifying"); err == nil {
			return l, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, err
}

func notifyBeat(dir string, notifying bool, pid int, now time.Time) error {
	l, err := notifyBeatLock(dir)
	if err != nil {
		return err
	}
	defer l.Release()
	if !notifying && pid > 0 {
		var cur notifyMenuBeat
		if notifyRead(dir, notifyMenuFile, &cur) && cur.PID != 0 && cur.PID != pid {
			return nil // another menu has written since: it is not this one's to retract
		}
	}
	return notifyWrite(dir, notifyMenuFile, notifyMenuBeat{Version: 1, At: now.UTC(), Notifying: notifying, PID: pid})
}

// panelNotifyCommand is `panel notify set|heartbeat`, the menu's way to write
// the shared files (it runs only the looptender commands it is allowed).
func panelNotifyCommand(e env, args []string) int {
	usage := func() int {
		e.errf("panel notify: usage: looptender panel notify set --kind KIND (--on|--off) | heartbeat [--notifying=true|false]")
		return exitUsage
	}
	if len(args) == 0 {
		return usage()
	}
	fs := flag.NewFlagSet("panel notify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	panelDir := fs.String("panel-directory", "", "")
	dir := ""
	switch args[0] {
	case "set":
		kind := fs.String("kind", "", "")
		on, off := fs.Bool("on", false, ""), fs.Bool("off", false, "")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || *on == *off || !notifyKindKnown(*kind) {
			return usage()
		}
		dir = panelClientsDir(*panelDir)
		if setNotify(dir, *kind, nil, *on, time.Now()) != nil {
			e.errf("panel notify: could not save")
			return exitFailed
		}
	case "heartbeat":
		notifying := fs.Bool("notifying", true, "")
		pid := fs.Int("pid", 0, "")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
			return usage()
		}
		dir = panelClientsDir(*panelDir)
		if *pid < 0 || notifyBeat(dir, *notifying, *pid, time.Now()) != nil {
			e.errf("panel notify: could not save")
			return exitFailed
		}
	default:
		return usage()
	}
	return exitOK
}

// ---- the panel server's route: the page saves its choices here. It is not a
// loop change, so it is available on a read-only panel, behind the same guard
// (host, origin, fetch metadata, JSON, the session) and its own anti-forgery
// value.

const notifyContext = "keepgoing-panel-notify\x00"

func notifyFormValue(secret []byte, session string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(notifyContext + session))
	return hex.EncodeToString(m.Sum(nil))
}

type notifyRequest struct {
	Kind    string `json:"kind,omitempty"`
	On      *bool  `json:"on,omitempty"`
	Browser *bool  `json:"browser,omitempty"`
}

func (h *panelHandler) serveNotify(w http.ResponseWriter, r *http.Request, session string) {
	want := notifyFormValue(h.notifySecret, session)
	got := r.Header.Get(panelFormHeader)
	if len(got) != len(want) || !hmac.Equal([]byte(got), []byte(want)) {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	var req notifyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || (req.Browser == nil) == (req.On == nil) || (req.Browser != nil && req.Kind != "") || (req.On != nil && !notifyKindKnown(req.Kind)) {
		http.Error(w, "refused", http.StatusBadRequest)
		return
	}
	dir := h.notifyDir
	on := false
	if req.On != nil {
		on = *req.On
	}
	if err := setNotify(dir, req.Kind, req.Browser, on, h.state.now()); err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(readNotifyState(dir, h.state.now()))
}
