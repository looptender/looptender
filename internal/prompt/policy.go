package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/looptender/looptender/internal/config"
)

// The standing wake policy is the guidance setup writes above the task in a
// new loop's prompt file. A loop follows policy updates only while its
// prompt file is byte for byte the file setup, or prompt-policy apply, last
// wrote and recorded beside it (RecordPolicy): each wake then sends
// the running build's wording, followed by the file's own task. Any edit at
// all, including deleting a paragraph so that the file reads like an older
// wording, ends the updates; the file is then sent exactly as written. A
// file without a record is never upgraded: MatchPolicy only names the
// wording it holds, for status. Nothing here writes the prompt file.

// Policy sources reported by status and doctor.
const (
	// PolicyFollowing: the prompt file is the one recorded; the current
	// wording is sent.
	PolicyFollowing = "following"
	// PolicyGenerated: the prompt file reads as a wording setup wrote, but
	// it is not the recorded file (no record, or edited since): it is sent
	// as written.
	PolicyGenerated = "generated_not_following"
	// PolicyCustom: the prompt file differs from every wording setup wrote;
	// it is sent as written.
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
// path and task. Otherwise it returns text unchanged and false. A match
// alone does not mean the loop follows updates: see followedText.
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
	// FileVersion is the wording the prompt file reads as, when it reads as
	// one setup wrote.
	FileVersion int `json:"fileVersion,omitempty"`
	// Recorded says a record of the file to follow exists, whether or not
	// the file is still that file.
	Recorded bool `json:"recorded"`
	// CurrentVersion is the wording this build sends to following loops.
	CurrentVersion int `json:"currentVersion"`
}

// DescribePolicy classifies a loop's prompt. It reads the prompt file and
// the record; it writes nothing.
func DescribePolicy(loop config.Loop) (PolicyState, error) {
	s := PolicyState{Source: PolicyInline, CurrentVersion: CurrentPolicyVersion()}
	if loop.PromptFile == "" {
		return s, nil
	}
	raw, err := os.ReadFile(loop.PromptFile)
	if err != nil {
		return s, err
	}
	r, _ := LoadPolicyRecord(loop.PromptFile)
	s.Recorded = r != nil
	m, ok := MatchPolicy(string(raw))
	switch {
	case !ok:
		s.Source = PolicyCustom
	case r != nil && r.SHA256 == policyDigest(raw):
		s.Source, s.FileVersion = PolicyFollowing, m.Version
	default:
		s.Source, s.FileVersion = PolicyGenerated, m.Version
	}
	return s, nil
}

// followedText is the text a wake sends from a prompt file the loop follows
// policy updates for: the current wording and the file's task. ok is false
// when the loop does not follow them.
func followedText(loop config.Loop) (string, bool) {
	raw, err := os.ReadFile(loop.PromptFile)
	if err != nil {
		return "", false
	}
	r, err := LoadPolicyRecord(loop.PromptFile)
	if err != nil || r == nil || r.SHA256 != policyDigest(raw) {
		return "", false
	}
	text, _, ok := CurrentWording(string(raw))
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// PolicyRecordPath is the record of the prompt file whose policy part
// follows updates: a file beside it. Not in the state directory, which setup
// does not create.
func PolicyRecordPath(promptFile string) string { return promptFile + ".wake-policy.json" }

// PolicyRecord names the prompt file a loop follows policy updates for, by
// the SHA-256 of its bytes, and the wording it was written with.
type PolicyRecord struct {
	Version       int    `json:"version"`
	PolicyVersion int    `json:"policyVersion"`
	SHA256        string `json:"sha256"`
}

func policyDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LoadPolicyRecord returns nil, nil when there is no record.
func LoadPolicyRecord(promptFile string) (*PolicyRecord, error) {
	b, err := os.ReadFile(PolicyRecordPath(promptFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r PolicyRecord
	if err := json.Unmarshal(b, &r); err != nil || r.Version != 1 || len(r.SHA256) != 64 {
		return nil, errors.New("wake policy record unreadable")
	}
	return &r, nil
}

// RecordPolicy records that the loop follows policy updates for the prompt
// file whose bytes are prompt, written with the current wording. Only setup
// and prompt-policy apply, a person's commands, call it.
func RecordPolicy(promptFile string, promptBytes []byte) error {
	b, err := json.Marshal(PolicyRecord{Version: 1, PolicyVersion: CurrentPolicyVersion(), SHA256: policyDigest(promptBytes)})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(promptFile), ".wake-policy-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, PolicyRecordPath(promptFile))
}
