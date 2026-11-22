package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/userdir"
)

func registryRun(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	args = append([]string{"registry"}, args...)
	args = append(args, "--directory", dir, "--json")
	code := run(context.Background(), args, &out, &errs)
	return code, out.String(), errs.String()
}
func registryConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keepgoing.yaml")
	// A deliberately nonexistent CLI and prompt file prove discovery does not
	// need a provider installation or read task content.
	body := `version: 1
loop:
  name: example-loop
  state_dir: state
  adapter: bb
  provider: example
  bb: {thread: thr_example, cli: nonexistent-provider-fixture}
  prompt_file: private-missing-task.txt
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestRegistryCLIReferencesDoNotControlLoops(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "registry")
	cfg := registryConfig(t)
	runtime := filepath.Join(filepath.Dir(cfg), "state")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	// Deliberately opaque bytes: discovery must not load or rewrite control.
	control := filepath.Join(runtime, "control.json")
	original := []byte("PRIVATE-HELD-CONTROL")
	os.WriteFile(control, original, 0600)
	code, out, errs := registryRun(t, dir, "add", "-c", cfg)
	var added registryOutput
	if code != 0 || json.Unmarshal([]byte(out), &added) != nil || len(added.Entries) != 1 {
		t.Fatal(code, out, errs)
	}
	id := added.Entries[0].ID
	for _, args := range [][]string{{"list"}, {"add", "-c", cfg}} {
		code, out, errs = registryRun(t, dir, args...)
		if code != 0 || strings.Contains(out+errs, filepath.Dir(cfg)) || strings.Contains(out+errs, "PRIVATE") {
			t.Fatal(code, out, errs)
		}
	}
	code, out, errs = registryRun(t, dir, "list", "--private")
	var full registryOutput
	if code != 0 || json.Unmarshal([]byte(out), &full) != nil || full.Entries[0].ConfigPath == "" || full.Entries[0].StateDir == "" {
		t.Fatal(code, out, errs)
	}
	saved, _ := os.ReadFile(filepath.Join(dir, registry.FileName))
	if strings.Contains(string(saved), "prompt") || strings.Contains(string(saved), "thr_example") {
		t.Fatal("registry copied task/target fields")
	}
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	code, out, errs = registryRun(t, dir, "list")
	if code != 0 || !strings.Contains(out, id) {
		t.Fatal(code, out, errs)
	}
	code, out, errs = registryRun(t, dir, "remove", "--id", id)
	if code != 0 {
		t.Fatal(code, out, errs)
	}
	after, _ := os.ReadFile(control)
	if string(after) != string(original) {
		t.Fatal("registration changed controls")
	}
	files, _ := os.ReadDir(runtime)
	if len(files) != 1 {
		t.Fatal("discovery created runtime files")
	}
}
func TestRegistryCLIRejectsCheckoutLockAndBadInput(t *testing.T) {
	cfg := registryConfig(t)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0700)
	if code, _, _ := registryRun(t, filepath.Join(root, "registry"), "add", "-c", cfg); code == 0 {
		t.Fatal("checkout accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "registry")); !os.IsNotExist(err) {
		t.Fatal("checkout written")
	}
	dir := filepath.Join(t.TempDir(), "registry")
	if code, _, _ := registryRun(t, dir, "list"); code != 0 {
		t.Fatal("missing list failed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("list wrote registry")
	}
	held, err := lock.AcquireNamed(dir, registry.LockName)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := registryRun(t, dir, "add", "-c", cfg)
	held.Release()
	if code != exitSkipped {
		t.Fatal("lock result", code)
	}
	os.WriteFile(cfg, []byte("PRIVATE-BROKEN-CONFIG"), 0600)
	code, out, errs := registryRun(t, dir, "add", "-c", cfg)
	if code == 0 || strings.Contains(out+errs, "PRIVATE") || strings.Contains(out+errs, cfg) {
		t.Fatal("bad config exposed", code, out, errs)
	}
	for _, args := range [][]string{{"remove"}, {"add"}, {"list", "--id", "other"}, {"remove", "--id", "../other"}, {"list", "--unknown"}, {"list", "unexpected"}} {
		if code, _, _ := registryRun(t, dir, args...); code == 0 {
			t.Fatal("invalid args accepted", args)
		}
	}
}

// userConfigHome points the operating system's user configuration directory
// at a private directory the way each platform reads it, and says where the
// default registry then is. Windows reads APPDATA, not USERPROFILE.
func userConfigHome(t *testing.T) (base string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"LOOPTENDER_REGISTRY_DIR", "KEEPGOING_REGISTRY_DIR", "APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(name, "")
	}
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", filepath.Join(root, "roaming"))
		t.Setenv("USERPROFILE", filepath.Join(root, "profile")) // not where the registry goes
		return filepath.Join(root, "roaming")
	case "darwin":
		t.Setenv("HOME", filepath.Join(root, "home"))
		return filepath.Join(root, "home", "Library", "Application Support")
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	return filepath.Join(root, "config")
}

func TestRegistryDefaultDirectoryIsTheUserConfigDirectory(t *testing.T) {
	base := userConfigHome(t)
	cfg := registryConfig(t)
	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{"registry", "add", "-c", cfg, "--json"}, &out, &errs); code != exitOK {
		t.Fatalf("add without --directory: %d %s %s", code, out.String(), errs.String())
	}
	if _, err := os.Stat(filepath.Join(base, userdir.Name, "registry", registry.FileName)); err != nil {
		t.Fatalf("the reference is not under the user configuration directory: %v", err)
	}
}

func TestRegistryAddNamesAnUndefinedUserConfigDirectory(t *testing.T) {
	userConfigHome(t)
	for _, name := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(name, "")
	}
	cfg := registryConfig(t)
	var out, errs bytes.Buffer
	if code := run(context.Background(), []string{"registry", "add", "-c", cfg}, &out, &errs); code != exitFailed || !strings.Contains(errs.String(), "user configuration directory is not defined") || strings.Contains(errs.String(), "outside Git checkouts") {
		t.Fatalf("%d %s", code, errs.String())
	}
}

func TestRegistryPublicationWarningNamesItsCause(t *testing.T) {
	cfg, err := config.Load(registryConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	userConfigHome(t)
	for _, name := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(name, "")
	}
	var out, errs bytes.Buffer
	if publishEnabledLoop(env{context.Background(), &out, &errs}, cfg) {
		t.Fatal("published without a registry directory")
	}
	if !strings.Contains(errs.String(), registryPublicationWarning) || !strings.Contains(errs.String(), "registry: the user configuration directory is not defined") {
		t.Fatalf("warning lost its cause: %q", errs.String())
	}
}
