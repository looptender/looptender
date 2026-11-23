package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ModelStepSettingName holds the model step-down setting (looptender
// model-step). Like the usage watch's, it is a record in the state
// directory, not a key of the loop's configuration, so a build without it
// loads the configuration as before and never reads it.
const ModelStepSettingName = "model-step-setting.json"

// ModelStepName records a step-down in force: the model the conversation
// was stepped down from, the one it runs on, and when the blocked window
// resets.
const ModelStepName = "model-step.json"

// ModelStepSetting is the order the loop's own conversation may step down
// through, strongest first, on the same account.
type ModelStepSetting struct {
	Models []string  `json:"models"`
	SetAt  time.Time `json:"setAt"`
}

var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,79}$`)

// Valid: two to six model names, each of its own weekly-limit family.
func (s ModelStepSetting) Valid() bool {
	if len(s.Models) < 2 || len(s.Models) > 6 {
		return false
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		f := ModelFamily(m)
		if !modelName.MatchString(m) || f == "" || seen[f] {
			return false
		}
		seen[f] = true
	}
	return true
}

// ModelFamily is the model's weekly-limit family: the name a provider's
// per-model window uses (Claude's seven_day_<family>): the first part of the
// name that is not the provider's own ("claude-opus-5-5" is opus,
// "sonnet-4" is sonnet). Two models of one family share that window, so a
// step-down order holds at most one of each, and a window's model is
// matched to the family exactly.
func ModelFamily(m string) string {
	for _, p := range strings.FieldsFunc(strings.ToLower(m), func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ':' || r == '/' }) {
		if p == "claude" || p == "anthropic" || p == "" || (p[0] >= '0' && p[0] <= '9') {
			continue
		}
		return p
	}
	return ""
}

// ModelStep is a step-down in force, or one that failed for this window.
type ModelStep struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Window   string    `json:"window"`
	ResetsAt time.Time `json:"resetsAt"`
	At       time.Time `json:"at"`
	// Failed: the model could not be changed. It is not tried again until
	// the window resets; Failure says why, in fixed words.
	Failed  bool   `json:"failed,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// ModelStepNoteName holds the one line the agent is told with its next
// message after its model was changed, so it knows which model it is on and
// why.
const ModelStepNoteName = "model-step-note.json"

// ModelStepNote is that line, until it is sent.
type ModelStepNote struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// LoadModelStepNote reads the line; nil, nil when there is none.
func LoadModelStepNote(stateDir string) (*ModelStepNote, error) {
	var n ModelStepNote
	found, err := loadSmallRecord(stateDir, ModelStepNoteName, &n)
	if err != nil || !found {
		return nil, err
	}
	return &n, nil
}

// SaveModelStepNote replaces the line, or removes it when n is nil.
func SaveModelStepNote(stateDir string, n *ModelStepNote) error {
	if n == nil {
		return removeRecord(stateDir, ModelStepNoteName)
	}
	return saveSmallRecord(stateDir, ModelStepNoteName, n)
}

// LoadModelStepSetting reads the setting; nil, nil when step-down is off.
func LoadModelStepSetting(stateDir string) (*ModelStepSetting, error) {
	var s ModelStepSetting
	found, err := loadSmallRecord(stateDir, ModelStepSettingName, &s)
	if err != nil || !found {
		return nil, err
	}
	if !s.Valid() {
		return nil, fmt.Errorf("%s: not a valid setting", ModelStepSettingName)
	}
	return &s, nil
}

// SaveModelStepSetting replaces the setting, or removes it (off) when s is
// nil.
func SaveModelStepSetting(stateDir string, s *ModelStepSetting) error {
	if s == nil {
		return removeRecord(stateDir, ModelStepSettingName)
	}
	if !s.Valid() {
		return fmt.Errorf("%s: not a valid setting", ModelStepSettingName)
	}
	return saveSmallRecord(stateDir, ModelStepSettingName, s)
}

// LoadModelStep reads the step in force; nil, nil when there is none.
func LoadModelStep(stateDir string) (*ModelStep, error) {
	var s ModelStep
	found, err := loadSmallRecord(stateDir, ModelStepName, &s)
	if err != nil || !found {
		return nil, err
	}
	return &s, nil
}

// SaveModelStep replaces the step in force, or removes it when s is nil.
func SaveModelStep(stateDir string, s *ModelStep) error {
	if s == nil {
		return removeRecord(stateDir, ModelStepName)
	}
	return saveSmallRecord(stateDir, ModelStepName, s)
}

func removeRecord(stateDir, name string) error {
	if err := os.Remove(filepath.Join(stateDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
