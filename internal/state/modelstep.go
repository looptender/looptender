package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Valid: two to six distinct model names.
func (s ModelStepSetting) Valid() bool {
	if len(s.Models) < 2 || len(s.Models) > 6 {
		return false
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		if !modelName.MatchString(m) || seen[m] {
			return false
		}
		seen[m] = true
	}
	return true
}

// ModelStep is a step-down in force.
type ModelStep struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Window   string    `json:"window"`
	ResetsAt time.Time `json:"resetsAt"`
	At       time.Time `json:"at"`
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
