//go:build !darwin && !linux

package main

import (
	"bytes"
	"context"
	"github.com/looptender/looptender/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlFreezeUnsupported(t *testing.T) {
	for _, raw := range [][]byte{{}, []byte(`{"enabled":`), {0, 1, '{'}} {
		dir := t.TempDir()
		path := filepath.Join(dir, "control.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{}
		cfg.Loop.StateDir = dir
		var out, errs bytes.Buffer
		handled, code := pauseDamagedControl(env{context.Background(), &out, &errs}, cfg)
		if !handled || code == 0 || !strings.Contains(errs.String(), "unsupported on this platform") {
			t.Fatalf("unsupported freeze not explicit: %v %d %s", handled, code, errs.String())
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatal("unsupported freeze changed evidence")
		}
	}
}
