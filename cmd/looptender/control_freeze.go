package main

// A freeze is an incompatible, evidence-preserving restrictive record, not a
// repaired control.File. Old struct decoders reject its top-level array.
import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
)

const freezeFormat = "looptender-control-freeze-v1"
const freezeRawLimit = 1 << 20
const freezeWireLimit = 2 << 20

var errFreezeUnavailable = errors.New("control.json freeze unavailable; evidence unchanged or publication unconfirmed; no repair or release performed")

type controlFreeze struct {
	Format      string `json:"format"`
	Action      string `json:"action"`
	Authority   string `json:"authority"`
	Incident    string `json:"incident"`
	Caller      string `json:"caller"`
	Source      string `json:"source"`
	Directory   string `json:"directory"`
	DirectoryID string `json:"directoryID"`
	OriginalID  string `json:"originalID"`
	Length      int    `json:"length"`
	SHA256      string `json:"sha256"`
	Raw         []byte `json:"raw"`
}

type freezeSnapshot struct {
	raw             []byte
	file, dir       os.FileInfo
	path, directory string
}

func readFreezeSnapshot(dir string) (freezeSnapshot, error) {
	var s freezeSnapshot
	abs, err := filepath.Abs(dir)
	if err != nil {
		return s, errFreezeUnavailable
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return s, errFreezeUnavailable
	}
	// Keep the caller's path too: later checks must catch replaced aliases.
	s.directory = resolved
	s.path = filepath.Join(abs, control.FileName)
	s.dir, err = os.Stat(abs)
	if err != nil || !s.dir.IsDir() {
		return s, errFreezeUnavailable
	}
	before, err := os.Lstat(s.path)
	if err != nil || !before.Mode().IsRegular() {
		return s, errFreezeUnavailable
	}
	f, err := openFreezeRecord(s.path)
	if err != nil {
		return s, errFreezeUnavailable
	}
	defer f.Close()
	s.file, err = f.Stat()
	if err != nil || !s.file.Mode().IsRegular() || !os.SameFile(before, s.file) || s.file.Size() > freezeWireLimit {
		return s, errFreezeUnavailable
	}
	s.raw, err = io.ReadAll(io.LimitReader(f, freezeWireLimit+1))
	if err != nil || len(s.raw) > freezeWireLimit {
		return s, errFreezeUnavailable
	}
	return s, nil
}

func (s freezeSnapshot) unchanged() error {
	next, err := readFreezeSnapshot(filepath.Dir(s.path))
	if err != nil || next.directory != s.directory || !os.SameFile(s.dir, next.dir) || !os.SameFile(s.file, next.file) || !bytes.Equal(s.raw, next.raw) {
		return errFreezeUnavailable
	}
	return nil
}

func freezeDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func parseControlFreeze(raw []byte, directory, directoryID, source string) error {
	var records []controlFreeze
	if json.Unmarshal(raw, &records) != nil || len(records) != 1 {
		return errFreezeUnavailable
	}
	r := records[0]
	canonical, err := json.Marshal(records)
	if err != nil || !bytes.Equal(canonical, raw) || r.Format != freezeFormat || r.Action != "pause" || r.Authority != "unknown" || len(r.Incident) != 32 || len(r.Caller) > 4096 || r.Directory != directory || r.DirectoryID != directoryID || r.OriginalID == "" || r.Source != source || len(r.Source) != 64 || r.Raw == nil || len(r.Raw) > freezeRawLimit || r.Length != len(r.Raw) || r.SHA256 != freezeDigest(r.Raw) || json.Valid(r.Raw) {
		return errFreezeUnavailable
	}
	if _, err := hex.DecodeString(r.Incident); err != nil {
		return errFreezeUnavailable
	}
	// Never accept a nested or damaged envelope as original evidence.
	if bytes.HasPrefix(bytes.TrimSpace(r.Raw), []byte("[")) {
		return errFreezeUnavailable
	}
	return nil
}

// pauseDamagedControl intercepts only malformed content or a reserved array.
// Healthy records keep the ordinary control/history path and its exact errors.
func pauseDamagedControl(e env, cfg *config.Config) (bool, int) {
	// Refuse unsafe file kinds before the ordinary loader can follow or block.
	info, err := os.Lstat(filepath.Join(cfg.Loop.StateDir, control.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return false, 0
	} // existing missing-control semantics
	if err != nil || !info.Mode().IsRegular() {
		e.errf("control.json unreadable or not a regular file; pause unavailable")
		return true, exitFailed
	}
	s, err := readFreezeSnapshot(cfg.Loop.StateDir)
	if err != nil {
		// Do not hand a raced path to an ordinary loader after a failed safe read.
		e.errf("control.json safe read unavailable; pause not acknowledged")
		return true, exitFailed
	}
	array := bytes.HasPrefix(bytes.TrimSpace(s.raw), []byte("["))
	if !array && json.Valid(s.raw) {
		if !ordinaryPauseRecord(s.raw) {
			e.errf("control.json schema unavailable; evidence unchanged; no freeze performed")
			return true, exitFailed
		}
		return false, 0
	}
	return pauseDamagedSnapshot(e, cfg, s, array)
}

// The preflight snapshot is deliberately retained across lock acquisition: a
// changed or replaced record must be retried, even if it is now readable.
func pauseDamagedSnapshot(e env, cfg *config.Config, s freezeSnapshot, array bool) (bool, int) {
	fail := func(err error) (bool, int) { e.errf("%v", err); return true, exitFailed }
	if !freezeSupported() {
		return fail(errors.New("control.json freeze unsupported on this platform; evidence unchanged; Pause not acknowledged"))
	}
	if len(s.raw) > freezeWireLimit || (!array && len(s.raw) > freezeRawLimit) {
		return fail(errFreezeUnavailable)
	}
	tick, err := acquireWithin(e.ctx, cfg.Loop.StateDir, 2*time.Second)
	if err != nil {
		e.errf("pause freeze not acknowledged: check drain unavailable; prior effects may be unknown")
		return true, exitSkipped
	}
	defer tick.Release()
	ctl, err := lock.AcquireNamed(cfg.Loop.StateDir, lock.Control)
	if err != nil {
		return fail(errFreezeUnavailable)
	}
	defer ctl.Release()
	verify := func() error {
		if cfg.CheckSources() != nil {
			return errFreezeUnavailable
		}
		return s.unchanged()
	}
	if err := verify(); err != nil {
		return fail(err)
	}
	source := cfg.SourcesDigest()
	if array {
		if err := parseControlFreeze(s.raw, s.directory, freezeNativeID(s.dir), source); err != nil {
			return fail(err)
		}
		// A previous rename may have succeeded with uncertain directory durability.
		if err := syncFreezeDirectory(s.directory); err != nil {
			return fail(errFreezeUnavailable)
		}
	} else {
		if json.Valid(s.raw) || len(source) != 64 || len(os.Getenv("BB_THREAD_ID")) > 4096 {
			return fail(errFreezeUnavailable)
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return fail(errFreezeUnavailable)
		}
		raw := append([]byte{}, s.raw...)
		record := controlFreeze{freezeFormat, "pause", "unknown", hex.EncodeToString(id), os.Getenv("BB_THREAD_ID"), source, s.directory, freezeNativeID(s.dir), freezeNativeID(s.file), len(raw), freezeDigest(raw), raw}
		wire, err := json.Marshal([]controlFreeze{record})
		if err != nil {
			return fail(errFreezeUnavailable)
		}
		if err := publishControlFreeze(s.path, wire, verify); err != nil {
			return fail(err)
		}
	}
	if err := cfg.CheckSources(); err != nil {
		return fail(errFreezeUnavailable)
	}
	fmt.Fprintln(e.stdout, "pause: restrictive freeze persisted; original control evidence retained, owner and holds unknown. No repair or release is available; prior actions are not retracted.")
	return true, exitOK
}

// Hooks are values passed by fixtures, never mutable production globals.
type freezePublication struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	replace func(string, string) error
	syncDir func(string) error
}

func publishControlFreeze(path string, wire []byte, verify func() error) error {
	return publishControlFreezeWith(path, wire, verify, freezePublication{(*os.File).Write, (*os.File).Sync, (*os.File).Close, os.Rename, syncFreezeDirectory})
}
func publishControlFreezeWith(path string, wire []byte, verify func() error, ops freezePublication) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".control-freeze-*")
	if err != nil {
		return errFreezeUnavailable
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	n, err := ops.write(f, wire)
	if err != nil || n != len(wire) {
		return errFreezeUnavailable
	}
	if ops.sync(f) != nil || ops.close(f) != nil {
		return errFreezeUnavailable
	}
	if verify() != nil {
		return errFreezeUnavailable
	}
	if ops.replace(name, path) != nil {
		return errFreezeUnavailable
	}
	// Never restore the old record after publication, even on an uncertain sync.
	if ops.syncDir(filepath.Dir(path)) != nil {
		return errFreezeUnavailable
	}
	return nil
}

func validateOrdinaryPauseRecord(raw []byte) error {
	if !ordinaryPauseRecord(raw) {
		return errors.New("control.json schema unavailable; evidence unchanged; no freeze performed")
	}
	return nil
}

// Do not normalize unsupported valid JSON while classifying malformed input.
// This checks encoding shape only; it never reconstructs ownership or holds.
func ordinaryPauseRecord(raw []byte) bool {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var ctl control.File
	if d.Decode(&ctl) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	return freezeExactJSON(d, reflect.TypeOf(control.File{}), 0)
}

// Match exact JSON tags recursively rather than approximating encoding/json's
// Unicode case folding. Null is legitimate only for nullable pointer/slice
// fields emitted by existing controls; scalar null must not erase a hold.
func freezeExactJSON(d *json.Decoder, typ reflect.Type, depth int) bool {
	if depth > 64 {
		return false
	}
	tok, err := d.Token()
	if err != nil {
		return false
	}
	if tok == nil {
		return typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	delim, container := tok.(json.Delim)
	if !container {
		return typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{})
	}
	switch delim {
	case '{':
		if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{}) {
			return false
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.PkgPath == "" && name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return false
			}
			field, ok := fields[name]
			if !ok {
				return false
			}
			seen[name] = true
			if !freezeExactJSON(d, field, depth+1) {
				return false
			}
		}
	case '[':
		if typ.Kind() != reflect.Slice {
			return false
		}
		for d.More() {
			if !freezeExactJSON(d, typ.Elem(), depth+1) {
				return false
			}
		}
	default:
		return false
	}
	_, err = d.Token()
	return err == nil
}
