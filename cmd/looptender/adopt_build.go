package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/diskspace"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/state"
)

// A running daemon adopts the build installed at the path it was started
// from. Between checks, when that path leads to a different file than the
// one the daemon is running, and the new file passes its own validation of
// this loop's configuration, the daemon replaces its program image with it
// (exec): the same process, the same daemon lock, the same holds, owner,
// state and history. Only the build changes.
//
// Rule for which build: the one a person installed at the launch path, and
// no other. The daemon never looks for builds itself, so it never moves to
// a build nobody installed there. By default it moves only when the launch
// path reaches the build through a symbolic link whose target changed (an
// install pointer such as `current` moved); a file overwritten in place is
// reported, not adopted, unless loop.daemon.adopt_installed_build is set to
// true. It refuses a build that declares an older state format than its own
// (adoptStateFormat), because an older build does not read a newer state
// directory safely; going back to one is the documented stop, restore and
// start, by a person. Version labels are never compared.

const (
	adoptCommandName = "__adopt-build"
	// adoptProtocol is the handover this build speaks: the flags of
	// adoptCommandName. A build that answers the probe with another number,
	// or not at all, is never switched to.
	adoptProtocol = 1
	// adoptStateFormat rises whenever the records an earlier build would
	// rewrite without understanding them change: state.json, control.json,
	// the incident journal, sends.json, health.json, deliveries.json and
	// goal.json. TestAdoptStateFormatFollowsTheSchemas fails when their
	// fields change without a rise; a new value of an existing field does not
	// fail it, and needs a rise by hand. A daemon does not switch to a
	// build with a lower number.
	adoptStateFormat = 6
	adoptRecordName  = "build-adoption.json"
	adoptProbeLimit  = 10 * time.Second
	adoptCheckLimit  = 60 * time.Second
	adoptImageLimit  = 128 << 20
)

// Outcomes in the build adoption record.
const (
	adoptSwitching = "switching" // the daemon is replacing its image
	adoptAdopted   = "adopted"   // the new build is running
	adoptRefused   = "refused"   // the daemon stays on its build; Reason says why
	adoptReturned  = "returned"  // the new build could not start; the earlier one runs again
	adoptFailed    = "failed"    // neither build is running: no supervisor
	// adoptHandedOff: the loop's owner changed during the switch, and the
	// daemon exited as it does when that happens at any check.
	adoptHandedOff = "handed_off"
)

type adoptBuildID struct {
	Label  string `json:"label,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// adoptRecord is the last build change of a loop's daemon. It holds no
// paths and no output of the other build: only fixed reasons.
type adoptRecord struct {
	Version int          `json:"version"`
	Outcome string       `json:"outcome"`
	At      time.Time    `json:"at"`
	Mode    string       `json:"mode,omitempty"`
	From    adoptBuildID `json:"from"`
	To      adoptBuildID `json:"to"`
	Reason  string       `json:"reason,omitempty"`
}

func loadAdoptRecord(dir string) (*adoptRecord, error) {
	b, err := os.ReadFile(filepath.Join(dir, adoptRecordName))
	if err != nil {
		return nil, err
	}
	var r adoptRecord
	if err := json.Unmarshal(b, &r); err != nil || r.Version != 1 {
		return nil, errors.New("build adoption record unreadable")
	}
	return &r, nil
}

func saveAdoptRecord(dir string, r adoptRecord) error {
	r.Version = 1
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".build-adoption-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(0o600)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return snapshot.Replace(f.Name(), filepath.Join(dir, adoptRecordName))
}

// adoptProbe is a build's answer to `__adopt-build --probe`.
type adoptProbe struct {
	Protocol    int    `json:"protocol"`
	StateFormat int    `json:"stateFormat"`
	Version     string `json:"version"`
	// ConfigArg: this build accepts the configuration path after the
	// handover descriptor (`--handover-fd N -c PATH`), so a process list
	// still names the loop after a switch. An earlier build answers without
	// it and is handed only `--handover-fd N`, the one form it accepts.
	ConfigArg bool `json:"configArg,omitempty"`
}

// adoptCandidate is the file now installed at the launch path, opened: the
// checks, the probe, validate and the exec all use this file.
type adoptCandidate struct {
	path     string // resolved
	file     *os.File
	identity state.FileIdentity
	sha      string
	// viaLink: the launch path now leads to a different path than the
	// running build's, as when a link such as current is moved; false for a
	// file written over in place.
	viaLink bool
	// dirs holds every directory the resolution went through: those that
	// hold a link of the chain and the one holding the file.
	dirs []string
}

func (c *adoptCandidate) close() {
	if c != nil && c.file != nil {
		c.file.Close()
	}
}

// buildAdopter is a daemon's watch on its launch path.
type buildAdopter struct {
	launch string // absolute path the daemon was started by: argv[0]
	// from is the running build's own file, symbolic links resolved, for
	// a new build that cannot start to return to.
	from string
	// loop is the loop's name, for the run log; taken from the
	// configuration at each safe point.
	loop string
	// disk is the daemon's disk watch, handed to the new build so a switch
	// does not start a new low-disk episode; diskSeed is the one handed in.
	disk, diskSeed *diskWatch
	image          *state.ExecutableImage
	cfgPath        string
	stateDir       string
	mode           string // daemon, foreground, background, service
	// expectedStateDir is the service's bound state directory, checked
	// again by the new build.
	expectedStateDir string
	daemonLock       *lock.Lock
	// off says why this daemon never adopts a build; "" when it can.
	off string
	// skip holds candidate digests already refused, here or recorded, so
	// a refused build is not probed again at every check. noted holds those
	// only reported (replaced in place), which a change of setting may still
	// adopt.
	skip, noted map[string]bool
	known       map[state.FileIdentity]string
	// Stand-ins for tests.
	run  func(ctx context.Context, c *adoptCandidate, args ...string) ([]byte, int, error)
	exec func(c *adoptCandidate, argv []string, env []string, keep []*os.File) error
	dirs func(dirs []string) string
	// ready says why the loop cannot be handed to a new build now, or "".
	ready func(lease *ownerLease) string
	// monitor is the daemon's live diagnostics, whose start and last pass
	// a switch hands on; nil when they are unavailable.
	monitor *daemoninfo.Server
	// lastMoment runs just before the last check of the file; tests only.
	lastMoment func()
	now        func() time.Time
}

func newBuildAdopter(cfgPath, stateDir, mode, expectedStateDir string, daemonLock *lock.Lock) *buildAdopter {
	a := &buildAdopter{cfgPath: cfgPath, stateDir: stateDir, mode: mode, expectedStateDir: expectedStateDir, daemonLock: daemonLock, image: processExecutableImage,
		skip: map[string]bool{}, noted: map[string]bool{}, known: map[state.FileIdentity]string{}, run: runAdoptCommand, exec: execAdoptCandidate, dirs: adoptDirsRefusal, now: time.Now}
	a.ready = a.loopReady
	if len(os.Args) > 0 {
		a.launch = os.Args[0]
	}
	switch {
	case !adoptSupported:
		a.off = "not available on this platform"
	case !filepath.IsAbs(a.launch):
		a.off = "the daemon was not started by an absolute path"
	case a.image == nil:
		a.off = "the running build's file could not be identified"
	case daemonLock == nil:
		a.off = "the daemon lock cannot be handed on"
	}
	if a.off == "" {
		// The running file, found again with links resolved; on some
		// systems the recorded path is the link it was started through.
		from, err := filepath.EvalSymlinks(a.image.Path)
		info, serr := os.Stat(from)
		if id, ok := state.IdentityOf(info); err != nil || serr != nil || !ok || id != a.image.Identity {
			a.off = "the running build's file has already been replaced; restart supervision to use the installed one"
		} else {
			a.from = from
		}
	}
	if r, err := loadAdoptRecord(stateDir); err == nil {
		switch r.Outcome {
		case adoptReturned:
			// From is the build that could not start; To is this one.
			if r.From.SHA256 != "" {
				a.skip[r.From.SHA256] = true
			}
		case adoptRefused, adoptFailed:
			if r.To.SHA256 != "" {
				a.skip[r.To.SHA256] = true
			}
		}
	}
	return a
}

func runAdoptCommand(ctx context.Context, c *adoptCandidate, args ...string) ([]byte, int, error) {
	cmd := adoptCommand(ctx, c, args...)
	cmd.Env = adoptCommandEnv(os.Environ())
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, -1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// adoptCommandEnv is the daemon's environment without BB_THREAD_ID, for
// the candidate's probe and validate. The daemon's ownership is its lease,
// checked under its own locks (loopReady); the thread that started it may
// since have lost ownership to a handoff the supervisor made, which the
// daemon follows. Validating as that thread would refuse every build
// installed after such a handoff.
func adoptCommandEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "BB_THREAD_ID=") {
			out = append(out, kv)
		}
	}
	return out
}

// resolveLaunch resolves the launch path one component at a time and
// returns the file, every directory that held a link on the way (a link to
// a directory included) and the one holding the file, and whether any link
// was followed.
func resolveLaunch(p string) (target string, dirs []string, viaLink bool, err error) {
	if !filepath.IsAbs(p) {
		return "", nil, false, errors.New("not absolute")
	}
	p = filepath.Clean(p)
	for hops := 0; hops < 255; hops++ {
		parts := strings.Split(strings.TrimPrefix(p, string(filepath.Separator)), string(filepath.Separator))
		cur, linked := string(filepath.Separator), false
		for i, part := range parts {
			next := filepath.Join(cur, part)
			info, err := os.Lstat(next)
			if err != nil {
				return "", nil, false, err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				cur = next
				continue
			}
			link, err := os.Readlink(next)
			if err != nil {
				return "", nil, false, err
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(cur, link)
			}
			dirs = append(dirs, cur)
			p, viaLink, linked = filepath.Join(append([]string{link}, parts[i+1:]...)...), true, true
			break
		}
		if !linked {
			return p, append(dirs, filepath.Dir(p)), viaLink, nil
		}
	}
	return "", nil, false, errors.New("too many links")
}

// candidate opens the file installed at the launch path when it is not the
// running build; nil when it is, or when nothing usable is there. The
// caller closes it.
func (a *buildAdopter) candidate() *adoptCandidate {
	target, dirs, _, err := resolveLaunch(a.launch)
	if err != nil {
		return nil // missing or mid-install: look again after the next check
	}
	f, err := os.Open(target)
	if err != nil {
		return nil
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > adoptImageLimit {
		f.Close()
		return nil
	}
	id, ok := state.IdentityOf(info)
	if !ok || id == a.image.Identity {
		f.Close()
		return nil
	}
	sha, ok := a.known[id]
	if !ok {
		if sha, err = adoptDigestFile(f); err != nil {
			f.Close()
			return nil
		}
		a.known = map[state.FileIdentity]string{id: sha} // one entry: the latest file seen
	}
	if sha == a.image.Digest() {
		f.Close()
		return nil // the same build in another file
	}
	// Installed by moving a link: the launch path now leads to another
	// path than the running build's. A file written over in place is at the
	// same path.
	return &adoptCandidate{path: target, file: f, identity: id, sha: sha, viaLink: target != a.from, dirs: dirs}
}

// adoptDigestFile hashes an open file from its start.
func adoptDigestFile(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, adoptImageLimit+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verify says whether the open file is still, by identity and contents, the
// one that was checked, and still the file at the launch path.
func (a *buildAdopter) verify(c *adoptCandidate) bool {
	info, err := c.file.Stat()
	if err != nil {
		return false
	}
	if id, ok := state.IdentityOf(info); !ok || id != c.identity {
		return false
	}
	if sha, err := adoptDigestFile(c.file); err != nil || sha != c.sha {
		return false
	}
	target, _, _, err := resolveLaunch(a.launch)
	if err != nil || target != c.path {
		return false
	}
	named, err := os.Stat(target)
	return err == nil && os.SameFile(info, named)
}

func shortDigest(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// loopReady says why the loop cannot be handed to a new build now, or "".
// These are conditions a running daemon rides through (a configuration
// that does not load or names another state directory, a pending
// transfer) or ends on (a new owner); a switch never turns one into the
// end of supervision.
func (a *buildAdopter) loopReady(lease *ownerLease) string {
	fresh, err := config.Load(a.cfgPath)
	switch {
	case err != nil:
		return "the configuration does not load"
	case fresh.Loop.StateDir != a.stateDir:
		return adoptStateDirReason
	case checkTransferSources(fresh, false) != nil:
		return "a transfer of the loop is pending"
	case checkOwnership(a.stateDir, lease) != nil:
		return "the loop's owner changed"
	}
	return ""
}

const adoptStateDirReason = "the configuration names another state_dir; restart the daemon deliberately to apply it, and the installed build with it"

var errAdoptChanged = errors.New("changed since it was checked")

// adoptPayload is what a switching daemon hands to the new image, through
// an inherited pipe, so that nothing of it shows in the process list.
type adoptPayload struct {
	Config string `json:"config"`
	// Loop is the loop's name, for the run log of a build that cannot
	// load the configuration. Absent from an earlier build's handover.
	Loop            string `json:"loop,omitempty"`
	StateDir        string `json:"stateDir"`
	Mode            string `json:"mode"`
	LockFD          uint64 `json:"lockFd"`
	From            string `json:"from"`
	FromSHA         string `json:"fromSha"`
	FromLabel       string `json:"fromLabel"`
	ExpectSHA       string `json:"expectSha"`
	OwnerThread     string `json:"ownerThread"`
	OwnerGeneration uint64 `json:"ownerGeneration"`
	// ExpectedStateDir is a service's bound state directory.
	ExpectedStateDir string `json:"expectedStateDir,omitempty"`
	// DiskEpisode is the daemon's low-disk episode, so the new build does
	// not tell of it again. Absent from an earlier build's handover.
	DiskEpisode *diskEpisode `json:"diskEpisode,omitempty"`
	// Returning, when set, is why the build handing over could not start:
	// this image is the earlier build, taking the daemon back.
	Returning string `json:"returning,omitempty"`
	// DaemonStartedAt is when the daemon process started, and
	// DaemonLastPassStartedAt and DaemonLastPassAt when its last completed
	// pass began and ended, as the build handing over knew them: the new
	// build reports the process's start, not the switch, as its own.
	// Absent from an earlier build's handover.
	DaemonStartedAt         *time.Time `json:"daemonStartedAt,omitempty"`
	DaemonLastPassStartedAt *time.Time `json:"daemonLastPassStartedAt,omitempty"`
	DaemonLastPassAt        *time.Time `json:"daemonLastPassAt,omitempty"`
	// ConfigArg: the build handing over accepts the configuration path in
	// its arguments (see adoptProbe.ConfigArg), for a return to it.
	ConfigArg bool `json:"configArg,omitempty"`
}

const adoptPayloadLimit = 32 << 10

// handover execs path as argv0 with the payload on an inherited pipe. It
// returns only when the image was not replaced. withConfig adds the
// configuration path after the descriptor, for the process list, when the
// build receiving it accepts that; the payload stays what the new build
// reads.
func handover(p adoptPayload, argv0 string, withConfig bool, run func(argv []string, keep []*os.File) error) error {
	b, err := json.Marshal(p)
	if err != nil || len(b) > adoptPayloadLimit {
		return errors.New("handover too large")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = w.Write(b) // well under the pipe's buffer: never blocks
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	fd, err := keepFDAcrossExec(r)
	if err != nil {
		return err
	}
	argv := []string{argv0, adoptCommandName, "--handover-fd", strconv.FormatUint(uint64(fd), 10)}
	if withConfig {
		argv = append(argv, "-c", p.Config)
	}
	return run(argv, []*os.File{r})
}

// atSafePoint runs after a check has finished and been saved, before the
// daemon waits for the next one. It returns only when the daemon stays on
// its build; a switch replaces the process.
func (a *buildAdopter) atSafePoint(ctx context.Context, cfg *config.Config, lease *ownerLease) {
	setting := cfg.Loop.Daemon.AdoptInstalledBuild
	if a == nil || a.off != "" || ctx.Err() != nil || (setting != nil && !*setting) {
		return
	}
	a.loop = cfg.Loop.Name
	c := a.candidate()
	if c == nil {
		return
	}
	defer c.close()
	if a.skip[c.sha] {
		return
	}
	from := adoptBuildID{Label: version, SHA256: a.image.Digest()}
	to := adoptBuildID{SHA256: c.sha}
	if diskspace.Of(a.stateDir).Level() == diskspace.Critical {
		// A switch writes records: not on a nearly full volume. Said once
		// for this build; it is tried again after each check.
		if !a.noted[c.sha+adoptDiskReason] {
			a.noted[c.sha+adoptDiskReason] = true
			a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: adoptDiskReason})
		}
		return
	}
	if setting == nil && !c.viaLink {
		// Overwritten in place (go install, go build -o): reported once,
		// not adopted by default.
		if !a.noted[c.sha] {
			a.noted[c.sha] = true
			a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: adoptInPlaceReason})
		}
		return
	}
	refuse := func(reason string) {
		a.skip[c.sha] = true
		a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: reason})
	}
	if reason := a.ready(lease); reason != "" {
		// Not this build's fault: no switch now, nothing refused for good.
		// A state_dir change lasts until a restart, so it is said once.
		if reason == adoptStateDirReason && !a.noted[c.sha+reason] {
			a.noted[c.sha+reason] = true
			a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: reason})
		}
		return
	}
	if reason := adoptRunnable(); reason != "" {
		refuse(reason)
		return
	}
	if reason := adoptFileRefusal(c.file); reason != "" {
		refuse(reason)
		return
	}
	if reason := a.dirs(c.dirs); reason != "" {
		refuse(reason)
		return
	}
	pctx, cancel := context.WithTimeout(ctx, adoptProbeLimit)
	out, code, err := a.run(pctx, c, adoptCommandName, "--probe")
	cancel()
	if ctx.Err() != nil {
		return
	}
	var probe adoptProbe
	if err != nil || code != 0 || json.Unmarshal(out, &probe) != nil || probe.Protocol != adoptProtocol {
		refuse(adoptTakeOverReason)
		return
	}
	to.Label = dockBuildLabel(probe.Version)
	if probe.StateFormat < adoptStateFormat {
		refuse(adoptStateFormatReason)
		return
	}
	vctx, cancel := context.WithTimeout(ctx, adoptCheckLimit)
	_, code, err = a.run(vctx, c, "validate", "-c", a.cfgPath)
	cancel()
	if ctx.Err() != nil {
		return
	}
	if err != nil || code != 0 {
		refuse(adoptValidateReason)
		return
	}
	// No check of this loop may be running in another process: hold the
	// tick lock until the image is replaced, which releases it.
	tick, err := lock.Acquire(a.stateDir)
	if err != nil {
		return // busy: try after the next check
	}
	defer tick.Release()
	if !a.verify(c) || a.ready(lease) != "" {
		return // changed while it was checked: start over after the next check
	}
	fd, restore, err := a.daemonLock.KeepAcrossExec()
	if err != nil {
		refuse("the daemon lock cannot be handed on")
		return
	}
	defer restore()
	thread, generation := "", uint64(0)
	if lease != nil {
		thread, generation = lease.thread, lease.generation
	}
	payload := adoptPayload{Config: a.cfgPath, Loop: a.loop, StateDir: a.stateDir, Mode: a.mode, LockFD: uint64(fd), From: a.from, FromSHA: from.SHA256, FromLabel: from.Label,
		ExpectSHA: c.sha, OwnerThread: thread, OwnerGeneration: generation, ExpectedStateDir: a.expectedStateDir, ConfigArg: adoptConfigArg()}
	if adoptConfigArg() {
		payload.DaemonStartedAt, payload.DaemonLastPassStartedAt, payload.DaemonLastPassAt = optionalTime(a.monitor.StartedAt()), optionalTime(a.monitor.LastPassStartedAt()), optionalTime(a.monitor.LastPassAt())
		payload.DiskEpisode = a.disk.episode()
	}
	a.note(adoptRecord{Outcome: adoptSwitching, Mode: a.mode, From: from, To: to})
	err = handover(payload, a.launch, probe.ConfigArg && adoptConfigArg(), func(argv []string, keep []*os.File) error {
		if a.lastMoment != nil {
			a.lastMoment()
		}
		if !a.verify(c) { // as close to the exec as it can be
			return errAdoptChanged
		}
		return a.exec(c, argv, os.Environ(), keep)
	})
	// Reached only when the image was not replaced.
	if errors.Is(err, errAdoptChanged) {
		// Another file arrived at the last moment: nothing ran; look at
		// it after the next check.
		a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: adoptChangedReason})
		return
	}
	a.skip[c.sha] = true
	a.note(adoptRecord{Outcome: adoptRefused, Mode: a.mode, From: from, To: to, Reason: adoptStartReason})
}

// The reasons a switch is refused that a reader tells apart; see
// adoptRefusalCauses in build_freshness.go, which says what each means for a
// person.
const (
	adoptTakeOverReason    = "the installed build cannot take over a running daemon; restart supervision to use it"
	adoptStateFormatReason = "the installed build is older than this loop's state format; going back needs the documented stop, restore and start"
	adoptValidateReason    = "the installed build does not validate this loop's configuration"
	adoptChangedReason     = "the installed build changed just before the switch; it is checked again after the next check"
	adoptStartReason       = "the installed build could not be started; this daemon carries on with its build"
)

const adoptDiskReason = "waiting for room: the volume that holds the state directory is nearly full (below 100 MiB free); the switch is tried again after each check"

const adoptInPlaceReason = "a different build is at the launch path, written over the old file rather than installed by moving a link; restart supervision to use it, or set loop.daemon.adopt_installed_build: true to adopt such builds"

// note saves the record and adds it to runs.jsonl.
func (a *buildAdopter) note(r adoptRecord) {
	r.At = a.now().UTC()
	noteAdoption(a.stateDir, a.loop, r)
}

// noteAdoption saves the record and adds it to runs.jsonl under the loop's
// name ("" when the configuration could not be read).
func noteAdoption(dir, loop string, r adoptRecord) {
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	_ = saveAdoptRecord(dir, r)
	extra := map[string]any{"from": shortDigest(r.From.SHA256), "to": shortDigest(r.To.SHA256)}
	if r.Mode != "" {
		extra["mode"] = r.Mode
	}
	_ = state.Append(dir, state.Event{At: r.At, Loop: loop, Event: "daemon", Reason: "build_" + r.Outcome, Error: r.Reason, Extra: extra})
}

// adoptBuildCommand is the hidden entry a daemon execs into: the probe, or
// the rest of a switch. A switch that cannot finish starting returns to the
// build it came from, once.
func adoptBuildCommand(e env, args []string) int {
	if len(args) == 1 && args[0] == "--probe" {
		_ = json.NewEncoder(e.stdout).Encode(adoptProbe{Protocol: adoptProtocol, StateFormat: adoptStateFormat, Version: version, ConfigArg: adoptConfigArg()})
		return exitOK
	}
	// The configuration path after the descriptor is there for the process
	// list only: what the build runs is the payload's.
	if !(len(args) == 2 || adoptConfigArg() && len(args) == 4 && args[2] == "-c" && filepath.IsAbs(args[3])) || args[0] != "--handover-fd" {
		return exitUsage
	}
	n, err := strconv.ParseUint(args[1], 10, 31)
	if err != nil {
		return exitUsage
	}
	f := os.NewFile(uintptr(n), "handover")
	if f == nil {
		return exitUsage
	}
	b, err := io.ReadAll(io.LimitReader(f, adoptPayloadLimit+1))
	f.Close()
	var p adoptPayload
	if err != nil || len(b) > adoptPayloadLimit || json.Unmarshal(b, &p) != nil || !p.valid() {
		e.errf("adopt build: no handover")
		return exitUsage
	}
	return p.run(e)
}

func (p adoptPayload) valid() bool {
	return filepath.IsAbs(p.Config) && filepath.IsAbs(p.StateDir) && filepath.IsAbs(p.From) && p.FromSHA != "" && p.ExpectSHA != "" && p.LockFD <= 1<<30
}

// adoptReturnConfigWait is how long a returning build waits for a
// configuration that does not load, as an edit in progress; tests shorten it.
var adoptReturnConfigWait = 30 * time.Second

func (p adoptPayload) run(e env) int {
	if p.Mode == "background" {
		// As in the daemon start --background launched: its output goes
		// nowhere once it has started.
		e.stdout, e.stderr = io.Discard, io.Discard
	}
	// The new build checks what it needs, and returns to the earlier build
	// on any failure before supervising begins. The earlier build, coming
	// back, takes up its loop as it was: it does not refuse what it rode
	// through before the switch.
	returning := p.Returning != ""
	cfg, err := config.Load(p.Config)
	for deadline := time.Now().Add(adoptReturnConfigWait); err != nil && returning && time.Now().Before(deadline); {
		time.Sleep(time.Second)
		cfg, err = config.Load(p.Config)
	}
	if err != nil {
		return p.back(e, "the configuration could not be loaded by the new build")
	}
	if p.Loop == "" {
		p.Loop = cfg.Loop.Name // an earlier build's handover does not carry it
	}
	if cfg.Loop.StateDir != p.StateDir {
		if !returning {
			return p.back(e, "the configuration's state_dir changed; restart the daemon deliberately to apply it")
		}
		keepStateDir(cfg, p.StateDir)
	}
	if p.ExpectedStateDir != "" && !returning {
		if actual, err := canonicalFuturePath(cfg.Loop.StateDir); err != nil || actual != p.ExpectedStateDir {
			return p.back(e, "the state directory changed; remove and reinstall the service explicitly")
		}
	}
	if processExecutableImage == nil || processExecutableImage.Digest() != p.ExpectSHA {
		return p.back(e, "the installed build changed during the switch")
	}
	lease := &ownerLease{thread: p.OwnerThread, generation: p.OwnerGeneration}
	if err := checkOwnership(p.StateDir, lease); err != nil {
		// As at any check: the daemon has handed off and exits.
		noteAdoption(p.StateDir, p.Loop, adoptRecord{Outcome: adoptHandedOff, Mode: p.Mode, From: p.fromID(), To: p.toID(), Reason: "the loop's owner changed during the switch; the daemon exited as it does at any check"})
		return ownershipError(e, err)
	}
	if !returning {
		if err := checkTransferSources(cfg, false); err != nil {
			return p.back(e, "a transfer of the loop's sources is pending")
		}
	}
	daemonLock, err := lock.Inherit(uintptr(p.LockFD), filepath.Join(p.StateDir, lock.Daemon+".lock"))
	if err != nil {
		// The descriptor is still open, so the lock stays held for the
		// earlier build.
		return p.back(e, "the daemon lock was not handed over")
	}
	if returning {
		noteAdoption(p.StateDir, p.Loop, adoptRecord{Outcome: adoptReturned, Mode: p.Mode, From: p.fromID(), To: adoptBuildID{Label: version, SHA256: p.ExpectSHA}, Reason: p.Returning})
	} else {
		noteAdoption(p.StateDir, p.Loop, adoptRecord{Outcome: adoptAdopted, Mode: p.Mode, From: p.fromID(), To: p.toID()})
	}
	monitor, err := daemoninfo.Start(p.StateDir, version, false)
	if err != nil {
		e.errf("warning: live daemon diagnostics unavailable; supervision will continue")
	} else {
		defer monitor.Close()
		// The same process goes on: its start and last pass are the
		// earlier build's, not this switch.
		if adoptConfigArg() {
			monitor.Continue(timeOrZero(p.DaemonStartedAt), timeOrZero(p.DaemonLastPassStartedAt), timeOrZero(p.DaemonLastPassAt))
		}
	}
	defer daemonLock.Release()
	a := newBuildAdopter(p.Config, p.StateDir, p.Mode, p.ExpectedStateDir, daemonLock)
	if adoptConfigArg() { // the earlier form never read it
		a.diskSeed = p.DiskEpisode.watch()
	}
	return daemonLoop(e, cfg, p.Config, false, lease, monitor, a)
}

// keepStateDir is what a returning build does with a configuration that
// names another state directory, as the earlier daemon did: it keeps its
// own, and does no provider work on that configuration until a restart.
func keepStateDir(cfg *config.Config, dir string) {
	cfg.Loop.StateDir = dir
	cfg.MarkSourcesStale()
}

func (p adoptPayload) fromID() adoptBuildID {
	return adoptBuildID{Label: dockBuildLabel(p.FromLabel), SHA256: p.FromSHA}
}

func (p adoptPayload) toID() adoptBuildID {
	return adoptBuildID{Label: version, SHA256: p.ExpectSHA}
}

// back returns to the build the daemon came from, which still holds the
// daemon lock through the inherited descriptor. A return that fails, or a
// second one, leaves no supervisor: recorded, and shown by status and
// doctor.
func (p adoptPayload) back(e env, reason string) int {
	if p.Returning != "" {
		return p.giveUp(e, reason)
	}
	earlier, why := openEarlierBuild(p.From, p.FromSHA)
	if earlier == nil {
		return p.giveUp(e, reason+"; the earlier build cannot be returned to: "+why)
	}
	defer earlier.close()
	self := p.From
	if processExecutableImage != nil {
		self = processExecutableImage.Path
	}
	argv0 := self
	if len(os.Args) > 0 && filepath.IsAbs(os.Args[0]) {
		argv0 = os.Args[0] // the launch path, kept for the next install
	}
	r := p
	r.From, r.FromSHA, r.FromLabel, r.ExpectSHA, r.Returning, r.ConfigArg = self, p.ExpectSHA, version, p.FromSHA, reason, adoptConfigArg()
	_ = handover(r, argv0, p.ConfigArg && adoptConfigArg(), func(argv []string, keep []*os.File) error {
		return execAdoptCandidate(earlier, argv, os.Environ(), keep)
	})
	return p.giveUp(e, reason+"; returning to the earlier build failed")
}

// openEarlierBuild opens the build a switch came from, for a return, after
// the same checks as any build: its contents, its owner and mode, its
// directories.
func openEarlierBuild(path, sha string) (*adoptCandidate, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "its file is gone"
	}
	c := &adoptCandidate{path: path, file: f, sha: sha}
	if got, err := adoptDigestFile(f); err != nil || got != sha {
		c.close()
		return nil, "its file changed"
	}
	if reason := adoptFileRefusal(f); reason != "" {
		c.close()
		return nil, reason
	}
	if reason := adoptDirsRefusal([]string{filepath.Dir(path)}); reason != "" {
		c.close()
		return nil, reason
	}
	return c, ""
}

func (p adoptPayload) giveUp(e env, reason string) int {
	noteAdoption(p.StateDir, p.Loop, adoptRecord{Outcome: adoptFailed, Mode: p.Mode, From: p.fromID(), To: p.toID(), Reason: reason + "; no supervisor is running, start supervision again"})
	e.errf("adopt build: %s", reason)
	return exitFailed
}

// launchExecutable is the path this command was started by, kept as given,
// so that a link to the installed build stays a link, when it leads to this
// process's own file; otherwise the process's own file. A daemon started
// by that path can follow a new build installed there (some systems report
// only the link's target as the process's file).
func launchExecutable() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if len(os.Args) == 0 || os.Args[0] == "" {
		return self, nil
	}
	arg := os.Args[0]
	var p string
	switch {
	case filepath.IsAbs(arg):
		p = arg
	case filepath.Base(arg) != arg:
		p, _ = filepath.Abs(arg)
	default:
		if found, err := exec.LookPath(arg); err == nil {
			p, _ = filepath.Abs(found)
		}
	}
	if p == "" {
		return self, nil
	}
	a, err1 := os.Stat(p)
	b, err2 := os.Stat(self)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		return self, nil
	}
	return p, nil
}

// followMode is what the daemon publishes about following new installs
// (daemoninfo.Snapshot.Adoption).
func (a *buildAdopter) followMode(cfg *config.Config) string {
	switch {
	case a == nil:
		return "unavailable: this daemon runs in dry-run mode"
	case a.off != "":
		return "unavailable: " + a.off
	}
	switch setting := cfg.Loop.Daemon.AdoptInstalledBuild; {
	case setting == nil:
		return "moved_link"
	case *setting:
		return "any_install"
	default:
		return "off"
	}
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// watch gives the adopter the daemon's live diagnostics, whose start and
// last pass a switch hands on.
func (a *buildAdopter) watch(m *daemoninfo.Server) {
	if a != nil {
		a.monitor = m
	}
}

// adoptEarlierForm, set at link time (-X main.adoptEarlierForm=1) and only
// by the tests, makes this build speak the handover as builds from before
// the configuration argument did: its probe answer has no configArg, it
// accepts exactly `--handover-fd N`, hands only that on, and neither sends
// nor reads the daemon's start, last pass and low-disk episode. It stands in
// for a released earlier build in a switch in either direction.
var adoptEarlierForm string

func adoptConfigArg() bool { return adoptEarlierForm == "" }
