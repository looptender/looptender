package telegram

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
)

const (
	// TokenEnv is the environment variable name holding a personal Telegram bot token.
	TokenEnv = "LOOPTENDER_TELEGRAM_BOT_TOKEN"
	// ChatIDEnv optionally overrides the destination Telegram chat ID for outbound reports.
	ChatIDEnv = "LOOPTENDER_TELEGRAM_CHAT_ID"
	// APIURLEnv optionally overrides the Telegram Bot API origin (for offline stand-ins).
	APIURLEnv = "LOOPTENDER_TELEGRAM_API_URL"

	// TokenFile stores the bot token inside the private Telegram state directory (0600).
	TokenFile = "token.env"

	maxTokenFileBytes = 4096
)

// ErrTokenUnavailable never embeds paths, tokens, or raw OS errors.
var ErrTokenUnavailable = errors.New("telegram: bot token unavailable")

// ErrTokenPermissions is a token file that group or others can read or
// write, or that this user does not own. It is not used until fixed.
var ErrTokenPermissions = errors.New("telegram: the bot token file is readable by others or not owned by this user, so it is not used; run chmod 600 on token.env in the Telegram state directory, or run telegram setup again")

// TokenFromEnvironment reports whether LOOPTENDER_TELEGRAM_BOT_TOKEN is set:
// it then overrides the token file for every command that reads the token.
func TokenFromEnvironment() bool { return strings.TrimSpace(os.Getenv(TokenEnv)) != "" }

// TokenFilePrivate reports whether a token file's mode lets only its owner
// read it and its owner is this user (unix; elsewhere the platform's own
// access control applies and this is true).
func TokenFilePrivate(info os.FileInfo) bool { return privateFile(info) }

// TokenFileState is the saved token file as status and doctor say it:
// missing, private, or not_private (ErrTokenPermissions). Its contents are
// not read.
func TokenFileState(dir string) string {
	if !filepath.IsAbs(dir) {
		return "missing"
	}
	info, err := snapshot.LstatRecord(filepath.Join(dir, TokenFile))
	if err != nil || !info.Mode().IsRegular() {
		return "missing"
	}
	if !TokenFilePrivate(info) {
		return "not_private"
	}
	return "private"
}

// DefaultBotCommands are registered via setMyCommands during setup.
var DefaultBotCommands = []BotCommand{
	{Command: "status", Description: "Show status of registered loops"},
	{Command: "loops", Description: "List registered loops"},
	{Command: "pause", Description: "Pause a loop"},
	{Command: "stop", Description: "Stop a loop"},
	{Command: "resume", Description: "Resume a held loop with a ticket"},
	{Command: "help", Description: "Show available commands"},
}

// SaveToken atomically persists token to token.env with 0600 permissions.
func SaveToken(dir, token string) error {
	if !filepath.IsAbs(dir) {
		return ErrTokenUnavailable
	}
	if err := validateToken(token); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrTokenUnavailable
	}
	l, err := lock.AcquireNamed(dir, "telegram")
	if err != nil {
		return ErrTokenUnavailable
	}
	defer l.Release()

	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return ErrTokenUnavailable
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return ErrTokenUnavailable
	}
	if _, err := f.WriteString(TokenEnv + "=" + token + "\n"); err != nil {
		f.Close()
		return ErrTokenUnavailable
	}
	if err := snapshot.Sync(f); err != nil {
		f.Close()
		return ErrTokenUnavailable
	}
	if err := f.Close(); err != nil {
		return ErrTokenUnavailable
	}
	if err := snapshot.Replace(tmp, filepath.Join(dir, TokenFile)); err != nil {
		return ErrTokenUnavailable
	}
	return nil
}

// LoadToken reads the bot token from LOOPTENDER_TELEGRAM_BOT_TOKEN or token.env in dir.
func LoadToken(dir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(TokenEnv)); v != "" {
		if err := validateToken(v); err != nil {
			return "", err
		}
		return v, nil
	}
	return LoadTokenFile(dir)
}

// LoadTokenFile reads and validates token.env inside dir without consulting the environment.
func LoadTokenFile(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", ErrTokenUnavailable
	}
	path := filepath.Join(dir, TokenFile)
	info, err := snapshot.LstatRecord(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxTokenFileBytes {
		return "", ErrTokenUnavailable
	}
	f, err := snapshot.OpenRecord(path)
	if err != nil {
		return "", ErrTokenUnavailable
	}
	defer f.Close()
	opened, statErr := f.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || opened.Size() > maxTokenFileBytes || !os.SameFile(info, opened) {
		return "", ErrTokenUnavailable
	}
	// Checked at every read: a token file another user could read, or one
	// this user does not own, is not used.
	if !TokenFilePrivate(opened) {
		return "", ErrTokenPermissions
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTokenFileBytes+1))
	if err != nil || len(b) > maxTokenFileBytes {
		return "", ErrTokenUnavailable
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == TokenEnv {
			token := strings.Trim(strings.TrimSpace(val), `"'`)
			if err := validateToken(token); err != nil {
				return "", err
			}
			return token, nil
		}
	}
	return "", ErrTokenUnavailable
}

// HasTokenFile reports whether token.env exists as a regular file inside dir.
func HasTokenFile(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	info, err := snapshot.LstatRecord(filepath.Join(dir, TokenFile))
	return err == nil && info.Mode().IsRegular()
}

// RemoveToken deletes token.env and its snapshot marker under the lock.
func RemoveToken(dir string) error {
	if !filepath.IsAbs(dir) {
		return ErrTokenUnavailable
	}
	l, err := lock.AcquireNamed(dir, "telegram")
	if err != nil {
		return ErrTokenUnavailable
	}
	defer l.Release()
	if err := snapshot.Remove(filepath.Join(dir, TokenFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrTokenUnavailable
	}
	return nil
}
