//go:build unix

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/router/channel"
	"github.com/looptender/looptender/internal/router/pointer"
	"github.com/looptender/looptender/internal/userdir"
)

const (
	routerStateFileName  = "router.json"
	pointerStateFileName = "pointer-state.json"
	routerPlanTermsLine  = "Using several subscriptions through one router, and moving one conversation between them, may be governed by your plan's terms. Check them for each subscription you add. A provider may treat requests whose login changes mid-conversation as unusual and restrict an account."
)

type routerStateFile struct {
	Enabled bool `json:"enabled"`
}

type pointerEntry struct {
	ClientID     string         `json:"clientId"`
	Path         string         `json:"path"`
	OriginalMode os.FileMode    `json:"originalMode"`
	Record       pointer.Record `json:"record"`
}

type pointerStateFile struct {
	Clients pointer.Clients         `json:"clients"`
	Entries map[string]pointerEntry `json:"entries"`
}

type routerStatusOut struct {
	Enabled      bool   `json:"enabled"`
	Directory    string `json:"directory"` // private, missing, or untrusted
	Answering    bool   `json:"answering"`
	PointedCount int    `json:"pointedCount"`
	HasFrontCert bool   `json:"hasFrontCert"`
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }
func (s *stringListFlag) Set(v string) error {
	if strings.TrimSpace(v) != "" {
		*s = append(*s, v)
	}
	return nil
}

func routerCommand(e env, args []string) int {
	if len(args) == 0 {
		e.errf("usage: router status|on|off|run|front [flags]")
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return routerStatusCommand(e, rest)
	case "on":
		return routerOnCommand(e, rest)
	case "off":
		return routerOffCommand(e, rest)
	case "run":
		return routerRunCommand(e, rest)
	case "front":
		return routerFrontCommand(e, rest)
	default:
		e.errf("usage: router status|on|off|run|front [flags]")
		return exitUsage
	}
}

func resolveRouterDir(flagDir string) (string, error) {
	if strings.TrimSpace(flagDir) != "" {
		return flagDir, nil
	}
	return userdir.State("router")
}

func routerStatusCommand(e env, args []string) int {
	fs := flag.NewFlagSet("router status", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dirFlag := fs.String("dir", "", "the router's private directory")
	asJSON := fs.Bool("json", false, "print structured status")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		e.errf("usage: router status [--dir DIR] [--json]")
		return exitUsage
	}
	dir, err := resolveRouterDir(*dirFlag)
	if err != nil {
		e.errf("router: cannot resolve state directory")
		return exitFailed
	}

	out := routerStatusOut{Directory: "untrusted"}
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		out.Directory = "missing"
	} else if _, err := channel.CheckDirectory(dir); err == nil {
		out.Directory = "private"
		if st, err := readRouterState(dir); err == nil {
			out.Enabled = st.Enabled
		}
		if ps, err := readPointerState(dir); err == nil {
			out.PointedCount = len(ps.Entries)
		}
		if _, err := os.Lstat(filepath.Join(dir, routerFrontCertName)); err == nil {
			out.HasFrontCert = true
		}
		if c, err := net.DialTimeout("unix", filepath.Join(dir, channel.SocketName), 200*time.Millisecond); err == nil {
			_ = c.Close()
			out.Answering = true
		}
	}

	if *asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return exitOK
	}
	stateWord := "off"
	if out.Enabled {
		stateWord = "on"
	}
	fmt.Fprintf(e.stdout, "router: %s (directory %s, answering %v, pointed %d)\n", stateWord, out.Directory, out.Answering, out.PointedCount)
	return exitOK
}

func routerOnCommand(e env, args []string) int {
	if os.Getenv("BB_THREAD_ID") != "" {
		e.errf("router: refused inside a BB thread; run from an ordinary terminal")
		return exitFailed
	}
	fs := flag.NewFlagSet("router on", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dirFlag := fs.String("dir", "", "the router's private directory")
	frontURL := fs.String("front-url", "", "loopback URL of the running front (for example http://127.0.0.1:PORT)")
	useTLS := fs.Bool("tls", false, "include front-cert.pem from the router directory in pointed settings")
	allowUnstarted := fs.Bool("allow-unstarted", false, "enable even when the router socket is not currently answering")
	var settingsFiles stringListFlag
	fs.Var(&settingsFiles, "settings", "settings file to point at the front (repeatable)")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		e.errf("usage: router on [--dir DIR] [--front-url URL] [--tls] [--settings FILE]... [--allow-unstarted]")
		return exitUsage
	}

	dir, err := resolveRouterDir(*dirFlag)
	if err != nil {
		e.errf("router: cannot resolve state directory")
		return exitFailed
	}
	if err := ensurePrivateDir(dir); err != nil {
		e.errf("router: %v", err)
		return exitFailed
	}

	if !*allowUnstarted {
		c, err := net.DialTimeout("unix", filepath.Join(dir, channel.SocketName), 200*time.Millisecond)
		if err != nil {
			e.errf("router: router process is not answering in %s; start `looptender router run --dir %s` first", dir, dir)
			return exitFailed
		}
		_ = c.Close()
	}

	if len(settingsFiles) > 0 {
		if err := validateLoopbackURL(*frontURL); err != nil {
			e.errf("router: %v", err)
			return exitUsage
		}
	}
	trustFile := ""
	if *useTLS {
		trustFile = filepath.Join(dir, routerFrontCertName)
		if st, err := os.Lstat(trustFile); err != nil || !st.Mode().IsRegular() {
			e.errf("router: front certificate %s not found", trustFile)
			return exitFailed
		}
	}

	ps, err := readPointerState(dir)
	if err != nil {
		e.errf("router: cannot read pointer state: %v", err)
		return exitFailed
	}
	if ps.Entries == nil {
		ps.Entries = map[string]pointerEntry{}
	}

	var applied []pointerEntry
	rollback := func() {
		for i := len(applied) - 1; i >= 0; i-- {
			a := applied[i]
			_ = pointer.RemoveFile(a.Path, a.Record, a.OriginalMode)
		}
	}

	for _, file := range settingsFiles {
		absPath, err := filepath.Abs(file)
		if err != nil {
			rollback()
			e.errf("router: invalid settings path")
			return exitFailed
		}
		if _, already := ps.Entries[absPath]; already {
			continue
		}
		origMode := os.FileMode(0o600)
		if st, err := os.Lstat(absPath); err == nil {
			origMode = st.Mode().Perm()
		}
		clientID := clientIDForPath(absPath)
		secret, err := ps.Clients.Issue(clientID)
		if err != nil {
			rollback()
			e.errf("router: could not issue client secret")
			return exitFailed
		}
		rec, err := pointer.PointFile(absPath, pointer.Pointer{
			BaseURL:   *frontURL,
			Secret:    secret,
			TrustFile: trustFile,
		})
		if err != nil {
			rollback()
			e.errf("router: could not point %s: %v", absPath, err)
			return exitFailed
		}
		entry := pointerEntry{ClientID: clientID, Path: absPath, OriginalMode: origMode, Record: rec}
		ps.Entries[absPath] = entry
		applied = append(applied, entry)
	}

	if err := writePrivateJSON(filepath.Join(dir, pointerStateFileName), ps); err != nil {
		rollback()
		e.errf("router: could not save pointer state: %v", err)
		return exitFailed
	}
	if err := writePrivateJSON(filepath.Join(dir, routerStateFileName), routerStateFile{Enabled: true}); err != nil {
		rollback()
		e.errf("router: could not save router state: %v", err)
		return exitFailed
	}

	fmt.Fprintln(e.stdout, routerPlanTermsLine)
	fmt.Fprintf(e.stdout, "router: on (%d pointed)\n", len(ps.Entries))
	return exitOK
}

func routerOffCommand(e env, args []string) int {
	if os.Getenv("BB_THREAD_ID") != "" {
		e.errf("router: refused inside a BB thread; run from an ordinary terminal")
		return exitFailed
	}
	fs := flag.NewFlagSet("router off", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	dirFlag := fs.String("dir", "", "the router's private directory")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		e.errf("usage: router off [--dir DIR]")
		return exitUsage
	}

	dir, err := resolveRouterDir(*dirFlag)
	if err != nil {
		e.errf("router: cannot resolve state directory")
		return exitFailed
	}
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		fmt.Fprintln(e.stdout, "router: already off")
		return exitOK
	}
	if _, err := channel.CheckDirectory(dir); err != nil {
		e.errf("router: %v", err)
		return exitFailed
	}

	ps, err := readPointerState(dir)
	if err != nil {
		e.errf("router: cannot read pointer state: %v", err)
		return exitFailed
	}

	var failed int
	for path, entry := range ps.Entries {
		nextRec, err := pointer.UnpointFile(entry.Path, entry.Record, &ps.Clients, entry.ClientID)
		if err != nil && !errors.Is(err, pointer.ErrChanged) {
			failed++
			e.errf("router: could not unpoint %s: %v", path, err)
			continue
		}
		if err == nil {
			if rerr := pointer.RemoveFile(entry.Path, nextRec, entry.OriginalMode); rerr != nil {
				failed++
				e.errf("router: could not remove pointer from %s: %v", path, rerr)
				entry.Record = nextRec
				ps.Entries[path] = entry
				continue
			}
		}
		delete(ps.Entries, path)
	}

	if err := writePrivateJSON(filepath.Join(dir, pointerStateFileName), ps); err != nil {
		e.errf("router: could not save pointer state: %v", err)
		return exitFailed
	}
	if err := writePrivateJSON(filepath.Join(dir, routerStateFileName), routerStateFile{Enabled: false}); err != nil {
		e.errf("router: could not save router state: %v", err)
		return exitFailed
	}
	if failed > 0 {
		return exitFailed
	}
	fmt.Fprintln(e.stdout, "router: off")
	return exitOK
}

func ensurePrivateDir(dir string) error {
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	_, err := channel.CheckDirectory(dir)
	return err
}

func validateLoopbackURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("front-url must be an http or https loopback URL")
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("front-url must target loopback")
	}
	return nil
}

func clientIDForPath(absPath string) string {
	sum := sha256.Sum256([]byte(absPath))
	return "c" + hex.EncodeToString(sum[:8])
}

func readRouterState(dir string) (routerStateFile, error) {
	var out routerStateFile
	b, err := os.ReadFile(filepath.Join(dir, routerStateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func readPointerState(dir string) (pointerStateFile, error) {
	var out pointerStateFile
	b, err := os.ReadFile(filepath.Join(dir, pointerStateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func writePrivateJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".looptender-router-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
