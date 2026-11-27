package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestDockOverviewGlossaryAndBoundaryInvariants checks vocabulary rules,
// absence of fake Work Intensity controls, honest build revision wording,
// forced-colors support for Overview/Accounts/Task-Flow elements, and
// read-only guarantees on Home.
func TestDockOverviewGlossaryAndBoundaryInvariants(t *testing.T) {
	html := dockUIText(t, "index.html")
	css := dockUIText(t, "dock.css")
	js := dockUIText(t, "dock.js")

	// Glossary compliance: user-facing copy and accessibility labels must never
	// use the banned term "node" or misname looptender as a "server".
	if re := regexp.MustCompile(`(?i)\bnodes?\b`); re.MatchString(html) {
		t.Errorf("dockui/index.html uses banned glossary term node(s)")
	}
	for _, m := range regexp.MustCompile(`"[^"\n]*"`).FindAllString(js, -1) {
		if regexp.MustCompile(`(?i)\bnodes?\b`).MatchString(m) {
			t.Errorf("dock.js string literal contains banned term node(s): %s", m)
		}
		if regexp.MustCompile(`(?i)\b(?:looptender|panel)\s+server\b`).MatchString(m) {
			t.Errorf("dock.js string literal misnames looptender as a server: %s", m)
		}
	}

	// No fake Work Intensity slider or policy controls shipped in live panel UI.
	for _, fake := range []string{`type="range"`, `type = "range"`, "work-intensity", "intensity-slider", "Unleashed", "Fresh measured quota"} {
		if strings.Contains(html, fake) || strings.Contains(js, fake) {
			t.Errorf("panel UI contains unimplemented control or disallowed provider-fresh claim %q", fake)
		}
	}

	// Quota provenance v1 parser + fail-closed invariants.
	quotaBody := jsFunction(t, js, "function accountQuotaSummary(c) {")
	for _, want := range []string{"provenance", "providerObservedAt !== null", "parseRFC3339", "epochSec", "nanos"} {
		if !strings.Contains(quotaBody, want) {
			t.Errorf("accountQuotaSummary lacks provenance v1 invariant %q", want)
		}
	}
	if !strings.Contains(js, "accountQuotaMalformedProvenance(") {
		t.Errorf("dock.js lacks accountQuotaMalformedProvenance fail-closed helper")
	}

	// Header & build identity honesty invariants.
	for _, want := range []string{
		`id="build-pill"`,
		`href="#/versions"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	for _, want := range []string{
		`"Saved records read at "`,
		`Build unknown (dev)`,
		`release label · commit revision not embedded`,
		`renderBuildPill(`,
		`reconcileActiveIssue(`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("dock.js lacks %q", want)
		}
	}

	// Guard against unemitted backend fields and enforce emitted target + partial/failed invariants.
	for _, unemitted := range []string{"questionId", "targetRevision", "ownerRevision", "openUrl", "structuredOpenUrl"} {
		if strings.Contains(js, unemitted) {
			t.Errorf("dock.js references unemitted field or helper %q", unemitted)
		}
	}
	fpBody := jsFunction(t, js, "function issueFingerprint(nItem, loopObj, includeIdentifiers) {")
	for _, want := range []string{"currentRoute", "identifiers", "currentChainRouteKey", "has_title", "canonMaskedNote"} {
		if !strings.Contains(fpBody, want) {
			t.Errorf("issueFingerprint must include backend-emitted field/normalization %q", want)
		}
	}
	uncBody := jsFunction(t, js, "function taskUnconfirmedText(d, nItem, loopObj) {")
	for _, want := range []string{"state.failed", "d.complete === false", `loopObj.evidence === "partial"`, `loopObj.evidence === "unavailable"`} {
		if !strings.Contains(uncBody, want) {
			t.Errorf("taskUnconfirmedText lacks %q", want)
		}
	}
	loadBody := jsFunction(t, js, "function load(byHand) {")
	for _, want := range []string{"syncTaskUnconfirmedDOM(", "scrubNeedForHiddenIds(", "scrubLoopForHiddenIds("} {
		if !strings.Contains(loadBody, want) {
			t.Errorf("load(byHand) lacks %q", want)
		}
	}
	for _, fnHead := range []string{"function scrubNeedForHiddenIds(n) {", "function scrubLoopForHiddenIds(l) {"} {
		if !strings.Contains(js, fnHead) {
			t.Errorf("dock.js lacks %q", fnHead)
		}
	}
	if !strings.Contains(js, "scrubRetainedForHiddenIds()") {
		t.Errorf("dock.js must scrub retained state immediately when identifiers are hidden")
	}
	if !strings.Contains(jsFunction(t, js, "function prepareChange(key, action, amount, issueFp, issueBaseFp, issueIdsMode) {"), "changeSerial") {
		t.Errorf("prepareChange must guard out-of-order responses with changeSerial")
	}
	confirmBody := jsFunction(t, js, "function confirmChange(option) {")
	if !strings.Contains(confirmBody, "seq !== state.changeSerial") || !strings.Contains(confirmBody, "supersededChangeResult") {
		t.Errorf("confirmChange must guard all continuations with changeSerial and preserve supersededChangeResult")
	}

	// Home view and Setup Map must never initiate account auto-checks or quota POSTs.
	for _, fnHead := range []string{
		"function renderHome(d, pane) {",
		"function setupMapPanel(d, cats) {",
		"function attentionPanel(d, cats) {",
		"function overviewHealthBar(d, cats) {",
	} {
		body := jsFunction(t, js, fnHead)
		for _, banned := range []string{"maybeAutoCheckAccounts(", "accountCheckPost(", "runAccountChecks("} {
			if strings.Contains(body, banned) {
				t.Errorf("%s calls prohibited home-load action %s", fnHead, banned)
			}
		}
	}

	// Forced colors rules must cover new pressed, selected, card, and drawer states.
	for _, want := range []string{
		`.chip-btn[aria-pressed="true"], .view-switch button[aria-pressed="true"], .cat-pill[aria-pressed="true"]`,
		`.map-card.is-selected`,
		`.edge-badge-bg`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dock.css forced-colors rules lack %q", want)
		}
	}
}
