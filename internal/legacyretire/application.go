//go:build darwin || linux

package legacyretire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/daemoninfo"
	"golang.org/x/sys/unix"
)

// Selection is an explicit reviewed manual invocation, not a PID discovery rule.
// Build/start/executable image expectations must be supplied from independent
// rollout evidence. The helper never retargets a failed selection.
type Selection struct {
	ConfigPath       string   `json:"configPath"`
	StatePath        string   `json:"statePath"`
	PID              int      `json:"pid"`
	Build            string   `json:"build"`
	StartedAt        string   `json:"startedAt"`
	Executable       string   `json:"executable"`
	ExecutableDigest string   `json:"executableDigest"`
	Arguments        []string `json:"arguments"`
}
type appDeps struct {
	inspect func(int) (Process, error)
	query   func(context.Context, string) (daemoninfo.Snapshot, error)
	arm     func(context.Context, Binding) (Observer, error)
}

func productionDeps() appDeps { return appDeps{Inspect, daemoninfo.Query, ArmNative} }

type appGuard struct {
	selection     Selection
	deps          appDeps
	dir           int
	directory     Identity
	locks         map[string]int
	identities    map[string]Identity
	caller        string
	contextDigest string
	initial       Binding
	sources       []fileEvidence
}
type fileEvidence struct {
	Path     string
	Identity Identity
	Digest   string
}

// contextDigest binds the current operator/owner selection without publishing
// values. It does NOT reconstruct a legacy process's historic environment or
// authorize replacement with an inferred account/provider context.
func currentContextDigest() string {
	names := []string{"BB_THREAD_ID", "BB_PROJECT_ID", "BB_ENVIRONMENT_ID", "BB_CLI", "HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "LOOPTENDER_SESSION_DIR", "KEEPGOING_SESSION_DIR", "LOOPTENDER_REGISTRY_DIR", "KEEPGOING_REGISTRY_DIR", "LOOPTENDER_ROUTES_FILE", "KEEPGOING_ROUTES_FILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}
	values := make([][3]string, 0, len(names))
	for _, n := range names {
		v, ok := os.LookupEnv(n)
		present := "absent"
		if ok {
			present = "present"
		}
		values = append(values, [3]string{n, present, v})
	}
	b, _ := json.Marshal(values)
	return digest(b)
}
func snapshotPath(path string, limit int64, privateFile bool) ([]byte, fileEvidence, error) {
	var ev fileEvidence
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ev, ErrUnavailable
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return nil, ev, ErrUnavailable
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, ev, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || st.Size > limit || (privateFile && st.Mode&0077 != 0) {
		return nil, ev, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(raw)) > limit {
		return nil, ev, ErrUnavailable
	}
	var after unix.Stat_t
	if unix.Lstat(path, &after) != nil || identity(&st) != identity(&after) || st.Size != after.Size || st.Mtim != after.Mtim {
		return nil, ev, ErrUnavailable
	}
	return raw, fileEvidence{path, identity(&st), digest(raw)}, nil
}

// object parses structured fields only. Duplicate keys at any depth reject;
// consumed keys additionally reject decoder-style case/Unicode aliases.
func object(raw []byte, consumed ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() error
	walk = func() error {
		tok, e := dec.Token()
		if e != nil {
			return e
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, e := dec.Token()
				if e != nil {
					return e
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return ErrUnavailable
				}
				seen[k] = true
				if e = walk(); e != nil {
					return e
				}
			}
			end, e := dec.Token()
			if e != nil || end != json.Delim('}') {
				return ErrUnavailable
			}
		case '[':
			for dec.More() {
				if e = walk(); e != nil {
					return e
				}
			}
			end, e := dec.Token()
			if e != nil || end != json.Delim(']') {
				return ErrUnavailable
			}
		default:
			return ErrUnavailable
		}
		return nil
	}
	if e := walk(); e != nil {
		return nil, ErrUnavailable
	}
	if _, e := dec.Token(); e != io.EOF {
		return nil, ErrUnavailable
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrUnavailable
	}
	for key := range fields {
		for _, name := range consumed {
			if key != name && strings.EqualFold(key, name) {
				return nil, ErrUnavailable
			}
		}
	}
	return fields, nil
}
func textField(m map[string]json.RawMessage, key string, optional bool) (string, error) {
	raw, ok := m[key]
	if !ok && optional {
		return "", nil
	}
	var value string
	if !ok || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return "", ErrUnavailable
	}
	return value, nil
}
func readOwner(raw []byte, caller string) (string, uint64, string, error) {
	m, e := object(raw, "ownership", "supersededBy", "pendingTransfer", "bbTarget")
	if e != nil {
		return "", 0, "", e
	}
	retired, e := textField(m, "supersededBy", true)
	if e != nil || retired != "" {
		return "", 0, "", ErrUnavailable
	}
	if pending, ok := m["pendingTransfer"]; ok && !bytes.Equal(pending, []byte("null")) {
		return "", 0, "", ErrUnavailable
	}
	owner, e := object(m["ownership"], "threadId", "generation")
	if e != nil {
		return "", 0, "", e
	}
	id, e := textField(owner, "threadId", false)
	if e != nil {
		return "", 0, "", e
	}
	var generation uint64
	if json.Unmarshal(owner["generation"], &generation) != nil || generation == 0 {
		return "", 0, "", ErrUnavailable
	}
	if e = control.CheckOwner(control.File{Ownership: &control.Ownership{ThreadID: id, Generation: generation}}, caller, false); e != nil {
		return "", 0, "", ErrUnavailable
	}
	target, e := textField(m, "bbTarget", true)
	return id, generation, target, e
}
func sourceEvidence(sel Selection) ([]fileEvidence, error) {
	raw, cfgEvidence, e := snapshotPath(sel.ConfigPath, 1<<20, false)
	if e != nil {
		return nil, e
	}
	cfg, e := config.Parse(raw)
	if e != nil {
		return nil, ErrUnavailable
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(filepath.Dir(sel.ConfigPath), p)
	}
	if resolve(cfg.Loop.StateDir) != sel.StatePath {
		return nil, ErrUnavailable
	}
	sources := []fileEvidence{cfgEvidence}
	if cfg.Loop.Fallbacks != "" {
		_, ev, e := snapshotPath(resolve(cfg.Loop.Fallbacks), 1<<20, false)
		if e != nil {
			return nil, e
		}
		sources = append(sources, ev)
	} else if cfg.Loop.FollowsStandard() {
		p, e := config.StandardRoutesPath()
		if e != nil {
			return nil, ErrUnavailable
		}
		_, ev, e := snapshotPath(p, 1<<20, true)
		if e != nil {
			return nil, e
		}
		sources = append(sources, ev)
	}
	return sources, nil
}
func acquireApp(ctx context.Context, sel Selection, deps appDeps) (*appGuard, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrUnavailable
	}
	if sel.PID <= 1 || sel.PID > 2147483647 || !filepath.IsAbs(sel.ConfigPath) || !filepath.IsAbs(sel.Executable) || sel.Build == "" || sel.StartedAt == "" || !validHash(sel.ExecutableDigest) || len(sel.Arguments) != 4 ||
		(!filepath.IsAbs(sel.Arguments[0])) || (sel.Arguments[1] != "daemon" && sel.Arguments[1] != "__start-background") || sel.Arguments[2] != "-c" || sel.Arguments[3] != sel.ConfigPath {
		return nil, ErrUnavailable
	}
	dir, id, e := openRoot(sel.StatePath)
	if e != nil {
		return nil, e
	}
	g := &appGuard{selection: sel, deps: deps, dir: dir, directory: id, locks: map[string]int{}, identities: map[string]Identity{}, caller: os.Getenv("BB_THREAD_ID"), contextDigest: currentContextDigest()}
	fail := func(e error) (*appGuard, error) { g.Close(); return nil, e }
	// Explicitly reject the known manager marker and manager invocation. This is
	// not a discovery guarantee for arbitrary third-party respawners: the rollout
	// selection must independently establish manual launch ownership.
	if !absent(dir, "service-launchd.json") || !absent(dir, "service-systemd.json") {
		return fail(ErrUnavailable)
	}
	for _, name := range []string{"tick.lock", "control.lock"} {
		fd, native, e := openRecord(dir, name, unix.O_RDWR)
		if e != nil {
			return fail(e)
		}
		g.locks[name] = fd
		g.identities[name] = native
		for {
			e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
			if e == nil {
				break
			}
			if e != unix.EWOULDBLOCK && e != unix.EAGAIN {
				return fail(ErrUnavailable)
			}
			select {
			case <-ctx.Done():
				return fail(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	// Daemon lock is inspected now but acquired only after native exit.
	fd, native, e := openRecord(dir, "daemon.lock", unix.O_RDWR)
	if e != nil {
		return fail(e)
	}
	heldErr := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	unix.Close(fd)
	if heldErr != unix.EWOULDBLOCK && heldErr != unix.EAGAIN {
		return fail(ErrUnavailable)
	}
	g.identities["daemon.lock"] = native
	b, e := g.Verify(ctx)
	if e != nil {
		return fail(e)
	}
	g.initial = cloneBinding(b)
	return g, nil
}
func (g *appGuard) checkNames() error {
	if g.dir < 0 || os.Getenv("BB_THREAD_ID") != g.caller || currentContextDigest() != g.contextDigest {
		return ErrUnavailable
	}
	fd, id, e := openRoot(g.selection.StatePath)
	if e != nil {
		return e
	}
	unix.Close(fd)
	if id != g.directory {
		return ErrUnavailable
	}
	for name, id := range g.identities {
		if !sameName(g.dir, name, id) {
			return ErrUnavailable
		}
	}
	if !absent(g.dir, "service-launchd.json") || !absent(g.dir, "service-systemd.json") {
		return ErrUnavailable
	}
	return nil
}
func (g *appGuard) Verify(ctx context.Context) (Binding, error) {
	var b Binding
	if e := ctx.Err(); e != nil {
		return b, e
	}
	if e := g.checkNames(); e != nil {
		return b, e
	}
	sources, e := sourceEvidence(g.selection)
	if e != nil {
		return b, e
	}
	controlRaw, ctl, e := snapshotPath(filepath.Join(g.selection.StatePath, control.FileName), 1<<20, true)
	if e != nil {
		return b, e
	}
	owner, generation, bbTarget, e := readOwner(controlRaw, g.caller)
	if e != nil {
		return b, e
	}
	stateRaw, stateEvidence, e := snapshotPath(filepath.Join(g.selection.StatePath, "state.json"), 4<<20, true)
	if e != nil {
		return b, e
	}
	stateFields, e := object(stateRaw, "activeTarget")
	if e != nil {
		return b, e
	}
	target, e := textField(stateFields, "activeTarget", true)
	if e != nil {
		return b, e
	}
	if target == "" {
		target = "primary"
	}
	_, endpointBefore, e := snapshotPath(filepath.Join(g.selection.StatePath, daemoninfo.FileName), 1024, true)
	if e != nil {
		return b, e
	}
	before, e := g.deps.inspect(g.selection.PID)
	if e != nil {
		return b, e
	}
	snap, e := g.deps.query(ctx, g.selection.StatePath)
	if e != nil {
		return b, e
	}
	after, e := g.deps.inspect(g.selection.PID)
	if e != nil || !reflect.DeepEqual(before, after) {
		return b, ErrUnavailable
	}
	_, endpointAfter, e := snapshotPath(filepath.Join(g.selection.StatePath, daemoninfo.FileName), 1024, true)
	if e != nil || endpointBefore != endpointAfter {
		return b, ErrUnavailable
	}
	if snap.ProcessID != g.selection.PID || snap.Version != g.selection.Build || snap.StartedAt.UTC().Format(time.RFC3339Nano) != g.selection.StartedAt || snap.StateID != digest([]byte(g.selection.StatePath)) || snap.DryRun || snap.ConfigUnavailableSince != nil {
		return b, ErrUnavailable
	}
	if after.Executable != g.selection.Executable || !reflect.DeepEqual(after.Arguments, g.selection.Arguments) {
		return b, ErrUnavailable
	}
	_, image, e := snapshotPath(after.Executable, 128<<20, false)
	if e != nil || image.Digest != g.selection.ExecutableDigest {
		return b, ErrUnavailable
	}
	// Native source/lock/endpoint identity supplements the byte digest. Only
	// current reviewed source snapshots are claimed, never old mapped source.
	composite, _ := json.Marshal(struct {
		Sources                   []fileEvidence
		Directory                 Identity
		Locks                     map[string]Identity
		Endpoint                  fileEvidence
		Image                     fileEvidence
		Target, BBTarget, Context string
	}{sources, g.directory, g.identities, endpointBefore, image, target, bbTarget, g.contextDigest})
	b = Binding{StateIdentity: g.directory, ConfigIdentity: sources[0].Identity, ExecutableIdentity: image.Identity, Target: target, BBTarget: bbTarget, Caller: g.caller, ContextDigest: g.contextDigest, ConfigPath: g.selection.ConfigPath, StatePath: g.selection.StatePath, SourceDigest: digest(composite), ControlDigest: ctl.Digest, ControlIdentity: ctl.Identity,
		Owner: owner, Generation: generation, PID: snap.ProcessID, Build: snap.Version, StartedAt: g.selection.StartedAt, StateID: snap.StateID, Executable: after.Executable, ExecutableDigest: image.Digest, Arguments: append([]string(nil), after.Arguments...), Token: after.Token}
	// Detect source/control replacement during authentication, using freshly
	// read regular snapshots. Cooperating mutations remain excluded by guards.
	finalSources, err := sourceEvidence(g.selection)
	if err != nil || !reflect.DeepEqual(finalSources, sources) {
		return Binding{}, ErrUnavailable
	}
	_, finalControl, err := snapshotPath(filepath.Join(g.selection.StatePath, control.FileName), 1<<20, true)
	if err != nil || finalControl != ctl {
		return Binding{}, ErrUnavailable
	}
	_, finalState, err := snapshotPath(filepath.Join(g.selection.StatePath, "state.json"), 4<<20, true)
	if err != nil || finalState != stateEvidence {
		return Binding{}, ErrUnavailable
	}
	if g.sources == nil {
		g.sources = append([]fileEvidence(nil), sources...)
	}
	if !b.valid() {
		return Binding{}, ErrUnavailable
	}
	if e = g.checkNames(); e != nil {
		return Binding{}, e
	}
	return b, nil
}
func (g *appGuard) Arm(ctx context.Context, b Binding) (Observer, error) {
	return g.deps.arm(ctx, cloneBinding(b))
}
func (g *appGuard) Retired(ctx context.Context, b Binding) (Evidence, error) {
	if e := ctx.Err(); e != nil {
		return Evidence{}, e
	}
	if e := g.checkNames(); e != nil {
		return Evidence{}, e
	}
	sources, e := sourceEvidence(g.selection)
	if e != nil || !reflect.DeepEqual(sources, g.sources) {
		return Evidence{}, ErrUnavailable
	}
	if !absent(g.dir, daemoninfo.FileName) {
		return Evidence{}, ErrUnavailable
	}
	fd, id, e := openRecord(g.dir, "daemon.lock", unix.O_RDWR)
	if e != nil {
		return Evidence{}, e
	}
	if id != g.identities["daemon.lock"] || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(fd)
		return Evidence{}, ErrUnavailable
	}
	g.locks["daemon.lock"] = fd // retain until all verification/publication finishes
	_, ctl, e := snapshotPath(filepath.Join(g.selection.StatePath, control.FileName), 1<<20, true)
	if e != nil {
		return Evidence{}, e
	}
	if e = g.checkNames(); e != nil || !absent(g.dir, daemoninfo.FileName) {
		return Evidence{}, ErrUnavailable
	}
	return Evidence{true, true, ctl.Digest, ctl.Identity}, nil
}
func (g *appGuard) Close() error {
	var result error
	for name, fd := range g.locks {
		result = errors.Join(result, unix.Close(fd))
		delete(g.locks, name)
	}
	if g.dir >= 0 {
		result = errors.Join(result, unix.Close(g.dir))
		g.dir = -1
	}
	return result
}

// PrepareApplication authenticates one exact manual selection under guards and
// creates its canonical incarnation attempt. Existing/missing/spent attempts are
// never automatically replaced. A separately retained ticket remains mandatory.
func PrepareApplication(ctx context.Context, sel Selection) (Ticket, error) {
	if !Supported() {
		return Ticket{}, ErrUnavailable
	}
	return prepareApplication(ctx, sel, productionDeps())
}
func prepareApplication(ctx context.Context, sel Selection, deps appDeps) (Ticket, error) {
	return prepareApplicationWith(ctx, sel, deps, hooks{})
}
func prepareApplicationWith(ctx context.Context, sel Selection, deps appDeps, h hooks) (Ticket, error) {
	g, e := acquireApp(ctx, sel, deps)
	if e != nil {
		return Ticket{}, e
	}
	defer g.Close()
	b := g.initial
	vault := filepath.Join(sel.StatePath, "legacy-retirement")
	if e = os.Mkdir(vault, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return Ticket{}, e
	}
	fd, vaultID, e := openRoot(vault)
	if e != nil {
		return Ticket{}, e
	}
	defer unix.Close(fd)
	key := incarnationKey(b)
	// A separate durable anchor survives removal/corruption of the attempt
	// directory. Explicit prepare cannot manufacture a new ticket for it.
	anchorID, e := create(fd, key+".reserved", []byte(key), h)
	if e != nil {
		return Ticket{}, ErrSpent
	}
	if e = directorySync(fd, "incarnation-reservation-sync", h); e != nil {
		return Ticket{}, e
	}
	// The durable anchor contents do not publish the vault name in its parent.
	// Sync the held state directory too, including an existing partial vault.
	if e = directorySync(g.dir, "incarnation-vault-parent-sync", h); e != nil {
		return Ticket{}, e
	}
	ticket, e := prepare(filepath.Join(vault, key), b, h)
	if e != nil {
		return Ticket{}, e
	}
	ticket.Vault, ticket.Anchor = vaultID, anchorID
	return ticket, nil
}

// RetireApplication consumes only an existing ticket. It never prepares another
// ticket or launches a replacement. Unsupported native platforms refuse.
func RetireApplication(ctx context.Context, t Ticket, sel Selection) error {
	if !Supported() {
		return ErrUnavailable
	}
	if t.Anchor.Inode == 0 || t.Vault.Inode == 0 {
		return ErrUnavailable
	}
	return Retire(ctx, t, func(ctx context.Context, b Binding) (Guard, error) {
		if e := validateApplicationTicket(t, b); e != nil {
			return nil, e
		}
		return acquireApp(ctx, sel, productionDeps())
	})
}

func incarnationKey(b Binding) string {
	raw, _ := json.Marshal(struct {
		State string
		PID   int
		Start string
		Token [8]uint32
	}{b.StateID, b.PID, b.StartedAt, b.Token})
	return digest(raw)
}
func validateApplicationTicket(t Ticket, b Binding) error {
	vault := filepath.Join(b.StatePath, "legacy-retirement")
	key := incarnationKey(b)
	if t.Root != filepath.Join(vault, key) || t.Anchor.Inode == 0 || t.Vault.Inode == 0 {
		return ErrUnavailable
	}
	fd, id, e := openRoot(vault)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	if id != t.Vault {
		return ErrUnavailable
	}
	raw, e := read(fd, key+".reserved", t.Anchor)
	if e != nil || string(raw) != key {
		return ErrUnavailable
	}
	return nil
}
