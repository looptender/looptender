// Package route decides which subscription serves a request of a
// conversation. It is the account router's decision (docs/design/router.md,
// "Routing") and nothing else: no I/O, no clock, no credential. The caller
// passes the time and keeps the state.
//
// The rules, in the order they apply:
//
//   - A conversation stays on the subscription that last served it while that
//     one has room.
//   - A new conversation goes to the first subscription in the person's order
//     that has room.
//   - Past the soft threshold a subscription takes no new conversations, and a
//     conversation on it moves only at a cold point: its first request after
//     it was quiet for longer than the prompt cache lasts.
//   - Past the hard threshold, or held after the provider's own limit answer,
//     a conversation moves on its next request.
//   - A conversation returns to a more preferred subscription that has room
//     again only at a cold point.
//   - A subscription that caused a move counts as full until its window resets
//     or a fresh reading shows it below the release level.
//   - Only a limit moves a conversation. A refusal, a restriction or a server
//     error never does: Observe takes such a subscription out of the order or
//     leaves it alone, and Choose never treats it as a reason to try another.
package route

import (
	"sort"
	"strings"
	"time"
)

// Settings are the person's thresholds. The zero value is not usable: start
// from Defaults.
type Settings struct {
	// Soft is the used percentage of any window from which a subscription
	// takes no new conversations.
	Soft float64
	// Hard is the used percentage from which a conversation leaves at once.
	Hard float64
	// Release is the used percentage a fresh reading must be under for a
	// subscription that caused a move to count as having room again.
	Release float64
	// Cold is how long a conversation must have been quiet for a move to
	// cost nothing: the prompt cache's lifetime.
	Cold time.Duration
	// PlacedShare is the estimated share of a window, in percentage points,
	// that each conversation placed since the last reading will use.
	PlacedShare float64
	// PinIdle is how long an unused pin is kept.
	PinIdle time.Duration
	// MaxPins caps the pins kept; the longest unused go first.
	MaxPins int
	// Stale is the age from which a reading no longer says where a new
	// conversation should go, and Near is how close to Soft, in percentage
	// points, a reading makes that uncertain too. New conversations are then
	// spread over the subscriptions with room instead of all going to the
	// first.
	Stale time.Duration
	Near  float64
	// MaxHold is the longest a limit answer is believed when no window
	// names a reset, and how long a subscription that caused a move counts
	// as full when nothing names a time.
	MaxHold time.Duration
	// ShortHold is used when a limit answer names nothing at all, and when a
	// hold turns out to have been set in the future.
	ShortHold time.Duration
}

// Defaults are the thresholds of the design document.
func Defaults() Settings {
	return Settings{
		Soft:        90,
		Hard:        98,
		Release:     85,
		Cold:        5 * time.Minute,
		PlacedShare: 1,
		PinIdle:     7 * 24 * time.Hour,
		MaxPins:     4096,
		Stale:       5 * time.Minute,
		Near:        5,
		MaxHold:     time.Hour,
		ShortHold:   time.Minute,
	}
}

// Window is one usage window of a subscription.
type Window struct {
	Name        string
	UsedPercent float64
	// ResetsAt is zero when the reading named no reset that could be
	// believed.
	ResetsAt time.Time
	// At is when the window was read. A window with no reset stops
	// counting MaxHold after it: nothing may stay shut for ever on a
	// reading that says no more. Every source of readings must set it
	// (Observe sets it from the observation's time when it is zero); a
	// window put into a State any other way with no At and no reset does
	// not count at all.
	At time.Time
	// Model is empty for a window that counts every model, else the model
	// family it counts ("opus" matches "claude-opus-…").
	Model string
}

// Reasons a subscription needs a person. A subscription with one is out of
// the order until a person clears it.
const (
	NeedsLogin      = "login_stale"
	NeedsRestricted = "restricted"
)

// Subscription is one entry of the person's order with what is known of it.
type Subscription struct {
	ID       string
	Disabled bool
	// NeedsPerson is empty, NeedsLogin or NeedsRestricted.
	NeedsPerson string
	Windows     []Window
	// ReadAt is when Windows were read from one of its own answers.
	ReadAt time.Time
	// Placed counts conversations placed on it since ReadAt.
	Placed int
	// HoldUntil keeps it out of the order after a limit answer; HoldSetAt is
	// when that was decided.
	HoldUntil time.Time
	HoldSetAt time.Time
	// LatchUntil is set when it caused a move: it counts as past the soft
	// threshold until then, or until a fresh reading under Release.
	LatchUntil time.Time
	// Unavailable lists models it was seen not to offer.
	Unavailable []string
}

// Pin is the subscription a conversation was last served by.
type Pin struct {
	Subscription string
	LastUsed     time.Time
}

// State is everything Choose reads. Order is the person's order.
type State struct {
	Order []Subscription
	Pins  map[string]Pin
}

// Request is one request to place.
type Request struct {
	// Conversation is empty when the request names none; it is then placed
	// like a new conversation every time and never pinned.
	Conversation string
	Model        string
	Now          time.Time
	// Exclude lists subscriptions that just refused this very request.
	Exclude []string
}

// Reason says why Choose answered as it did.
type Reason string

const (
	// Pinned: the conversation stays where it was.
	Pinned Reason = "pinned"
	// Placed: a conversation without a usable pin got the first subscription
	// with room.
	Placed Reason = "placed"
	// SoftMove: its subscription is past the soft threshold and the
	// conversation was cold.
	SoftMove Reason = "soft_move"
	// HardMove: its subscription is past the hard threshold, held, out of
	// the order, or just refused the request.
	HardMove Reason = "hard_move"
	// SwitchBack: a more preferred subscription has room again and the
	// conversation was cold.
	SwitchBack Reason = "switch_back"
	// NoRoom: no subscription can take the request.
	NoRoom Reason = "no_room"
)

// Decision is Choose's answer. Subscription is empty exactly when Reason is
// NoRoom; RoomAt is then the earliest time a subscription is expected to
// have room, zero when nothing says.
type Decision struct {
	Subscription string
	Reason       Reason
	RoomAt       time.Time
}

type level int

const (
	open level = iota // under the soft threshold
	soft              // takes no new conversations
	shut              // cannot serve this request
)

// counts reports whether window w limits a request for model at now. A
// window whose reset has passed does not; nor does one that named no reset
// and was read longer ago than MaxHold.
func counts(w Window, model string, now time.Time, set Settings) bool {
	if !w.ResetsAt.IsZero() && !w.ResetsAt.After(now) {
		return false
	}
	if w.ResetsAt.IsZero() && now.Sub(w.At) > set.MaxHold {
		return false
	}
	return w.Model == "" || strings.Contains(strings.ToLower(model), strings.ToLower(w.Model))
}

// holdEnd is when s's hold ends, zero when it has none at now. A hold set at
// a time later than now means the clock went back; it is then believed for
// ShortHold only. Choose changes nothing, so here that minute is counted
// from now; Expire is what cuts the hold for good, the first time it runs
// after the clock went back.
func holdEnd(s Subscription, set Settings, now time.Time) time.Time {
	if !s.HoldUntil.After(now) {
		return time.Time{}
	}
	if s.HoldSetAt.After(now) {
		if short := now.Add(set.ShortHold); short.Before(s.HoldUntil) {
			return short
		}
	}
	return s.HoldUntil
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// levelOf says how full s is for a request for model. placed adds the
// estimate for conversations placed since the reading, which counts only
// when deciding where a new conversation goes.
func levelOf(s Subscription, set Settings, rq Request, placed bool) level {
	if s.Disabled || s.NeedsPerson != "" || has(rq.Exclude, s.ID) || has(s.Unavailable, rq.Model) {
		return shut
	}
	if !holdEnd(s, set, rq.Now).IsZero() {
		return shut
	}
	lv := open
	if s.LatchUntil.After(rq.Now) {
		lv = soft
	}
	for _, w := range s.Windows {
		if !counts(w, rq.Model, rq.Now, set) {
			continue
		}
		used := w.UsedPercent
		if used >= set.Hard {
			return shut
		}
		if placed {
			used += float64(s.Placed) * set.PlacedShare
		}
		if used >= set.Soft {
			lv = soft
		}
	}
	return lv
}

// roomAt is when s is expected to serve a request for model again, zero when
// it can now or when nothing says (it needs a person, or does not offer the
// model).
func roomAt(s Subscription, set Settings, rq Request) time.Time {
	if s.Disabled || s.NeedsPerson != "" || has(s.Unavailable, rq.Model) {
		return time.Time{}
	}
	at := holdEnd(s, set, rq.Now)
	for _, w := range s.Windows {
		if counts(w, rq.Model, rq.Now, set) && w.UsedPercent >= set.Hard && w.ResetsAt.After(at) {
			at = w.ResetsAt
		}
	}
	return at
}

// first returns the first subscription in order at or under max, skipping
// the one named skip.
func first(st State, set Settings, rq Request, max level, placed bool, skip string) (string, bool) {
	for _, s := range st.Order {
		if s.ID != skip && levelOf(s, set, rq, placed) <= max {
			return s.ID, true
		}
	}
	return "", false
}

func find(st State, id string) (Subscription, int) {
	for i, s := range st.Order {
		if s.ID == id {
			return s, i
		}
	}
	return Subscription{}, -1
}

// Choose places one request. It changes nothing: the caller records what
// actually served the request with Served.
func Choose(st State, set Settings, rq Request) Decision {
	pin, pinned := st.Pins[rq.Conversation]
	cur, at := find(st, pin.Subscription)
	quiet := rq.Now.Sub(pin.LastUsed)
	if rq.Conversation == "" || !pinned || at < 0 || quiet > set.PinIdle {
		return place(st, set, rq, Placed)
	}
	// A pin last used in the future (the clock went back) is not cold yet:
	// Expire stamps it with the present, and it is cold a Cold later.
	cold := quiet > set.Cold
	switch levelOf(cur, set, rq, false) {
	case shut:
		return place(st, set, rq, HardMove)
	case soft:
		// Stopping is worse than staying on a nearly full subscription, and
		// moving mid-turn costs a full uncached prompt, so it moves only when
		// cold and only to a subscription that is itself under the threshold.
		if id, ok := first(st, set, rq, open, true, cur.ID); ok && cold {
			return Decision{Subscription: id, Reason: SoftMove}
		}
	default:
		if cold {
			for _, s := range st.Order[:at] {
				if levelOf(s, set, rq, true) == open {
					return Decision{Subscription: s.ID, Reason: SwitchBack}
				}
			}
		}
	}
	return Decision{Subscription: cur.ID, Reason: Pinned}
}

// place picks for a conversation with nowhere to stay. In order of
// preference: the first subscription that is known to be under the soft
// threshold (spread over those, when its reading is old or near the
// threshold); else the first of which nothing is known, in the person's
// order; else the first that can serve at all.
//
// A subscription with no reading is "unknown", which is not "has room": it
// is never preferred to one that is known to have room, and new
// conversations are never spread onto it. It is used when nothing known has
// room, which is how a spare that was never used gets its first request.
func place(st State, set Settings, rq Request, why Reason) Decision {
	for _, s := range st.Order {
		if len(s.Windows) == 0 || levelOf(s, set, rq, true) != open {
			continue
		}
		id := s.ID
		if uncertain(s, set, rq) {
			id = fewest(st, set, rq)
		}
		return Decision{Subscription: id, Reason: why}
	}
	if id, ok := first(st, set, rq, open, true, ""); ok {
		return Decision{Subscription: id, Reason: why}
	}
	if id, ok := first(st, set, rq, soft, false, ""); ok {
		return Decision{Subscription: id, Reason: why}
	}
	d := Decision{Reason: NoRoom}
	for _, s := range st.Order {
		if t := roomAt(s, set, rq); !t.IsZero() && (d.RoomAt.IsZero() || t.Before(d.RoomAt)) {
			d.RoomAt = t
		}
	}
	return d
}

// uncertain reports whether s's numbers are too old, or too close to the
// soft threshold, to send every new conversation its way.
func uncertain(s Subscription, set Settings, rq Request) bool {
	if rq.Now.Sub(s.ReadAt) > set.Stale {
		return true
	}
	for _, w := range s.Windows {
		if counts(w, rq.Model, rq.Now, set) && w.UsedPercent+set.Near >= set.Soft {
			return true
		}
	}
	return false
}

// fewest returns, among the subscriptions known to be under the soft
// threshold, the one with the fewest conversations placed since its
// reading; the person's order decides between equals.
func fewest(st State, set Settings, rq Request) string {
	best, n := "", 0
	for _, s := range st.Order {
		if len(s.Windows) > 0 && levelOf(s, set, rq, true) == open && (best == "" || s.Placed < n) {
			best, n = s.ID, s.Placed
		}
	}
	return best
}

// Served records that subscription id served the conversation at now, and
// that the conversation left the subscription it was pinned to when that is
// another one and why was a move for fullness. It keeps the pins within
// their cap.
func Served(st *State, set Settings, conversation, id string, why Reason, now time.Time) {
	_, at := find(*st, id)
	if at < 0 {
		return
	}
	old, had := st.Pins[conversation]
	if had && old.Subscription != id && (why == SoftMove || why == HardMove) {
		if s, i := find(*st, old.Subscription); i >= 0 {
			t := latchEnd(s, set, now)
			if t.IsZero() {
				// Nothing names a time: full for MaxHold, or until a fresh
				// reading under the release level.
				t = now.Add(set.MaxHold)
			}
			if t.After(st.Order[i].LatchUntil) {
				st.Order[i].LatchUntil = t
			}
		}
	}
	if conversation == "" {
		return
	}
	if !had || old.Subscription != id {
		st.Order[at].Placed++
	}
	if st.Pins == nil {
		st.Pins = map[string]Pin{}
	}
	st.Pins[conversation] = Pin{Subscription: id, LastUsed: now}
	Expire(st, set, now)
}

// latchEnd is until when a subscription that caused a move counts as full:
// its latest reset among the windows at or past the soft threshold, or the
// end of its hold. Zero when neither names a time.
func latchEnd(s Subscription, set Settings, now time.Time) time.Time {
	end := holdEnd(s, set, now)
	for _, w := range s.Windows {
		if w.UsedPercent >= set.Soft && w.ResetsAt.After(end) {
			end = w.ResetsAt
		}
	}
	return end
}

// Expire drops pins unused for PinIdle, pins to subscriptions no longer in
// the order, and the longest unused pins over MaxPins. It also settles what
// a clock that went back left behind: a hold set in the future is cut, once
// and for good, to ShortHold from now, and a pin last used in the future is
// stamped with now. Served runs it; a caller with nothing to serve runs it
// on its own timer.
func Expire(st *State, set Settings, now time.Time) {
	for i := range st.Order {
		s := &st.Order[i]
		if s.HoldSetAt.After(now) && s.HoldUntil.After(now) {
			if short := now.Add(set.ShortHold); short.Before(s.HoldUntil) {
				s.HoldUntil = short
			}
			s.HoldSetAt = now
		}
	}
	for c, p := range st.Pins {
		if _, at := find(*st, p.Subscription); at < 0 || now.Sub(p.LastUsed) > set.PinIdle {
			delete(st.Pins, c)
		} else if p.LastUsed.After(now) {
			p.LastUsed = now
			st.Pins[c] = p
		}
	}
	over := len(st.Pins) - set.MaxPins
	if set.MaxPins <= 0 || over <= 0 {
		return
	}
	keys := make([]string, 0, len(st.Pins))
	for c := range st.Pins {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := st.Pins[keys[i]], st.Pins[keys[j]]
		if !a.LastUsed.Equal(b.LastUsed) {
			return a.LastUsed.Before(b.LastUsed)
		}
		return keys[i] < keys[j]
	})
	for _, c := range keys[:over] {
		delete(st.Pins, c)
	}
}

// merge puts a reading's windows into what is known, by name. A known
// window the reading does not name is kept until its own reset has passed.
func merge(known, read []Window, at time.Time) []Window {
	out := make([]Window, 0, len(known)+len(read))
	named := map[string]bool{}
	for i, w := range read {
		named[w.Name] = true
		if w.At.IsZero() {
			read[i].At = at
		}
	}
	for _, w := range known {
		if !named[w.Name] && (w.ResetsAt.IsZero() || w.ResetsAt.After(at)) {
			out = append(out, w)
		}
	}
	return append(out, read...)
}

// Kind is what an answer said about the subscription that gave it.
type Kind int

const (
	// Reading: the answer carried the subscription's windows.
	Reading Kind = iota
	// Limited: the provider's own limit answer (429).
	Limited
	// Unauthorized: the provider rejected the subscription's login (401).
	Unauthorized
	// Restricted: the provider refused the subscription (403). Never a
	// reason to use another one for that request.
	Restricted
	// ModelUnavailable: the subscription does not offer the model.
	ModelUnavailable
	// Cleared: a person cleared NeedsPerson, or the login was read again.
	Cleared
	// Other: a server error, a network error, anything unclassified. It
	// changes nothing.
	Other
)

// Observation is one thing learnt about a subscription.
type Observation struct {
	Kind    Kind
	At      time.Time
	Windows []Window
	// RetryAfter is the limit answer's own wait, zero when it named none.
	RetryAfter time.Duration
	Model      string
}

// Observe takes in what an answer said about subscription id.
func Observe(st *State, set Settings, id string, o Observation) {
	_, at := find(*st, id)
	if at < 0 {
		return
	}
	s := &st.Order[at]
	switch o.Kind {
	case Reading:
		if len(o.Windows) == 0 {
			return
		}
		// An answer does not carry every window every time: what it names
		// replaces what was known of those windows, and the others stay.
		s.Windows, s.ReadAt, s.Placed = merge(s.Windows, o.Windows, o.At), o.At, 0
		if !s.HoldUntil.After(o.At) {
			s.HoldUntil, s.HoldSetAt = time.Time{}, time.Time{}
		}
		under := true
		for _, w := range s.Windows {
			if w.UsedPercent >= set.Release && (w.ResetsAt.IsZero() || w.ResetsAt.After(o.At)) {
				under = false
			}
		}
		if under {
			s.LatchUntil = time.Time{}
		}
	case Limited:
		// A reset that one of its full windows names is believed as it is.
		// The answer's own wait is believed up to the latest reset any of
		// its windows names, and for MaxHold at most when none names one: a
		// mistaken wait must not park a subscription for a year.
		var nearest, latest time.Time
		for _, w := range s.Windows {
			if !w.ResetsAt.After(o.At) {
				continue
			}
			if w.ResetsAt.After(latest) {
				latest = w.ResetsAt
			}
			if w.UsedPercent >= set.Soft && (nearest.IsZero() || w.ResetsAt.Before(nearest)) {
				nearest = w.ResetsAt
			}
		}
		until := o.At.Add(set.ShortHold)
		switch {
		case o.RetryAfter > 0:
			limit := latest
			if limit.IsZero() {
				limit = o.At.Add(set.MaxHold)
			}
			if until = o.At.Add(o.RetryAfter); until.After(limit) {
				until = limit
			}
		case !nearest.IsZero():
			until = nearest
		}
		if until.After(s.HoldUntil) {
			s.HoldUntil, s.HoldSetAt = until, o.At
		}
	case Unauthorized:
		s.NeedsPerson = NeedsLogin
	case Restricted:
		s.NeedsPerson = NeedsRestricted
	case ModelUnavailable:
		if o.Model != "" && !has(s.Unavailable, o.Model) {
			s.Unavailable = append(s.Unavailable, o.Model)
		}
	case Cleared:
		s.NeedsPerson = ""
	}
}
