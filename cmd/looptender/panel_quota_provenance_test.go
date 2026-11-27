package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelAccountQuotaProvenanceV1AndMalformedCrossProduct(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: provenance JS execution not performed")
	}
	provRaw, err := os.ReadFile(filepath.Join("testdata", "quota_provenance_588_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	cardsRaw, err := os.ReadFile(filepath.Join("testdata", "quota_provenance_588_cards.json"))
	if err != nil {
		t.Fatal(err)
	}
	js := strings.ReplaceAll(dockUIText(t, "dock.js"), "\r\n", "\n")
	source := `const assert = require("node:assert/strict");
const state = { server: { accountQuota: true } };
function span(s) { return s + " s"; }
function el(tag, cls, txt) { return { tag, className: cls || "", dataset: {}, textContent: txt == null ? "" : String(txt) }; }
function accountClock(kind, cls, mode, at) { const node = el(kind, cls); node.dataset.accountClock = mode; node.dataset.at = at; return node; }
`
	for _, head := range []string{
		"function accountQuotaSummary(c) {",
		"function accountQuotaRetrievedAt(saved) {",
		"function accountQuotaMalformedProvenance(c) {",
		"function accountClockText(mode, at, now, stale, checkAgain) {",
		"function accountQuotaStamp(saved, compact) {",
	} {
		source += jsFunction(t, js, head) + "\n}\n"
	}
	source += "\nconst provInput = " + string(provRaw) + ";\nconst cardsInput = " + string(cardsRaw) + ";\n"
	source += `
const clone = (v) => JSON.parse(JSON.stringify(v));

// 1. Actual #588 producer outputs across all 3 providers (panel list fixture).
assert.equal(provInput.accountQuotas.accounts.length, 3);
for (const entry of provInput.accountQuotas.accounts) {
  const s = clone(entry.summary);
  assert.equal(s.provenance.version, 1);
  assert.equal(s.provenance.providerObservedAt, null);
  const cV1 = { provider: entry.provider, quota: { summary: s } };
  assert.equal(accountQuotaSummary(cV1), s, "valid #588 v1 summary rejected for " + entry.provider);
  assert.equal(accountQuotaMalformedProvenance(cV1), false);
  assert.equal(accountQuotaRetrievedAt(s), s.provenance.retrievedAt);
  const stamp = accountQuotaStamp(s, false);
  assert.equal(stamp.dataset.accountClock, "retrieved");
  assert.equal(stamp.dataset.at, s.provenance.retrievedAt);
  const now = Date.parse(s.provenance.retrievedAt);
  const recentTxt = accountClockText(stamp.dataset.accountClock, stamp.dataset.at, now, false, false);
  assert.match(recentTxt, /Provider freshness unknown/);
  assert.doesNotMatch(recentTxt, /Measured|provider observed/i);

  // Explicit legacy absence of provenance must remain supported with local/provider-unknown semantics.
  const leg = clone(s);
  delete leg.provenance;
  const cLeg = { provider: entry.provider, quota: { summary: leg } };
  assert.equal(accountQuotaSummary(cLeg), leg, "legacy summary rejected for " + entry.provider);
  assert.equal(accountQuotaMalformedProvenance(cLeg), false);
  assert.equal(accountQuotaRetrievedAt(leg), leg.retrievedAt || leg.observedAt);
  const legStamp = accountQuotaStamp(leg, false);
  const legTxt = accountClockText(legStamp.dataset.accountClock, legStamp.dataset.at, now, false, false);
  assert.match(legTxt, /Provider freshness unknown/);
  assert.doesNotMatch(legTxt, /Measured/);
}

// 2. Actual #588 browser cards fixture (fresh, stale + failedRefresh, unavailable).
const cardProviders = cardsInput.accounts.cards;
assert.equal(cardProviders.length, 4);
assert.equal(accountQuotaSummary(cardProviders[0]), cardProviders[0].quota.summary);
assert.equal(accountQuotaSummary(cardProviders[1]), cardProviders[1].quota.summary);
assert.equal(accountQuotaSummary(cardProviders[2]), null);
assert.equal(accountQuotaMalformedProvenance(cardProviders[2]), false);
assert.equal(accountQuotaSummary(cardProviders[3]), cardProviders[3].quota.summary);

// 3. Valid timezone offset, trailing zero nanos, leap day, and exact sub-ms equality.
for (const entry of provInput.accountQuotas.accounts) {
  const s = clone(entry.summary);
  for (const okProv of [
    { version: 1, retrievedAt: "2030-03-04T13:00:00+01:00", providerObservedAt: null },
    { version: 1, retrievedAt: "2030-03-04T06:30:00-05:30", providerObservedAt: null },
    { version: 1, retrievedAt: "2030-03-04T12:00:00-00:00", providerObservedAt: null },
    { version: 1, retrievedAt: "2030-03-04T12:00:00.000000000Z", providerObservedAt: null },
  ]) {
    const good = { ...s, provenance: okProv };
    assert.ok(accountQuotaSummary({ provider: entry.provider, quota: { summary: good } }), "valid equivalent instant rejected");
  }
  const nanoMatch = clone(s);
  const nanoTs = "2030-03-04T12:00:00.123456789Z";
  nanoMatch.provenance.retrievedAt = nanoTs;
  if (entry.provider === "codex") nanoMatch.observedAt = nanoTs; else nanoMatch.retrievedAt = nanoTs;
  assert.ok(accountQuotaSummary({ provider: entry.provider, quota: { summary: nanoMatch } }));

  const leapMatch = clone(s);
  const leapTs = "2028-02-29T12:00:00Z";
  leapMatch.provenance.retrievedAt = leapTs;
  if (entry.provider === "codex") leapMatch.observedAt = leapTs; else leapMatch.retrievedAt = leapTs;
  assert.ok(accountQuotaSummary({ provider: entry.provider, quota: { summary: leapMatch } }));
}

// 4. Malformed cross-product across all 3 providers must fail closed and NEVER fall back to legacy.
for (const entry of provInput.accountQuotas.accounts) {
  const base = clone(entry.summary);
  const goodP = clone(base.provenance);
  const badProvs = [
    ["present null provenance", null],
    ["present undefined provenance", undefined],
    ["string provenance", "v1"],
    ["number provenance", 1],
    ["bool provenance", true],
    ["array provenance", []],
    ["empty object", {}],
    ["missing version", { retrievedAt: goodP.retrievedAt, providerObservedAt: null }],
    ["missing retrievedAt", { version: 1, providerObservedAt: null }],
    ["missing providerObservedAt", { version: 1, retrievedAt: goodP.retrievedAt }],
    ["extra key", { ...goodP, extra: "x" }],
    ["wrong casing Version", { Version: 1, retrievedAt: goodP.retrievedAt, providerObservedAt: null }],
    ["version 0", { ...goodP, version: 0 }],
    ["version 2", { ...goodP, version: 2 }],
    ["version -1", { ...goodP, version: -1 }],
    ["version 1.5", { ...goodP, version: 1.5 }],
    ["version string '1'", { ...goodP, version: "1" }],
    ["version boolean true", { ...goodP, version: true }],
    ["version null", { ...goodP, version: null }],
    ["version NaN", { ...goodP, version: NaN }],
    ["providerObservedAt timestamp", { ...goodP, providerObservedAt: "2030-03-04T12:00:00Z" }],
    ["providerObservedAt empty string", { ...goodP, providerObservedAt: "" }],
    ["providerObservedAt false", { ...goodP, providerObservedAt: false }],
    ["providerObservedAt 0", { ...goodP, providerObservedAt: 0 }],
    ["providerObservedAt object", { ...goodP, providerObservedAt: {} }],
    ["providerObservedAt array", { ...goodP, providerObservedAt: [] }],
    ["providerObservedAt undefined", { ...goodP, providerObservedAt: undefined }],
    ["retrievedAt null", { ...goodP, retrievedAt: null }],
    ["retrievedAt empty", { ...goodP, retrievedAt: "" }],
    ["retrievedAt number", { ...goodP, retrievedAt: 1898856000000 }],
    ["retrievedAt garbage text", { ...goodP, retrievedAt: "not-a-date" }],
    ["retrievedAt date only", { ...goodP, retrievedAt: "2030-03-04" }],
    ["retrievedAt space separator", { ...goodP, retrievedAt: "2030-03-04 12:00:00Z" }],
    ["retrievedAt lowercase t/z", { ...goodP, retrievedAt: "2030-03-04t12:00:00z" }],
    ["retrievedAt missing tz", { ...goodP, retrievedAt: "2030-03-04T12:00:00" }],
    ["retrievedAt trailing text", { ...goodP, retrievedAt: "2030-03-04T12:00:00Zgarbage" }],
    ["retrievedAt trailing newline", { ...goodP, retrievedAt: "2030-03-04T12:00:00Z\n" }],
    ["retrievedAt leading space", { ...goodP, retrievedAt: " 2030-03-04T12:00:00Z" }],
    ["retrievedAt non-leap Feb 29 (2030)", { ...goodP, retrievedAt: "2030-02-29T12:00:00Z" }],
    ["retrievedAt secular non-leap Feb 29 (2100)", { ...goodP, retrievedAt: "2100-02-29T12:00:00Z" }],
    ["retrievedAt Feb 30 normalization trap", { ...goodP, retrievedAt: "2030-02-30T12:00:00Z" }],
    ["retrievedAt Feb 31 normalization trap", { ...goodP, retrievedAt: "2030-02-31T12:00:00Z" }],
    ["retrievedAt Apr 31 normalization trap", { ...goodP, retrievedAt: "2030-04-31T12:00:00Z" }],
    ["retrievedAt hour 24", { ...goodP, retrievedAt: "2030-03-04T24:00:00Z" }],
    ["retrievedAt minute 60", { ...goodP, retrievedAt: "2030-03-04T12:60:00Z" }],
    ["retrievedAt offset +24:00", { ...goodP, retrievedAt: "2030-03-04T12:00:00+24:00" }],
    ["retrievedAt 10-digit nanos", { ...goodP, retrievedAt: "2030-03-04T12:00:00.1234567890Z" }],
    ["retrievedAt zero year 0001", { ...goodP, retrievedAt: "0001-01-01T00:00:00Z" }],
    ["retrievedAt second conflict", { ...goodP, retrievedAt: "2030-03-04T12:00:01Z" }],
    ["retrievedAt 1ns conflict vs whole second", { ...goodP, retrievedAt: "2030-03-04T12:00:00.000000001Z" }],
    ["retrievedAt wrong-sign offset +01/-01", { ...goodP, retrievedAt: "2030-03-04T13:00:00-01:00" }],
  ];
  for (const [label, badP] of badProvs) {
    const card = { provider: entry.provider, quota: { summary: { ...base, provenance: badP } } };
    assert.equal(accountQuotaSummary(card), null, entry.provider + " accepted " + label);
    assert.equal(accountQuotaMalformedProvenance(card), true, entry.provider + " missed malformed flag for " + label);
  }

  // JS millisecond truncation conflicts must NOT accept distinct instants.
  const subMsConflict = clone(base);
  subMsConflict.provenance.retrievedAt = "2030-03-04T12:00:00.123456789Z";
  if (entry.provider === "codex") subMsConflict.observedAt = "2030-03-04T12:00:00.123000000Z";
  else subMsConflict.retrievedAt = "2030-03-04T12:00:00.123000000Z";
  assert.equal(accountQuotaSummary({ provider: entry.provider, quota: { summary: subMsConflict } }), null, entry.provider + " accepted sub-ms truncation");

  const oneNsConflict = clone(base);
  oneNsConflict.provenance.retrievedAt = "2030-03-04T12:00:00.123456789Z";
  if (entry.provider === "codex") oneNsConflict.observedAt = "2030-03-04T12:00:00.123456788Z";
  else oneNsConflict.retrievedAt = "2030-03-04T12:00:00.123456788Z";
  assert.equal(accountQuotaSummary({ provider: entry.provider, quota: { summary: oneNsConflict } }), null, entry.provider + " accepted 1ns conflict");

  // Date normalization in legacy timestamps must also fail closed even with valid provenance.
  const normTrap = clone(base);
  normTrap.provenance.retrievedAt = "2030-02-31T12:00:00Z";
  if (entry.provider === "codex") normTrap.observedAt = "2030-02-31T12:00:00Z";
  else normTrap.retrievedAt = "2030-02-31T12:00:00Z";
  assert.equal(accountQuotaSummary({ provider: entry.provider, quota: { summary: normTrap } }), null, entry.provider + " accepted rolled-over calendar date");

  // Valid provenance cannot override legacy contract conflicts or unavailable state.
  for (const override of [
    { source: "unsupported" },
    { freshness: entry.provider === "codex" ? "provider_unknown" : "fresh" },
    { observedAt: "2030-03-04T12:00:00Z", retrievedAt: "2030-03-04T12:00:00Z" },
    { observedAt: "2030-03-04T12:00:00Z", retrievedAt: null },
    { state: "unavailable", freshness: "unavailable", rows: [] },
  ]) {
    const conflictCard = { provider: entry.provider, quota: { summary: { ...base, ...override } } };
    assert.equal(accountQuotaSummary(conflictCard), null, entry.provider + " accepted conflict override");
    assert.equal(accountQuotaMalformedProvenance(conflictCard), true);
  }
}

console.log("Provenance v1 + #588 fixtures + malformed cross-product PASS");
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=commonjs", "-")
	cmd.Stdin = bytes.NewBufferString(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provenance contract: %v\n%s", err, out)
	}
	t.Log(string(out))
}
func TestPanelAccountQuotaRawDuplicateKeysRejected(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: raw provenance JSON test not performed")
	}
	provRaw, err := os.ReadFile(filepath.Join("testdata", "quota_provenance_588_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	cardsRaw, err := os.ReadFile(filepath.Join("testdata", "quota_provenance_588_cards.json"))
	if err != nil {
		t.Fatal(err)
	}
	js := strings.ReplaceAll(dockUIText(t, "dock.js"), "\r\n", "\n")
	source := `const assert = require("node:assert/strict");
`
	for _, head := range []string{
		"function accountQuotaSummary(c) {",
		"function accountQuotaMalformedProvenance(c) {",
		"function parseDockQuotaJSON(rawText) {",
	} {
		source += jsFunction(t, js, head) + "\n}\n"
	}
	source += "\nconst provInput = " + string(provRaw) + ";\nconst cardsInput = " + string(cardsRaw) + ";\n"
	source += `
const clone = (v) => JSON.parse(JSON.stringify(v));
const ts = "2030-03-04T12:00:00Z";
const otherTs = "2030-03-04T12:00:01Z";

function wrapPayloads(rawCard) {
  const dockRaw = '{"schemaVersion":1,"readAt":"2030-03-04T12:00:00Z","registry":{"availability":"available"},"complete":true,"loops":[{"key":"l1","name":"loop-a","note":"Note has \\"version\\":2,\\"version\\":1 inside text","dup":1,"dup":2}],"notices":[{"id":"n1","id":"n2"}],"accounts":{"state":"available","complete":true,"cards":[' + rawCard + ']}}';
  const savedRaw = '{"schemaVersion":1,"reference":"ref_raw_test","accounts":{"state":"available","complete":true,"cards":[' + rawCard + ']}}';
  return [
    ["dock", dockRaw],
    ["saved-accounts", savedRaw],
  ];
}

for (const entry of provInput.accountQuotas.accounts) {
  const baseCard = entry.provider === "codex"
    ? { ...clone(cardsInput.accounts.cards[0]), provider: "codex" }
    : {
        id: "id_" + entry.provider,
        label: entry.label,
        provider: entry.provider,
        selection: "inherited",
        quota: { state: "reported", source: entry.summary.source, retrievedAt: entry.summary.retrievedAt, freshness: "provider_unknown", summary: clone(entry.summary) },
      };
  if (entry.provider === "codex") {
    baseCard.quota.summary.provenance.retrievedAt = ts;
    baseCard.quota.summary.observedAt = ts;
  }
  const goodProvJson = JSON.stringify(baseCard.quota.summary.provenance);
  const rawBaseCard = JSON.stringify(baseCard);

  // Control: valid #588 v1 payload including escaped-but-unique key passes through untouched.
  for (const [ep, raw] of wrapPayloads(rawBaseCard)) {
    const out = parseDockQuotaJSON(raw);
    const c = out.accounts.cards[0];
    assert.ok(accountQuotaSummary(c), ep + " rejected valid " + entry.provider);
    assert.equal(accountQuotaMalformedProvenance(c), false);
  }
  const escapedUnique = rawBaseCard.replace('"version":1', '"\\u0076ersion":1');
  for (const [ep, raw] of wrapPayloads(escapedUnique)) {
    const out = parseDockQuotaJSON(raw);
    const c = out.accounts.cards[0];
    assert.ok(accountQuotaSummary(c), ep + " rejected unique escaped key " + entry.provider);
    assert.equal(accountQuotaMalformedProvenance(c), false);
  }

  // Raw duplicate / escaped-equivalent provenance variants MUST fail closed in both endpoints.
  const swapProv = (fn) => rawBaseCard.replace('"provenance":' + goodProvJson, '"provenance":' + fn(goodProvJson));
  const badVariants = [
    ["dup version 2,1", swapProv((p) => p.replace('"version":1', '"version":2,"version":1'))],
    ["dup version 1,2", swapProv((p) => p.replace('"version":1', '"version":1,"version":2'))],
    ["dup version equal 1,1", swapProv((p) => p.replace('"version":1', '"version":1,"version":1'))],
    ["escaped dup version", swapProv((p) => p.replace('"version":1', '"version":2,"\\u0076ersion":1'))],
    ["dup providerObservedAt ts,null", swapProv((p) => p.replace('"providerObservedAt":null', '"providerObservedAt":"' + ts + '","providerObservedAt":null'))],
    ["dup providerObservedAt null,ts", swapProv((p) => p.replace('"providerObservedAt":null', '"providerObservedAt":null,"providerObservedAt":"' + ts + '"'))],
    ["dup providerObservedAt null,null", swapProv((p) => p.replace('"providerObservedAt":null', '"providerObservedAt":null,"providerObservedAt":null'))],
    ["escaped dup providerObservedAt", swapProv((p) => p.replace('"providerObservedAt":null', '"providerObservedAt":"' + ts + '","providerObserved\\u0041t":null'))],
    ["dup retrievedAt conflict other,ts", swapProv((p) => p.replace('"retrievedAt":"' + ts + '"', '"retrievedAt":"' + otherTs + '","retrievedAt":"' + ts + '"'))],
    ["dup retrievedAt conflict ts,other", swapProv((p) => p.replace('"retrievedAt":"' + ts + '"', '"retrievedAt":"' + ts + '","retrievedAt":"' + otherTs + '"'))],
    ["dup retrievedAt equal ts,ts", swapProv((p) => p.replace('"retrievedAt":"' + ts + '"', '"retrievedAt":"' + ts + '","retrievedAt":"' + ts + '"'))],
    ["escaped dup retrievedAt conflict", swapProv((p) => p.replace('"retrievedAt":"' + ts + '"', '"retrievedAt":"' + otherTs + '","retrieved\\u0041t":"' + ts + '"'))],
    ["escaped dup retrievedAt equal", swapProv((p) => p.replace('"retrievedAt":"' + ts + '"', '"retrievedAt":"' + ts + '","retrieved\\u0041t":"' + ts + '"'))],
    ["dup quota retrievedAt/observedAt", rawBaseCard.replace('"state":"reported"', '"retrievedAt":"' + otherTs + '","state":"reported"')],
    ["dup provenance key bad,good", rawBaseCard.replace('"provenance":' + goodProvJson, '"provenance":{"version":2},"provenance":' + goodProvJson)],
    ["dup provenance key null,good", rawBaseCard.replace('"provenance":' + goodProvJson, '"provenance":null,"provenance":' + goodProvJson)],
    ["dup provenance key good,good", rawBaseCard.replace('"provenance":' + goodProvJson, '"provenance":' + goodProvJson + ',"proven\\u0061nce":' + goodProvJson)],
    ["dup summary key in quota", rawBaseCard.replace('"summary":{', '"summary":{"state":"reported","provenance":{"version":2}},"summary":{')],
    ["dup quota key on card", rawBaseCard.replace('"quota":{', '"quota":{"summary":{"state":"reported","provenance":{"version":2}}},"quota":{')],
    ["dup provider key on card", rawBaseCard.replace('"provider":', '"provider":"other","provider":')],
  ];
  for (const [label, badRawCard] of badVariants) {
    for (const [ep, raw] of wrapPayloads(badRawCard)) {
      const parsed = parseDockQuotaJSON(raw);
      const cardOut = parsed.accounts.cards[0];
      assert.equal(accountQuotaSummary(cardOut), null, ep + " accepted " + entry.provider + " " + label);
      assert.equal(accountQuotaMalformedProvenance(cardOut), true, ep + " lacked malformed flag for " + entry.provider + " " + label);
    }
  }

  // Legacy card (no provenance) with duplicate legacy timestamp must also fail closed without displaying quota.
  const legCard = clone(baseCard);
  delete legCard.quota.summary.provenance;
  const rawLeg = JSON.stringify(legCard);
  const legField = entry.provider === "codex" ? "observedAt" : "retrievedAt";
  const legEsc = entry.provider === "codex" ? "observed\\u0041t" : "retrieved\\u0041t";
  const badLegRaw = rawLeg.replace('"' + legField + '":"' + ts + '"', '"' + legField + '":"' + otherTs + '","' + legEsc + '":"' + ts + '"');
  for (const [ep, raw] of wrapPayloads(badLegRaw)) {
    const out = parseDockQuotaJSON(raw);
    assert.equal(accountQuotaSummary(out.accounts.cards[0]), null, ep + " accepted ambiguous legacy key for " + entry.provider);
  }
}

// Root-level duplicate "accounts" or "cards" must taint every card even when first accounts is empty/null.
const goodCardJson = JSON.stringify(cardsInput.accounts.cards[0]);
for (const rootDup of [
  '{"schemaVersion":1,"accounts":null,"accounts":{"state":"available","cards":[' + goodCardJson + ']}}',
  '{"schemaVersion":1,"accounts":{"cards":null,"cards":[' + goodCardJson + ']}}',
  '{"schemaVersion":1,"accounts":{"cards":[]},"ac\\u0063ounts":{"state":"available","cards":[' + goodCardJson + ']}}',
]) {
  const out = parseDockQuotaJSON(rootDup);
  assert.equal(accountQuotaSummary(out.accounts.cards[0]), null);
  assert.equal(accountQuotaMalformedProvenance(out.accounts.cards[0]), true);
}

// Nesting depth > 64 inside payload is rejected cleanly.
const deep = '{"schemaVersion":1,' + '"x":{'.repeat(70) + '"k":1' + '}'.repeat(70) + '}';
assert.throws(() => parseDockQuotaJSON(deep), /schema/);

console.log("Raw JSON duplicate & escaped-equivalent provenance keys PASS");
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=commonjs", "-")
	cmd.Stdin = bytes.NewBufferString(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("raw duplicate provenance: %v\n%s", err, out)
	}
	t.Log(string(out))
}
