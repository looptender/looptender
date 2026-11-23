package config

import (
	"regexp"
	"time"
)

// UsageWatch opts a loop into reading, now and then, the subscription quota
// of the accounts catalog entry its own conversation runs on, and warning
// its agent once when the weekly window would run out before it resets at
// the rate used so far. The read is the catalog's quota read (no model
// request). It never acts on lanes and never changes a route.
type UsageWatch struct {
	// Account is the accounts catalog ID of the login the loop's primary
	// conversation uses.
	Account string `yaml:"account"`
	// Every is how often the quota is read: default 30m, from 10m to 24h.
	Every Duration `yaml:"every,omitempty"`
	// WarnWithin: the agent is warned once a window when, at the window's
	// rate so far, it would run out within this long and before its reset.
	// Default 12h, from 1h to 168h.
	WarnWithin Duration `yaml:"warn_within,omitempty"`
}

var usageWatchAccount = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (u *UsageWatch) defaults() {
	setDur(&u.Every, 30*time.Minute)
	setDur(&u.WarnWithin, 12*time.Hour)
}

func (u UsageWatch) valid() bool {
	return usageWatchAccount.MatchString(u.Account) &&
		u.Every.Duration >= 10*time.Minute && u.Every.Duration <= 24*time.Hour &&
		u.WarnWithin.Duration >= time.Hour && u.WarnWithin.Duration <= 168*time.Hour
}
