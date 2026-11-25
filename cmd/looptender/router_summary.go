package main

import (
	"context"
	"fmt"
)

// routerSummary is a read-only, privacy-preserving snapshot of the local
// account router state. It never starts or stops processes, reads
// credentials, or exposes paths, tokens, secrets, or account identifiers.
type routerSummary struct {
	Directory    string `json:"directory"`         // missing, private, or untrusted
	Answering    bool   `json:"answering"`         // whether the router socket answers
	Enabled      bool   `json:"enabled"`           // whether router.json records enabled: true
	PointedCount int    `json:"pointedCount"`      // count of pointed settings entries
	Service      string `json:"service,omitempty"` // not_installed, loaded, absent, partial, unknown, unavailable
}

// doctorRouter adds read-only account router visibility to doctorReport.
// It never fails doctor for users who have not enabled the router, and emits
// an advisory warning only when the router is enabled or pointed while its
// channel socket is not answering.
func doctorRouter(ctx context.Context, r *doctorReport, dir string, offline bool) {
	s, configured := readRouterSummary(ctx, dir, offline)
	if !configured {
		return
	}
	r.Router = &s
	if !s.Enabled && s.PointedCount == 0 {
		return
	}
	svc := s.Service
	if svc == "" {
		svc = "unavailable"
	}
	if s.Answering {
		r.add("router", "ok", fmt.Sprintf("Account router is enabled (%d pointed) and its channel socket is answering (service: %s).", s.PointedCount, svc), "")
		return
	}
	r.add("router", "warning", fmt.Sprintf("Account router is enabled (%d pointed) but its channel socket is not answering (service: %s).", s.PointedCount, svc), "Inspect with `looptender router status`, start it with `looptender router run` or `looptender router service install`, or restore pointed settings with `looptender router off`.")
}
