package supervise

import (
	"context"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
)

// limitAccountCheck asks for the catalog's login check of the account the
// active route states when that target is at a usage limit, so "which
// login reached it" can be told from a current fingerprint. It is the
// cheap check (the CLI's version and auth status), runs only with time to
// spare in the check, never on a dry run, and the caller's check spaces it
// out. A route that states no account is not checked: which login it uses
// is not known.
func (t *tick) limitAccountCheck(agent config.Agent, obs decide.Observation) {
	if t.opts.AccountCheck == nil || t.opts.DryRun || agent.Account == "" || obs.ErrorClass != decide.ErrRateLimit || t.left() < 20*time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, 12*time.Second)
	defer cancel()
	t.opts.AccountCheck(ctx, agent.Account)
}
