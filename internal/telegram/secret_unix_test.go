//go:build unix

package telegram

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A token file group or others can read is refused at every read, and said
// so; fixing its mode makes it usable again.
func TestTokenFileMustBePrivateAtEveryRead(t *testing.T) {
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	token := "123456789:AAexampleTokenOnlyForOfflineTests12"
	if err := SaveToken(dir, token); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadTokenFile(dir); err != nil || got != token || TokenFileState(dir) != "private" {
		t.Fatalf("private file: %q %v %s", got, err, TokenFileState(dir))
	}
	path := filepath.Join(dir, TokenFile)
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTokenFile(dir); !errors.Is(err, ErrTokenPermissions) || TokenFileState(dir) != "not_private" {
			t.Fatalf("mode %o: %v %s", mode, err, TokenFileState(dir))
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokenFile(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TokenEnv, "987654321:AAexampleOverrideForOfflineTests12")
	if !TokenFromEnvironment() {
		t.Fatal("environment override not reported")
	}
}
