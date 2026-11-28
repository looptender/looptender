package main

import (
	"errors"
	"time"

	"github.com/looptender/looptender/internal/config"
)

const resumeDeadlineMessage = "Deadline reached. Review loop.limits.deadline in this loop's configuration before resuming. Resume cannot change the deadline."

// Admission uses the same host-clock boundary as a check. It never changes the
// configuration or any hold; a later check still enforces the deadline.
func resumeDeadlineRefusal(cfg *config.Config, now time.Time) error {
	if cfg.Loop.Limits.DeadlineReached(now) {
		return errors.New(resumeDeadlineMessage)
	}
	return nil
}
