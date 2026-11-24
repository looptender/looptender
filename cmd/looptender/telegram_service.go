package main

import (
	"context"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/telegram"
)

// The Telegram poller as a service the person installs once
// (looptender telegram service install|status|uninstall). On macOS it is a
// launchd job in the per-user background domain (bootstrap user/<uid>,
// LimitLoadToSessionType Background, as background daemons are since #498),
// so it survives a lost login session; its property list stays in
// ~/Library/LaunchAgents and it is kept alive. On Linux it is a systemd user
// unit, enabled and started; it outlives a logout only with lingering on.
// Nothing installs it but this command, and this command refuses to run from
// a BB thread or a supervised session.

const (
	telegramServiceLabel = "dev.looptender.telegram-poll"
	telegramServiceUnit  = "looptender-telegram-poll.service"
)

// Stand-ins for tests: the service manager, the platform and the user.
var (
	telegramServiceRun = func(argv []string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	}
	telegramServiceGOOS = runtime.GOOS
	telegramServiceUID  = os.Getuid
)

// telegramPollerRunning reports whether a poller holds the Telegram
// directory's poll lock: any poller, from the service or a terminal.
func telegramPollerRunning(dir string) (bool, error) {
	return lock.Held(dir, telegramPollLock)
}

type telegramServicePlan struct {
	Kind    string // launchd or systemd
	File    string
	Body    []byte
	Install [][]string
	Remove  [][]string
	Target  string // launchd service target, or the systemd unit
}

func telegramServiceFor(goos, home string, uid int, exe string, extra []string) (telegramServicePlan, error) {
	args := append([]string{exe, "telegram", "poll"}, extra...)
	switch goos {
	case "darwin":
		file := filepath.Join(home, "Library", "LaunchAgents", telegramServiceLabel+".plist")
		target := fmt.Sprintf("user/%d/%s", uid, telegramServiceLabel)
		esc := func(v string) string {
			var b strings.Builder
			_ = xml.EscapeText(&b, []byte(v))
			return b.String()
		}
		var a strings.Builder
		for _, s := range args {
			a.WriteString("<string>" + esc(s) + "</string>")
		}
		body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + telegramServiceLabel + `</string>
<key>ProgramArguments</key><array>` + a.String() + `</array>
<key>LimitLoadToSessionType</key><string>Background</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>60</integer>
<key>StandardOutPath</key><string>/dev/null</string>
<key>StandardErrorPath</key><string>/dev/null</string>
</dict></plist>
`)
		return telegramServicePlan{Kind: "launchd", File: file, Body: body, Target: target,
			Install: [][]string{{"launchctl", "bootstrap", fmt.Sprintf("user/%d", uid), file}},
			Remove:  [][]string{{"launchctl", "bootout", target}}}, nil
	case "linux":
		file := filepath.Join(home, ".config", "systemd", "user", telegramServiceUnit)
		q := make([]string, len(args))
		for i, s := range args {
			q[i] = systemdQuote(s)
		}
		body := []byte("[Unit]\nDescription=looptender Telegram poller (Resume, Pause and Stop from the phone)\n\n[Service]\nType=exec\nExecStart=" + strings.Join(q, " ") +
			"\nEnvironment=\"BB_THREAD_ID=\"\nRestart=always\nRestartSec=30s\nStandardOutput=null\nStandardError=null\n\n[Install]\nWantedBy=default.target\n")
		return telegramServicePlan{Kind: "systemd", File: file, Body: body, Target: telegramServiceUnit,
			Install: [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", telegramServiceUnit}},
			Remove:  [][]string{{"systemctl", "--user", "disable", "--now", telegramServiceUnit}, {"systemctl", "--user", "daemon-reload"}}}, nil
	}
	return telegramServicePlan{}, errors.New("a Telegram poller service is not available on this platform; run looptender telegram poll in a terminal")
}

func telegramServiceCommand(e env, args []string) int {
	if len(args) == 0 {
		e.errf("usage: looptender telegram service install|status|uninstall [--dir DIR] [--registry DIR] [--dry-run]")
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("telegram service "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dirFlag := fs.String("dir", "", "private state directory for Telegram pairing (default: user state dir)")
	regFlag := fs.String("registry", "", "private loop registry directory (default: user registry dir)")
	dryRun := fs.Bool("dry-run", false, "install/uninstall: print what would be written and run, and change nothing")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	dir, err := resolveTelegramDir(*dirFlag)
	if err != nil {
		e.errf("telegram service: %v", err)
		return exitFailed
	}
	home, err := os.UserHomeDir()
	if err != nil {
		e.errf("telegram service: no home directory")
		return exitFailed
	}
	exe, err := launchExecutable()
	if err != nil {
		e.errf("telegram service: cannot locate the looptender executable")
		return exitFailed
	}
	var extra []string
	if *dirFlag != "" {
		extra = append(extra, "--dir", dir)
	}
	if *regFlag != "" {
		r, err := filepath.Abs(*regFlag)
		if err != nil {
			return exitUsage
		}
		extra = append(extra, "--registry", r)
	}
	plan, err := telegramServiceFor(telegramServiceGOOS, home, telegramServiceUID(), exe, extra)
	if err != nil {
		e.errf("telegram service: %v", err)
		return exitFailed
	}
	switch sub {
	case "install":
		// The person's command: a session supervised by looptender, or any
		// BB thread, does not install a service for them.
		if os.Getenv("BB_THREAD_ID") != "" || os.Getenv(control.SupervisedLoopVar) != "" {
			e.errf("telegram service install refused, nothing changed: run it yourself in Terminal, not from a BB thread or a supervised session")
			return exitFailed
		}
		if _, paired, err := telegram.LoadPairing(dir); err != nil || !paired {
			e.errf("telegram service install refused, nothing changed: Telegram is not paired; run looptender telegram setup first")
			return exitFailed
		}
		if *dryRun {
			fmt.Fprintf(e.stdout, "Would write %s:\n%s\nWould run:\n", plan.File, plan.Body)
			for _, c := range plan.Install {
				fmt.Fprintf(e.stdout, "  %s\n", strings.Join(c, " "))
			}
			return exitOK
		}
		if err := os.MkdirAll(filepath.Dir(plan.File), 0o700); err != nil {
			e.errf("telegram service install: %v", err)
			return exitFailed
		}
		if err := writeServiceFile(plan.File, plan.Body); err != nil {
			e.errf("telegram service install: the service file could not be written: %v", err)
			return exitFailed
		}
		if plan.Kind == "launchd" {
			_, _ = telegramServiceRun([]string{"launchctl", "bootout", plan.Target}) // a job loaded before: replaced
		}
		for _, c := range plan.Install {
			if out, err := telegramServiceRun(c); err != nil {
				e.errf("telegram service install: %s failed: %s", strings.Join(c, " "), strings.TrimSpace(string(out)))
				return exitFailed
			}
		}
		fmt.Fprintf(e.stdout, "Installed the Telegram poller (%s, %s). Resume, Pause and Stop from the phone work while it runs.\n", plan.Kind, plan.Target)
		if plan.Kind == "systemd" && !lingerOn() {
			fmt.Fprintln(e.stdout, "Lingering is off for this user, so the poller stops when you log out: `loginctl enable-linger` keeps it running.")
		}
		return exitOK
	case "uninstall":
		if *dryRun {
			fmt.Fprintln(e.stdout, "Would run:")
			for _, c := range plan.Remove {
				fmt.Fprintf(e.stdout, "  %s\n", strings.Join(c, " "))
			}
			fmt.Fprintf(e.stdout, "Would remove %s\n", plan.File)
			return exitOK
		}
		for _, c := range plan.Remove {
			_, _ = telegramServiceRun(c) // not loaded: nothing to stop
		}
		if err := os.Remove(plan.File); err != nil && !errors.Is(err, os.ErrNotExist) {
			e.errf("telegram service uninstall: %v", err)
			return exitFailed
		}
		fmt.Fprintln(e.stdout, "Removed the Telegram poller service. Pairing and token are unchanged (looptender telegram disconnect removes them).")
		return exitOK
	case "status":
		_, statErr := os.Stat(plan.File)
		installed := statErr == nil
		running, _ := telegramPollerRunning(dir)
		fmt.Fprintf(e.stdout, "Service: %s (%s)\n", map[bool]string{true: "installed", false: "not installed"}[installed], plan.File)
		if running {
			fmt.Fprintln(e.stdout, "Poller: running")
		} else {
			fmt.Fprintln(e.stdout, "Poller: not running, so taps on the phone do nothing")
		}
		if !installed {
			fmt.Fprintln(e.stdout, "Install it with: looptender telegram service install")
		}
		return exitOK
	}
	e.errf("usage: looptender telegram service install|status|uninstall [--dir DIR] [--registry DIR] [--dry-run]")
	return exitUsage
}
