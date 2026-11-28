package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPanelAccountClockAdvancesWithoutProviderEvidence(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	js := strings.ReplaceAll(dockUIText(t, "dock.js"), "\r\n", "\n")
	source := "const assert=require('node:assert/strict');function span(s){return s+' seconds';}\n"
	for _, head := range []string{"function accountClockText(mode, at, now, stale, checkAgain) {", "function accountWindowEnded(at, ended, now) {", "function accountSignInNeeded(c) {"} {
		source += jsFunction(t, js, head) + "\n}\n"
	}
	source += `
assert.equal(accountSignInNeeded({auth:{state:'reported_present'}}),false);
assert.equal(accountSignInNeeded({auth:{state:'unchecked'},localAuth:'file_present'}),false);
assert.equal(accountSignInNeeded({auth:{state:'unchecked'},localAuth:'file_missing'}),true);
assert.equal(accountSignInNeeded({auth:{state:'unchecked'},localAuth:'file_expired'}),true);
assert.equal(accountSignInNeeded({auth:{state:'reported_absent'}}),true);
assert.equal(accountSignInNeeded({}),false);
const at='2030-03-04T12:00:00Z', now=Date.parse(at);
assert.match(accountClockText('quota',at,now,false,false),/Saved snapshot/);
assert.match(accountClockText('quota',at,now,false,false),/Provider freshness unknown/);
assert.doesNotMatch(accountClockText('quota',at,now,false,false),/Measured/);
assert.match(accountClockText('quota',at,now+1801000,false,false),/Older reading/);
assert.match(accountClockText('quota',at,now,true,false),/Older reading/);
assert.match(accountClockText('retrieved',at,now,false,false),/Provider freshness unknown/);
assert.match(accountClockText('retrieved',at,now+1801000,false,false),/Older reading/);
assert.match(accountClockText('retrieved',at,now+86401000,false,false),/Provider freshness unknown/);
assert.doesNotMatch(accountClockText('retrieved',at,now,false,false),/Measured/);
assert.match(accountClockText('checked',at,now+300000,false,false),/Older check/);
assert.match(accountClockText('checked',at,now+86401000,false,false),/check again/);
assert.match(accountClockText('reset',at,now-1000,false,false),/Resets in 1 seconds/);
assert.match(accountClockText('reset',at,now,false,false),/passed.*refresh/);
assert.equal(accountWindowEnded(at,false,now-1),false);
assert.equal(accountWindowEnded(at,false,now),true);
assert.equal(accountWindowEnded('',false,now),false);
assert.equal(accountWindowEnded('',true,now),true);
assert.match(accountClockText('quota','invalid',now,false,false),/Time unavailable/);
assert.match(accountClockText('quota',at,now-1000,false,false),/device clock/);
console.log('Advancing age, stale, unknown freshness, reset boundary and missing/future time PASS');
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=commonjs", "-")
	cmd.Stdin = bytes.NewBufferString(source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clock contract: %v\n%s", err, out)
	}
	t.Log(string(out))
}
