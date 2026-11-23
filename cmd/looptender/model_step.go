package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// model-step turns the model step-down on or off for a loop, or shows it.
// Like usage-watch, the setting is a record in the state directory, never
// a key of the configuration.
func modelStepCommand(e env, args []string) int {
	fs := flag.NewFlagSet("model-step", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("c", "", configFlagHelp("configuration"))
	models := fs.String("models", "", "comma-separated models the loop's conversation may step down through, strongest first (2 to 6)")
	off := fs.Bool("off", false, "turn step-down off")
	asJSON := fs.Bool("json", false, "print the setting as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		e.errf("usage: model-step [-c CONFIG] [--models STRONGEST,NEXT[,...] | --off] [--json]")
		return exitUsage
	}
	if *off && *models != "" {
		e.errf("model-step: --off and --models do not go together")
		return exitUsage
	}
	if !configDefault(e, "model-step", fs, path) {
		return exitUsage
	}
	cfg, err := config.Load(*path)
	if err != nil {
		e.errf("model-step: configuration cannot be read or validated; run doctor for diagnostics")
		return exitUsage
	}
	dir := cfg.Loop.StateDir
	if (*off || *models != "") && os.MkdirAll(dir, 0o700) != nil {
		e.errf("model-step: the state directory cannot be created")
		return exitFailed
	}
	switch {
	case *off:
		if err := state.SaveModelStepSetting(dir, nil); err != nil {
			e.errf("model-step: %v", err)
			return exitFailed
		}
	case *models != "":
		s := state.ModelStepSetting{SetAt: time.Now().UTC()}
		for _, m := range strings.Split(*models, ",") {
			s.Models = append(s.Models, strings.TrimSpace(m))
		}
		if !s.Valid() {
			e.errf("model-step: --models takes 2 to 6 distinct model names, strongest first")
			return exitUsage
		}
		if err := state.SaveModelStepSetting(dir, &s); err != nil {
			e.errf("model-step: %v", err)
			return exitFailed
		}
		if cfg.Loop.Adapter != "bb" {
			e.errf("model-step: warning: step-down acts only on a BB conversation; this loop's adapter is %s", cfg.Loop.Adapter)
		}
	}
	v := describeModelStep(dir)
	if *asJSON {
		_ = json.NewEncoder(e.stdout).Encode(struct {
			SchemaVersion int            `json:"schemaVersion"`
			ModelStep     *modelStepView `json:"modelStep"`
		}{1, v})
		return exitOK
	}
	if v == nil {
		fmt.Fprintln(e.stdout, "model step-down: off")
		return exitOK
	}
	printModelStep(e.stdout, v)
	return exitOK
}

// modelStepView is the setting and any step in force, as status says them.
type modelStepView struct {
	Models     []string         `json:"models,omitempty"`
	Unreadable bool             `json:"unreadable,omitempty"`
	InForce    *state.ModelStep `json:"inForce,omitempty"`
}

func describeModelStep(stateDir string) *modelStepView {
	s, err := state.LoadModelStepSetting(stateDir)
	step, _ := state.LoadModelStep(stateDir)
	switch {
	case err != nil:
		return &modelStepView{Unreadable: true, InForce: step}
	case s == nil && step == nil:
		return nil
	}
	v := &modelStepView{InForce: step}
	if s != nil {
		v.Models = s.Models
	}
	return v
}

func printModelStep(w io.Writer, v *modelStepView) {
	switch {
	case v == nil:
		return
	case v.Unreadable:
		fmt.Fprintf(w, "model step-down: off: %s cannot be read; set it again with model-step\n", state.ModelStepSettingName)
	case len(v.Models) > 0:
		fmt.Fprintf(w, "model step-down: on, through %s, at a limit on one model's weekly window\n", strings.Join(v.Models, ", "))
	default:
		fmt.Fprintln(w, "model step-down: off")
	}
	if s := v.InForce; s != nil {
		fmt.Fprintf(w, "  stepped down from %s to %s (%s); steps back after %s, between turns\n", s.From, s.To, s.Window, s.ResetsAt.Format(time.RFC3339))
	}
}
