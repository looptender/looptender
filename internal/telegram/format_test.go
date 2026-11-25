package telegram

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatAlert(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 34, 56, 0, time.UTC)
	got := FormatAlert(AlertSummary{
		Device:     "example-mac",
		Loop:       "example-loop\x00",
		Event:      "await_user",
		Summary:    "Waiting for person\r\nNeeds review\x07",
		Route:      "primary-claude",
		HoldReason: "no_progress_timeout",
		At:         at,
	})
	want := "[await_user] example-loop (example-mac)\nWaiting for person\nNeeds review\nroute: primary-claude | hold: no_progress_timeout | 2026-11-20T12:34:56Z"
	if got != want {
		t.Fatalf("FormatAlert:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFormatAlertTruncatesToTelegramLimit(t *testing.T) {
	got := FormatAlert(AlertSummary{
		Loop:    "example-loop",
		Event:   "error",
		Summary: strings.Repeat("é", MaxMessageRunes+100),
	})
	if utf8.RuneCountInString(got) != MaxMessageRunes {
		t.Fatalf("rune count = %d, want %d", utf8.RuneCountInString(got), MaxMessageRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix")
	}
}

func TestDeviceLabelPrivacyAndSanitization(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace_and_control", in: " \t\n\x00 ", want: ""},
		{name: "collapses_whitespace_and_strips_control", in: " work\x07\tmac\nmini ", want: "work mac mini"},
		{name: "bounded_to_64_runes", in: strings.Repeat("a", MaxDeviceLabelRunes+10), want: strings.Repeat("a", MaxDeviceLabelRunes-1) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeDeviceLabel(tc.in); got != tc.want {
				t.Fatalf("SanitizeDeviceLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if got := EffectiveDeviceLabel(PairingRecord{}); got != DefaultDeviceLabel {
		t.Fatalf("default EffectiveDeviceLabel = %q, want %q", got, DefaultDeviceLabel)
	}
	if got := EffectiveDeviceLabel(PairingRecord{DeviceLabel: "  home\x00box  "}); got != "homebox" {
		t.Fatalf("configured EffectiveDeviceLabel = %q, want %q", got, "homebox")
	}
}
