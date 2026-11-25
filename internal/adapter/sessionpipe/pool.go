package sessionpipe

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// MaxPoolStatusBytes caps the JSON accepted from `sessionpipe pool --json`.
const MaxPoolStatusBytes = 1 << 20

// MaxSettingsBytes caps the settings file bytes read to check whether a
// Claude Code directory points at sessionpipe's pool.
const MaxSettingsBytes = 1 << 20

// maxPoolResetHorizon discards reset timestamps far in the future (clock jump
// or malformed payload).
const maxPoolResetHorizon = 30 * 24 * time.Hour

// PoolHealth classifies what `sessionpipe pool --json` reports without exposing
// account identifiers, emails, paths or secrets.
type PoolHealth string

const (
	PoolOff       PoolHealth = "off"
	PoolDown      PoolHealth = "down"
	PoolEmpty     PoolHealth = "empty"
	PoolExhausted PoolHealth = "exhausted"
	PoolDegraded  PoolHealth = "degraded"
	PoolReady     PoolHealth = "ready"
)

// ErrPoolStatus reports unreadable or unrecognized `sessionpipe pool --json` output.
var ErrPoolStatus = errors.New("unrecognized sessionpipe pool status")

// ErrSettingsJSON reports unreadable or malformed Claude settings JSON.
var ErrSettingsJSON = errors.New("unrecognized settings JSON")

// PoolSummary is the redacted, read-only view of sessionpipe's pool. It retains
// counts, listening state, port and sanitized reset timing, and intentionally
// omits account labels, emails, UUIDs, file paths and credentials.
type PoolSummary struct {
	Health           PoolHealth `json:"health"`
	On               bool       `json:"on"`
	Listening        bool       `json:"listening"`
	Port             int        `json:"port,omitempty"`
	InFlight         int        `json:"inFlight,omitempty"`
	TotalAccounts    int        `json:"totalAccounts"`
	EnabledAccounts  int        `json:"enabledAccounts"`
	ReadyAccounts    int        `json:"readyAccounts"`
	LimitedAccounts  int        `json:"limitedAccounts"`
	ErrorAccounts    int        `json:"errorAccounts"`
	DisabledAccounts int        `json:"disabledAccounts"`
	HasCurrent       bool       `json:"hasCurrent,omitempty"`
	EarliestReset    *time.Time `json:"earliestReset,omitempty"`
}

// ParsePoolStatus parses `sessionpipe pool --json` under size and schema guards
// and produces a privacy-redacted summary.
func ParsePoolStatus(raw []byte, now time.Time) (PoolSummary, error) {
	if len(raw) == 0 || len(raw) > MaxPoolStatusBytes {
		return PoolSummary{}, ErrPoolStatus
	}
	var payload struct {
		On        *bool `json:"on"`
		Port      *int  `json:"port"`
		Listening *bool `json:"listening"`
		InFlight  int   `json:"in_flight"`
		Accounts  []struct {
			Enabled *bool      `json:"enabled"`
			Status  string     `json:"status"`
			Current bool       `json:"current"`
			Until   *time.Time `json:"until"`
			Windows []struct {
				ResetsAt *time.Time `json:"resets_at"`
			} `json:"windows"`
		} `json:"accounts"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&payload); err != nil || payload.On == nil {
		return PoolSummary{}, ErrPoolStatus
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return PoolSummary{}, ErrPoolStatus
	}

	out := PoolSummary{
		On: *payload.On,
	}
	if payload.Listening != nil {
		out.Listening = *payload.Listening
	}
	if payload.Port != nil && *payload.Port >= 1 && *payload.Port <= 65535 {
		out.Port = *payload.Port
	}
	if payload.InFlight > 0 {
		out.InFlight = payload.InFlight
	}

	var earliest time.Time
	horizon := now.Add(maxPoolResetHorizon)
	considerReset := func(t *time.Time) {
		if t == nil || t.IsZero() || !t.After(now) || t.After(horizon) {
			return
		}
		utc := t.UTC()
		if earliest.IsZero() || utc.Before(earliest) {
			earliest = utc
		}
	}

	for _, acct := range payload.Accounts {
		out.TotalAccounts++
		enabled := acct.Enabled == nil || *acct.Enabled
		if !enabled || acct.Status == "disabled" {
			out.DisabledAccounts++
			continue
		}
		out.EnabledAccounts++
		if acct.Current {
			out.HasCurrent = true
		}
		switch acct.Status {
		case "ready":
			out.ReadyAccounts++
		case "limited":
			out.LimitedAccounts++
			considerReset(acct.Until)
			for _, w := range acct.Windows {
				considerReset(w.ResetsAt)
			}
		case "error":
			out.ErrorAccounts++
		default:
			out.ErrorAccounts++
		}
	}
	if !earliest.IsZero() {
		out.EarliestReset = &earliest
	}

	switch {
	case !out.On:
		out.Health = PoolOff
	case !out.Listening:
		out.Health = PoolDown
	case out.EnabledAccounts == 0:
		out.Health = PoolEmpty
	case out.ReadyAccounts > 0:
		out.Health = PoolReady
	case out.LimitedAccounts > 0:
		out.Health = PoolExhausted
	default:
		out.Health = PoolDegraded
	}
	return out, nil
}

// SettingsUsesPoolPort inspects Claude settings JSON read-only and reports
// whether env.ANTHROPIC_BASE_URL points at loopback on port. Credentials and
// all other environment values are ignored and never retained.
func SettingsUsesPoolPort(raw []byte, port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, nil
	}
	if len(raw) == 0 || len(raw) > MaxSettingsBytes {
		return false, ErrSettingsJSON
	}
	var root map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&root); err != nil || root == nil {
		return false, ErrSettingsJSON
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, ErrSettingsJSON
	}
	envRaw, ok := root["env"]
	if !ok || bytes.Equal(bytes.TrimSpace(envRaw), []byte("null")) {
		return false, nil
	}
	var envMap map[string]json.RawMessage
	if err := json.Unmarshal(envRaw, &envMap); err != nil || envMap == nil {
		return false, ErrSettingsJSON
	}
	urlRaw, ok := envMap["ANTHROPIC_BASE_URL"]
	if !ok {
		return false, nil
	}
	var base string
	if err := json.Unmarshal(urlRaw, &base); err != nil {
		return false, nil
	}
	return isLoopbackPortURL(base, port), nil
}

// SettingsFileUsesPoolPort checks path read-only without blocking on non-regular
// files or reading beyond MaxSettingsBytes. A missing file reports (false, nil).
func SettingsFileUsesPoolPort(path string, port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, ErrNotRegular
	}
	if info.Size() > MaxSettingsBytes {
		return false, ErrSettingsJSON
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxSettingsBytes+1))
	if err != nil {
		return false, err
	}
	if len(raw) > MaxSettingsBytes {
		return false, ErrSettingsJSON
	}
	return SettingsUsesPoolPort(raw, port)
}

func isLoopbackPortURL(raw string, port int) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	return u.Port() == strconv.Itoa(port)
}
