//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
)

type adoptWorld struct {
	dir, launch, installed string
	daemonLock             *lock.Lock
	a                      *buildAdopter
	runs                   [][]string
	execs                  [][]string
	payloads               []adoptPayload
	probe                  string
	validateCode           int
	execErr                error
}

// newAdoptWorld is a daemon whose launch path is a link to an installed
// file that is not the running test binary.
func newAdoptWorld(t *testing.T) *adoptWorld {
	t.Helper()
	if processExecutableImage == nil {
		t.Skip("running image unidentified")
	}
	w := &adoptWorld{dir: t.TempDir(), probe: `{"protocol":1,"stateFormat":3,"version":"v9.0.0-dev.fictional2"}`}
	w.installed = filepath.Join(w.dir, "builds", "new", "looptender")
	if err := os.MkdirAll(filepath.Dir(w.installed), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.installed, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	w.launch = filepath.Join(w.dir, "looptender")
	if err := os.Symlink(w.installed, w.launch); err != nil {
		t.Fatal(err)
	}
	l, err := lock.AcquireNamed(w.dir, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Release() })
	w.daemonLock = l
	w.a = newBuildAdopter(filepath.Join(w.dir, "looptender.yaml"), w.dir, "service", "/fictional/state", l)
	if w.a.off != "" {
		t.Fatalf("off: %s", w.a.off)
	}
	w.a.launch = w.launch
	w.a.now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	// The test's temporary directory may sit under a shared one; the
	// directory checks have their own test.
	w.a.dirs = func([]string) string { return "" }
	// The loop's own readiness has its own test.
	w.a.ready = func(*ownerLease) string { return "" }
	w.a.run = func(_ context.Context, c *adoptCandidate, args ...string) ([]byte, int, error) {
		w.runs = append(w.runs, append([]string{c.path}, args...))
		if args[0] == adoptCommandName {
			if w.probe == "" {
				return nil, 2, nil
			}
			return []byte(w.probe), 0, nil
		}
		return nil, w.validateCode, nil
	}
	w.a.exec = func(c *adoptCandidate, argv, env []string, keep []*os.File) error {
		w.execs = append(w.execs, append([]string{c.path}, argv...))
		var p adoptPayload
		if len(keep) != 1 || json.NewDecoder(keep[0]).Decode(&p) != nil {
			t.Error("no handover on the inherited pipe")
		}
		w.payloads = append(w.payloads, p)
		return w.execErr
	}
	return w
}

func (w *adoptWorld) record(t *testing.T) *adoptRecord {
	t.Helper()
	r, err := loadAdoptRecord(w.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func installedDigest(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sha, err := adoptDigestFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func TestAdoptionSwitchesToTheValidatedInstalledBuild(t *testing.T) {
	w := newAdoptWorld(t)
	w.execErr = errors.New("fictional exec failure")
	lease := &ownerLease{thread: "thr_fictional", generation: 4}
	w.a.atSafePoint(context.Background(), &config.Config{}, lease)
	resolved, _ := filepath.EvalSymlinks(w.installed)
	if len(w.runs) != 2 || w.runs[0][0] != resolved || w.runs[0][1] != adoptCommandName || w.runs[0][2] != "--probe" || !slices.Equal(w.runs[1][1:], []string{"validate", "-c", w.a.cfgPath}) {
		t.Fatalf("runs: %v", w.runs)
	}
	if len(w.execs) != 1 {
		t.Fatalf("execs: %v", w.execs)
	}
	argv := w.execs[0]
	// Nothing of the loop in the process list: only the descriptor.
	if argv[0] != resolved || len(argv) != 5 || argv[1] != w.launch || argv[2] != adoptCommandName || argv[3] != "--handover-fd" {
		t.Fatalf("exec: %v", argv)
	}
	for _, private := range []string{"thr_fictional", w.a.cfgPath, w.dir} {
		if strings.Contains(strings.Join(argv[1:], " "), private) && private != w.dir {
			t.Fatalf("%s in argv %v", private, argv)
		}
	}
	p := w.payloads[0]
	sha := installedDigest(t, w.installed)
	if p.ExpectSHA != sha || p.FromSHA != processExecutableImage.Digest() || p.OwnerThread != "thr_fictional" || p.OwnerGeneration != 4 ||
		p.ExpectedStateDir != "/fictional/state" || p.Mode != "service" || p.Config != w.a.cfgPath || p.StateDir != w.dir || p.Returning != "" {
		t.Fatalf("payload: %+v", p)
	}
	// The exec failed: the daemon carries on, holding its lock, and says so.
	if r := w.record(t); r == nil || r.Outcome != adoptRefused || !strings.Contains(r.Reason, "could not be started") || r.To.Label != "v9.0.0-dev.fictional2" {
		t.Fatalf("record: %+v", r)
	}
	if held, err := lock.Held(w.dir, lock.Daemon); err != nil || !held {
		t.Fatal("daemon lock released")
	}
	if tickHeld, _ := lock.Held(w.dir, lock.Tick); tickHeld {
		t.Fatal("tick lock kept")
	}
	b, _ := os.ReadFile(filepath.Join(w.dir, state.LogName))
	if !strings.Contains(string(b), `"reason":"build_switching"`) || !strings.Contains(string(b), `"reason":"build_refused"`) {
		t.Fatalf("runs.jsonl: %s", b)
	}
	// Refused once, not probed again at the next check.
	w.a.atSafePoint(context.Background(), &config.Config{}, lease)
	if len(w.runs) != 2 || len(w.execs) != 1 {
		t.Fatal("refused build tried again")
	}
	// A daemon started later reads the refusal and does not try it either.
	again := newBuildAdopter(w.a.cfgPath, w.dir, "service", "", w.daemonLock)
	if !again.skip[sha] {
		t.Fatal("recorded refusal not carried over")
	}
}

// A build that could not start and returned is not tried again by the
// build it returned to: no A, B, A, B.
func TestReturnedBuildIsNotTriedAgain(t *testing.T) {
	w := newAdoptWorld(t)
	sha := installedDigest(t, w.installed)
	noteAdoption(w.dir, adoptRecord{Outcome: adoptReturned, From: adoptBuildID{SHA256: sha}, To: adoptBuildID{SHA256: processExecutableImage.Digest()}, Reason: "fictional"})
	a := newBuildAdopter(w.a.cfgPath, w.dir, "service", "", w.daemonLock)
	if !a.skip[sha] || a.skip[processExecutableImage.Digest()] {
		t.Fatalf("skip: %v", a.skip)
	}
	a.launch, a.run, a.exec, a.dirs = w.launch, w.a.run, w.a.exec, w.a.dirs
	a.atSafePoint(context.Background(), &config.Config{}, nil)
	if len(w.runs) != 0 || len(w.execs) != 0 {
		t.Fatal("the build that returned was tried again")
	}
}

func TestAdoptionRefusals(t *testing.T) {
	uid := os.Getuid()
	for name, c := range map[string]struct {
		setup  func(*testing.T, *adoptWorld)
		reason string
		runs   int
	}{
		"no handover":        {func(_ *testing.T, w *adoptWorld) { w.probe = "" }, "cannot take over a running daemon", 1},
		"other protocol":     {func(_ *testing.T, w *adoptWorld) { w.probe = `{"protocol":2,"stateFormat":3}` }, "cannot take over a running daemon", 1},
		"older state format": {func(_ *testing.T, w *adoptWorld) { w.probe = `{"protocol":1,"stateFormat":2}` }, "older than this loop's state format", 1},
		"does not validate":  {func(_ *testing.T, w *adoptWorld) { w.validateCode = 2 }, "does not validate", 2},
		"writable by others": {func(_ *testing.T, w *adoptWorld) { os.Chmod(w.installed, 0o777) }, "changed by other users", 0},
		"not executable":     {func(_ *testing.T, w *adoptWorld) { os.Chmod(w.installed, 0o600) }, "not executable", 0},
		"another user's file": {func(t *testing.T, w *adoptWorld) {
			saved := adoptUID
			adoptUID = func() int { return uid + 1 }
			t.Cleanup(func() { adoptUID = saved })
		}, "not owned by this user", 0},
		"untrusted directory": {func(_ *testing.T, w *adoptWorld) {
			w.a.dirs = func([]string) string { return "a directory of the launch path can be written by other users" }
		}, "can be written by other users", 0},
	} {
		t.Run(name, func(t *testing.T) {
			w := newAdoptWorld(t)
			c.setup(t, w)
			w.a.atSafePoint(context.Background(), &config.Config{}, nil)
			if len(w.execs) != 0 || len(w.runs) != c.runs {
				t.Fatalf("runs %v execs %v", w.runs, w.execs)
			}
			if r := w.record(t); r == nil || r.Outcome != adoptRefused || !strings.Contains(r.Reason, c.reason) || r.From.SHA256 != processExecutableImage.Digest() {
				t.Fatalf("record: %+v", r)
			}
		})
	}
}

// By default only a build installed by moving a link is adopted; a file
// written over in place is reported. Set to true, both are; false, neither.
func TestAdoptionDefaultFollowsOnlyAMovedLink(t *testing.T) {
	on, off := true, false
	inPlace := func(t *testing.T) *adoptWorld {
		w := newAdoptWorld(t)
		// The launch path is the file itself.
		os.Remove(w.launch)
		if err := os.Rename(w.installed, w.launch); err != nil {
			t.Fatal(err)
		}
		w.installed = w.launch
		// The daemon was started from that file, which was then written
		// over at the same path.
		w.a.from, _ = filepath.EvalSymlinks(w.launch)
		return w
	}
	t.Run("in place, default", func(t *testing.T) {
		w := inPlace(t)
		w.a.atSafePoint(context.Background(), &config.Config{}, nil)
		if len(w.runs) != 0 || len(w.execs) != 0 {
			t.Fatal("adopted a file written over in place")
		}
		if r := w.record(t); r == nil || r.Outcome != adoptRefused || !strings.Contains(r.Reason, "adopt_installed_build: true") {
			t.Fatalf("record: %+v", r)
		}
		os.Remove(filepath.Join(w.dir, adoptRecordName))
		w.a.atSafePoint(context.Background(), &config.Config{}, nil)
		if w.record(t) != nil {
			t.Fatal("reported again at the next check")
		}
		// Not refused for good: setting true adopts it.
		cfg := &config.Config{}
		cfg.Loop.Daemon.AdoptInstalledBuild = &on
		w.a.atSafePoint(context.Background(), cfg, nil)
		if len(w.execs) != 1 {
			t.Fatal("explicit true did not adopt")
		}
	})
	t.Run("link, explicit false", func(t *testing.T) {
		w := newAdoptWorld(t)
		cfg := &config.Config{}
		cfg.Loop.Daemon.AdoptInstalledBuild = &off
		w.a.atSafePoint(context.Background(), cfg, nil)
		if len(w.runs) != 0 || w.record(t) != nil {
			t.Fatal("false still looked")
		}
	})
	t.Run("link, default", func(t *testing.T) {
		w := newAdoptWorld(t)
		w.a.atSafePoint(context.Background(), &config.Config{}, nil)
		if len(w.execs) != 1 {
			t.Fatal("a moved link was not adopted by default")
		}
	})
}

func TestAdoptionWaitsOrStaysWithoutRecording(t *testing.T) {
	for name, c := range map[string]func(*testing.T, *adoptWorld) context.Context{
		"same file": func(t *testing.T, w *adoptWorld) context.Context {
			os.Remove(w.launch)
			if err := os.Symlink(processExecutableImage.Path, w.launch); err != nil {
				t.Fatal(err)
			}
			return context.Background()
		},
		"nothing installed": func(t *testing.T, w *adoptWorld) context.Context {
			os.Remove(w.installed)
			return context.Background()
		},
		"shutting down": func(t *testing.T, w *adoptWorld) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		},
		"check running elsewhere": func(t *testing.T, w *adoptWorld) context.Context {
			tick, err := lock.Acquire(w.dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tick.Release() })
			return context.Background()
		},
		"changed after its checks": func(t *testing.T, w *adoptWorld) context.Context {
			run := w.a.run
			w.a.run = func(ctx context.Context, c *adoptCandidate, args ...string) ([]byte, int, error) {
				if args[0] == "validate" {
					// Another file put in place while it was validated.
					os.Remove(w.installed)
					os.WriteFile(w.installed, []byte("#!/bin/sh\nexit 1\n"), 0o700)
				}
				return run(ctx, c, args...)
			}
			return context.Background()
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newAdoptWorld(t)
			ctx := c(t, w)
			w.a.atSafePoint(ctx, &config.Config{}, nil)
			if len(w.execs) != 0 {
				t.Fatal("switched")
			}
			if r := w.record(t); r != nil {
				t.Fatalf("recorded %+v", r)
			}
			if len(w.a.skip) != 0 {
				t.Fatal("refused for good")
			}
		})
	}
}

func TestAdoptDirectoriesMustBeThisUsersAndClosed(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, mode os.FileMode) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, mode); err != nil {
			t.Fatal(err)
		}
		return d
	}
	closed := mk("closed", 0o755)
	open := mk("open", 0o777)
	sticky := mk("sticky", 0o1777)
	group := mk("group", 0o775)
	below := filepath.Join(open, "inside")
	if err := os.Mkdir(below, 0o700); err != nil {
		t.Fatal(err)
	}
	if r := adoptDirsRefusalUpTo([]string{closed}, root); r != "" {
		t.Fatalf("closed: %s", r)
	}
	for name, d := range map[string]string{"open": open, "sticky": sticky, "group": group, "below an open one": below} {
		if r := adoptDirsRefusalUpTo([]string{closed, d}, root); !strings.Contains(r, "written by other users") {
			t.Errorf("%s: %q", name, r)
		}
	}
	saved := adoptUID
	adoptUID = func() int { return os.Getuid() + 1 }
	defer func() { adoptUID = saved }()
	if r := adoptDirsRefusalUpTo([]string{closed}, root); !strings.Contains(r, "another user") {
		t.Fatalf("another user's directory: %q", r)
	}
}

// resolveLaunch reports each directory holding a link of the chain.
func TestResolveLaunchReportsEveryDirectory(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	builds := filepath.Join(root, "builds", "x")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{builds, bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(builds, "looptender"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("builds", "x"), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "current", "looptender"), filepath.Join(bin, "looptender")); err != nil {
		t.Fatal(err)
	}
	target, dirs, viaLink, err := resolveLaunch(filepath.Join(bin, "looptender"))
	if err != nil || target != filepath.Join(builds, "looptender") || !viaLink || !slices.Contains(dirs, bin) || !slices.Contains(dirs, builds) {
		t.Fatalf("%s %v %v %v", target, dirs, viaLink, err)
	}
	if _, _, viaLink, _ := resolveLaunch(filepath.Join(builds, "looptender")); viaLink {
		t.Fatal("a plain file reported as a link")
	}
	// A link to a directory: the directory holding it is reported too.
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(elsewhere, "bin")); err != nil {
		t.Fatal(err)
	}
	target, dirs, viaLink, err = resolveLaunch(filepath.Join(elsewhere, "bin", "looptender"))
	if err != nil || target != filepath.Join(builds, "looptender") || !viaLink || !slices.Contains(dirs, elsewhere) || !slices.Contains(dirs, bin) {
		t.Fatalf("%s %v %v %v", target, dirs, viaLink, err)
	}
}

// handedOver runs a payload as the new image would, with execImage stood
// in for; it returns what was execed and the payload handed on.
func handedOver(t *testing.T, p adoptPayload) (int, [][]string, []adoptPayload) {
	t.Helper()
	var execs [][]string
	var payloads []adoptPayload
	saved := execImage
	t.Cleanup(func() { execImage = saved })
	execImage = func(path string, argv, env []string) error {
		execs = append(execs, append([]string{path}, argv...))
		n, _ := strconv.Atoi(argv[len(argv)-1])
		// A duplicate: the descriptor itself belongs to the code under test,
		// and an *os.File on it would close it when collected.
		dup, err := syscall.Dup(n)
		if err != nil {
			t.Fatal(err)
		}
		f := os.NewFile(uintptr(dup), "handover")
		defer f.Close()
		var q adoptPayload
		if err := json.NewDecoder(f).Decode(&q); err != nil {
			t.Errorf("handover: %v", err)
		}
		payloads = append(payloads, q)
		return errors.New("fictional exec failure")
	}
	code := p.run(env{context.Background(), io.Discard, io.Discard})
	return code, execs, payloads
}

// returnWorld is a new image about to take over: a loop, its daemon lock
// held and handed on, and an earlier build on disk to return to.
type returnWorld struct {
	f       *doctorFixture
	p       adoptPayload
	earlier string
	fd      uintptr
}

func newReturnWorld(t *testing.T) *returnWorld {
	t.Helper()
	if processExecutableImage == nil {
		t.Skip("running image unidentified")
	}
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := lock.AcquireNamed(f.state, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Release() })
	fd, restore, err := l.KeepAcrossExec()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	earlier := filepath.Join(adoptTestDir(t), "looptender")
	if err := os.WriteFile(earlier, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	w := &returnWorld{f: f, earlier: earlier, fd: fd}
	w.p = adoptPayload{Config: f.config, StateDir: f.state, Mode: "service", LockFD: uint64(fd), From: earlier,
		FromSHA: installedDigest(t, earlier), FromLabel: "fixture-old", ExpectSHA: processExecutableImage.Digest()}
	return w
}

func (w *returnWorld) record(t *testing.T) *adoptRecord {
	t.Helper()
	r, err := loadAdoptRecord(w.f.state)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Every failure of the new build's own start checks returns to the
// earlier build, after checking that build's file, holding the lock
// throughout; once only.
func TestNewBuildReturnsOnEveryStartFailure(t *testing.T) {
	for name, c := range map[string]struct {
		change func(*testing.T, *returnWorld)
		reason string
	}{
		"configuration unreadable": {func(t *testing.T, w *returnWorld) { os.WriteFile(w.f.config, []byte("{"), 0o600) }, "could not be loaded"},
		"state_dir changed": {func(t *testing.T, w *returnWorld) {
			w.f.loop["state_dir"] = filepath.Join(w.f.root, "elsewhere")
			w.f.save(t)
		}, "state_dir changed"},
		"service directory moved": {func(t *testing.T, w *returnWorld) { w.p.ExpectedStateDir = "/fictional/bound" }, "remove and reinstall"},
		"file changed":            {func(t *testing.T, w *returnWorld) { w.p.ExpectSHA = strings.Repeat("b", 64) }, "changed during the switch"},
		"transfer pending": {func(t *testing.T, w *returnWorld) {
			if err := control.Save(w.f.state, control.File{SupersededBy: "fictional-successor"}); err != nil {
				t.Fatal(err)
			}
		}, "transfer"},
		"lock not handed over": {func(t *testing.T, w *returnWorld) {
			// A descriptor on another file: refused, and left open.
			other, err := os.Open(w.f.config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { other.Close() })
			w.p.LockFD = uint64(other.Fd())
		}, "lock was not handed over"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newReturnWorld(t)
			c.change(t, w)
			code, execs, payloads := handedOver(t, w.p)
			if code != exitFailed || len(execs) != 1 || execs[0][2] != adoptCommandName {
				r, err := loadAdoptRecord(w.f.state)
				runs, _ := os.ReadFile(filepath.Join(w.f.state, state.LogName))
				t.Fatalf("return: %d %v %+v %v\nruns: %s", code, execs, r, err, runs)
			}
			if runtime.GOOS == "linux" {
				if !strings.HasPrefix(execs[0][0], "/proc/self/fd/") {
					t.Fatalf("return not through the checked descriptor: %v", execs[0])
				}
			} else if execs[0][0] != w.earlier {
				t.Fatalf("returned to %s", execs[0][0])
			}
			q := payloads[0]
			if !strings.Contains(q.Returning, c.reason) || q.ExpectSHA != w.p.FromSHA || q.LockFD != w.p.LockFD || q.StateDir != w.f.state || q.Mode != "service" {
				t.Fatalf("handed back: %+v", q)
			}
			// The return failed too (stood in): recorded as no supervisor.
			if r := w.record(t); r.Outcome != adoptFailed || !strings.Contains(r.Reason, "returning to the earlier build failed") {
				t.Fatalf("record: %+v", r)
			}
			if held, _ := lock.Held(w.f.state, lock.Daemon); !held {
				t.Fatal("lock lost")
			}
		})
	}
}

// The return checks the earlier build's file first, as any build.
func TestReturnRefusesAChangedEarlierBuild(t *testing.T) {
	w := newReturnWorld(t)
	w.p.ExpectSHA = strings.Repeat("b", 64)
	if err := os.WriteFile(w.earlier, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, execs, _ := handedOver(t, w.p)
	if code != exitFailed || len(execs) != 0 {
		t.Fatalf("ran a changed earlier build: %d %v", code, execs)
	}
	if r := w.record(t); r.Outcome != adoptFailed || !strings.Contains(r.Reason, "its file changed") {
		t.Fatalf("record: %+v", r)
	}
}

// The earlier build, coming back, takes up its loop as it was: it does not
// fail on what it rode through before the switch. The daemon loop is
// entered and, its context ended, returns at once.
func TestReturningBuildTakesUpItsLoop(t *testing.T) {
	for name, change := range map[string]func(*testing.T, *returnWorld){
		"state_dir changed": func(t *testing.T, w *returnWorld) {
			w.f.loop["state_dir"] = filepath.Join(w.f.root, "elsewhere")
			w.f.save(t)
		},
		"transfer pending": func(t *testing.T, w *returnWorld) {
			if err := control.Save(w.f.state, control.File{SupersededBy: "fictional-successor"}); err != nil {
				t.Fatal(err)
			}
		},
		"service binding": func(t *testing.T, w *returnWorld) { w.p.ExpectedStateDir = "/fictional/bound" },
	} {
		t.Run(name, func(t *testing.T) {
			w := newReturnWorld(t)
			change(t, w)
			w.p.Returning = "fictional reason"
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if code := w.p.run(env{ctx, io.Discard, io.Discard}); code != exitOK {
				t.Fatalf("code %d", code)
			}
			if r := w.record(t); r.Outcome != adoptReturned {
				t.Fatalf("record: %+v", r)
			}
		})
	}
	t.Run("configuration unreadable throughout", func(t *testing.T) {
		saved := adoptReturnConfigWait
		adoptReturnConfigWait = 0
		t.Cleanup(func() { adoptReturnConfigWait = saved })
		w := newReturnWorld(t)
		os.WriteFile(w.f.config, []byte("{"), 0o600)
		w.p.Returning = "fictional reason"
		code, execs, _ := handedOver(t, w.p)
		if code != exitFailed || len(execs) != 0 {
			t.Fatalf("%d %v", code, execs)
		}
		if r := w.record(t); r.Outcome != adoptFailed {
			t.Fatalf("record: %+v", r)
		}
	})
}

// An owner change during the switch ends the daemon as at any check: no
// return, no "failed", no alarm.
func TestOwnerChangeDuringTheSwitchHandsOff(t *testing.T) {
	for _, returning := range []string{"", "fictional reason"} {
		w := newReturnWorld(t)
		if err := control.Save(w.f.state, control.File{Ownership: &control.Ownership{ThreadID: "thr_newowner01", Generation: 2}}); err != nil {
			t.Fatal(err)
		}
		w.p.Returning = returning
		code, execs, _ := handedOver(t, w.p)
		if code == exitOK || len(execs) != 0 {
			t.Fatalf("%d %v", code, execs)
		}
		r := w.record(t)
		if r.Outcome != adoptHandedOff {
			t.Fatalf("record: %+v", r)
		}
		if s := describeBuildAdoption(w.f.state, daemonStatus{State: "not_recorded"}, time.Now().Add(time.Hour)); s == nil || !s.Supervised {
			t.Fatalf("status alarms on a hand-off: %+v", s)
		}
	}
}

// The daemon does not switch while its loop could not be handed over: a
// configuration that does not load or names another state_dir, a pending
// transfer, a new owner. Nothing is refused for good.
func TestAdoptionWaitsWhileTheLoopIsNotReady(t *testing.T) {
	for name, c := range map[string]struct {
		change func(*testing.T, *doctorFixture) *ownerLease
		noted  bool
	}{
		"configuration does not load": {func(t *testing.T, f *doctorFixture) *ownerLease {
			os.WriteFile(f.config, []byte("{"), 0o600)
			return nil
		}, false},
		"state_dir changed": {func(t *testing.T, f *doctorFixture) *ownerLease {
			f.loop["state_dir"] = filepath.Join(f.root, "elsewhere")
			f.save(t)
			return nil
		}, true},
		"transfer pending": {func(t *testing.T, f *doctorFixture) *ownerLease {
			if err := control.Save(f.state, control.File{SupersededBy: "fictional-successor"}); err != nil {
				t.Fatal(err)
			}
			return nil
		}, false},
		"owner changed": {func(t *testing.T, f *doctorFixture) *ownerLease {
			if err := control.Save(f.state, control.File{Ownership: &control.Ownership{ThreadID: "thr_newowner01", Generation: 2}}); err != nil {
				t.Fatal(err)
			}
			return &ownerLease{thread: "thr_oldowner01", generation: 1}
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newAdoptWorld(t)
			f := newDoctorFixture(t)
			if err := os.MkdirAll(f.state, 0o700); err != nil {
				t.Fatal(err)
			}
			w.a.cfgPath, w.a.stateDir = f.config, f.state
			w.a.ready = w.a.loopReady
			if r := w.a.loopReady(nil); r != "" {
				t.Fatalf("fixture not ready: %s", r)
			}
			lease := c.change(t, f)
			w.a.atSafePoint(context.Background(), &config.Config{}, lease)
			w.a.atSafePoint(context.Background(), &config.Config{}, lease)
			if len(w.runs) != 0 || len(w.execs) != 0 || len(w.a.skip) != 0 {
				t.Fatalf("runs %v execs %v skip %v", w.runs, w.execs, w.a.skip)
			}
			r, err := loadAdoptRecord(f.state)
			if c.noted {
				if err != nil || r.Outcome != adoptRefused || !strings.Contains(r.Reason, "state_dir") {
					t.Fatalf("record: %+v %v", r, err)
				}
			} else if err == nil {
				t.Fatalf("recorded %+v", r)
			}
		})
	}
}

// The loop's readiness is checked again under the tick lock: a change
// between the first look and the switch stops it, refusing nothing.
func TestReadinessIsCheckedAgainUnderTheTickLock(t *testing.T) {
	w := newAdoptWorld(t)
	calls := 0
	w.a.ready = func(*ownerLease) string {
		calls++
		if calls == 1 {
			return ""
		}
		if held, _ := lock.Held(w.dir, lock.Tick); !held {
			t.Error("second readiness check without the tick lock")
		}
		return "the loop's owner changed"
	}
	w.a.atSafePoint(context.Background(), &config.Config{}, nil)
	if calls != 2 || len(w.runs) != 2 || len(w.execs) != 0 || len(w.a.skip) != 0 || w.record(t) != nil {
		t.Fatalf("calls %d runs %v execs %v skip %v record %+v", calls, w.runs, w.execs, w.a.skip, w.record(t))
	}
}

// A returning build that finds another state_dir keeps its own and does no
// provider work on that configuration.
func TestReturningBuildKeepsItsStateDirWithoutProviderWork(t *testing.T) {
	f := newDoctorFixture(t)
	f.loop["state_dir"] = filepath.Join(f.root, "elsewhere")
	f.save(t)
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.CheckSources(); err != nil {
		t.Fatal(err)
	}
	keepStateDir(cfg, f.state)
	if cfg.Loop.StateDir != f.state || cfg.CheckSources() == nil {
		t.Fatalf("state dir %s; sources verified: %v", cfg.Loop.StateDir, cfg.CheckSources() == nil)
	}
}

// A file that arrives in the last moment before the exec is caught by the
// last check: nothing runs, nothing is refused for good.
func TestLastCheckBeforeTheExec(t *testing.T) {
	w := newAdoptWorld(t)
	w.a.lastMoment = func() {
		os.Remove(w.installed)
		os.WriteFile(w.installed, []byte("#!/bin/sh\nexit 1\n"), 0o700)
	}
	w.a.atSafePoint(context.Background(), &config.Config{}, nil)
	if len(w.execs) != 0 || len(w.a.skip) != 0 {
		t.Fatalf("execs %v skip %v", w.execs, w.a.skip)
	}
	if r := w.record(t); r == nil || !strings.Contains(r.Reason, "changed just before the switch") {
		t.Fatalf("record: %+v", r)
	}
	if held, _ := lock.Held(w.dir, lock.Daemon); !held {
		t.Fatal("daemon lock released")
	}
}

// The new image takes the daemon lock over from the old one, and refuses a
// descriptor that is not that lock.
func TestDaemonLockIsHandedAcrossExec(t *testing.T) {
	dir := t.TempDir()
	l, err := lock.AcquireNamed(dir, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	fd, restore, err := l.KeepAcrossExec()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	other := filepath.Join(dir, "other.lock")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dup, err := dupFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Inherit(dup, other); err == nil {
		t.Fatal("inherited a descriptor as the wrong lock")
	}
	dup, err = dupFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := lock.Inherit(dup, filepath.Join(dir, lock.Daemon+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireNamed(dir, lock.Daemon); !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("lock free after inheriting: %v", err)
	}
	_ = inherited
	if _, err := lock.Inherit(9999, filepath.Join(dir, lock.Daemon+".lock")); err == nil {
		t.Fatal("inherited a closed descriptor")
	}
}

func dupFD(fd uintptr) (uintptr, error) {
	n, err := syscallDup(int(fd))
	return uintptr(n), err
}

func syscallDup(fd int) (int, error) { return syscall.Dup(fd) }
