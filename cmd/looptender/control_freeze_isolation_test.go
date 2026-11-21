//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Both legacy and current names are required: v0.2.0 knows only KEEPGOING_*.
// No inherited environment is used by a frozen executable or its descendants.
func frozenEnvironment(t *testing.T, f *fixture) []string {
	t.Helper()
	values := map[string]string{
		"HOME": filepath.Join(f.dir, "home"), "USERPROFILE": filepath.Join(f.dir, "home"),
		"XDG_CONFIG_HOME": filepath.Join(f.dir, "config-home"), "XDG_STATE_HOME": filepath.Join(f.dir, "state-home"), "XDG_CACHE_HOME": filepath.Join(f.dir, "cache-home"),
		"APPDATA": filepath.Join(f.dir, "appdata"), "LOCALAPPDATA": filepath.Join(f.dir, "local-appdata"),
		"TMPDIR": filepath.Join(f.dir, "tmp"), "TMP": filepath.Join(f.dir, "tmp"), "TEMP": filepath.Join(f.dir, "tmp"),
		"PATH": filepath.Join(f.dir, "bin"), "FAKE_BB": f.bbDir, "BB_THREAD_ID": "", "LANG": "C", "LC_ALL": "C",
	}
	for _, prefix := range []string{"KEEPGOING_", "LOOPTENDER_"} {
		values[prefix+"SESSION_DIR"] = filepath.Join(f.dir, "sessions")
		values[prefix+"REGISTRY_DIR"] = filepath.Join(f.dir, "registry")
		values[prefix+"ROUTES_FILE"] = filepath.Join(f.dir, "routes.yaml")
	}
	for key, value := range values {
		if key == "BB_THREAD_ID" || key == "LANG" || key == "LC_ALL" || strings.HasSuffix(key, "ROUTES_FILE") {
			continue
		}
		if err := os.MkdirAll(value, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// The shell stand-ins need only these utilities. Never put a host provider,
	// BB executable, user bin directory or inherited PATH on the child's PATH.
	for name, target := range map[string]string{"cat": "/bin/cat", "cp": "/bin/cp", "sleep": "/bin/sleep", "touch": "/usr/bin/touch"} {
		path := filepath.Join(f.dir, "bin", name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+values[k])
	}
	return result
}

func newFrozenFixture(t *testing.T) *fixture {
	t.Helper()
	// Also confine in-process setup and testexec's script cache. These are test
	// variables, not a production caller-identity override.
	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP"} {
		path := filepath.Join(root, strings.ToLower(key))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	f := newFixture(t)
	canonical, err := filepath.EvalSymlinks(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.dir = canonical
	f.cfg = filepath.Join(canonical, filepath.Base(f.cfg))
	f.bbDir = filepath.Join(canonical, "bb")
	for _, entry := range frozenEnvironment(t, f) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	return f
}

// Logs are deliberately retained outside t.TempDir, even on success. They
// contain only fictional command output from the allowlisted environment.
type frozenLog struct {
	mu        sync.Mutex
	file      *os.File
	remaining int
	truncated bool
	captured  bytes.Buffer
}

func (l *frozenLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if len(p) > l.remaining {
		l.truncated = true
		p = p[:l.remaining]
	}
	l.remaining -= len(p)
	l.captured.Write(p)
	_, err := l.file.Write(p)
	return n, err
}
func (l *frozenLog) text() string       { l.mu.Lock(); defer l.mu.Unlock(); return l.captured.String() }
func (l *frozenLog) wasTruncated() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.truncated }
func (l *frozenLog) close(t *testing.T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.file.Sync(); err != nil {
		t.Errorf("retained log sync: %v", err)
	}
	if err := l.file.Close(); err != nil {
		t.Errorf("retained log close: %v", err)
	}
}

var frozenHostTemp = os.TempDir()

func frozenWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func frozenCanonical(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != canonical {
		t.Fatal("frozen evidence and executable paths must exist and be canonical absolute paths")
	}
	return canonical
}

// Disjoint writable trees cannot publish replacements over pinned artifacts.
func frozenSeparation(fixture, logs, binary, policy string) error {
	for _, artifact := range []string{binary, policy} {
		info, err := os.Lstat(artifact)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 {
			return fmt.Errorf("artifact must be regular and read-only")
		}
		parent := filepath.Dir(artifact)
		parentInfo, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		if !parentInfo.IsDir() || parentInfo.Mode().Perm()&0222 != 0 {
			return fmt.Errorf("artifact directory must be read-only")
		}
		for _, writable := range []string{fixture, logs} {
			canonical, err := filepath.EvalSymlinks(writable)
			if err != nil || canonical != writable {
				return fmt.Errorf("write root must be canonical")
			}
			artifactCanonical, err := filepath.EvalSymlinks(artifact)
			if err != nil || artifactCanonical != artifact {
				return fmt.Errorf("artifact must be canonical")
			}
			writableInfo, err := os.Stat(writable)
			if err != nil {
				return err
			}
			if os.SameFile(parentInfo, writableInfo) || frozenWithin(parent, writable) || frozenWithin(writable, parent) {
				return fmt.Errorf("artifact directory and writable roots must be disjoint")
			}
		}
	}
	return nil
}

type frozenPathIdentity map[string]os.FileInfo

func frozenIdentities(paths ...string) (frozenPathIdentity, error) {
	result := frozenPathIdentity{}
	for _, path := range paths {
		for {
			info, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlink in pinned path")
			}
			result[path] = info
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
			path = parent
		}
	}
	return result, nil
}
func (ids frozenPathIdentity) recheck() error {
	for path, before := range ids {
		after, err := os.Lstat(path)
		if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
			return fmt.Errorf("pinned path replaced or changed before launch")
		}
	}
	return nil
}

func frozenCommand(t *testing.T, ctx context.Context, f *fixture, args ...string) (*exec.Cmd, *frozenLog) {
	t.Helper()
	binary := os.Getenv("FROZEN_CONTROL_READER")
	logs := os.Getenv("FROZEN_CONTROL_LOG_DIR")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(logs) {
		t.Fatal("frozen fixtures require absolute FROZEN_CONTROL_READER and durable FROZEN_CONTROL_LOG_DIR")
	}
	logs = frozenCanonical(t, logs)
	root := frozenCanonical(t, os.Getenv("FROZEN_CONTROL_EVIDENCE_ROOT"))
	temp, err := filepath.EvalSymlinks(frozenHostTemp)
	if err != nil {
		t.Fatal(err)
	}
	if !frozenWithin(root, logs) || frozenWithin(f.dir, root) || frozenWithin(temp, root) {
		t.Fatal("frozen evidence root must be durable, outside host temporary storage and fixture cleanup roots")
	}
	binary = frozenCanonical(t, binary)
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || !frozenWithin(root, binary) {
		t.Fatal("frozen executable must be a read-only regular artifact within the evidence root")
	}
	identities, err := frozenIdentities(binary, logs, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if expected := os.Getenv("FROZEN_CONTROL_SHA256"); len(expected) != 64 || expected != digest {
		t.Fatal("frozen executable does not match the explicitly reviewed SHA256")
	}
	policy := frozenCanonical(t, os.Getenv("FROZEN_CONTROL_SANDBOX_POLICY"))
	if !frozenWithin(root, policy) {
		t.Fatal("policy must be within evidence root")
	}
	if err := frozenSeparation(f.dir, logs, binary, policy); err != nil {
		t.Fatal(err)
	}
	policyIdentities, err := frozenIdentities(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyRaw, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest := fmt.Sprintf("%x", sha256.Sum256(policyRaw))
	if expected := os.Getenv("FROZEN_CONTROL_POLICY_SHA256"); len(expected) != 64 || expected != policyDigest {
		t.Fatal("containment policy does not match the explicitly reviewed SHA256")
	}
	if runtime.GOOS != "darwin" {
		t.Fatal("frozen replay requires independently reviewed native containment; only Darwin harness is defined")
	}
	log, err := os.CreateTemp(logs, "frozen-*.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(log, "time=%s binary-sha256=%s policy-sha256=%s fixture=%s command=%s\n", time.Now().UTC().Format(time.RFC3339Nano), digest, policyDigest, f.dir, strings.Join(args, " ")); err != nil {
		log.Close()
		t.Fatal(err)
	}
	sink := &frozenLog{file: log, remaining: 64 << 10}
	t.Cleanup(func() { sink.close(t) })
	if err := identities.recheck(); err != nil {
		t.Fatal(err)
	}
	if err := policyIdentities.recheck(); err != nil {
		t.Fatal(err)
	}
	sandboxArgs := []string{"-D", "FIXTURE=" + f.dir, "-D", "EVIDENCE=" + logs, "-D", "BINARY=" + binary, "-f", policy, binary}
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append(sandboxArgs, args...)...)
	cmd.Dir = f.dir
	cmd.Env = frozenEnvironment(t, f)
	cmd.Stdout, cmd.Stderr = sink, sink
	t.Logf("retained frozen output: %s", log.Name())
	return cmd, sink
}

func TestFrozenHarnessEnvironmentIsolation(t *testing.T) {
	// Poison the parent context; none of these values may reach the child.
	t.Setenv("ANTHROPIC_API_KEY", "fictional-secret-sentinel")
	t.Setenv("BB_CLI", "/fictional/unconfigured/bb")
	t.Setenv("HTTPS_PROXY", "http://fictional.invalid")
	f := newFrozenFixture(t)
	environment := frozenEnvironment(t, f)
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"fictional-secret-sentinel", "BB_CLI=", "HTTPS_PROXY=", "FROZEN_CONTROL_READER=", "FROZEN_CONTROL_LOG_DIR="} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("inherited context escaped allowlist")
		}
	}
	for _, prefix := range []string{"KEEPGOING_", "LOOPTENDER_"} {
		for _, suffix := range []string{"SESSION_DIR", "REGISTRY_DIR", "ROUTES_FILE"} {
			if !strings.Contains(joined, prefix+suffix+"="+f.dir+string(os.PathSeparator)) {
				t.Fatal("unconfined storage binding")
			}
		}
	}
	// This executes only /bin/sh, never a frozen binary or provider. It checks
	// both compatibility spellings and default homes with a hostile parent env.
	cmd := exec.Command("/bin/sh", "-c", `test -z "$ANTHROPIC_API_KEY$BB_CLI$HTTPS_PROXY" && test "$KEEPGOING_SESSION_DIR" = "$LOOPTENDER_SESSION_DIR" && test "$KEEPGOING_REGISTRY_DIR" = "$LOOPTENDER_REGISTRY_DIR" && test "$KEEPGOING_ROUTES_FILE" = "$LOOPTENDER_ROUTES_FILE" && test "$PATH" = "$PWD/bin" && test "$HOME" = "$PWD/home" && echo isolated > "$TMPDIR/fixture-proof"`)
	cmd.Dir = f.dir
	cmd.Env = environment
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolation sentinel failed: %v %s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(f.dir, "tmp", "fixture-proof")); err != nil || string(b) != "isolated\n" {
		t.Fatal("missing confined sentinel")
	}
}

func TestFrozenHarnessArtifactSeparation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, logs, artifacts := filepath.Join(root, "fixture"), filepath.Join(root, "logs"), filepath.Join(root, "artifacts")
	for _, p := range []string{fixture, logs, artifacts} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary, policy := filepath.Join(artifacts, "reader"), filepath.Join(artifacts, "policy")
	for _, p := range []string{binary, policy} {
		if err := os.WriteFile(p, []byte("fictional"), 0500); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(artifacts, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(artifacts, 0700) })
	if err := frozenSeparation(fixture, logs, binary, policy); err != nil {
		t.Fatal(err)
	}
	for _, writable := range []string{root, artifacts, filepath.Join(artifacts, "nested")} {
		if err := frozenSeparation(fixture, writable, binary, policy); err == nil {
			t.Fatal("overlap accepted")
		}
		if err := frozenSeparation(writable, logs, binary, policy); err == nil {
			t.Fatal("fixture overlap accepted")
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(logs, alias); err != nil {
		t.Fatal(err)
	}
	if err := frozenSeparation(fixture, alias, binary, policy); err == nil {
		t.Fatal("symlink root accepted")
	}
	if err := os.Chmod(policy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := frozenSeparation(fixture, logs, binary, policy); err == nil {
		t.Fatal("writable policy accepted")
	}
	os.Chmod(policy, 0500)
	ids, err := frozenIdentities(fixture, logs, binary, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := ids.recheck(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(logs, logs+"-old"); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(logs, 0700)
	if err := ids.recheck(); err == nil {
		t.Fatal("native directory replacement accepted")
	}
	ids, err = frozenIdentities(binary)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(artifacts, 0700)
	if err := os.Rename(binary, binary+"-old"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(binary, []byte("fictional"), 0500)
	os.Chmod(artifacts, 0500)
	if err := ids.recheck(); err == nil {
		t.Fatal("same-byte artifact replacement accepted")
	}
}

func TestFrozenLogTruncation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "bounded-log")
	if err != nil {
		t.Fatal(err)
	}
	sink := &frozenLog{file: file, remaining: 3}
	defer sink.close(t)
	if n, err := sink.Write([]byte("ok\n")); err != nil || n != 3 || sink.wasTruncated() {
		t.Fatal("exact bound misclassified")
	}
	if n, err := sink.Write([]byte("warning: omitted\n")); err != nil || n != 17 || !sink.wasTruncated() {
		t.Fatal("discarded diagnostic not detected")
	}
	if frozenSuccessfulOutput("validate", 0, sink.text(), sink.wasTruncated()) {
		t.Fatal("truncated success accepted")
	}
}
