package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBackgroundLaunchPerPlatform(t *testing.T) {
	found := func(name string) (string, error) { return "/example/bin/" + name, nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	child := []string{"/example/looptender", "__start-background", "-c", "/example/looptender.yaml"}
	p, ok := backgroundLaunch("darwin", found, "dev.looptender.loop.example", child)
	if !ok || p.Kind != "launchd" || !reflect.DeepEqual(p.Argv, append([]string{"/example/bin/launchctl", "submit", "-l", "dev.looptender.loop.example", "--",
		"/bin/sh", "-c", launchWrapper, "looptender-launch", "/example/bin/launchctl", "dev.looptender.loop.example"}, child...)) {
		t.Fatalf("%+v", p)
	}
	p, ok = backgroundLaunch("linux", found, "looptender-example", child)
	if !ok || p.Kind != "systemd" || !reflect.DeepEqual(p.Argv, append([]string{"/example/bin/systemd-run", "--user", "--unit", "looptender-example", "--collect", "--quiet", "--"}, child...)) {
		t.Fatalf("%+v", p)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if _, ok := backgroundLaunch(goos, missing, "x", child); ok {
			t.Fatalf("%s: launched without its service manager", goos)
		}
	}
	if _, ok := backgroundLaunch("windows", found, "x", child); ok {
		t.Fatal("windows has no service-manager launch")
	}
	a, b := launchLabel("darwin", "/example/state"), launchLabel("darwin", "/example/state")
	if a == b || !strings.HasPrefix(a, "dev.looptender.loop.") || !strings.HasPrefix(launchLabel("linux", "/example/state"), "looptender-") {
		t.Fatalf("labels %q %q", a, b)
	}
}

// The launched child takes its environment from the file the starting
// command wrote, removes it, and keeps its job's label; started again
// without it (a service manager restarting the job), it does not run.
func TestLaunchedChildTakesItsEnvironmentOnce(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no launchctl: removing the job is a no-op here
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.json")
	if err := os.WriteFile(envFile, []byte(`["PATH=`+os.Getenv("PATH")+`","EXAMPLE_LAUNCH_VAR=kept"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(launchLabelEnv, "")
	var applied []string
	saved := applyLaunchEnv
	applyLaunchEnv = func(v []string) { applied = v }
	t.Cleanup(func() { applyLaunchEnv = saved })
	rest, ack, err := launchedChildArgs([]string{"--launch-label", "dev.looptender.loop.example", "--env-file", envFile, "--ack-file", filepath.Join(dir, "ack.json"), "-c", "/example/looptender.yaml"})
	if err != nil || ack != filepath.Join(dir, "ack.json") || !reflect.DeepEqual(rest, []string{"-c", "/example/looptender.yaml"}) {
		t.Fatalf("%v %q %v", rest, ack, err)
	}
	if len(applied) != 2 || applied[1] != "EXAMPLE_LAUNCH_VAR=kept" || os.Getenv(launchLabelEnv) != "dev.looptender.loop.example" {
		t.Fatal("environment not taken from the file")
	}
	if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("environment file left behind")
	}
	if _, _, err := launchedChildArgs([]string{"--launch-label", "x", "--env-file", envFile, "-c", "y"}); err == nil {
		t.Fatal("ran again without its environment")
	}
	if rest, ack, err := launchedChildArgs([]string{"-c", "y"}); err != nil || ack != "" || len(rest) != 2 {
		t.Fatalf("an ordinary child start changed: %v %q %v", rest, ack, err)
	}
}

// A launched daemon gets only what it needs of the starting command's
// environment: never a token or other secret, of the thread or of
// looptender's own reporters.
func TestLaunchEnvironmentCarriesNoSecret(t *testing.T) {
	got := launchEnvironment([]string{"PATH=/example/bin", "HOME=/example/home", "LANG=C.UTF-8", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/example/claude",
		"LOOPTENDER_SESSION_DIR=/example/sessions", "LOOPTENDER_NTFY_TOKEN=fictional-secret", "LOOPTENDER_WEBHOOK_SECRET=fictional-secret",
		"LOOPTENDER_TELEGRAM_BOT_TOKEN=fictional-secret", "EXAMPLE_API_TOKEN=fictional-secret", "GITHUB_TOKEN=fictional-secret",
		"BB_THREAD_ID=thr_example_launch", "BB_SESSION=fictional-secret", "AWS_SECRET_ACCESS_KEY=fictional-secret", "EDITOR=vi"})
	want := []string{"PATH=/example/bin", "HOME=/example/home", "LANG=C.UTF-8", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/example/claude", "LOOPTENDER_SESSION_DIR=/example/sessions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
