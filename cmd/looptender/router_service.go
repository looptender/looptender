//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/looptender/looptender/internal/router/channel"
)

const routerServiceRecordName = "service-router.json"

type routerServiceHost struct {
	platform   string
	home       string
	executable string
	searchPath string
	uid        int
	configHome string
	linger     func() bool
	query      func(context.Context, ...string) ([]byte, int, error)
	run        func(context.Context, ...string) (int, error)
}

func localRouterServiceHost() routerServiceHost {
	home, _ := os.UserHomeDir()
	exe, _ := launchExecutable()
	h := routerServiceHost{
		platform:   runtime.GOOS,
		home:       home,
		executable: exe,
		searchPath: os.Getenv("PATH"),
		uid:        os.Getuid(),
		configHome: os.Getenv("XDG_CONFIG_HOME"),
		linger:     lingerOn,
		query:      systemctlQuery,
		run:        launchctlRun,
	}
	if h.platform == "linux" {
		h.run = func(ctx context.Context, args ...string) (int, error) {
			_, code, err := systemctlQuery(ctx, args...)
			return code, err
		}
	}
	return h
}

func (h routerServiceHost) backend() string {
	if h.platform == "linux" {
		return "systemd"
	}
	return "launchd"
}

func (h routerServiceHost) definitionDir() string {
	if h.platform == "linux" {
		base := h.configHome
		if base == "" {
			base = filepath.Join(h.home, ".config")
		}
		return filepath.Join(base, "systemd", "user")
	}
	return filepath.Join(h.home, "Library", "LaunchAgents")
}

type routerServiceRecord struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Backend         string `json:"backend"`
	UID             int    `json:"uid"`
	Dir             string `json:"dir"`
	Executable      string `json:"executable"`
	AccountsDir     string `json:"accountsDir,omitempty"`
	Port            int    `json:"port,omitempty"`
	Upstream        string `json:"upstream,omitempty"`
	TLS             bool   `json:"tls,omitempty"`
	CoreLabel       string `json:"coreLabel"`
	CoreDefinition  string `json:"coreDefinition"`
	FrontLabel      string `json:"frontLabel"`
	FrontDefinition string `json:"frontDefinition"`
}

type routerServiceUnitStatus struct {
	Label             string `json:"label"`
	Definition        string `json:"definition"`
	DefinitionPresent bool   `json:"definitionPresent"`
	Registration      string `json:"registration"`
}

type routerServiceReport struct {
	Backend   string                  `json:"backend"`
	Operation string                  `json:"operation"`
	DryRun    bool                    `json:"dryRun"`
	Recorded  bool                    `json:"recorded"`
	Dir       string                  `json:"dir"`
	Answering bool                    `json:"answering"`
	Lingering *bool                   `json:"lingering,omitempty"`
	Core      routerServiceUnitStatus `json:"core"`
	Front     routerServiceUnitStatus `json:"front"`
	Message   string                  `json:"message"`
}

func routerServiceIdentities(backend, dir string) (coreLabel, frontLabel string) {
	sum := sha256.Sum256([]byte(dir))
	id := fmt.Sprintf("%x", sum[:8])
	if backend == "systemd" {
		return "looptender-router-core-" + id + ".service", "looptender-router-front-" + id + ".service"
	}
	return "dev.looptender.router.core." + id, "dev.looptender.router.front." + id
}

func newRouterServiceRecord(h routerServiceHost, dir, accountsDir string, port int, upstream string, useTLS bool) (routerServiceRecord, error) {
	backend := h.backend()
	coreLabel, frontLabel := routerServiceIdentities(backend, dir)
	suffix := ""
	if backend == "launchd" {
		suffix = ".plist"
	}
	defDir := h.definitionDir()
	rec := routerServiceRecord{
		SchemaVersion:   1,
		Backend:         backend,
		UID:             h.uid,
		Dir:             dir,
		Executable:      h.executable,
		AccountsDir:     accountsDir,
		Port:            port,
		Upstream:        upstream,
		TLS:             useTLS,
		CoreLabel:       coreLabel,
		CoreDefinition:  filepath.Join(defDir, coreLabel+suffix),
		FrontLabel:      frontLabel,
		FrontDefinition: filepath.Join(defDir, frontLabel+suffix),
	}
	return rec, validateRouterServiceRecord(h, dir, rec)
}

func validateRouterServiceRecord(h routerServiceHost, dir string, rec routerServiceRecord) error {
	coreLabel, frontLabel := routerServiceIdentities(h.backend(), dir)
	suffix := ""
	if h.backend() == "launchd" {
		suffix = ".plist"
	}
	defDir := h.definitionDir()
	if rec.SchemaVersion != 1 || rec.Backend != h.backend() || rec.UID != h.uid || rec.Dir != dir ||
		rec.CoreLabel != coreLabel || rec.FrontLabel != frontLabel ||
		rec.CoreDefinition != filepath.Join(defDir, coreLabel+suffix) ||
		rec.FrontDefinition != filepath.Join(defDir, frontLabel+suffix) {
		return errors.New("router service record does not match this user and state directory")
	}
	paths := []string{h.home, defDir, rec.Dir, rec.CoreDefinition, rec.FrontDefinition}
	if rec.Executable != "" {
		paths = append(paths, rec.Executable)
	}
	if rec.AccountsDir != "" {
		paths = append(paths, rec.AccountsDir)
	}
	for _, s := range paths {
		if !filepath.IsAbs(s) || filepath.Clean(s) != s || !utf8.ValidString(s) || containsServiceControl(s) {
			return errors.New("router service paths must be clean absolute paths without control characters")
		}
	}
	if !utf8.ValidString(h.searchPath) || containsServiceControl(h.searchPath) {
		return errors.New("PATH must not contain control characters")
	}
	if rec.Backend == "systemd" && strings.ContainsAny(rec.Executable, "\"'\\") {
		return errors.New("systemd executable path must not contain quotes or backslashes")
	}
	return nil
}

func routerServiceCommand(e env, args []string, h routerServiceHost) int {
	if len(args) == 0 {
		e.errf("usage: looptender router service install|status|remove [--dir DIR] [--dry-run] [--json]")
		return exitUsage
	}
	op, rest := args[0], args[1:]
	if op != "install" && op != "status" && op != "remove" {
		e.errf("router service: expected install, status, or remove")
		return exitUsage
	}
	fs := flag.NewFlagSet("router service "+op, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dirFlag := fs.String("dir", "", "the router's private directory")
	accountsFlag := fs.String("accounts", "", "install: optional accounts catalog directory")
	portFlag := fs.Int("port", 0, "install: loopback port for the front (0 lets the front pick one)")
	upstreamFlag := fs.String("upstream", "", "install: upstream provider URL")
	tlsFlag := fs.Bool("tls", false, "install: serve TLS on the front with a loopback certificate")
	dryRun := fs.Bool("dry-run", false, "install/remove: preview changes without writing files or calling the service manager")
	asJSON := fs.Bool("json", false, "print structured output")
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 0 {
		e.errf("router service: unexpected positional arguments")
		return exitUsage
	}
	if op != "install" && (*accountsFlag != "" || *portFlag != 0 || *upstreamFlag != "" || *tlsFlag) {
		e.errf("router service: --accounts, --port, --upstream, and --tls are only valid for install")
		return exitUsage
	}
	if op == "status" && *dryRun {
		e.errf("router service: --dry-run is only valid for install or remove")
		return exitUsage
	}
	if (op == "install" || op == "remove") && supervisedCaller() {
		e.errf("router service %s refused inside a supervised session or BB thread; run from an ordinary terminal", op)
		return exitFailed
	}
	if (h.platform != "darwin" && h.platform != "linux") || h.uid < 0 || h.home == "" {
		e.errf("router service: user-service management currently supports macOS launchd and Linux systemd user services")
		return exitFailed
	}

	dir, err := resolveRouterDir(*dirFlag)
	if err != nil {
		e.errf("router service: cannot resolve state directory")
		return exitFailed
	}
	dir, err = canonicalFuturePath(dir)
	if err != nil {
		e.errf("router service: cannot resolve state directory")
		return exitFailed
	}
	for _, p := range []string{dir, h.definitionDir()} {
		if err := serviceOutsideCheckout(p); err != nil {
			e.errf("router service: %v", err)
			return exitFailed
		}
	}

	if op == "install" {
		if h.executable == "" {
			e.errf("router service: cannot locate the looptender executable")
			return exitFailed
		}
		if _, err := exec.LookPath(h.executable); err != nil {
			e.errf("router service: current executable is unavailable; install a stable looptender binary first")
			return exitFailed
		}
		if *portFlag < 0 || *portFlag > 65535 {
			e.errf("router service: invalid port %d", *portFlag)
			return exitUsage
		}
		if *upstreamFlag != "" {
			u, err := url.Parse(*upstreamFlag)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				e.errf("router service: invalid upstream URL")
				return exitUsage
			}
		}
	}

	accountsDir := ""
	if *accountsFlag != "" {
		accountsDir, err = canonicalFuturePath(*accountsFlag)
		if err != nil {
			e.errf("router service: cannot resolve accounts directory")
			return exitFailed
		}
	}

	rec, recorded, err := readRouterServiceRecord(dir)
	if err != nil {
		e.errf("router service: %v", err)
		return exitFailed
	}
	if !recorded || op == "install" {
		rec, err = newRouterServiceRecord(h, dir, accountsDir, *portFlag, *upstreamFlag, *tlsFlag)
		if err != nil {
			e.errf("router service: %v", err)
			return exitFailed
		}
	} else {
		if err := validateRouterServiceRecord(h, dir, rec); err != nil {
			e.errf("router service: %v", err)
			return exitFailed
		}
	}

	switch op {
	case "status":
		return routerServiceStatus(e, h, rec, recorded, *asJSON)
	case "install":
		return routerServiceInstall(e, h, rec, recorded, *dryRun, *asJSON)
	case "remove":
		return routerServiceRemove(e, h, rec, recorded, *dryRun, *asJSON)
	}
	return exitUsage
}

func routerServiceArgs(rec routerServiceRecord) (coreArgs, frontArgs []string) {
	coreArgs = []string{rec.Executable, "router", "run", "--dir", rec.Dir}
	if rec.AccountsDir != "" {
		coreArgs = append(coreArgs, "--accounts", rec.AccountsDir)
	}
	frontArgs = []string{rec.Executable, "router", "front", "--dir", rec.Dir}
	if rec.Port > 0 {
		frontArgs = append(frontArgs, "--port", strconv.Itoa(rec.Port))
	}
	if rec.Upstream != "" {
		frontArgs = append(frontArgs, "--upstream", rec.Upstream)
	}
	if rec.TLS {
		frontArgs = append(frontArgs, "--tls")
	}
	return coreArgs, frontArgs
}

func routerServiceDefinitions(h routerServiceHost, rec routerServiceRecord) (coreDef, frontDef []byte, err error) {
	coreArgs, frontArgs := routerServiceArgs(rec)
	if rec.Backend == "systemd" {
		render := func(desc string, args []string, after string) []byte {
			q := make([]string, len(args))
			for i, s := range args {
				q[i] = systemdQuote(":" + s)
			}
			var b strings.Builder
			b.WriteString("[Unit]\nDescription=" + desc + "\n")
			if after != "" {
				b.WriteString("After=" + after + "\nWants=" + after + "\n")
			}
			b.WriteString("\n[Service]\nType=exec\nExecStart=" + strings.Join(q, " ") +
				"\nEnvironment=" + systemdQuote("PATH="+h.searchPath) + " \"BB_THREAD_ID=\"\n" +
				"Restart=always\nRestartSec=5s\nTimeoutStopSec=10s\nStandardOutput=null\nStandardError=null\n\n[Install]\nWantedBy=default.target\n")
			return []byte(b.String())
		}
		return render("looptender account router core", coreArgs, ""),
			render("looptender account router front", frontArgs, rec.CoreLabel), nil
	}

	renderPlist := func(label string, args []string) ([]byte, error) {
		var b bytes.Buffer
		var xmlErr error
		str := func(s string) {
			b.WriteString("<string>")
			if err := xml.EscapeText(&b, []byte(s)); err != nil {
				xmlErr = err
			}
			b.WriteString("</string>\n")
		}
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
		b.WriteString("<key>Label</key>")
		str(label)
		b.WriteString("<key>ProgramArguments</key><array>\n")
		for _, a := range args {
			str(a)
		}
		b.WriteString("</array>\n<key>EnvironmentVariables</key><dict><key>PATH</key>")
		str(h.searchPath)
		b.WriteString("<key>BB_THREAD_ID</key><string></string></dict>\n")
		b.WriteString("<key>LimitLoadToSessionType</key><string>Background</string>\n")
		b.WriteString("<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n<key>ThrottleInterval</key><integer>30</integer>\n")
		b.WriteString("<key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string>\n</dict></plist>\n")
		return b.Bytes(), xmlErr
	}
	coreDef, err = renderPlist(rec.CoreLabel, coreArgs)
	if err != nil {
		return nil, nil, err
	}
	frontDef, err = renderPlist(rec.FrontLabel, frontArgs)
	return coreDef, frontDef, err
}

func (rec routerServiceRecord) launchdDomain() string { return fmt.Sprintf("user/%d", rec.UID) }

func routerServiceRegistration(ctx context.Context, h routerServiceHost, rec routerServiceRecord, label, defPath string) string {
	if rec.Backend == "systemd" {
		p := servicePlan{Backend: "systemd", Label: label, Definition: defPath}
		return inspectSystemd(ctx, serviceHost{platform: h.platform, query: h.query}, p).registration
	}
	target := rec.launchdDomain() + "/" + label
	code, err := h.run(ctx, "print", target)
	if err == nil && code == 0 {
		return "loaded"
	}
	if err == nil && code == 113 {
		code, err = h.run(ctx, "print", rec.launchdDomain())
		if err == nil && code == 0 {
			return "absent"
		}
	}
	return "unknown"
}

func routerServiceStatus(e env, h routerServiceHost, rec routerServiceRecord, recorded, asJSON bool) int {
	_, coreErr := os.Lstat(rec.CoreDefinition)
	_, frontErr := os.Lstat(rec.FrontDefinition)
	answering := false
	if _, err := channel.CheckDirectory(rec.Dir); err == nil {
		if c, err := net.DialTimeout("unix", filepath.Join(rec.Dir, channel.SocketName), 200*time.Millisecond); err == nil {
			_ = c.Close()
			answering = true
		}
	}
	rep := routerServiceReport{
		Backend:   rec.Backend,
		Operation: "status",
		Recorded:  recorded,
		Dir:       rec.Dir,
		Answering: answering,
		Core: routerServiceUnitStatus{
			Label:             rec.CoreLabel,
			Definition:        rec.CoreDefinition,
			DefinitionPresent: coreErr == nil,
			Registration:      routerServiceRegistration(e.ctx, h, rec, rec.CoreLabel, rec.CoreDefinition),
		},
		Front: routerServiceUnitStatus{
			Label:             rec.FrontLabel,
			Definition:        rec.FrontDefinition,
			DefinitionPresent: frontErr == nil,
			Registration:      routerServiceRegistration(e.ctx, h, rec, rec.FrontLabel, rec.FrontDefinition),
		},
	}
	if rec.Backend == "systemd" && h.linger != nil {
		v := h.linger()
		rep.Lingering = &v
	}
	switch {
	case !rep.Core.DefinitionPresent && !rep.Front.DefinitionPresent && !recorded:
		rep.Message = "Router services are not installed."
	case rep.Core.Registration == "loaded" && rep.Front.Registration == "loaded":
		rep.Message = "Router core and front services are installed and loaded."
	default:
		rep.Message = fmt.Sprintf("Router services state: core %s, front %s.", rep.Core.Registration, rep.Front.Registration)
	}
	return writeRouterServiceReport(e, rep, asJSON, exitOK)
}

func routerServiceInstall(e env, h routerServiceHost, rec routerServiceRecord, recorded, dryRun, asJSON bool) int {
	coreDef, frontDef, err := routerServiceDefinitions(h, rec)
	if err != nil {
		e.errf("router service: cannot render service definitions")
		return exitFailed
	}
	rep := routerServiceReport{
		Backend:   rec.Backend,
		Operation: "install",
		DryRun:    dryRun,
		Recorded:  recorded,
		Dir:       rec.Dir,
		Core: routerServiceUnitStatus{
			Label:      rec.CoreLabel,
			Definition: rec.CoreDefinition,
		},
		Front: routerServiceUnitStatus{
			Label:      rec.FrontLabel,
			Definition: rec.FrontDefinition,
		},
	}
	if dryRun {
		rep.Message = "Dry run: would write router core and front service definitions and register them."
		return writeRouterServiceReport(e, rep, asJSON, exitOK)
	}

	if err := ensurePrivateDir(rec.Dir); err != nil {
		e.errf("router service: %v", err)
		return exitFailed
	}
	if err := os.MkdirAll(h.definitionDir(), 0o755); err != nil {
		e.errf("router service: cannot create service directory: %v", err)
		return exitFailed
	}

	prevCore, hadCore := readOptionalServiceBytes(rec.CoreDefinition)
	prevFront, hadFront := readOptionalServiceBytes(rec.FrontDefinition)
	prevRecord, hadRecord := readOptionalServiceBytes(filepath.Join(rec.Dir, routerServiceRecordName))

	rollbackFiles := func() {
		restoreOrRemove := func(path string, prev []byte, had bool) {
			if had {
				_ = writeServiceFile(path, prev)
			} else {
				_ = os.Remove(path)
			}
		}
		restoreOrRemove(rec.FrontDefinition, prevFront, hadFront)
		restoreOrRemove(rec.CoreDefinition, prevCore, hadCore)
		restoreOrRemove(filepath.Join(rec.Dir, routerServiceRecordName), prevRecord, hadRecord)
	}

	if err := writeServiceFile(rec.CoreDefinition, coreDef); err != nil {
		rollbackFiles()
		e.errf("router service: cannot write core definition: %v", err)
		return exitFailed
	}
	if err := writeServiceFile(rec.FrontDefinition, frontDef); err != nil {
		rollbackFiles()
		e.errf("router service: cannot write front definition: %v", err)
		return exitFailed
	}
	if err := writePrivateJSON(filepath.Join(rec.Dir, routerServiceRecordName), rec); err != nil {
		rollbackFiles()
		e.errf("router service: cannot write service record: %v", err)
		return exitFailed
	}

	if rec.Backend == "launchd" {
		_, _ = h.run(e.ctx, "bootout", rec.launchdDomain()+"/"+rec.FrontLabel)
		_, _ = h.run(e.ctx, "bootout", rec.launchdDomain()+"/"+rec.CoreLabel)
		if code, err := h.run(e.ctx, "bootstrap", rec.launchdDomain(), rec.CoreDefinition); err != nil || code != 0 {
			rollbackFiles()
			e.errf("router service: launchd bootstrap core failed (exit %d, err %v)", code, err)
			return exitFailed
		}
		if code, err := h.run(e.ctx, "bootstrap", rec.launchdDomain(), rec.FrontDefinition); err != nil || code != 0 {
			_, _ = h.run(e.ctx, "bootout", rec.launchdDomain()+"/"+rec.CoreLabel)
			rollbackFiles()
			e.errf("router service: launchd bootstrap front failed (exit %d, err %v)", code, err)
			return exitFailed
		}
	} else {
		if code, err := h.run(e.ctx, "daemon-reload"); err != nil || code != 0 {
			rollbackFiles()
			e.errf("router service: systemctl daemon-reload failed (exit %d, err %v)", code, err)
			return exitFailed
		}
		if code, err := h.run(e.ctx, "enable", "--now", rec.CoreLabel); err != nil || code != 0 {
			rollbackFiles()
			e.errf("router service: systemctl enable core failed (exit %d, err %v)", code, err)
			return exitFailed
		}
		if code, err := h.run(e.ctx, "enable", "--now", rec.FrontLabel); err != nil || code != 0 {
			_, _ = h.run(e.ctx, "disable", "--now", rec.CoreLabel)
			rollbackFiles()
			e.errf("router service: systemctl enable front failed (exit %d, err %v)", code, err)
			return exitFailed
		}
		if h.linger != nil {
			v := h.linger()
			rep.Lingering = &v
		}
	}

	rep.Recorded = true
	rep.Core.DefinitionPresent = true
	rep.Front.DefinitionPresent = true
	rep.Core.Registration = "loaded"
	rep.Front.Registration = "loaded"
	rep.Message = "Installed and started router core and front services."
	return writeRouterServiceReport(e, rep, asJSON, exitOK)
}

func routerServiceRemove(e env, h routerServiceHost, rec routerServiceRecord, recorded, dryRun, asJSON bool) int {
	_, coreErr := os.Lstat(rec.CoreDefinition)
	_, frontErr := os.Lstat(rec.FrontDefinition)
	rep := routerServiceReport{
		Backend:   rec.Backend,
		Operation: "remove",
		DryRun:    dryRun,
		Recorded:  recorded,
		Dir:       rec.Dir,
		Core: routerServiceUnitStatus{
			Label:             rec.CoreLabel,
			Definition:        rec.CoreDefinition,
			DefinitionPresent: coreErr == nil,
		},
		Front: routerServiceUnitStatus{
			Label:             rec.FrontLabel,
			Definition:        rec.FrontDefinition,
			DefinitionPresent: frontErr == nil,
		},
	}
	if dryRun {
		rep.Message = "Dry run: would unload and remove router core and front service definitions."
		return writeRouterServiceReport(e, rep, asJSON, exitOK)
	}

	if rec.Backend == "launchd" {
		_, _ = h.run(e.ctx, "bootout", rec.launchdDomain()+"/"+rec.FrontLabel)
		_, _ = h.run(e.ctx, "bootout", rec.launchdDomain()+"/"+rec.CoreLabel)
	} else {
		_, _ = h.run(e.ctx, "disable", "--now", rec.FrontLabel)
		_, _ = h.run(e.ctx, "disable", "--now", rec.CoreLabel)
		_, _ = h.run(e.ctx, "daemon-reload")
	}

	for _, path := range []string{rec.FrontDefinition, rec.CoreDefinition, filepath.Join(rec.Dir, routerServiceRecordName)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			e.errf("router service: could not remove %s: %v", path, err)
			return exitFailed
		}
	}

	rep.Recorded = false
	rep.Core.DefinitionPresent = false
	rep.Front.DefinitionPresent = false
	rep.Core.Registration = "absent"
	rep.Front.Registration = "absent"
	rep.Message = "Removed router core and front services."
	return writeRouterServiceReport(e, rep, asJSON, exitOK)
}

func readRouterServiceRecord(dir string) (routerServiceRecord, bool, error) {
	path := filepath.Join(dir, routerServiceRecordName)
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return routerServiceRecord{}, false, nil
		}
		return routerServiceRecord{}, false, err
	}
	if !st.Mode().IsRegular() {
		return routerServiceRecord{}, false, errors.New("router service record must be a regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return routerServiceRecord{}, false, err
	}
	var rec routerServiceRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return routerServiceRecord{}, false, err
	}
	return rec, true, nil
}

func readOptionalServiceBytes(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

func writeRouterServiceReport(e env, rep routerServiceReport, asJSON bool, code int) int {
	if asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return code
	}
	fmt.Fprintln(e.stdout, rep.Message)
	fmt.Fprintf(e.stdout, "  core:  %s (%s)\n", rep.Core.Label, rep.Core.Definition)
	fmt.Fprintf(e.stdout, "  front: %s (%s)\n", rep.Front.Label, rep.Front.Definition)
	if rep.Lingering != nil && !*rep.Lingering {
		fmt.Fprintln(e.stdout, "  lingering: off (`loginctl enable-linger` keeps user services running after logout)")
	}
	return code
}
