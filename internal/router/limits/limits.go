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
// names were seen on real answers; the forms of the values (a fraction for
// use, seconds since 1970 for a reset) are the ones a Claude Code client
// accepted from a stand-in, and have not been read from a real answer.
const claudePrefix = "anthropic-ratelimit-unified-"

// claudeWindows are the window names the headers use and the names the rest
// of looptender uses for the same windows.
var claudeWindows = map[string]string{"5h": "five_hour", "7d": "seven_day"}

// Reading is what one answer's headers say.
type Reading struct {
	// Windows are the windows the answer named, in name order. An answer
	// does not name every window every time.
	Windows []route.Window
	// Status is the overall status the answer gave: "allowed",
	// "allowed_warning", "rejected", or "" when it gave none.
	Status string
	// Reset is the overall reset the answer named, zero when it named none.
	Reset time.Time
}

// Claude reads the limit headers of one answer. headers are by lower-case
// name. A window is returned only when its use could be read.
func Claude(headers map[string]string) Reading {
	r := Reading{Status: headers[claudePrefix+"status"], Reset: seconds(headers[claudePrefix+"reset"])}
	for name, value := range headers {
		short, ok := strings.CutSuffix(strings.TrimPrefix(name, claudePrefix), "-utilization")
		if !ok || !strings.HasPrefix(name, claudePrefix) || short == "" {
			continue
		}
		used, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || used < 0 {
			continue
		}
		if used <= 1.5 {
			used *= 100 // a fraction; anything larger is already a percentage
		}
		w := route.Window{Name: short, UsedPercent: used, ResetsAt: seconds(headers[claudePrefix+short+"-reset"])}
		if known, ok := claudeWindows[short]; ok {
			w.Name = known
		}
		r.Windows = append(r.Windows, w)
	}
	sort.Slice(r.Windows, func(i, j int) bool { return r.Windows[i].Name < r.Windows[j].Name })
	return r
}

// seconds reads a time given as seconds since 1970, zero when it is not one.
func seconds(v string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}
