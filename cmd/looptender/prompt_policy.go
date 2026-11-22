package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/prompt"
)

// promptPolicyCommand shows where a loop's standing wake policy comes from,
// and, only when a person runs apply, rewrites its prompt file so that it
// follows the built-in policy. Sends never write the file.
func promptPolicyCommand(e env, args []string) int {
	const usage = "usage: prompt-policy show|apply -c CONFIG [--json]"
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(e.stdout, usage)
		return exitOK
	}
	if len(args) == 0 || (args[0] != "show" && args[0] != "apply") {
		e.errf(usage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("prompt-policy "+sub, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	path := fs.String("c", "", "config path")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return exitOK
	} else if err != nil || fs.NArg() != 0 || *path == "" {
		e.errf(usage)
		return exitUsage
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		e.errf("prompt-policy: cannot resolve config path")
		return exitUsage
	}
	cfg, err := config.Load(abs)
	if err != nil {
		e.errf("config: %v", err)
		return exitUsage
	}
	if sub == "show" {
		s := describeWakePolicy(cfg.Loop)
		if s == nil {
			e.errf("prompt-policy: the prompt cannot be read")
			return exitFailed
		}
		if *asJSON {
			return writeJSON(e, s)
		}
		printWakePolicy(e.stdout, s)
		return exitOK
	}
	r, err := applyPromptPolicy(cfg.Loop, abs, time.Now())
	if err != nil {
		e.errf("prompt-policy apply: %v", err)
		return exitFailed
	}
	if *asJSON {
		return writeJSON(e, r)
	}
	fmt.Fprintln(e.stdout, r.Message)
	return exitOK
}

type promptPolicyApplied struct {
	Changed bool   `json:"changed"`
	Backup  string `json:"backup,omitempty"`
	Version int    `json:"version"`
	Message string `json:"message"`
}

func writeJSON(e env, v any) int {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return exitFailed
	}
	return exitOK
}

// applyPromptPolicy rewrites the prompt file as setup would write it today:
// the current wording, then the text after the file's first line reading
// exactly "Task:". A file already on a built-in wording keeps its command
// prefix and configuration path; it needs no rewrite and gets none. The
// previous file is kept beside it.
func applyPromptPolicy(l config.Loop, cfgPath string, now time.Time) (promptPolicyApplied, error) {
	r := promptPolicyApplied{Version: prompt.CurrentPolicyVersion()}
	if l.PromptFile == "" {
		return r, errors.New("the prompt is set in the configuration (loop.prompt), not in a prompt file; move it to a file named by loop.prompt_file first")
	}
	info, err := os.Lstat(l.PromptFile)
	if err != nil || !info.Mode().IsRegular() {
		return r, errors.New("the prompt file is missing or not a regular file")
	}
	raw, err := os.ReadFile(l.PromptFile)
	if err != nil {
		return r, errors.New("the prompt file cannot be read")
	}
	if m, ok := prompt.MatchPolicy(string(raw)); ok {
		r.Message = fmt.Sprintf("The prompt file already follows the built-in wake policy (it holds version %d; version %d is sent). Nothing was written.", m.Version, r.Version)
		return r, nil
	}
	task, ok := taskAfterLine(string(raw))
	if !ok {
		return r, errors.New("the prompt file has no line reading exactly `Task:` with the task after it; put the task after such a line and run this again. Nothing was written")
	}
	text := prompt.GeneratedPrompt(initCommandPrefix(), commandQuote(cfgPath), task)
	backup := l.PromptFile + ".before-policy-" + now.UTC().Format("20060102T150405Z")
	if err := os.WriteFile(backup, raw, info.Mode().Perm()); err != nil {
		return r, errors.New("the previous prompt file could not be kept; nothing was written")
	}
	if err := replacePromptFile(l.PromptFile, []byte(text), info.Mode().Perm()); err != nil {
		return r, fmt.Errorf("the prompt file could not be written (%v); the previous copy is %s", err, backup)
	}
	r.Changed, r.Backup = true, backup
	r.Message = fmt.Sprintf("The prompt file now follows the built-in wake policy (version %d) with its task kept. The previous file is %s.", r.Version, backup)
	return r, nil
}

// taskAfterLine returns the trimmed text after the first line that is exactly
// "Task:".
func taskAfterLine(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var i int
	switch {
	case strings.HasPrefix(text, "Task:\n"):
		i = len("Task:\n")
	default:
		j := strings.Index(text, "\nTask:\n")
		if j < 0 {
			return "", false
		}
		i = j + len("\nTask:\n")
	}
	task := strings.TrimSpace(text[i:])
	return task, task != ""
}

func replacePromptFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prompt-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
