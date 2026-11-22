package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A prompt, generated or hand-edited, tells its agent which program runs the
// loop's control commands. A prompt written for another copy of the program,
// or one that no longer exists, sends the agent to a build that does not read
// this loop's state directory safely. This finds those programs in any prompt
// text by reading it; it never runs them and never rewrites the prompt.

// promptCommandFinding is one program a prompt names, and how it compares
// with the program running now.
type promptCommandFinding struct {
	Path    string `json:"path"`
	Verdict string `json:"verdict"` // other_build or missing; an installed program is not listed
	Count   int    `json:"count"`
	// LastChanged is how long ago the file last changed, for a file that exists.
	LastChanged string `json:"lastChanged,omitempty"`
}

// controlSubcommands are the commands a prompt gives its agent to run on the
// loop; a program followed by one of them and "-c <this loop's config>" is a
// looptender program whatever it is called.
var controlSubcommands = map[string]bool{
	"complete": true, "await-user": true, "resume": true, "status": true, "pause": true, "stop": true,
	"enable": true, "tick": true, "context": true, "doctor": true, "validate": true, "runs": true, "timeline": true,
}

// promptProgramNames are the file names that clearly name a looptender
// program, the current one and the old one.
func promptProgramName(base string) bool {
	switch strings.ToLower(strings.TrimSuffix(base, ".exe")) {
	case "looptender", "keepgoing":
		return true
	}
	return false
}

// promptTokens splits text into shell-like words: single and double quotes
// group, backslash escapes nothing, and a word is cut at white space. Markdown
// backticks, brackets and trailing punctuation are trimmed from each word.
func promptTokens(text string) [][]string {
	var lines [][]string
	for _, line := range strings.Split(text, "\n") {
		var toks []string
		var cur strings.Builder
		var quote rune
		started := false
		flush := func() {
			if started {
				toks = append(toks, strings.Trim(cur.String(), "`,;:()[]<>"))
			}
			cur.Reset()
			started = false
		}
		for _, r := range line {
			switch {
			case quote != 0:
				if r == quote {
					quote = 0
				} else {
					cur.WriteRune(r)
				}
			case r == '\'' || r == '"':
				quote, started = r, true
			case r == ' ' || r == '\t' || r == '\r':
				flush()
			default:
				cur.WriteRune(r)
				started = true
			}
		}
		flush()
		lines = append(lines, toks)
	}
	return lines
}

func promptExpand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// promptPathLike says a word is a path to a program: absolute, or under the
// home directory. A relative word is not resolved: there is no directory to
// resolve it against.
func promptPathLike(w string) bool {
	if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/") {
		return true
	}
	// A drive path means nothing off Windows: it is not looked up.
	return runtime.GOOS == "windows" && len(w) > 2 && w[1] == ':' && (w[2] == '\\' || w[2] == '/')
}

// scanPromptPrograms returns the programs text names, in order of first
// appearance, with how often. cfgPath is the loop's configuration path; with
// it, any program followed by a control command and "-c cfgPath" counts, not
// only one named looptender or keepgoing.
func scanPromptPrograms(text, cfgPath string) (paths []string, counts map[string]int, dropped int) {
	counts = map[string]int{}
	add := func(w string) {
		p := promptExpand(w)
		if counts[p] == 0 {
			if len(paths) >= promptMaxPrograms {
				dropped++
				return
			}
			paths = append(paths, p)
		}
		counts[p]++
	}
	cfg := filepath.Clean(promptExpand(cfgPath))
	for _, toks := range promptTokens(text) {
		taken := map[int]bool{}
		for i, w := range toks {
			if cfgPath != "" && w == "-c" && i >= 2 && i+1 < len(toks) && filepath.Clean(promptExpand(toks[i+1])) == cfg && controlSubcommands[toks[i-1]] && promptPathLike(toks[i-2]) {
				add(toks[i-2])
				taken[i-2] = true
			}
		}
		for i, w := range toks {
			if !taken[i] && promptPathLike(w) && promptProgramName(filepath.Base(w)) {
				add(w)
			}
		}
	}
	return paths, counts, dropped
}

// Bounds on one scan: how much of the prompt is read, how many distinct
// programs are looked at, how many findings are listed, how many files may be
// hashed, and how long the file system may take.
const (
	promptReadLimit   = 1 << 20
	promptMaxPrograms = 64
	promptMaxFindings = 16
	promptHashFiles   = 4
	promptScanTimeout = 3 * time.Second
)

// promptCommandResult is what a scan found, within the bounds.
type promptCommandResult struct {
	Findings []promptCommandFinding
	// More counts programs found past the bounds and not listed.
	More int
	// Unchecked is set when the file system did not answer in time or the
	// prompt was longer than was read: what is listed may be incomplete.
	Unchecked bool
}

// promptCommandFindings compares each program a prompt names with the program
// running now, and lists those that are another file or missing. skip is a
// program already reported elsewhere (the generated wording's own prefix).
// Only the first promptReadLimit bytes of text are read.
func promptCommandFindings(text, cfgPath, skip string) promptCommandResult {
	var res promptCommandResult
	if len(text) > promptReadLimit {
		text, res.Unchecked = text[:promptReadLimit], true
	}
	paths, counts, dropped := scanPromptPrograms(text, cfgPath)
	res.More = dropped
	done := make(chan promptCommandResult, 1)
	go func() {
		out := res
		budget := &hashBudget{files: promptHashFiles}
		for _, p := range paths {
			if skip != "" && p == promptExpand(skip) {
				continue
			}
			verdict, resolved := checkProgramWith(p, budget)
			if verdict != prefixOtherBuild && verdict != prefixMissing {
				continue
			}
			if len(out.Findings) >= promptMaxFindings {
				out.More++
				continue
			}
			f := promptCommandFinding{Path: p, Verdict: verdict, Count: counts[p]}
			if verdict == prefixOtherBuild {
				if info, err := os.Stat(resolved); err == nil {
					f.LastChanged = dockSpan(time.Since(info.ModTime())) + " ago"
				}
			}
			out.Findings = append(out.Findings, f)
		}
		done <- out
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(promptScanTimeout):
		// A path that does not answer (a hung mount) must not hold status.
		res.Unchecked = true
		return res
	}
}

// promptCommandsSentence says what is wrong and what to change by hand. The
// named programs were not run to ask their version: they are files named in
// a prompt that anyone with access to it could have edited.
func promptCommandsSentence(r promptCommandResult, generated bool) string {
	fs := r.Findings
	if len(fs) == 0 {
		if r.Unchecked {
			return " Programs the prompt names for the loop's commands could not all be checked (the prompt is longer than is read, or a path did not answer)."
		}
		return ""
	}
	var parts []string
	for _, f := range fs {
		switch f.Verdict {
		case prefixMissing:
			parts = append(parts, fmt.Sprintf("%s (%d time(s)) does not exist or is not executable", f.Path, f.Count))
		default:
			parts = append(parts, fmt.Sprintf("%s (%d time(s)) is not the program running now (last changed %s)", f.Path, f.Count, f.LastChanged))
		}
	}
	fix := "Change those commands by hand to run " + promptExecutable() + "; this command never rewrites a prompt that is not generated wording."
	if generated {
		fix = "`prompt-policy apply --refresh-prefix` previews rewriting the policy part's commands; the same path elsewhere in the task text is changed by hand to run " + promptExecutable() + "."
	}
	if r.More > 0 {
		parts = append(parts, fmt.Sprintf("and %d more not listed", r.More))
	}
	if r.Unchecked {
		parts = append(parts, "the check was cut short, so there may be others")
	}
	return fmt.Sprintf(" The prompt also names other programs for the loop's commands: %s. A build older than this one does not read a newer state directory safely (this build writes state format %d): its control commands can fail and it drops fields from state.json it does not know. The named programs were not run to ask their version. %s", strings.Join(parts, "; "), adoptStateFormat, fix)
}
