package decide

import (
	"math"
	"testing"
	"time"
)

func TestDecideQuotaSwitchTable(t *testing.T) {
	base := func() Input {
		return Input{
			Control:  Control{Enabled: true},
			InWindow: true,
			Obs:      Observation{State: Idle, ErrorClass: ErrNone},
			Failover: Failover{Available: true},
			QuotaSwitch: QuotaSwitch{
				Threshold:        0.85,
				ReadingAvailable: true,
				HighestUsed:      0.85,
				ReadingAge:       10 * time.Minute,
				MaxAge:           6 * time.Hour,
				Window:           "seven_day",
			},
		}
	}

	cases := []struct {
		name            string
		edit            func(*Input)
		wantAction      Action
		wantQuotaSwitch bool
	}{
		{
			name:            "switches at exact threshold",
			edit:            func(_ *Input) {},
			wantAction:      Handoff,
			wantQuotaSwitch: true,
		},
		{
			name:            "switches above threshold",
			edit:            func(in *Input) { in.QuotaSwitch.HighestUsed = 0.95 },
			wantAction:      Handoff,
			wantQuotaSwitch: true,
		},
		{
			name:       "disabled when threshold is zero",
			edit:       func(in *Input) { in.QuotaSwitch.Threshold = 0; in.QuotaSwitch.HighestUsed = 1.0 },
			wantAction: Wake,
		},
		{
			name:       "below threshold wakes normally",
			edit:       func(in *Input) { in.QuotaSwitch.HighestUsed = 0.849 },
			wantAction: Wake,
		},
		{
			name:       "unavailable reading never switches",
			edit:       func(in *Input) { in.QuotaSwitch.ReadingAvailable = false },
			wantAction: Wake,
		},
		{
			name:       "stale flag never switches",
			edit:       func(in *Input) { in.QuotaSwitch.Stale = true },
			wantAction: Wake,
		},
		{
			name:       "age past max_age never switches",
			edit:       func(in *Input) { in.QuotaSwitch.ReadingAge = 6*time.Hour + time.Second },
			wantAction: Wake,
		},
		{
			name:       "negative age never switches",
			edit:       func(in *Input) { in.QuotaSwitch.ReadingAge = -time.Second },
			wantAction: Wake,
		},
		{
			name:       "missing max_age never switches",
			edit:       func(in *Input) { in.QuotaSwitch.MaxAge = 0 },
			wantAction: Wake,
		},
		{
			name:       "malformed NaN usage never switches",
			edit:       func(in *Input) { in.QuotaSwitch.HighestUsed = math.NaN() },
			wantAction: Wake,
		},
		{
			name:       "malformed >1 usage never switches",
			edit:       func(in *Input) { in.QuotaSwitch.HighestUsed = 1.2 },
			wantAction: Wake,
		},
		{
			name:       "no confirmed fallback wakes normally",
			edit:       func(in *Input) { in.Failover.Available = false },
			wantAction: Wake,
		},
		{
			name:       "handoff deferred returns none",
			edit:       func(in *Input) { in.HandoffDeferred = true },
			wantAction: None,
		},
		{
			name:       "active turn does not switch mid-turn",
			edit:       func(in *Input) { in.Obs.State = Active; in.StallAfter = time.Hour },
			wantAction: None,
		},
		{
			name:       "refusal streak never routes",
			edit:       func(in *Input) { in.RefusalStreak = 1 },
			wantAction: Wake,
		},
		{
			name:       "restricted turn holds and never routes",
			edit:       func(in *Input) { in.Obs.State = Error; in.Obs.ErrorClass = ErrRestricted },
			wantAction: AwaitUser,
		},
		{
			name: "refused turn continues in place and never routes",
			edit: func(in *Input) {
				in.Obs.State = Error
				in.Obs.ErrorClass = ErrRefused
				in.SinceRefusal = 5 * time.Minute
			},
			wantAction: Reprompt,
		},
		{
			name:       "stop wins over threshold (active)",
			edit:       func(in *Input) { in.Control.Stop = true; in.Obs.State = Active },
			wantAction: Stop,
		},
		{
			name:       "stop wins over threshold (idle)",
			edit:       func(in *Input) { in.Control.Stop = true },
			wantAction: None,
		},
		{
			name:       "paused wins over threshold",
			edit:       func(in *Input) { in.Control.Paused = true },
			wantAction: None,
		},
		{
			name:       "awaiting user wins over threshold",
			edit:       func(in *Input) { in.Control.AwaitingUser = true },
			wantAction: None,
		},
		{
			name:       "completed wins over threshold",
			edit:       func(in *Input) { in.Control.Completed = true },
			wantAction: None,
		},
		{
			name:       "observed target with wake_only never switches",
			edit:       func(in *Input) { in.ObservedOnly = true; in.ObservedWake = true },
			wantAction: None,
		},
		{
			name:       "observed target never switches",
			edit:       func(in *Input) { in.ObservedOnly = true },
			wantAction: None,
		},
		{
			name:       "outside window never switches",
			edit:       func(in *Input) { in.InWindow = false },
			wantAction: None,
		},
		{
			name:       "network down never switches",
			edit:       func(in *Input) { in.NetworkDown = true },
			wantAction: None,
		},
		{
			name:       "queued messages prevent switch",
			edit:       func(in *Input) { in.Obs.Queued = 1 },
			wantAction: None,
		},
		{
			name:       "background work prevents switch",
			edit:       func(in *Input) { in.Obs.Background = 1 },
			wantAction: None,
		},
		{
			name:       "switch_back_ready takes precedence when returning to primary",
			edit:       func(in *Input) { in.SwitchBackReady = true },
			wantAction: SwitchBack,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.edit(&in)
			got := Decide(in)
			if got.Action != tc.wantAction || got.QuotaSwitch != tc.wantQuotaSwitch {
				t.Fatalf("Decide() = %+v, want Action=%s QuotaSwitch=%v", got, tc.wantAction, tc.wantQuotaSwitch)
			}
		})
	}
}
