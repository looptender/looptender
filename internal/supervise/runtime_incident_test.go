package supervise

import (
	"errors"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
)

func TestSafetyRefusalHistoryFailureRetainsBothErrors(t *testing.T) {
	w := newWorld(t, true, false)
	l, err := lock.AcquireNamed(w.dir, "incident")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	_, err = Tick(t.Context(), w.cfg, Options{LastConfigReadAt: time.Now()})
	if !errors.Is(err, ErrLastConfigNotStop) || !errors.Is(err, incident.ErrUnavailable) {
		t.Fatal(err)
	}
}
