#!/usr/bin/env python3
"""Explicit coordinator-owned compact attempts; never invoked by a supervisor."""
import argparse
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time
import uuid

sys.dont_write_bytecode = True
from coordinator import Commands, file_hash
from coordinator_state import Journal, Refused, decode, digest, private_read, require

ROLES = {"authorization", "goal", "tasks", "obligations", "ownership", "approvals", "holds"}
LIMIT = 1024 * 1024


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def string(value):
    return isinstance(value, str) and 0 < len(value) <= 4096 and all(ord(c) >= 32 for c in value)


def mapping(value):
    require(isinstance(value, dict), "object_required")
    return value


def sources(items):
    require(isinstance(items, dict) and set(items) == ROLES, "continuity_roles")
    for item in items.values():
        require(isinstance(item, dict) and set(item) == {"path", "sha256"}, "continuity_schema")
        require(isinstance(item["sha256"], str) and len(item["sha256"]) == 64, "continuity_schema")
        require(file_hash(item["path"])[0] == item["sha256"], "continuity_changed")


def load_grant(path):
    g = decode(private_read(path, 100 * 1024))
    require(isinstance(g, dict) and set(g) == {"version", "id", "request", "caller", "binding", "bb", "effectiveBB", "keepgoing", "maxAttempts", "cooldownSeconds", "continuity"}, "grant_schema")
    require(g["version"] == 1 and type(g["version"]) is int and all(string(g[k]) for k in ("id", "request", "caller")), "grant_schema")
    require(re.fullmatch(r"thr_[A-Za-z0-9_-]{1,128}", g["caller"]) is not None and g["caller"] == os.environ.get("BB_THREAD_ID"), "caller_changed")
    require(integer(g["maxAttempts"], 1, 8) and integer(g["cooldownSeconds"], 3600, 7 * 86400), "grant_bounds")
    b = g["binding"]
    require(isinstance(b, dict) and set(b) == {"loop", "config", "runtime", "thread", "project", "environment", "host", "provider", "owner", "generation", "goal", "goalRevision"}, "binding_schema")
    require(all(string(b[k]) for k in b if k not in {"generation", "goalRevision"}) and integer(b["generation"], 1, 2**53) and integer(b["goalRevision"], 1, 2**53), "binding_schema")
    require(all(re.fullmatch(r"thr_[A-Za-z0-9_-]{1,128}", b[k]) is not None for k in ("thread", "owner")), "thread_identity")
    require(isinstance(g["continuity"], dict) and set(g["continuity"]) == ROLES and any(isinstance(v, dict) and v.get("path") == b["config"] for v in g["continuity"].values()), "config_source_required")
    for k in ("config", "runtime"):
        require(Path(b[k]).is_absolute() and str(Path(b[k]).resolve()) == b[k], "canonical_binding_required")
    for k in ("bb", "effectiveBB", "keepgoing"):
        v = g[k]
        require(isinstance(v, dict) and set(v) == {"path", "sha256"} and string(v["path"]) and isinstance(v["sha256"], str) and len(v["sha256"]) == 64, "executable_schema")
    g["context"] = {k: g[k] for k in ("id", "caller", "binding", "bb", "effectiveBB", "keepgoing", "maxAttempts", "cooldownSeconds")}
    return g


def check_sources(g):
    require(os.environ.get("BB_THREAD_ID") == g["caller"], "caller_changed")
    require(str(Path(os.environ.get("BB_CLI") or g["bb"]["path"]).resolve()) == str(Path(g["effectiveBB"]["path"]).resolve()), "bb_reexec_changed")
    for k in ("bb", "effectiveBB", "keepgoing"):
        require(file_hash(g[k]["path"], 256 * LIMIT)[0] == g[k]["sha256"], "executable_changed")
    sources(g["continuity"])


def reading(value):
    return isinstance(value, dict) and set(value) == {"usedTokens", "modelContextWindow", "estimated"} and type(value["estimated"]) is bool and integer(value["usedTokens"], 0, 400_000_000) and integer(value["modelContextWindow"], 1, 100_000_000)


def continuity_shape(value):
    return isinstance(value, dict) and set(value) == ROLES and all(isinstance(v, dict) and set(v) == {"path", "sha256"} and string(v["path"]) and Path(v["path"]).is_absolute() and isinstance(v["sha256"], str) and re.fullmatch(r"[0-9a-f]{64}", v["sha256"]) is not None for v in value.values())


def validate(s):
    require(isinstance(s, dict) and set(s) == {"version", "context", "attempts"} and type(s["version"]) is int and s["version"] == 1, "journal_schema")
    require(isinstance(s["context"], dict) and isinstance(s["attempts"], list) and len(s["attempts"]) <= 8, "journal_schema")
    ids, requests = set(), set()
    for a in s["attempts"]:
        require(isinstance(a, dict) and set(a) == {"id", "request", "at", "dispatchedAt", "seq", "before", "continuity", "state", "completion", "after", "receipt"}, "attempt_schema")
        require(string(a["id"]) and a["id"] not in ids and string(a["request"]) and a["request"] not in requests, "attempt_identity")
        require(integer(a["at"], 1, 2**53) and integer(a["seq"], 0, 2**53) and reading(a["before"]) and continuity_shape(a["continuity"]), "attempt_schema")
        require(a["dispatchedAt"] is None or integer(a["dispatchedAt"], a["at"], 2**53), "attempt_timing")
        require(a["state"] == "unknown" or a["dispatchedAt"] is not None, "dispatch_required")
        require(a["state"] in {"unknown", "accepted", "completed"}, "attempt_state")
        require((a["state"] == "completed") == all(isinstance(a[k], dict) for k in ("completion", "after", "receipt")), "attempt_history")
        if a["state"] == "completed":
            e, r = a["completion"], a["receipt"]
            require(set(e) == {"seq", "type", "item", "turn"} and integer(e["seq"], a["seq"] + 1, 2**53) and e["type"] == "item/completed" and string(e["item"]) and string(e["turn"]) and reading(a["after"]), "completion_history")
            require(set(r) == {"attempt", "priorContinuity", "currentContinuity", "accepted"} and r["attempt"] == a["id"] and r["accepted"] is True and r["priorContinuity"] == digest(a["continuity"]) and isinstance(r["currentContinuity"], str) and re.fullmatch(r"[0-9a-f]{64}", r["currentContinuity"]) is not None, "receipt_history")
        if a["state"] != "completed":
            require(all(a[k] is None for k in ("completion", "after", "receipt")), "attempt_history")
        ids.add(a["id"]); requests.add(a["request"])
    require(all(a["state"] == "completed" for a in s["attempts"][:-1]), "unresolved_history")


class CompactJournal(Journal):
    def load(self, g):
        if not self.path.exists() and not self.path.is_symlink():
            s = {"version": 1, "context": g["context"], "attempts": []}
        else:
            s = decode(private_read(self.path))
        validate(s)
        require(s["context"] == g["context"], "journal_binding_changed")
        return s

    def save(self, s):
        validate(s)
        if self.path.exists() or self.path.is_symlink():
            old = decode(private_read(self.path)); validate(old)
            require(old["context"] == s["context"] and len(old["attempts"]) <= len(s["attempts"]) <= len(old["attempts"]) + 1, "journal_transition")
            for a, b in zip(old["attempts"], s["attempts"]):
                require(all(a[k] == b[k] for k in ("id", "request", "at", "seq", "before", "continuity")), "journal_transition")
                require(a["dispatchedAt"] == b["dispatchedAt"] or a["state"] == "unknown" and b["state"] in {"unknown", "accepted"} and b["dispatchedAt"] is not None and (a["dispatchedAt"] is None or b["dispatchedAt"] >= a["dispatchedAt"]), "journal_transition")
                require(b["state"] in {"unknown": {"unknown", "accepted", "completed"}, "accepted": {"accepted", "completed"}, "completed": {"completed"}}[a["state"]], "journal_transition")
                require(a["state"] != "completed" or a == b, "journal_transition")
        raw = json.dumps(s, sort_keys=True).encode()
        require(len(raw) <= 256 * 1024, "journal_full")
        name = None
        try:
            with tempfile.NamedTemporaryFile(dir=self.path.parent, prefix=".compact-", delete=False) as f:
                name = f.name; os.fchmod(f.fileno(), 0o600)
                f.write(raw); f.flush(); os.fsync(f.fileno())
            os.replace(name, self.path); name = None
            fd = os.open(self.path.parent, os.O_RDONLY)
            try: os.fsync(fd)
            finally: os.close(fd)
        finally:
            if name: os.unlink(name)


class CompactCommands(Commands):
    def status(self, safe=True):
        g, b = self.grant, self.grant["binding"]
        s = mapping(self.cli(["status"]))
        mapping(s.get("targetAgent")); mapping(s.get("control")); mapping(s.get("goal")); mapping(s["control"].get("ownership"))
        require(isinstance(s, dict) and s.get("loop") == b["loop"] and s.get("stateDir") == b["runtime"] and s.get("target") == "primary" and s.get("targetConfigured") is True and s.get("targetAgent", {}).get("bbThread") == b["thread"] and s.get("targetAgent", {}).get("provider") == b["provider"], "target_changed")
        c = s.get("control", {})
        require(c.get("ownership", {}).get("threadId") == b["owner"] and c.get("ownership", {}).get("generation") == b["generation"] and s.get("goal", {}).get("id") == b["goal"] and s.get("goal", {}).get("revision") == b["goalRevision"] and not s.get("goalError") and not s.get("unreadable") and not s.get("sendRecord"), "ownership_goal_unavailable")
        if safe:
            require(s.get("controlState") == "enabled" and c.get("enabled") is True and all(c.get(k) is False for k in ("paused", "stop", "awaitingUser")) and c.get("completed", False) is False and not c.get("pendingTransfer") and not c.get("supersededBy") and s.get("deadlineReached") is False, "runtime_hold")
            for k, flag in (("sendAllowance", "reached"), ("progressAllowance", "due")):
                v = s.get(k)
                require(v is None or isinstance(v, dict) and v.get(flag) is False and not v.get("error"), "runtime_allowance")
        return s

    def thread(self, tid, parent=None, safe=True):
        b = self.grant["binding"]
        r = mapping(self.call("bb", ["thread", "show", tid, "--json"], limit=64 * 1024))
        t, e = mapping(r.get("thread")), mapping(r.get("environment"))
        require(t.get("id") == tid and t.get("projectId") == b["project"] and t.get("environmentId") == b["environment"] and e.get("id") == b["environment"] and e.get("hostId") == b["host"] and "archivedAt" in t and t["archivedAt"] is None and "deletedAt" in t and t["deletedAt"] is None, "thread_identity_changed")
        require(t.get("parentThreadId") == parent if parent else t.get("providerId") == b["provider"], "thread_identity_changed")
        if safe:
            require(t.get("status") == "idle" and type(t.get("queuedMessageCount")) is int and t["queuedMessageCount"] == 0 and type(t.get("activeBackgroundAgentCount")) is int and t["activeBackgroundAgentCount"] == 0, "not_idle_empty")
            pending = self.call("bb", ["thread", "interactions", "list", tid, "--json"], limit=64 * 1024)
            require(isinstance(pending, list) and len(pending) < 500 and all(isinstance(i, dict) and i.get("threadId") == tid and i.get("status") in {"resolved", "interrupted"} for i in pending), "pending_input")

    def snapshot(self):
        self.status()
        b = self.grant["binding"]
        # Assignment inventory supplies explicit coverage, unlike a truncated
        # bare thread listing. No assignment is written or inferred from text.
        v = self.cli(["assignment", "inventory"], timeout=20)
        require(isinstance(v, dict) and v.get("coverage") == "observed_direct_children" and isinstance(v.get("lanes"), list) and len(v["lanes"]) <= 32, "child_coverage_unavailable")
        ids = []
        for row in v["lanes"]:
            require(isinstance(row, dict) and string(row.get("thread")) and row.get("archived") is False and row.get("execution") == "idle", "child_not_idle")
            ids.append(row["thread"])
            self.thread(row["thread"], b["thread"])
        require(len(set(ids)) == len(ids), "child_coverage_unavailable")
        self.thread(b["thread"])
        return sorted(ids)

    def context(self):
        v = mapping(self.call("bb", ["thread", "context", self.grant["binding"]["thread"], "--json"], limit=64 * 1024)).get("usage")
        require(isinstance(v, dict) and type(v.get("estimated")) is bool and integer(v.get("usedTokens"), 0, 400_000_000) and integer(v.get("modelContextWindow"), 1, 100_000_000), "context_unavailable")
        return {k: v[k] for k in ("usedTokens", "modelContextWindow", "estimated")}

    def cursor(self):
        b = self.grant["binding"]
        p = Path(b["runtime"]) / "targets" / "primary" / "bb-cursor.json"
        c = decode(private_read(p))
        require(c.get("thread") == b["thread"] and integer(c.get("lastSeq"), 0, 2**53), "cursor_unavailable")
        return c["lastSeq"]

    def events(self, after):
        # Read at most 2,000 new events from the supervisor's existing cursor,
        # never rebuild a whole transcript. Only lifecycle fields are inspected.
        out, seq = [], after
        for _ in range(20):
            rows = self.call("bb", ["thread", "log", self.grant["binding"]["thread"], "--json", "--after-seq", str(seq), "--limit", "100"])
            require(isinstance(rows, list) and len(rows) <= 100, "event_coverage_unavailable")
            for e in rows:
                require(isinstance(e, dict) and integer(e.get("seq"), seq + 1, 2**53) and e.get("threadId") == self.grant["binding"]["thread"], "event_sequence_changed")
                seq = e["seq"]
                d = e.get("data", {}); item = d.get("item", {}) if isinstance(d, dict) else {}
                if e.get("type") in {"item/started", "item/completed"} and isinstance(item, dict) and item.get("type") == "contextCompaction":
                    scope = e.get("scope", {})
                    require(string(item.get("id")) and isinstance(scope, dict) and scope.get("kind") == "turn" and string(scope.get("turnId")), "compaction_identity_unavailable")
                    out.append({"seq": seq, "type": e["type"], "item": item["id"], "turn": scope["turnId"]})
            if len(rows) < 100: return seq, out
        raise Refused("event_coverage_bound")


def advance(g, journal, commands, execute=False, receipt=None, now=None):
    require(Path(g["binding"]["runtime"]) not in journal.path.parents, "journal_outside_runtime_required")
    clock = (lambda: int(time.time())) if now is None else (lambda: now)
    now = clock()
    s = journal.load(g)
    pending = next((a for a in s["attempts"] if a["state"] != "completed"), None)
    if pending:
        if receipt is None:
            return {"action": "hold_pending_attempt", "state": pending["state"]}
        require(execute, "reconcile_requires_execute")
        require(pending["dispatchedAt"] is not None, "dispatch_unconfirmed")
        for attempt in s["attempts"]: sources(attempt["continuity"])
        check_sources(g); commands.status(safe=False); commands.thread(g["binding"]["thread"], safe=False)
        _, events = commands.events(pending["seq"])
        starts = [e for e in events if e["type"] == "item/started"]
        done = [e for e in events if e["type"] == "item/completed"]
        require(len(starts) == len(done) == 1 and starts[0]["seq"] < done[0]["seq"] and all(starts[0][k] == done[0][k] for k in ("item", "turn")), "completion_unconfirmed")
        require(isinstance(receipt, dict) and set(receipt) == {"attempt", "priorContinuity", "currentContinuity", "accepted"} and receipt["accepted"] is True and receipt["attempt"] == pending["id"] and receipt["priorContinuity"] == digest(pending["continuity"]) and receipt["currentContinuity"] == digest(g["continuity"]), "continuity_receipt_required")
        after = commands.context(); check_sources(g)
        for attempt in s["attempts"]: sources(attempt["continuity"])
        pending.update(state="completed", completion=done[0], after=after, receipt=receipt)
        journal.save(s)
        return {"action": "completion_and_receipt_recorded", "savings": "unknown"}
    require(receipt is None, "no_pending_attempt")
    require(not any(a["request"] == g["request"] for a in s["attempts"]), "request_already_attempted")
    require(len(s["attempts"]) < g["maxAttempts"], "attempt_cap")
    require(not s["attempts"] or now >= (s["attempts"][-1]["dispatchedAt"] or s["attempts"][-1]["at"]) + g["cooldownSeconds"], "cooldown")
    for attempt in s["attempts"]: sources(attempt["continuity"])
    check_sources(g)
    before_children = commands.snapshot()
    seq, events = commands.events(commands.cursor())
    # Do not add a compact on top of an unconsumed compact observation.
    require(not events, "recent_compaction_reconcile_first")
    require(commands.snapshot() == before_children, "child_roster_changed")
    seq, changed = commands.events(seq)
    require(not changed, "recent_compaction_reconcile_first")
    before = commands.context()
    check_sources(g)
    seq, changed = commands.events(seq)
    require(not changed, "recent_compaction_reconcile_first")
    if not execute: return {"action": "eligible_manual_request", "dryRun": True, "measurementFreshness": "provider_timestamp_unavailable"}
    a = {"id": uuid.uuid4().hex, "request": g["request"], "at": clock(), "dispatchedAt": None, "seq": seq, "before": before, "continuity": g["continuity"], "state": "unknown", "completion": None, "after": None, "receipt": None}
    s["attempts"].append(a); journal.save(s)
    # Narrow the local race after durable reservation. This still is not an
    # atomic transaction with BB, controls or another host request.
    check_sources(g)
    require(commands.snapshot() == before_children, "child_roster_changed")
    _, changed = commands.events(seq)
    require(not changed, "recent_compaction_reconcile_first")
    # Durable dispatch fence excludes reservations refused before submission.
    # Its timestamp also excludes preparation and admission from cooldown.
    a["dispatchedAt"] = clock(); journal.save(s)
    try:
        reply = commands.call("bb", ["thread", "compact", g["binding"]["thread"], "--json"], timeout=20, limit=64 * 1024)
        require(isinstance(reply, dict) and reply.get("ok") is True and reply.get("threadId") == g["binding"]["thread"], "compact_unconfirmed")
        a["dispatchedAt"] = max(a["dispatchedAt"], clock())
        a["state"] = "accepted"; journal.save(s)
    except (Refused, OSError):
        # Include elapsed command time even when its result is uncertain.
        a["dispatchedAt"] = max(a["dispatchedAt"], clock()); journal.save(s)
        return {"action": "hold_pending_attempt", "state": "unknown"}
    return {"action": "request_accepted_not_completed", "state": "accepted"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--grant", required=True); parser.add_argument("--journal", required=True)
    parser.add_argument("--execute", action="store_true"); parser.add_argument("--receipt")
    args = parser.parse_args()
    try:
        g = load_grant(args.grant)
        receipt = decode(private_read(args.receipt)) if args.receipt else None
        j = CompactJournal(args.journal)
        with j.locked(): result = advance(g, j, CompactCommands(g), args.execute, receipt)
        print(json.dumps(result)); return 0
    except Refused as error:
        reason = str(error)
        if re.fullmatch(r"[a-z_]{1,80}", reason) is None: reason = "compact_workflow_refused"
        print(json.dumps({"action": "refused", "reason": reason})); return 1
    except (OSError, ValueError, TypeError, KeyError):
        # Never return command output, paths, transcript text or source content.
        print(json.dumps({"action": "refused", "reason": "compact_workflow_unavailable_or_changed"})); return 1


if __name__ == "__main__": sys.exit(main())
