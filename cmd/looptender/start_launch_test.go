package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
)

func TestBackgroundLaunchPerPlatform(t *testing.T) {
	found := func(name string) (string, error) { return "/example/bin/" + name, nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	child := []string{"/example/looptender", "__start-background", "-c", "/example/looptender.yaml"}
	p, ok := backgroundLaunch("darwin", found, "dev.looptender.loop.example", child, 501, "/example/state")
	plist, script := "/example/state/launch-dev.looptender.loop.example.plist", "/example/state/launch-dev.looptender.loop.example.sh"
	if !ok || p.Kind != "launchd" || p.Target != "user/501/dev.looptender.loop.example" ||
		!reflect.DeepEqual(p.Argv, []string{"/example/bin/launchctl", "bootstrap", "user/501", plist}) || len(p.Files) != 2 {
		t.Fatalf("%+v", p)
	}
	// The background domain, at load, not kept alive, running the script.
	for _, want := range []string{"<key>Label</key><string>dev.looptender.loop.example</string>", "<key>LimitLoadToSessionType</key><string>Background</string>",
		"<key>RunAtLoad</key><true/>", "<key>KeepAlive</key><false/>", "<string>/bin/sh</string><string>" + script + "</string>"} {
		if !strings.Contains(string(p.Files[plist]), want) {
			t.Fatalf("plist lacks %s: %s", want, p.Files[plist])
		}
	}
	if s := string(p.Files[script]); !strings.Contains(s, "'/example/looptender' '__start-background' '-c' '/example/looptender.yaml'\n") ||
		!strings.HasSuffix(s, "rm -f '"+plist+"' '"+script+"'\nexec '/example/bin/launchctl' bootout 'user/501/dev.looptender.loop.example'\n") {
		t.Fatalf("script: %s", s)
	}
	p, ok = backgroundLaunch("linux", found, "looptender-example", child, 1000, "/example/state")
	if !ok || p.Kind != "systemd" || !reflect.DeepEqual(p.Argv, append([]string{"/example/bin/systemd-run", "--user", "--unit", "looptender-example", "--collect", "--quiet", "--"}, child...)) {
		t.Fatalf("%+v", p)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if _, ok := backgroundLaunch(goos, missing, "x", child, 501, "/example/state"); ok {
			t.Fatalf("%s: launched without its service manager", goos)
		}
	}
	if _, ok := backgroundLaunch("windows", found, "x", child, 501, "/example/state"); ok {
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
// environment: no token or other secret, of the thread or of looptender's
// own reporters, except the variables this loop's own report names. The
// looptender variables it drops are listed by name.
func TestLaunchEnvironmentCarriesNoSecret(t *testing.T) {
	got, dropped := launchEnvironment([]string{"PATH=/example/bin", "HOME=/example/home", "LANG=C.UTF-8", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/example/claude",
		"LOOPTENDER_SESSION_DIR=/example/sessions", "LOOPTENDER_NTFY_TOKEN=fictional-secret", "LOOPTENDER_WEBHOOK_SECRET=fictional-secret",
		"LOOPTENDER_TELEGRAM_BOT_TOKEN=fictional-secret", "EXAMPLE_API_TOKEN=fictional-secret", "GITHUB_TOKEN=fictional-secret",
		"BB_THREAD_ID=thr_example_launch", "BB_SESSION=fictional-secret", "AWS_SECRET_ACCESS_KEY=fictional-secret", "EDITOR=vi"},
		map[string]bool{"LOOPTENDER_NTFY_TOKEN": true})
	want := []string{"PATH=/example/bin", "HOME=/example/home", "LANG=C.UTF-8", "LC_ALL=C", "CLAUDE_CONFIG_DIR=/example/claude", "LOOPTENDER_SESSION_DIR=/example/sessions", "LOOPTENDER_NTFY_TOKEN=fictional-secret"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(dropped, []string{"LOOPTENDER_WEBHOOK_SECRET", "LOOPTENDER_TELEGRAM_BOT_TOKEN"}) {
		t.Fatalf("%v %v", got, dropped)
	}
}

// The report's names are its url_env, secret_env and user_env, defaults
// included, with each default's earlier KEEPGOING_ name.
func TestReportEnvNamesAreTheReportsOwn(t *testing.T) {
	cfg, err := config.Parse([]byte("version: 1\nloop:\n  name: example\n  state_dir: /example/state\n  adapter: bb\n  bb: {thread: thr_example}\n  prompt: Continue the fictional example.\n  report:\n    format: ntfy\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := reportEnvNames(cfg)
	for _, n := range []string{"LOOPTENDER_NTFY_TOKEN", "KEEPGOING_NTFY_TOKEN", "LOOPTENDER_NTFY_URL"} {
		if !got[n] {
			t.Fatalf("%s missing: %v", n, got)
		}
	}
	if got["LOOPTENDER_TELEGRAM_BOT_TOKEN"] || len(reportEnvNames(&config.Config{})) != 0 {
		t.Fatalf("%v", got)
	}
}

// No test switch reaches a launched daemon, whatever its name, and its name
// is listed with the others it did not get.
func TestLaunchEnvironmentDropsTestSwitches(t *testing.T) {
	got, dropped := launchEnvironment([]string{"PATH=/example/bin", "LOOPTENDER_TEST_FIXED_HOST=1", "KEEPGOING_TEST_ADAPTER_EXIT=3", "LOOPTENDER_SESSION_DIR=/example/sessions"},
		map[string]bool{"LOOPTENDER_TEST_FIXED_HOST": true})
	if !reflect.DeepEqual(got, []string{"PATH=/example/bin", "LOOPTENDER_SESSION_DIR=/example/sessions"}) ||
		!reflect.DeepEqual(dropped, []string{"LOOPTENDER_TEST_FIXED_HOST", "KEEPGOING_TEST_ADAPTER_EXIT"}) {
		t.Fatalf("%v %v", got, dropped)
	}
}
