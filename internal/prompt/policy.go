package prompt

import (
	"fmt"
	"strings"
)

// The standing wake policy is the guidance setup writes above the task in a
// new loop's prompt file. It belongs to the binary, not to the loop: a
// prompt file whose policy part is exactly one that setup wrote is sent with
// the policy wording of the running build, followed by the file's own task.
// Any other prompt file is sent exactly as written. Nothing here writes a
// file.

// Policy sources reported by status and doctor.
const (
	// PolicyBuiltIn: the prompt file's policy part is one setup wrote; the
	// current wording is sent.
	PolicyBuiltIn = "built_in"
	// PolicyCustom: the prompt file differs from every wording setup wrote;
	// it is sent as written and receives no policy updates.
	PolicyCustom = "custom"
	// PolicyInline: the prompt is set in the configuration (loop.prompt),
	// not in a prompt file; it is sent as written.
	PolicyInline = "inline"
)

// CurrentPolicyVersion is the version of the wording this build sends.
func CurrentPolicyVersion() int { return len(policyHeads) }

// PolicyHead returns the current wording, with the command prefix and the
// configuration path filled in, ending with the "Task:" line.
func PolicyHead(prefix, configPath string) string {
	return renderHead(len(policyHeads), prefix, configPath)
}

// GeneratedPrompt returns a new prompt file: the current wording followed
// by the task.
func GeneratedPrompt(prefix, configPath, task string) string {
	return PolicyHead(prefix, configPath) + task + "\n"
}

func renderHead(version int, prefix, configPath string) string {
	return fmt.Sprintf(policyHeads[version-1], prefix, configPath, prefix, configPath)
}

// PolicyMatch is what a prompt text was recognised as.
type PolicyMatch struct {
	// Version is the wording the text holds, 1 to CurrentPolicyVersion().
	Version int
	// Prefix and ConfigPath are the command prefix and configuration path
	// written into it, exactly as written.
	Prefix, ConfigPath string
	// Task is everything after the "Task:" line, exactly as written.
	Task string
}

// MatchPolicy reports whether text is a prompt setup wrote, with any task
// after its "Task:" line: everything before that line must be, byte for
// byte, one of the wordings setup has written, with one command prefix and
// one configuration path used throughout. Leading and trailing white space
// of the whole text are ignored, as when a prompt file is read.
func MatchPolicy(text string) (PolicyMatch, bool) {
	text = strings.TrimSpace(text)
	for v := len(policyHeads); v >= 1; v-- {
		if m, ok := matchHead(v, text); ok {
			return m, true
		}
	}
	return PolicyMatch{}, false
}

func matchHead(version int, text string) (PolicyMatch, bool) {
	parts := strings.Split(policyHeads[version-1], "%s")
	if len(parts) != 5 || !strings.HasPrefix(text, parts[0]) {
		return PolicyMatch{}, false
	}
	rest := text[len(parts[0]):]
	i := strings.Index(rest, parts[1])
	if i <= 0 {
		return PolicyMatch{}, false
	}
	prefix := rest[:i]
	rest = rest[i+len(parts[1]):]
	j := strings.Index(rest, parts[2])
	if j <= 0 {
		return PolicyMatch{}, false
	}
	configPath := rest[:j]
	head := renderHead(version, prefix, configPath)
	if !strings.HasPrefix(text, head) {
		return PolicyMatch{}, false
	}
	return PolicyMatch{Version: version, Prefix: prefix, ConfigPath: configPath, Task: text[len(head):]}, true
}

// CurrentWording returns text with its policy part replaced by the current
// wording when MatchPolicy recognises it, keeping its prefix, configuration
// path and task. Otherwise it returns text unchanged and false.
func CurrentWording(text string) (string, PolicyMatch, bool) {
	m, ok := MatchPolicy(text)
	if !ok {
		return text, m, false
	}
	if m.Version == CurrentPolicyVersion() {
		return strings.TrimSpace(text), m, true
	}
	return PolicyHead(m.Prefix, m.ConfigPath) + m.Task, m, true
}

// PolicyState describes where a loop's standing wake policy comes from.
type PolicyState struct {
	Source string `json:"source"`
	// FileVersion is the wording the prompt file holds, for PolicyBuiltIn.
	FileVersion int `json:"fileVersion,omitempty"`
	// CurrentVersion is the wording this build sends to built-in loops.
	CurrentVersion int `json:"currentVersion"`
}

// DescribePolicy classifies a loop's prompt text. fromFile is false when the
// prompt comes from the configuration itself.
func DescribePolicy(text string, fromFile bool) PolicyState {
	s := PolicyState{Source: PolicyInline, CurrentVersion: CurrentPolicyVersion()}
	if !fromFile {
		return s
	}
	if m, ok := MatchPolicy(text); ok {
		s.Source, s.FileVersion = PolicyBuiltIn, m.Version
		return s
	}
	s.Source = PolicyCustom
	return s
}
