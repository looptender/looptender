package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadedSourcesRejectChangesAndRecover(t *testing.T) {
	for _, source := range []string{"config", "fallbacks"} {
		for _, change := range []string{"same-size", "grow", "truncate", "remove", "directory"} {
			t.Run(source+"/"+change, func(t *testing.T) {
				dir := t.TempDir()
				path, fbPath := filepath.Join(dir, "keepgoing.yaml"), filepath.Join(dir, "fallbacks.yaml")
				raw := valid + "  fallbacks: fallbacks.yaml\n"
				put := func(p, s string) {
					t.Helper()
					if err := os.WriteFile(p, []byte(s), 0600); err != nil {
						t.Fatal(err)
					}
				}
				put(path, raw)
				put(fbPath, validFallbacks)
				cfg, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				// In-memory scheduling adjustments do not change the source snapshot.
				cfg.Loop.Daemon.ControlPoll.Duration = time.Millisecond
				if err := cfg.CheckSources(); err != nil {
					t.Fatal(err)
				}
				target, original := path, raw
				want := "configuration changed or is unreadable"
				if source == "fallbacks" {
					target, original = fbPath, validFallbacks
					want = "fallback " + want
				}
				info, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "same-size":
					put(target, strings.Replace(original, "thr_example123", "thr_example789", 1))
					// Digest, not size/mtime or parsed semantic equivalence, establishes identity.
					if err := os.Chtimes(target, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "grow":
					put(target, original+"# edited\n")
				case "truncate":
					put(target, "")
				case "remove":
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := cfg.CheckSources(); err == nil || err.Error() != want {
					t.Fatalf("got %v, want %s", err, want)
				}
				if change == "directory" {
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
				}
				put(target, original)
				if err := cfg.CheckSources(); err != nil {
					t.Fatalf("restored bytes rejected: %v", err)
				}
				// A new valid snapshot accepts an edit that an old one rejects.
				put(target, original+"# new snapshot\n")
				fresh, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.CheckSources() == nil {
					t.Fatal("old snapshot accepted")
				}
				if err := fresh.CheckSources(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestSnapshotsArePrivateAndParseHasNoSources(t *testing.T) {
	cfg, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	fb, err := ParseFallbacks([]byte(validFallbacks))
	if err != nil {
		t.Fatal(err)
	}
	cfg.FallbackSet = fb
	if err := cfg.CheckSources(); err != nil {
		t.Fatal(err)
	}
	// Use source paths distinct from all exported configuration fields.
	cfg.source = snapshot("/fictional/private-source.yaml", []byte(valid))
	fb.source = snapshot("/fictional/private-fallbacks.yaml", []byte(validFallbacks))
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		b, err := marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"private-source", "private-fallbacks", "sourceSnapshot", fmt.Sprintf("%x", sha256.Sum256([]byte(valid)))} {
			if strings.Contains(string(b), forbidden) {
				t.Fatalf("serialized snapshot metadata: %s", forbidden)
			}
		}
	}
	// The snapshot must describe the parsed bytes, even if the source was
	// replaced before snapshot creation (no second read of the source).
	path := filepath.Join(t.TempDir(), "keepgoing.yaml")
	if err := os.WriteFile(path, []byte(valid+"# later\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.source = snapshot(path, []byte(valid))
	if cfg.CheckSources() == nil {
		t.Fatal("snapshot captured a later read")
	}
}

func TestSourcesDigestBindsFallbackBytesAndPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keepgoing.yaml")
	fb := filepath.Join(dir, "fallbacks.yaml")
	for p, b := range map[string]string{path: valid + "  fallbacks: fallbacks.yaml\n", fb: validFallbacks} {
		if err := os.WriteFile(p, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := cfg.SourcesDigest()
	if len(digest) != 64 {
		t.Fatal("missing sources digest")
	}
	if err := os.WriteFile(fb, []byte(validFallbacks+"# changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if next.SourceDigest() != cfg.SourceDigest() || next.SourcesDigest() == digest || cfg.CheckSources() == nil {
		t.Fatal("fallback bytes not bound")
	}
	alias := filepath.Join(dir, "copy.yaml")
	b, _ := os.ReadFile(path)
	os.WriteFile(alias, b, 0600)
	copied, err := Load(alias)
	if err != nil || copied.SourcesDigest() == next.SourcesDigest() {
		t.Fatal("path not bound", err)
	}
	parsed, err := Parse([]byte(valid))
	if err != nil || parsed.SourcesDigest() != "" {
		t.Fatal("in-memory source invented", err)
	}
}

func TestCheckSourceBytesUsesLoadedSnapshotsWithoutIO(t *testing.T) {
	dir := t.TempDir()
	path, fb := filepath.Join(dir, "looptender.yaml"), filepath.Join(dir, "fallbacks.yaml")
	raw := []byte(valid + "  fallbacks: fallbacks.yaml\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	fallbackRaw := []byte(validFallbacks)
	if err := os.WriteFile(fb, fallbackRaw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fb); err != nil {
		t.Fatal(err)
	}
	if err := cfg.CheckSourceBytes(raw, fallbackRaw); err != nil {
		t.Fatal("comparison performed IO", err)
	}
	for _, pair := range [][2][]byte{{nil, fallbackRaw}, {[]byte{}, fallbackRaw}, {append(append([]byte{}, raw...), ' '), fallbackRaw}, {raw, nil}, {raw, []byte{}}, {raw, append(append([]byte{}, fallbackRaw...), ' ')}} {
		if cfg.CheckSourceBytes(pair[0], pair[1]) == nil {
			t.Fatal("mismatched read certified")
		}
	}
	cfg.MarkSourcesStale()
	if cfg.CheckSourceBytes(raw, fallbackRaw) == nil {
		t.Fatal("stale source certified")
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CheckSourceBytes(raw, nil) == nil {
		t.Fatal("Parse-only source certified")
	}
	// Present-empty differs from missing, even though an empty document is not
	// a valid loaded config. Exercise the captured primitive directly.
	empty := &Config{source: snapshot(path, []byte{})}
	if empty.CheckSourceBytes([]byte{}, nil) != nil || empty.CheckSourceBytes(nil, nil) == nil {
		t.Fatal("empty conflated with missing")
	}
	empty.source = sourceSnapshot{path: path, absent: true}
	if empty.CheckSourceBytes([]byte{}, nil) == nil || empty.CheckSourceBytes(nil, nil) == nil {
		t.Fatal("unavailable certified")
	}
	empty.source = snapshot(path, raw)
	empty.FallbackSet = &Fallbacks{source: snapshot(fb, []byte{})}
	if empty.CheckSourceBytes(raw, []byte{}) != nil || empty.CheckSourceBytes(raw, nil) == nil {
		t.Fatal("empty required fallback conflated with absent input")
	}
	empty.FallbackSet.source = sourceSnapshot{path: fb, absent: true}
	if empty.CheckSourceBytes(raw, nil) == nil || empty.CheckSourceBytes(raw, []byte{}) == nil {
		t.Fatal("unavailable fallback certified")
	}
	empty.FallbackSet = nil
	empty.source = snapshot(path, raw)
	if empty.CheckSourceBytes(raw, []byte{}) == nil {
		t.Fatal("unexpected empty fallback certified")
	}
}
