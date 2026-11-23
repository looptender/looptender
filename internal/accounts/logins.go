package accounts

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
)

// Which login a catalog entry's directory is logged in to, as a keyed
// fingerprint: never the identity itself. Two entries with the same
// fingerprint are the same login; an entry whose fingerprint changed was
// logged in to another account since it was last checked. The key is the
// catalog's own salt, so a fingerprint means nothing outside this catalog.
// Like the quota cache, the record is separate from the catalog: older
// catalog readers never see it.

// LoginsName is the record's file in the catalog directory.
const LoginsName = "account-logins.json"

const loginDefinition = "looptender login fingerprint v1"

// LoginMark is one check's fingerprint.
type LoginMark struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

// LoginEntry is an entry's last fingerprint, and the different one before
// it, if the login changed.
type LoginEntry struct {
	ID       string     `json:"id"`
	Last     LoginMark  `json:"last"`
	Previous *LoginMark `json:"previous,omitempty"`
}

type loginsFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	Entries       []LoginEntry `json:"entries"`
}

// LoginFingerprint keys a provider's account identity with the catalog's
// salt. "" when the catalog has no salt or the identity is empty.
func LoginFingerprint(f File, provider, identity string) string {
	key, err := hex.DecodeString(f.Salt)
	identity = strings.TrimSpace(identity)
	if err != nil || len(key) == 0 || identity == "" {
		return ""
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(loginDefinition + "\n" + provider + "\n" + identity))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// LoadLogins reads the record by entry ID; an absent record is empty.
func LoadLogins(dir string) (map[string]LoginEntry, error) {
	out := map[string]LoginEntry{}
	if checkDir(dir) != nil {
		return out, ErrInvalid
	}
	r, err := snapshot.OpenRecord(filepath.Join(dir, LoginsName))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, ErrInvalid
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil || len(b) > MaxBytes {
		return out, ErrInvalid
	}
	var f loginsFile
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || f.SchemaVersion != 1 {
		return out, ErrInvalid
	}
	for _, e := range f.Entries {
		out[e.ID] = e
	}
	return out, nil
}

// RecordLogin keeps a check's fingerprint for an entry. A fingerprint that
// differs from the last one keeps the last as Previous. Entries no longer in
// the catalog are dropped.
func RecordLogin(dir, id, fingerprint string, at time.Time) error {
	if fingerprint == "" || checkDir(dir) != nil {
		return ErrInvalid
	}
	l, err := lock.AcquireNamed(dir, "accounts")
	if err != nil {
		return err
	}
	defer l.Release()
	f, err := Load(dir)
	if err != nil {
		return err
	}
	have, err := LoadLogins(dir)
	if err != nil {
		have = map[string]LoginEntry{} // a damaged record is replaced
	}
	e := have[id]
	e.ID = id
	if e.Last.Fingerprint != "" && e.Last.Fingerprint != fingerprint {
		prev := e.Last
		e.Previous = &prev
	}
	e.Last = LoginMark{Fingerprint: fingerprint, At: at.UTC()}
	have[id] = e
	out := loginsFile{SchemaVersion: 1, Entries: []LoginEntry{}}
	for _, c := range f.Entries {
		if x, ok := have[c.ID]; ok {
			out.Entries = append(out.Entries, x)
		}
	}
	b, err := json.Marshal(out)
	if err != nil || len(b) > MaxBytes {
		return ErrInvalid
	}
	return control.WritePrivateFile(filepath.Join(dir, LoginsName), append(b, '\n'))
}

// LoginView is what a card says of its login.
type LoginView struct {
	// State is same_as (another entry has the same login), changed (a
	// different login than at the check before), or checked.
	State string `json:"state"`
	Text  string `json:"text"`
	// SameAs are the other entries' labels with the same login.
	SameAs []string `json:"sameAs,omitempty"`
	// CheckedAt is the last check; ChangedFrom the check before it whose
	// login was different.
	CheckedAt   time.Time  `json:"checkedAt"`
	ChangedFrom *time.Time `json:"changedFrom,omitempty"`
}

// AttachLogins says on each card what its login fingerprint shows.
func AttachLogins(v *View, logins map[string]LoginEntry) {
	for i := range v.Cards {
		c := &v.Cards[i]
		e, ok := logins[c.ID]
		if !ok || e.Last.Fingerprint == "" {
			continue
		}
		lv := &LoginView{State: "checked", Text: "Login checked " + e.Last.At.Format("2006-01-02") + "; the same as no other entry.", CheckedAt: e.Last.At}
		for j := range v.Cards {
			o := v.Cards[j]
			if j != i && o.Provider == c.Provider && logins[o.ID].Last.Fingerprint == e.Last.Fingerprint {
				lv.SameAs = append(lv.SameAs, o.Label)
			}
		}
		if len(lv.SameAs) > 0 {
			lv.State, lv.Text = "same_as", "Same login as "+strings.Join(lv.SameAs, ", ")+"."
		}
		if p := e.Previous; p != nil {
			at := p.At
			lv.ChangedFrom = &at
			changed := "A different login than when checked on " + p.At.Format("2006-01-02") + "."
			if lv.State == "same_as" {
				lv.Text = changed + " " + lv.Text
			} else {
				lv.State, lv.Text = "changed", changed
			}
		}
		c.Login = lv
	}
}
