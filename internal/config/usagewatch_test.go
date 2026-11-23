package config

import (
	"strings"
	"testing"
	"time"
)

func TestUsageWatchIsOptInAndBounded(t *testing.T) {
	cfg, err := Parse([]byte(valid))
	if err != nil || cfg.Loop.UsageWatch != nil {
		t.Fatal(err)
	}
	cfg, err = Parse([]byte(valid + "  usage_watch: {account: example-claude-a}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if u := cfg.Loop.UsageWatch; u.Every.Duration != 30*time.Minute || u.WarnWithin.Duration != 12*time.Hour {
		t.Fatal(u)
	}
	for _, extra := range []string{"every: 30m", "account: ''", "account: '../x'", "account: a, every: 5m", "account: a, every: 25h", "account: a, warn_within: 30m", "account: a, warn_within: 169h"} {
		if _, err := Parse([]byte(valid + "  usage_watch: {" + extra + "}\n")); err == nil {
			t.Fatal("accepted", extra)
		} else if !strings.Contains(err.Error(), "usage_watch") {
			t.Fatal(err)
		}
	}
}
