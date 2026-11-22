package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// promptExecutable is the program file this build runs as; a test replaces it.
var promptExecutable = keepgoingExecutable

// Prefix verdicts: what a wake prompt's command prefix runs, against the
// program file running now.
const (
	prefixInstalled  = "installed"   // the same file as this build's
	prefixOtherBuild = "other_build" // a different file that exists
	prefixMissing    = "missing"     // no such executable file
	prefixUnknown    = "unknown"     // the prefix is not a plain quoted program path
	// prefixNotProgram: the path is a directory, such as a state directory
	// named looptender. It is not a program and is not reported.
	prefixNotProgram = "not_program"
	// prefixUnverified: a file of the same size as this build's that the
	// scan's hashing budget did not allow comparing. It is not called another
	// program, and it is counted as not looked at.
	prefixUnverified = "unverified"
)

// promptPrefixProgram is the program path a command prefix names: the prefix
// setup writes is one quoted path (after "& " on Windows). Anything else is
// not understood.
func promptPrefixProgram(prefix string) (string, bool) {
	p := strings.TrimSpace(prefix)
	p = strings.TrimPrefix(p, "& ")
	if len(p) >= 2 && p[0] == '\'' && p[len(p)-1] == '\'' {
		inner := p[1 : len(p)-1]
		if runtime.GOOS == "windows" {
			inner = strings.ReplaceAll(inner, "''", "'")
		} else {
			inner = strings.ReplaceAll(inner, `'\''`, "'")
		}
		return inner, inner != "" && !strings.Contains(inner, "\x00")
	}
	if p == "" || strings.ContainsAny(p, " \t\n'\"$`;&|<>(){}*?") {
		return "", false
	}
	return p, true
}

// checkPromptPrefix compares what a prompt's command prefix runs with this
// build's program file. It reads file metadata only and runs nothing.
func checkPromptPrefix(prefix string) (verdict, program string) {
	program, ok := promptPrefixProgram(prefix)
	if !ok {
		return prefixUnknown, ""
	}
	verdict, _ = checkProgramPath(program)
	if verdict == prefixNotProgram {
		verdict = prefixMissing
	}
	return verdict, program
}

// hashBudget bounds the same-bytes comparisons one scan may make: the number
// of files hashed and the size of each.
type hashBudget struct{ files int }

const hashFileLimit = 64 << 20

// checkProgramPath is checkProgramWith for one program, with room to hash one.
func checkProgramPath(program string) (verdict, resolved string) {
	return checkProgramWith(program, &hashBudget{files: 1})
}

// checkProgramWith compares a program path with this build's own file: the
// same file (a link such as ~/.local/bin/looptender that leads to it counts),
// or a copy with the same size and bytes, is installed. It reads file metadata
// and, for a file of the same size, its bytes while the budget lasts; it runs
// nothing. A same-size file that the budget does not allow hashing is not
// called another program: its verdict is unknown. A directory is not a
// program. The resolved path is what a bare name found on PATH, or a link,
// leads to.
func checkProgramWith(program string, b *hashBudget) (verdict, resolved string) {
	path := program
	if !strings.ContainsRune(path, os.PathSeparator) {
		found, err := exec.LookPath(path)
		if err != nil {
			return prefixMissing, ""
		}
		path = found
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return prefixNotProgram, ""
	}
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return prefixMissing, ""
	}
	resolved = realPath(path)
	self, err := os.Stat(realPath(promptExecutable()))
	if err != nil {
		return prefixUnknown, resolved
	}
	switch {
	case os.SameFile(info, self):
		return prefixInstalled, resolved
	case info.Size() != self.Size():
		return prefixOtherBuild, resolved
	case b.files <= 0 || info.Size() > hashFileLimit:
		return prefixUnverified, resolved
	}
	b.files--
	if sameFileBytes(path, realPath(promptExecutable())) {
		return prefixInstalled, resolved
	}
	return prefixOtherBuild, resolved
}

// sameFileBytes says whether two files hold the same bytes.
func sameFileBytes(a, b string) bool {
	da, errA := fileDigest(a)
	db, errB := fileDigest(b)
	return errA == nil && errB == nil && da == db
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, hashFileLimit+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// promptRefreshPrefix is the command prefix for this program, written as setup
// writes it.
func promptRefreshPrefix() string {
	prefix := commandQuote(promptExecutable())
	if runtime.GOOS == "windows" {
		prefix = "& " + prefix
	}
	return prefix
}
