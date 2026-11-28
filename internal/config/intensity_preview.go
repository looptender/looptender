package config

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

// ProjectionReason is a fixed refusal code, never a whole-config validity verdict.
type ProjectionReason string

const (
	ProjectionInvalidConfig         ProjectionReason = "invalid_config"
	ProjectionUnsupportedAdapter    ProjectionReason = "unsupported_adapter"
	ProjectionUnrepresentableConfig ProjectionReason = "unrepresentable_config"
)

type intensityState string

type intensityAdapter string

// IntensityConfigProjectionV1 contains only allowlisted supplied declarations.
// It certifies neither full configuration validation nor effective policy.
// Every JSON field is required; optional quantities use explicit nulls.
type IntensityConfigProjectionV1 struct {
	SchemaVersion       int                `json:"schemaVersion"`
	Source              intensityState     `json:"source"`
	ConfigFormatVersion int                `json:"configFormatVersion"`
	Adapter             intensityAdapter   `json:"adapter"`
	Profile             intensityProfile   `json:"profile"`
	Limits              intensityLimits    `json:"limits"`
	Pacing              intensityPacing    `json:"pacing"`
	Selection           intensitySelection `json:"selection"`
	ClaudeBudget        intensityBudget    `json:"claudeBudget"`
	Coverage            intensityCoverage  `json:"coverage"`
}

type intensityProfile struct {
	State intensityState `json:"state"`
}
type intensityCount struct {
	State intensityState `json:"state"`
	Count *string        `json:"count"`
}
type intensityDeadline struct {
	State intensityState `json:"state"`
	UTC   *string        `json:"utc"`
}
type intensityDuration struct {
	State       intensityState `json:"state"`
	Nanoseconds *string        `json:"nanoseconds"`
}
type intensityCreation struct {
	State            intensityState `json:"state"`
	NewConversations *int           `json:"newConversations"`
	ChildSuccessors  *int           `json:"childSuccessors"`
}
type intensityLimits struct {
	SendAllowance     intensityCount    `json:"sendAllowance"`
	Deadline          intensityDeadline `json:"deadline"`
	NoProgressTimeout intensityDuration `json:"noProgressTimeout"`
	Creation          intensityCreation `json:"creation"`
}
type intensityIdleSpacing struct {
	State              intensityState `json:"state"`
	FirstNanoseconds   *string        `json:"firstNanoseconds"`
	MaximumNanoseconds *string        `json:"maximumNanoseconds"`
}
type intensityPacing struct {
	MinimumWakeNanoseconds string               `json:"minimumWakeNanoseconds"`
	IdleSpacing            intensityIdleSpacing `json:"idleSpacing"`
}
type intensitySelection struct {
	Model  intensityState `json:"model"`
	Effort intensityState `json:"effort"`
}
type intensityBudget struct {
	State intensityState `json:"state"`
	USD   *string        `json:"usd"`
}
type intensityCoverage struct {
	SharedConcurrency   intensityState `json:"sharedConcurrency"`
	AllowanceReserve    intensityState `json:"allowanceReserve"`
	PeriodSpend         intensityState `json:"periodSpend"`
	EffectivePolicy     intensityState `json:"effectivePolicy"`
	DynamicState        intensityState `json:"dynamicState"`
	InvocationOverrides intensityState `json:"invocationOverrides"`
}

// ProjectIntensityConfig copies selected structural/scalar declarations into a
// detached, bounded DTO. The caller owns full validation, default application,
// route resolution and source checks outside this pure function, when required.
// Success checks only projected invariants, not that the caller met those gates.
// Unprojected fields are deliberately not inspected. Do not mutate c concurrently.
func ProjectIntensityConfig(c *Config) (*IntensityConfigProjectionV1, ProjectionReason) {
	if c == nil || c.Version != 1 {
		return nil, ProjectionInvalidConfig
	}
	l := &c.Loop
	var adapter intensityAdapter
	var model, effort intensityState
	budget := intensityBudget{State: "not_applicable"}
	creation := intensityCreation{State: "not_applicable"}
	switch l.Adapter {
	case "bb":
		if l.BB == nil {
			return nil, ProjectionInvalidConfig
		}
		adapter = "bb"
		model, effort = intensityPresence(l.BB.Model), intensityPresence(l.BB.Reasoning)
		n, s := l.Limits.MaxNewThreads, l.Limits.MaxChildSuccessors
		if n < 1 || n > MaxNewThreadsCeiling || s < 1 || s > MaxChildSuccessorsCeiling {
			return nil, ProjectionInvalidConfig
		}
		creation = intensityCreation{State: "declared", NewConversations: &n, ChildSuccessors: &s}
	case "claude":
		if l.Claude == nil {
			return nil, ProjectionInvalidConfig
		}
		adapter = "claude"
		model, effort = intensityPresence(l.Claude.Model), intensityPresence(l.Claude.Effort)
		v := l.Claude.MaxBudgetUSD
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, ProjectionUnrepresentableConfig
		}
		budget.State = "unset"
		if v > 0 {
			usd := strconv.FormatFloat(v, 'g', -1, 64)
			if len(usd) > 32 {
				return nil, ProjectionUnrepresentableConfig
			}
			budget = intensityBudget{State: "declared_option", USD: &usd}
		}
	case "codex":
		if l.Codex == nil {
			return nil, ProjectionInvalidConfig
		}
		adapter = "codex"
		model, effort = intensityPresence(l.Codex.Model), "not_applicable"
	default:
		return nil, ProjectionUnsupportedAdapter
	}
	if l.Limits.MaxSends < 0 || l.Progress.MinWakeInterval.Duration < time.Minute {
		return nil, ProjectionInvalidConfig
	}
	send := intensityCount{State: "unset"}
	if l.Limits.MaxSends > 0 {
		count := strconv.FormatInt(l.Limits.MaxSends, 10)
		send = intensityCount{State: "declared", Count: &count}
	}
	deadline := intensityDeadline{State: "unset"}
	if l.Limits.Deadline != "" {
		instant, err := time.ParseInLocation(time.RFC3339Nano, l.Limits.Deadline, time.UTC)
		if err != nil {
			return nil, ProjectionInvalidConfig
		}
		instant = instant.UTC()
		if instant.Year() < 2000 || instant.Year() > 9999 {
			return nil, ProjectionUnrepresentableConfig
		}
		utc := instant.Format(time.RFC3339Nano)
		if len(utc) > 30 {
			return nil, ProjectionUnrepresentableConfig
		}
		deadline = intensityDeadline{State: "declared", UTC: &utc}
	}
	noProgress := intensityDuration{State: "unset"}
	if d := l.Limits.NoProgressTimeout.Duration; d != 0 {
		if d < time.Minute {
			return nil, ProjectionInvalidConfig
		}
		ns := strconv.FormatInt(int64(d), 10)
		noProgress = intensityDuration{State: "declared", Nanoseconds: &ns}
	}
	idle := intensityIdleSpacing{State: "off"}
	if spacing := l.Progress.IdleSpacing; spacing != nil {
		first, maximum := spacing.First.Duration, spacing.Max.Duration
		if first < time.Minute || maximum < first || maximum > 24*time.Hour {
			return nil, ProjectionInvalidConfig
		}
		firstNS, maxNS := strconv.FormatInt(int64(first), 10), strconv.FormatInt(int64(maximum), 10)
		idle = intensityIdleSpacing{State: "declared", FirstNanoseconds: &firstNS, MaximumNanoseconds: &maxNS}
	}
	result := &IntensityConfigProjectionV1{
		SchemaVersion: 1, Source: "supplied_config_declarations", ConfigFormatVersion: c.Version, Adapter: adapter,
		Profile:   intensityProfile{State: "not_configured"},
		Limits:    intensityLimits{SendAllowance: send, Deadline: deadline, NoProgressTimeout: noProgress, Creation: creation},
		Pacing:    intensityPacing{MinimumWakeNanoseconds: strconv.FormatInt(int64(l.Progress.MinWakeInterval.Duration), 10), IdleSpacing: idle},
		Selection: intensitySelection{Model: model, Effort: effort}, ClaudeBudget: budget,
		Coverage: intensityCoverage{SharedConcurrency: "not_implemented", AllowanceReserve: "not_implemented", PeriodSpend: "not_implemented", EffectivePolicy: "not_computed", DynamicState: "not_sampled", InvocationOverrides: "not_evaluated"},
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 4096 {
		return nil, ProjectionUnrepresentableConfig
	}
	return result, ""
}

func intensityPresence(value string) intensityState {
	if value == "" {
		return "unset"
	}
	return "configured_redacted"
}
