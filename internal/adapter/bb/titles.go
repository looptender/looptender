package bb

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

var inheritedTitle = regexp.MustCompile(`^From “(.*)” · keepgoing: .+ \(continued, [a-f0-9]{8}\)$`)

// ContinuationTitle reads only the explicitly continued thread's display
// metadata. The caller saves the result before spawning so uncertain creation
// recovery uses exactly the title BB received. Failure leaves the unique
// loop-based title intact; naming never changes a supervision decision.
func (a *Adapter) ContinuationTitle(ctx context.Context, source, fallback string) string {
	if !childIDPattern.MatchString(source) {
		return fallback
	}
	out, err := a.run(ctx, "", "thread", "show", source, "--json")
	if _, failed := envelopeError(out); failed || err != nil {
		return fallback
	}
	var resp struct {
		Thread *struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Fallback string `json:"titleFallback"`
		} `json:"thread"`
	}
	if json.Unmarshal(out, &resp) != nil || resp.Thread == nil || resp.Thread.ID != source {
		return fallback
	}
	for _, title := range []string{resp.Thread.Title, resp.Thread.Fallback} {
		title = strings.TrimSpace(title)
		if parts := inheritedTitle.FindStringSubmatch(title); parts != nil {
			title = parts[1]
		}
		// These are continuation scaffolding, not useful task names. Do not
		// copy our own decorated title again on a later handoff.
		if title == "" || strings.HasPrefix(title, "Continue from ") || strings.HasPrefix(title, "keepgoing: ") || strings.HasPrefix(title, "From “") {
			continue
		}
		if strings.ContainsFunc(title, unicode.IsControl) {
			continue
		}
		runes := []rune(title)
		if len(runes) > 120 {
			title = string(runes[:120]) + "…"
		}
		// Attribute the old title rather than claiming its identity/model
		// for the successor. Provider/model selection stays in SpawnRequest.
		return "From “" + title + "” · " + fallback
	}
	return fallback
}
