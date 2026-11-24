package route

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)

// sub builds a subscription with one all-models weekly window that resets
// in two days.
func sub(id string, used float64) Subscription {
	return Subscription{ID: id, ReadAt: t0, Windows: []Window{{Name: "seven_day", UsedPercent: used, ResetsAt: t0.Add(48 * time.Hour)}}}
}

func state(pins map[string]Pin, subs ...Subscription) State {
	return State{Order: subs, Pins: pins}
}

func pinned(id string, quiet time.Duration) map[string]Pin {
	return map[string]Pin{"c1": {Subscription: id, LastUsed: t0.Add(-quiet)}}
}

func TestChoose(t *testing.T) {
	set := Defaults()
	warm, cold := time.Minute, 10*time.Minute
	held := sub("main", 50)
	held.HoldUntil, held.HoldSetAt = t0.Add(30*time.Minute), t0.Add(-time.Minute)
	clockBack := sub("main", 50)
	clockBack.HoldUntil, clockBack.HoldSetAt = t0.Add(30*time.Minute), t0.Add(10*time.Minute)
	restricted := sub("main", 10)
	restricted.NeedsPerson = NeedsRestricted
	stale := sub("main", 10)
	stale.NeedsPerson = NeedsLogin
	disabled := sub("main", 10)
	disabled.Disabled = true
	noOpus := sub("main", 10)
	noOpus.Unavailable = []string{"claude-opus-9"}
	opusFull := sub("main", 10)
	opusFull.Windows = append(opusFull.Windows, Window{Name: "seven_day_opus", Model: "opus", UsedPercent: 100, ResetsAt: t0.Add(24 * time.Hour)})
	reset := sub("main", 100)
	reset.Windows[0].ResetsAt = t0.Add(-time.Minute)
	latched := sub("main", 60)
	latched.LatchUntil = t0.Add(time.Hour)
	crowded := sub("main", 86)
	crowded.Placed = 4

	cases := []struct {
		name   string
		st     State
		rq     Request
		want   string
		reason Reason
		roomAt time.Time
	}{
		{name: "a new conversation gets the first in order", st: state(nil, sub("main", 10), sub("spare", 10)), want: "main", reason: Placed},
		{name: "a new conversation skips one past the soft threshold", st: state(nil, sub("main", 91), sub("spare", 10)), want: "spare", reason: Placed},
		{name: "past soft everywhere, the first that can still serve", st: state(nil, sub("main", 95), sub("spare", 92)), want: "main", reason: Placed},
		{name: "a new conversation skips one past the hard threshold", st: state(nil, sub("main", 98), sub("spare", 95)), want: "spare", reason: Placed},
		{name: "a pinned conversation stays", st: state(pinned("spare", warm), sub("main", 10), sub("spare", 10)), want: "spare", reason: Pinned},
		{name: "a pinned conversation with a pin to a removed subscription is placed", st: state(pinned("gone", warm), sub("main", 10)), want: "main", reason: Placed},
		{name: "past soft, mid-turn: stays", st: state(pinned("main", warm), sub("main", 93), sub("spare", 10)), want: "main", reason: Pinned},
		{name: "past soft, cold: moves", st: state(pinned("main", cold), sub("main", 93), sub("spare", 10)), want: "spare", reason: SoftMove},
		{name: "past soft, cold, nowhere under soft: stays", st: state(pinned("main", cold), sub("main", 93), sub("spare", 94)), want: "main", reason: Pinned},
		{name: "past hard, mid-turn: moves at once", st: state(pinned("main", warm), sub("main", 98.5), sub("spare", 10)), want: "spare", reason: HardMove},
		{name: "past hard, the other past soft: still moves", st: state(pinned("main", warm), sub("main", 99), sub("spare", 95)), want: "spare", reason: HardMove},
		{name: "held after a limit answer: moves at once", st: state(pinned("main", warm), held, sub("spare", 10)), want: "spare", reason: HardMove},
		{name: "the subscription that just refused is excluded", st: state(pinned("main", warm), sub("main", 50), sub("spare", 10)), rq: Request{Exclude: []string{"main"}}, want: "spare", reason: HardMove},
		{name: "switch back to the preferred one, cold", st: state(pinned("spare", cold), sub("main", 10), sub("spare", 10)), want: "main", reason: SwitchBack},
		{name: "no switch back mid-turn", st: state(pinned("spare", warm), sub("main", 10), sub("spare", 10)), want: "spare", reason: Pinned},
		{name: "no switch back to one past soft", st: state(pinned("spare", cold), sub("main", 91), sub("spare", 10)), want: "spare", reason: Pinned},
		{name: "no switch back to one that caused a move", st: state(pinned("spare", cold), latched, sub("spare", 10)), want: "spare", reason: Pinned},
		{name: "a window that has reset no longer counts", st: state(nil, reset, sub("spare", 10)), want: "main", reason: Placed},
		{name: "a restricted subscription is out of the order", st: state(nil, restricted, sub("spare", 10)), want: "spare", reason: Placed},
		{name: "a stale login is out of the order", st: state(nil, stale, sub("spare", 10)), want: "spare", reason: Placed},
		{name: "a disabled subscription is out of the order", st: state(nil, disabled, sub("spare", 10)), want: "spare", reason: Placed},
		{name: "a model it does not offer", st: state(nil, noOpus, sub("spare", 10)), rq: Request{Model: "claude-opus-9"}, want: "spare", reason: Placed},
		{name: "the same subscription for another model", st: state(nil, noOpus, sub("spare", 10)), rq: Request{Model: "claude-sonnet-9"}, want: "main", reason: Placed},
		{name: "one model's window is full: that model goes elsewhere", st: state(nil, opusFull, sub("spare", 10)), rq: Request{Model: "claude-opus-9"}, want: "spare", reason: Placed},
		{name: "one model's window is full: other models stay", st: state(nil, opusFull, sub("spare", 10)), rq: Request{Model: "claude-sonnet-9"}, want: "main", reason: Placed},
		{name: "conversations placed since the reading count as use", st: state(nil, crowded, sub("spare", 10)), want: "spare", reason: Placed},
		{name: "but they do not move a conversation already there", st: state(pinned("main", cold), crowded, sub("spare", 10)), want: "main", reason: Pinned},
		{name: "no conversation named: placed, never pinned", st: state(pinned("spare", warm), sub("main", 10), sub("spare", 10)), rq: Request{Conversation: "-"}, want: "main", reason: Placed},
		{name: "no room anywhere: the earliest reset", st: state(pinned("main", warm), sub("main", 100), held), want: "", reason: NoRoom, roomAt: t0.Add(30 * time.Minute)},
		{name: "a hold set in the future is believed for a minute", st: state(nil, clockBack), want: "", reason: NoRoom, roomAt: t0.Add(time.Minute)},
		{name: "no room and nothing says when", st: state(nil, restricted), want: "", reason: NoRoom},
		{name: "a pin unused for longer than pins are kept is no pin", st: state(pinned("main", 8*24*time.Hour), sub("main", 93), sub("spare", 94)), want: "main", reason: Placed},
		{name: "a pin used six days ago still holds", st: state(pinned("main", 6*24*time.Hour), sub("main", 93), sub("spare", 94)), want: "main", reason: Pinned},
		{name: "a pin last used in the future is not cold", st: state(pinned("spare", -time.Hour), sub("main", 10), sub("spare", 10)), want: "spare", reason: Pinned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rq := c.rq
			rq.Now = t0
			switch rq.Conversation {
			case "":
				rq.Conversation = "c1"
			case "-":
				rq.Conversation = ""
			}
			if rq.Model == "" {
				rq.Model = "claude-sonnet-9"
			}
			before := fmt.Sprintf("%+v", c.st)
			got := Choose(c.st, set, rq)
			if got.Subscription != c.want || got.Reason != c.reason || !got.RoomAt.Equal(c.roomAt) {
				t.Fatalf("got %q %s room at %v, want %q %s room at %v", got.Subscription, got.Reason, got.RoomAt, c.want, c.reason, c.roomAt)
			}
			if after := fmt.Sprintf("%+v", c.st); after != before {
				t.Fatalf("Choose changed the state:\n%s\n%s", before, after)
			}
		})
	}
}

// What never moves a conversation: a refusal, a stale login and a server
// error on the subscription it is on are not limits. The first two take the
// subscription out of the order for every conversation; the third changes
// nothing. None of them holds the subscription or marks it full.
func TestObserveOnlyALimitHolds(t *testing.T) {
	set := Defaults()
	for _, k := range []Kind{Unauthorized, Restricted, ModelUnavailable, Other} {
		st := state(nil, sub("main", 10))
		Observe(&st, set, "main", Observation{Kind: k, At: t0, Model: "claude-opus-9"})
		s := st.Order[0]
		if !s.HoldUntil.IsZero() || !s.LatchUntil.IsZero() {
			t.Fatalf("kind %d held or latched the subscription: %+v", k, s)
		}
	}
	st := state(nil, sub("main", 10), sub("spare", 10))
	Observe(&st, set, "main", Observation{Kind: Other, At: t0})
	if got := Choose(st, set, Request{Conversation: "c1", Now: t0}); got.Subscription != "main" {
		t.Fatalf("a server error moved new conversations to %q", got.Subscription)
	}
	Observe(&st, set, "main", Observation{Kind: Restricted, At: t0})
	if st.Order[0].NeedsPerson != NeedsRestricted {
		t.Fatalf("restricted not recorded: %+v", st.Order[0])
	}
	Observe(&st, set, "main", Observation{Kind: Cleared, At: t0})
	if st.Order[0].NeedsPerson != "" {
		t.Fatalf("not cleared: %+v", st.Order[0])
	}
	Observe(&st, set, "nobody", Observation{Kind: Limited, At: t0})
}

func TestObserveLimitHolds(t *testing.T) {
	set := Defaults()
	far := sub("main", 100)
	near := sub("main", 100)
	near.Windows[0].ResetsAt = t0.Add(20 * time.Minute)
	bare := sub("main", 50)
	bare.Windows[0].ResetsAt = time.Time{}
	cases := []struct {
		name  string
		s     Subscription
		after time.Duration
		want  time.Duration
	}{
		{"the wait it names", sub("main", 50), 2 * time.Minute, 2 * time.Minute},
		{"a named wait of five hours, inside a window's reset", sub("main", 50), 5 * time.Hour, 5 * time.Hour},
		{"a named wait is believed up to the latest reset a window names", sub("main", 50), 400 * time.Hour, 48 * time.Hour},
		{"a named wait with no reset named anywhere: an hour at most", bare, 400 * time.Hour, time.Hour},
		{"no wait named: the nearest reset of a full window", near, 0, 20 * time.Minute},
		{"no wait named and a far reset: the reset, not an hour", far, 0, 48 * time.Hour},
		{"nothing named at all: a minute", sub("main", 50), 0, time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := state(nil, c.s)
			Observe(&st, set, "main", Observation{Kind: Limited, At: t0, RetryAfter: c.after})
			if got := st.Order[0].HoldUntil.Sub(t0); got != c.want {
				t.Fatalf("held for %v, want %v", got, c.want)
			}
			// A shorter hold never replaces a longer one.
			Observe(&st, set, "main", Observation{Kind: Limited, At: t0, RetryAfter: time.Second})
			if got := st.Order[0].HoldUntil.Sub(t0); got != c.want {
				t.Fatalf("a later, shorter hold replaced it: %v", got)
			}
		})
	}
}

// A subscription that caused a move stays full until its window resets or a
// fresh reading shows it under the release level, so two nearly full
// subscriptions cannot trade a conversation on noisy readings.
func TestServedLatchesTheSubscriptionLeft(t *testing.T) {
	set := Defaults()
	st := state(pinned("main", 10*time.Minute), sub("main", 93), sub("spare", 10))
	d := Choose(st, set, Request{Conversation: "c1", Now: t0})
	if d.Reason != SoftMove {
		t.Fatalf("want a soft move, got %+v", d)
	}
	Served(&st, set, "c1", d.Subscription, d.Reason, t0)
	if got := st.Pins["c1"]; got.Subscription != "spare" || !got.LastUsed.Equal(t0) {
		t.Fatalf("pin not moved: %+v", got)
	}
	if st.Order[1].Placed != 1 {
		t.Fatalf("placement not counted: %+v", st.Order[1])
	}
	if want := t0.Add(48 * time.Hour); !st.Order[0].LatchUntil.Equal(want) {
		t.Fatalf("latched until %v, want its reset %v", st.Order[0].LatchUntil, want)
	}

	later := t0.Add(time.Hour)
	// A reading that dropped a little (noise) does not bring it back.
	Observe(&st, set, "main", Observation{Kind: Reading, At: later, Windows: []Window{{Name: "seven_day", UsedPercent: 88, ResetsAt: t0.Add(48 * time.Hour)}}})
	if got := Choose(st, set, Request{Conversation: "c1", Now: later}); got.Subscription != "spare" {
		t.Fatalf("traded back on a reading above the release level: %+v", got)
	}
	// One under the release level does.
	Observe(&st, set, "main", Observation{Kind: Reading, At: later, Windows: []Window{{Name: "seven_day", UsedPercent: 40, ResetsAt: t0.Add(48 * time.Hour)}}})
	if got := Choose(st, set, Request{Conversation: "c1", Now: later}); got.Subscription != "main" || got.Reason != SwitchBack {
		t.Fatalf("no switch back after a fresh low reading: %+v", got)
	}
	// Staying put is not a placement and latches nothing.
	Served(&st, set, "c1", "spare", Pinned, later)
	if st.Order[1].Placed != 1 || !st.Order[1].LatchUntil.IsZero() {
		t.Fatalf("staying counted as a placement or latched: %+v", st.Order[1])
	}
	Served(&st, set, "c1", "nobody", Placed, later)
	Served(&st, set, "", "main", Placed, later)
	if _, ok := st.Pins[""]; ok {
		t.Fatal("a request naming no conversation was pinned")
	}
}

// Conversations that arrive together, before any answer carries new
// numbers, do not all land on a subscription near its threshold.
func TestPlacementsSpreadBeforeTheNextReading(t *testing.T) {
	set := Defaults()
	st := state(nil, sub("main", 87), sub("spare", 10))
	on := map[string]int{}
	for i := 0; i < 8; i++ {
		c := fmt.Sprintf("c%d", i)
		d := Choose(st, set, Request{Conversation: c, Now: t0})
		Served(&st, set, c, d.Subscription, d.Reason, t0)
		on[d.Subscription]++
	}
	if on["main"] != 3 || on["spare"] != 5 {
		t.Fatalf("placed %v, want 3 on main (87, 88, 89) and the rest on spare", on)
	}
	// A reading replaces the estimate.
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{{Name: "seven_day", UsedPercent: 87.2, ResetsAt: t0.Add(time.Hour)}}})
	if st.Order[0].Placed != 0 {
		t.Fatalf("reading kept the estimate: %+v", st.Order[0])
	}
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0})
	if len(st.Order[0].Windows) != 1 {
		t.Fatal("a reading with no windows erased the known ones")
	}
}

func TestExpire(t *testing.T) {
	set := Defaults()
	set.MaxPins = 2
	st := state(map[string]Pin{
		"old":    {Subscription: "main", LastUsed: t0.Add(-8 * 24 * time.Hour)},
		"gone":   {Subscription: "removed", LastUsed: t0},
		"first":  {Subscription: "main", LastUsed: t0.Add(-3 * time.Hour)},
		"second": {Subscription: "main", LastUsed: t0.Add(-2 * time.Hour)},
		"third":  {Subscription: "main", LastUsed: t0.Add(-time.Hour)},
	}, sub("main", 10))
	Expire(&st, set, t0)
	if len(st.Pins) != 2 || st.Pins["second"].Subscription == "" || st.Pins["third"].Subscription == "" {
		t.Fatalf("kept %v, want the two most recently used", st.Pins)
	}
}

// A reading after the hold's end clears the hold; one before it does not.
func TestReadingClearsAnEndedHold(t *testing.T) {
	set := Defaults()
	st := state(nil, sub("main", 50))
	Observe(&st, set, "main", Observation{Kind: Limited, At: t0, RetryAfter: 10 * time.Minute})
	w := []Window{{UsedPercent: 50, ResetsAt: t0.Add(time.Hour)}}
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0.Add(time.Minute), Windows: w})
	if st.Order[0].HoldUntil.IsZero() {
		t.Fatal("a reading during the hold cleared it")
	}
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0.Add(11 * time.Minute), Windows: w})
	if !st.Order[0].HoldUntil.IsZero() {
		t.Fatal("a reading after the hold kept it")
	}
}

// R22: a hold set in the future (the clock went back) is cut to a minute
// once, not a minute from every later moment.
func TestExpireCutsAHoldSetInTheFuture(t *testing.T) {
	set := Defaults()
	s := sub("main", 50)
	s.HoldSetAt, s.HoldUntil = t0.Add(time.Hour), t0.Add(2*time.Hour)
	st := state(nil, s)
	rq := Request{Conversation: "c1", Model: "m"}
	rq.Now = t0
	if d := Choose(st, set, rq); d.Reason != NoRoom || !d.RoomAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("before Expire: %+v", d)
	}
	Expire(&st, set, t0)
	if got := st.Order[0]; !got.HoldUntil.Equal(t0.Add(time.Minute)) || !got.HoldSetAt.Equal(t0) {
		t.Fatalf("hold not cut for good: %+v", got)
	}
	Expire(&st, set, t0.Add(30*time.Second))
	if !st.Order[0].HoldUntil.Equal(t0.Add(time.Minute)) {
		t.Fatal("the cut slid forward")
	}
	rq.Now = t0.Add(61 * time.Second)
	if d := Choose(st, set, rq); d.Subscription != "main" {
		t.Fatalf("still held 61 s later: %+v", d)
	}
	// And at the time the hold was stamped with, the full hold is not served.
	rq.Now = t0.Add(61 * time.Minute)
	if d := Choose(st, set, rq); d.Subscription != "main" {
		t.Fatalf("the full hold came back: %+v", d)
	}
}

// A reading that is old says little about where a new conversation should
// go: new ones are spread, not all sent to the first subscription.
func TestStaleReadingSpreadsNewConversations(t *testing.T) {
	set := Defaults()
	old := sub("main", 60)
	old.ReadAt = t0.Add(-48 * time.Hour)
	st := state(nil, old, sub("spare", 10))
	on := map[string]int{}
	for i := 0; i < 4; i++ {
		c := fmt.Sprintf("c%d", i)
		d := Choose(st, set, Request{Conversation: c, Now: t0})
		Served(&st, set, c, d.Subscription, d.Reason, t0)
		on[d.Subscription]++
	}
	if on["main"] != 2 || on["spare"] != 2 {
		t.Fatalf("placed %v, want two on each", on)
	}
	// With a fresh reading far from the threshold, the person's order rules.
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{{Name: "seven_day", UsedPercent: 60, ResetsAt: t0.Add(time.Hour)}}})
	for i := 4; i < 7; i++ {
		c := fmt.Sprintf("c%d", i)
		if d := Choose(st, set, Request{Conversation: c, Now: t0}); d.Subscription != "main" {
			t.Fatalf("fresh reading: %s went to %q", c, d.Subscription)
		} else {
			Served(&st, set, c, d.Subscription, d.Reason, t0)
		}
	}
}

// A move away from a subscription whose windows name no reset still latches
// it, for an hour.
func TestLatchWithoutANamedTime(t *testing.T) {
	set := Defaults()
	s := sub("main", 93)
	s.Windows[0].ResetsAt, s.Windows[0].At = time.Time{}, t0
	st := state(pinned("main", 10*time.Minute), s, sub("spare", 10))
	d := Choose(st, set, Request{Conversation: "c1", Now: t0})
	Served(&st, set, "c1", d.Subscription, d.Reason, t0)
	if d.Reason != SoftMove || !st.Order[0].LatchUntil.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v, latched until %v", d, st.Order[0].LatchUntil)
	}
}

// Expire's own age rule, apart from the cap, and a pin from the future.
func TestExpireByAge(t *testing.T) {
	set := Defaults()
	st := state(map[string]Pin{
		"eight days": {Subscription: "main", LastUsed: t0.Add(-8 * 24 * time.Hour)},
		"six days":   {Subscription: "main", LastUsed: t0.Add(-6 * 24 * time.Hour)},
		"future":     {Subscription: "main", LastUsed: t0.Add(time.Hour)},
	}, sub("main", 10))
	Expire(&st, set, t0)
	if _, ok := st.Pins["eight days"]; ok || len(st.Pins) != 2 {
		t.Fatalf("kept %v", st.Pins)
	}
	if !st.Pins["future"].LastUsed.Equal(t0) {
		t.Fatalf("a pin from the future kept its time: %v", st.Pins["future"])
	}
}

// An answer that names only some windows does not erase the others: the
// provider's ordinary answers carry the weekly window and leave the
// five-hour one out.
func TestReadingMergesIntoWhatIsKnown(t *testing.T) {
	set := Defaults()
	st := state(nil, Subscription{ID: "main"}, sub("spare", 10))
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{
		{Name: "five_hour", UsedPercent: 99, ResetsAt: t0.Add(2 * time.Hour)},
		{Name: "seven_day", UsedPercent: 40, ResetsAt: t0.Add(72 * time.Hour)},
	}})
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0.Add(time.Minute), Windows: []Window{
		{Name: "seven_day", UsedPercent: 41, ResetsAt: t0.Add(72 * time.Hour)},
	}})
	if got := st.Order[0].Windows; len(got) != 2 {
		t.Fatalf("windows %+v: the five-hour window was forgotten", got)
	}
	if d := Choose(st, set, Request{Conversation: "c1", Now: t0.Add(time.Minute)}); d.Subscription != "spare" {
		t.Fatalf("a full five-hour window no answer named again was ignored: %+v", d)
	}
	for _, w := range st.Order[0].Windows {
		if w.Name == "seven_day" && w.UsedPercent != 41 {
			t.Fatalf("the named window was not replaced: %+v", w)
		}
	}
	// A window whose reset has passed is dropped at the next reading.
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0.Add(3 * time.Hour), Windows: []Window{
		{Name: "seven_day", UsedPercent: 42, ResetsAt: t0.Add(72 * time.Hour)},
	}})
	if got := st.Order[0].Windows; len(got) != 1 || got[0].Name != "seven_day" {
		t.Fatalf("windows %+v", got)
	}
	// A subscription latched as full is not released by a reading that
	// names only a window that was never the full one.
	st = state(nil, sub("main", 10))
	st.Order[0].Windows = []Window{{Name: "five_hour", UsedPercent: 95, ResetsAt: t0.Add(2 * time.Hour)}}
	st.Order[0].LatchUntil = t0.Add(2 * time.Hour)
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{{Name: "seven_day", UsedPercent: 40, ResetsAt: t0.Add(72 * time.Hour)}}})
	if st.Order[0].LatchUntil.IsZero() {
		t.Fatal("a reading of another window released the latch")
	}
}

// A window that is full and names no reset shuts a subscription for an hour
// from when it was read, not for ever.
func TestFullWindowWithNoResetReopens(t *testing.T) {
	set := Defaults()
	st := state(nil, Subscription{ID: "main"})
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{{Name: "seven_day", UsedPercent: 100}}})
	rq := Request{Conversation: "c1", Model: "m"}
	rq.Now = t0.Add(59 * time.Minute)
	if d := Choose(st, set, rq); d.Reason != NoRoom {
		t.Fatalf("within the hour: %+v", d)
	}
	rq.Now = t0.Add(61 * time.Minute)
	if d := Choose(st, set, rq); d.Subscription != "main" {
		t.Fatalf("after the hour it is still shut: %+v", d)
	}
	// With a reset named it stays shut until then, however long.
	st = state(nil, Subscription{ID: "main"})
	Observe(&st, set, "main", Observation{Kind: Reading, At: t0, Windows: []Window{{Name: "seven_day", UsedPercent: 100, ResetsAt: t0.Add(72 * time.Hour)}}})
	rq.Now = t0.Add(48 * time.Hour)
	if d := Choose(st, set, rq); d.Reason != NoRoom || !d.RoomAt.Equal(t0.Add(72*time.Hour)) {
		t.Fatalf("a window with a reset reopened early: %+v", d)
	}
}

// A subscription of which nothing is known is not one that "has room": a
// new conversation goes to one known to have room first, is never spread
// onto an unknown one, and reaches an unknown one only when nothing known
// can take it.
func TestUnknownIsNotRoom(t *testing.T) {
	set := Defaults()
	unknown := func(id string) Subscription { return Subscription{ID: id} }
	place := func(st State, c string) string {
		d := Choose(st, set, Request{Conversation: c, Model: "m", Now: t0})
		Served(&st, set, c, d.Subscription, d.Reason, t0)
		return d.Subscription
	}
	// Known room after an unknown one in the order: the known one.
	if got := place(state(nil, unknown("main"), sub("spare", 10)), "c1"); got != "spare" {
		t.Fatalf("with a known subscription that has room: %q", got)
	}
	// Nothing known anywhere: the person's order, and every conversation to
	// the first, not spread over subscriptions nobody has read.
	st := state(nil, unknown("main"), unknown("spare"))
	for i := 0; i < 4; i++ {
		if got := place(st, fmt.Sprintf("c%d", i)); got != "main" {
			t.Fatalf("conversation %d went to %q", i, got)
		}
	}
	// A known one with an old reading spreads over known ones only.
	old := sub("main", 60)
	old.ReadAt = t0.Add(-48 * time.Hour)
	st = state(nil, old, unknown("spare"), sub("third", 10))
	on := map[string]int{}
	for i := 0; i < 4; i++ {
		on[place(st, fmt.Sprintf("c%d", i))]++
	}
	if on["spare"] != 0 || on["main"] != 2 || on["third"] != 2 {
		t.Fatalf("placed %v", on)
	}
	// The known ones are full: now the unknown one is used.
	if got := place(state(nil, sub("main", 100), unknown("spare")), "c1"); got != "spare" {
		t.Fatalf("with the known subscription full: %q", got)
	}
}
