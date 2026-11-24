// Package limits turns the limit headers of a provider's answer, as the
// account router's front reports them, into the windows the routing decision
// reads (internal/router/route). It does no I/O.
package limits

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/router/route"
)

// claudePrefix starts every limit header of Claude Code's provider. The
// names were seen on real answers. The forms of the values are NOT verified:
// a fraction for use and seconds since 1970 for a reset are what a Claude
// Code client accepted from a stand-in, and no real answer's values were
// kept. So this reader accepts exactly those forms and treats everything
// else as unknown.
const claudePrefix = "anthropic-ratelimit-unified-"

// claudeWindows are the window names the headers use and the names the rest
// of looptender uses for the same windows.
var claudeWindows = map[string]string{"5h": "five_hour", "7d": "seven_day"}

// Reading is what one answer's headers say.
type Reading struct {
	// Windows are the windows the answer named and that could be read, in
	// name order. An answer does not name every window every time. A window
	// whose use could not be read in the expected form is not here: it is
	// unknown, which is neither "has room" nor "full".
	Windows []route.Window
	// Status is the overall status the answer gave when it is one of the
	// three known words, else "".
	Status string
	// Reset is the overall reset the answer named, zero when it named none
	// that could be believed.
	Reset time.Time
}

// The furthest a reset is believed to lie from the moment it is read. A
// time outside this range is treated as not given.
const (
	resetPast   = 24 * time.Hour
	resetAhead  = 35 * 24 * time.Hour
	fractionMax = 2 // use is a fraction of the window; a little over 1 when past the limit
)

// Claude reads the limit headers of one answer at now. headers are by
// lower-case name.
//
// It is strict on purpose. Only the windows it has names for become windows:
// another header that ends in "-utilization", such as an overage figure,
// must never shut a subscription. A use is accepted only as a plain decimal
// fraction from 0 to 2; a reset only as whole seconds since 1970 that lie
// within a day behind and five weeks ahead of now. Anything else is unknown
// and yields nothing: a wrong reading is worse than none.
func Claude(headers map[string]string, now time.Time) Reading {
	r := Reading{Reset: seconds(headers[claudePrefix+"reset"], now)}
	switch status := headers[claudePrefix+"status"]; status {
	case "allowed", "allowed_warning", "rejected":
		r.Status = status
	}
	for short, name := range claudeWindows {
		used, ok := fraction(headers[claudePrefix+short+"-utilization"])
		if !ok {
			continue
		}
		r.Windows = append(r.Windows, route.Window{Name: name, UsedPercent: used * 100, ResetsAt: seconds(headers[claudePrefix+short+"-reset"], now), At: now})
	}
	sort.Slice(r.Windows, func(i, j int) bool { return r.Windows[i].Name < r.Windows[j].Name })
	return r
}

// fraction reads a use written as a plain decimal fraction: digits, at most
// one point, nothing else. No sign, no exponent, no word.
func fraction(v string) (float64, bool) {
	if v == "" || len(v) > 12 || strings.Trim(v, "0123456789.") != "" || strings.Count(v, ".") > 1 || v == "." {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > fractionMax {
		return 0, false
	}
	return f, true
}

// seconds reads a time given as whole seconds since 1970, zero when it is
// not one or lies outside the range a reset can plausibly have.
func seconds(v string, now time.Time) time.Time {
	if v == "" || len(v) > 11 || strings.Trim(v, "0123456789") != "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	t := time.Unix(n, 0).UTC()
	if t.Before(now.Add(-resetPast)) || t.After(now.Add(resetAhead)) {
		return time.Time{}
	}
	return t
}
