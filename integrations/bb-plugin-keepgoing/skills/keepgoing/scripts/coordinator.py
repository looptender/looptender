#!/usr/bin/env python3
"""Explicitly granted agent-side new-child workflow; never a supervisor action."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import stat
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
from coordinator_state import Journal, Refused, decode, digest, next_task, private_read, require, reserve, settle

LIMIT = 1024 * 1024
OPERATION_END = float("inf")


def text(value, maximum=4096):
    return isinstance(value, str) and 0 < len(value.encode()) <= maximum and all(ord(c) >= 32 and ord(c) != 127 for c in value)


def file_hash(path, maximum=LIMIT, collect=False):
    require(Path(path).is_absolute(), "absolute_path_required")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode) and info.st_size <= maximum, "source_bound")
        total, chunks, hashed = 0, [], hashlib.sha256()
        while True:
            require(time.monotonic() < OPERATION_END - 2, "operation_budget")
            chunk = os.read(fd, min(65536, maximum + 1 - total))
            if not chunk:
                break
            total += len(chunk)
            require(total <= maximum, "source_bound")
            hashed.update(chunk)
            if collect:
                chunks.append(chunk)
        require(total == info.st_size and all(getattr(os.fstat(fd), k) == getattr(info, k) for k in ("st_dev", "st_ino", "st_size", "st_mtime_ns", "st_ctime_ns")), "source_changed")
        return hashed.hexdigest(), b"".join(chunks)
    finally:
        os.close(fd)


def load_grant(path):
    g = decode(private_read(path, 100 * 1024), 100 * 1024)
    require(isinstance(g, dict) and set(g) == {"version", "id", "revision", "direction", "instructionRef", "binding", "recordId", "project", "environment", "host", "provider", "model", "effort", "bb", "effectiveBB", "keepgoing", "sources", "maxAttempts", "maxChildren", "tasks"}, "grant_schema")
    require(type(g["version"]) is int and g["version"] == 1 and all(type(g[k]) is int and 1 <= g[k] <= 128 for k in ("revision", "direction", "maxAttempts")) and type(g["maxChildren"]) is int and 1 <= g["maxChildren"] <= 32, "grant_bounds")
    require(g["direction"] <= g["revision"], "grant_direction")
    for k in ("id", "instructionRef", "recordId", "project", "environment", "host", "provider", "model", "effort"):
        require(text(g[k], 256), "grant_schema")
    b = g["binding"]
    require(isinstance(b, dict) and set(b) == {"loop", "config", "runtime", "coordinator", "owner", "generation", "goal", "goalRevision"} and b["coordinator"] == b["owner"] and os.environ.get("BB_THREAD_ID") == b["owner"], "caller_binding")
    require(all(text(b[k]) for k in ("loop", "config", "runtime", "coordinator", "owner", "goal")) and all(type(b[k]) is int and b[k] > 0 for k in ("generation", "goalRevision")), "grant_schema")
    require(str(Path(b["config"]).resolve()) == b["config"] and str(Path(b["runtime"]).resolve()) == b["runtime"] and Path(b["config"]).is_absolute() and Path(b["runtime"]).is_absolute(), "canonical_binding_required")
    for key in ("bb", "effectiveBB", "keepgoing"):
        item = g[key]
        require(isinstance(item, dict) and set(item) == {"path", "sha256"} and text(item["path"]) and Path(item["path"]).is_absolute() and text(item["sha256"], 64) and len(item["sha256"]) == 64, "executable_binding")
    require(isinstance(g["sources"], list) and 1 <= len(g["sources"]) <= 64, "source_bound")
    refs = {}
    for item in g["sources"]:
        require(isinstance(item, dict) and set(item) == {"ref", "path", "sha256"} and text(item["ref"]) and text(item["path"]) and text(item["sha256"], 64) and len(item["sha256"]) == 64 and item["ref"] not in refs, "source_schema")
        refs[item["ref"]] = item
    require(g["instructionRef"] in refs and any(i["path"] == b["config"] for i in refs.values()), "authority_source_required")
    require(isinstance(g["tasks"], list) and 1 <= len(g["tasks"]) <= 32, "task_bound")
    ids = set()
    for t in g["tasks"]:
        require(isinstance(t, dict) and set(t) == {"id", "scopeRefs", "authorizationRefs", "briefRef", "resources", "dependsOn", "personGate", "parkedAt"}, "task_schema")
        require(text(t["id"], 64) and t["id"] not in ids and t["id"][0].islower() and all(c in "abcdefghijklmnopqrstuvwxyz0123456789_-" for c in t["id"]), "task_identity")
        ids.add(t["id"])
        require(type(t["personGate"]) is bool and type(t["parkedAt"]) is int and t["parkedAt"] >= 0 and isinstance(t["dependsOn"], list) and len(t["dependsOn"]) <= 32 and isinstance(t["resources"], list) and len(t["resources"]) <= 8 and all(text(r, 128) for r in t["resources"]) and len(set(t["resources"])) == len(t["resources"]), "task_schema")
        for k in ("scopeRefs", "authorizationRefs"):
            require(isinstance(t[k], list) and 1 <= len(t[k]) <= 32 and all(r in refs for r in t[k]), "task_authority_required")
        require(t["briefRef"] in refs, "task_brief_required")
        t["scopeDigest"] = digest({"task": t, "sources": [refs[r] for r in sorted(set(t["scopeRefs"] + t["authorizationRefs"] + [t["briefRef"]]))]})
    require(all(all(d in ids and d != t["id"] for d in t["dependsOn"]) for t in g["tasks"]), "task_dependency")
    g["context"] = {k: g[k] for k in ("binding", "recordId", "project", "environment", "host", "provider", "model", "effort", "bb", "effectiveBB", "keepgoing")}
    check_sources(g)
    return g


def check_sources(g):
    require(os.environ.get("BB_THREAD_ID") == g["binding"]["owner"], "caller_binding")
    effective = os.environ.get("BB_CLI") or g["bb"]["path"]
    require(str(Path(effective).resolve()) == str(Path(g["effectiveBB"]["path"]).resolve()), "bb_reexec_changed")
    for key in ("bb", "effectiveBB", "keepgoing"):
        require(file_hash(g[key]["path"], 256 * LIMIT)[0] == g[key]["sha256"], "executable_changed")
    for item in g["sources"]:
        require(file_hash(item["path"])[0] == item["sha256"], "source_changed")


class Commands:
    """Same streaming subprocess adapter for production and fake executables."""
    def __init__(self, grant):
        self.grant = grant
        self.deadline = min(OPERATION_END, time.monotonic() + 60)
        self.calls = 0

    def call(self, executable, args, stdin=b"", timeout=2, limit=LIMIT):
        self.calls += 1
        require(self.calls <= 430 and self.deadline - time.monotonic() > 2, "operation_budget")
        end = min(self.deadline - 2, time.monotonic() + timeout)
        process = subprocess.Popen([self.grant[executable]["path"], *args], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
        selector = selectors.DefaultSelector()
        raw = bytearray()
        try:
            os.set_blocking(process.stdout.fileno(), False)
            os.set_blocking(process.stdin.fileno(), False)
            selector.register(process.stdout, selectors.EVENT_READ)
            if stdin:
                require(len(stdin) <= LIMIT, "stdin_bound")
                selector.register(process.stdin, selectors.EVENT_WRITE)
            else:
                process.stdin.close()
            sent = 0
            while selector.get_map():
                require(time.monotonic() < end, "command_timeout")
                for key, _ in selector.select(max(0, end - time.monotonic())):
                    if key.fileobj is process.stdin:
                        count = os.write(process.stdin.fileno(), stdin[sent:sent + 65536])
                        sent += count
                        if sent == len(stdin):
                            selector.unregister(process.stdin); process.stdin.close()
                    else:
                        chunk = os.read(process.stdout.fileno(), min(65536, limit + 1 - len(raw)))
                        if not chunk:
                            selector.unregister(process.stdout)
                        else:
                            raw.extend(chunk)
                            require(len(raw) <= limit, "command_output_bound")
            require(process.wait(timeout=max(0.001, end - time.monotonic())) == 0, "command_failed")
            value = decode(raw, limit)
            require(not (isinstance(value, dict) and (value.get("ok") is False or value.get("error") is not None)), "command_refused")
            return value
        except (BrokenPipeError, subprocess.TimeoutExpired):
            raise Refused("command_unconfirmed") from None
        finally:
            selector.close()
            # Local client cleanup says nothing about host-side creation outcome.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                raise Refused("client_reap_unconfirmed") from None
            process.stdout.close()
            if not process.stdin.closed:
                process.stdin.close()

    def cli(self, args, **kw):
        return self.call("keepgoing", args + ["-c", self.grant["binding"]["config"], "--json"], **kw)

    def status(self):
        check_sources(self.grant)
        s = self.cli(["status"])
        b = self.grant["binding"]
        require(isinstance(s, dict) and s.get("loop") == b["loop"] and s.get("target") == "primary" and s.get("targetConfigured") is True and s.get("targetAgent", {}).get("bbThread") == b["coordinator"], "current_target_changed")
        c = s.get("control", {})
        require(isinstance(c, dict) and isinstance(c.get("ownership"), dict) and isinstance(s.get("goal"), dict) and isinstance(s.get("assignments"), dict) and isinstance(s.get("targetAgent"), dict), "current_status_unavailable")
        require(s.get("controlState") == "enabled" and c.get("enabled") is True and all(c.get(k) is False for k in ("paused", "stop", "awaitingUser")) and c.get("completed", False) is False and not c.get("supersededBy") and not c.get("pendingTransfer") and c.get("bbTarget", b["coordinator"]) == b["coordinator"], "runtime_hold")
        owner = c.get("ownership", {})
        require(owner.get("threadId") == b["owner"] and owner.get("generation") == b["generation"] and s.get("goal", {}).get("id") == b["goal"] and s.get("goal", {}).get("revision") == b["goalRevision"] and not s.get("goalError") and s.get("assignments", {}).get("availability") == "available", "current_binding_changed")
        require(not s.get("unreadable") and not s.get("sendRecord") and s.get("deadlineReached") is False, "current_status_unavailable")
        for key, flag in (("sendAllowance", "reached"), ("progressAllowance", "due")):
            a = s.get(key)
            require(a is None or isinstance(a, dict) and a.get(flag) is False and not a.get("error"), "runtime_allowance")
        return s

    def record(self, expected):
        r = self.cli(["assignment", "show", "--private"])
        require(isinstance(r, dict) and r.get("version") in (1, 2) and r.get("id") == self.grant["recordId"] and isinstance(r.get("revisions"), list) and 1 <= len(r["revisions"]) <= 32, "assignment_record")
        x = r["revisions"][-1]
        require(x.get("revision") == expected and x.get("binding") == self.grant["binding"], "assignment_revision_changed")
        require(isinstance(x.get("rows"), list) and len(x["rows"]) <= 200 and x.get("inventory", {}).get("coverage") == "observed_direct_children", "assignment_coverage")
        return x

    def thread(self, child):
        reply = self.call("bb", ["thread", "show", child, "--json"], limit=64 * 1024)
        require(isinstance(reply, dict) and isinstance(reply.get("thread"), dict) and isinstance(reply.get("environment"), dict), "child_unavailable")
        t, e = reply["thread"], reply["environment"]
        g = self.grant
        require(t.get("id") == child and t.get("parentThreadId") == g["binding"]["coordinator"] and t.get("projectId") == g["project"] and t.get("environmentId") == g["environment"] and e and e.get("id") == g["environment"] and e.get("hostId") == g["host"], "child_identity_changed")
        require(t.get("status") in {"idle", "active", "error", "pending", "starting", "stopping"} and "archivedAt" in t and "deletedAt" in t and t["deletedAt"] is None and text(t.get("providerId"), 256) and t.get("visibility") in {"visible", "hidden"} and type(t.get("queuedMessageCount")) is int and 0 <= t["queuedMessageCount"] <= 10000, "child_unavailable")
        pending = self.call("bb", ["thread", "interactions", "list", child, "--json"], limit=64 * 1024)
        require(isinstance(pending, list) and len(pending) < 500 and all(isinstance(i, dict) and i.get("threadId") == child and i.get("status") in {"resolved", "interrupted"} for i in pending), "pending_or_unknown_input")
        return {"execution": t["status"], "archived": t["archivedAt"] is not None, "pending": False, "queued": t["queuedMessageCount"], "provider": t.get("providerId"), "visibility": t.get("visibility")}

    def snapshot(self, expected, extra_child=None):
        self.status()
        parent = self.call("bb", ["thread", "show", self.grant["binding"]["coordinator"], "--json"], limit=64 * 1024)
        t, e = parent.get("thread", {}), parent.get("environment", {})
        g = self.grant
        require(t.get("id") == g["binding"]["coordinator"] and t.get("projectId") == g["project"] and t.get("environmentId") == g["environment"] and e.get("id") == g["environment"] and e.get("hostId") == g["host"] and t.get("deletedAt") is None and "deletedAt" in t and t.get("archivedAt") is None and "archivedAt" in t, "parent_context_changed")
        require(type(t.get("queuedMessageCount")) is int and t["queuedMessageCount"] == 0, "parent_input_pending")
        pending = self.call("bb", ["thread", "interactions", "list", g["binding"]["coordinator"], "--json"], limit=64 * 1024)
        require(isinstance(pending, list) and len(pending) < 500 and all(isinstance(i, dict) and i.get("threadId") == g["binding"]["coordinator"] and i.get("status") in {"resolved", "interrupted"} for i in pending), "parent_input_pending")
        x = self.record(expected)
        inventory = self.cli(["assignment", "inventory"], timeout=20)
        require(isinstance(inventory, dict) and inventory.get("coverage") == "observed_direct_children" and isinstance(inventory.get("lanes"), list) and len(inventory["lanes"]) < 200, "inventory_unavailable")
        observed = {i["thread"]: i for i in inventory["lanes"]}
        reviewed = {i["thread"]: i for i in x["inventory"]["lanes"]}
        require(len(observed) == len(inventory["lanes"]) and len(reviewed) == len(x["inventory"]["lanes"]) and set(observed) == set(reviewed) | ({extra_child} if extra_child else set()) and all(observed[k]["archived"] == reviewed[k]["archived"] for k in reviewed), "inventory_changed")
        children = {child: self.thread(child) for child in observed}
        require(all(children[k]["execution"] == observed[k]["execution"] and children[k]["archived"] == observed[k]["archived"] for k in observed), "inventory_changed")
        return {"recordId": self.grant["recordId"], "revision": expected, "rows": x["rows"], "children": children}

    def final_fence(self, expected, extra_child=None, previous=None):
        fresh = self.snapshot(expected, extra_child)
        if previous:
            require(fresh["children"] == previous["children"] and fresh["rows"] == previous["rows"], "final_observation_changed")
        # Fresh source/control/owner reread nearest to publication. This is not
        # atomic with BB; a submitted request can outlive a subsequent hold.
        self.status()
        self.record(expected)
        require(self.deadline - time.monotonic() >= 12, "operation_budget")

    def spawn(self, task):
        g = self.grant
        source = next(s for s in g["sources"] if s["ref"] == task["briefRef"])
        hashed, raw = file_hash(source["path"], 64 * 1024, collect=True)
        require(hashed == source["sha256"], "source_changed")
        args = ["thread", "spawn", "--json", "--parent-thread", g["binding"]["coordinator"], "--project", g["project"], "--environment", g["environment"], "--host", g["host"], "--provider", g["provider"], "--model", g["model"], "--reasoning-level", g["effort"], "--visibility", "visible", "--parent-notifications", "auto", "--title", "Delegated " + task["id"], "--prompt-file", "-"]
        reply = self.call("bb", args, stdin=raw, timeout=15)
        require(isinstance(reply, dict) and text(reply.get("id"), 132) and reply["id"].startswith("thr_"), "spawn_reply_unconfirmed")
        return reply["id"]

    def map_child(self, task, child, snapshot, directory):
        existing = [r for r in snapshot["rows"] if r["spec"]["id"] == task["id"]]
        spec = {"id": task["id"], "lane": child, "scopeRefs": task["scopeRefs"], "authorizationRefs": task["authorizationRefs"]}
        if existing:
            require(len(existing) == 1 and existing[0]["spec"] == spec, "mapping_conflict")
            return snapshot["revision"]
        require(not any(r["spec"]["lane"] == child for r in snapshot["rows"]), "duplicate_owner")
        plan = {"assignments": [r["spec"] for r in snapshot["rows"]] + [spec]}
        with tempfile.NamedTemporaryFile(dir=directory, prefix=".assignment-plan-", mode="w") as stream:
            os.fchmod(stream.fileno(), 0o600)
            json.dump(plan, stream); stream.flush(); os.fsync(stream.fileno())
            self.cli(["assignment", "review", "--file", stream.name, "--id", snapshot["recordId"], "--revision", str(snapshot["revision"])], timeout=10)
        current = self.record(snapshot["revision"] + 1)
        require(any(r["spec"] == spec for r in current["rows"]), "mapping_unconfirmed")
        return snapshot["revision"] + 1


def verify_artifacts(items):
    require(isinstance(items, list) and 1 <= len(items) <= 16, "verification_artifacts")
    for item in items:
        require(isinstance(item, dict) and set(item) == {"path", "sha256"} and text(item["path"]) and text(item["sha256"], 64), "verification_schema")
        require(file_hash(item["path"])[0] == item["sha256"], "verification_artifact_changed")


def advance(g, journal, commands, expected, execute=False, receipts=(), reconcile=None):
    state = journal.load(g)
    unresolved = [a for a in state["attempts"] if a["state"] in {"unknown", "mapping_pending"}]
    if unresolved and not reconcile:
        return {"action": "reconcile_unknown_attempt", "nextAction": "reconcile_exact_intent_child_and_assignment_mapping", "pendingIntents": [{k: a[k] for k in ("id", "task", "child", "state")} for a in unresolved], "blocked": None, "independentReady": None, "coverage": "not_revalidated", "completed": len(state["completed"]), "attempted": state["spent"]}
    extra = reconcile[1] if reconcile else None
    snap = commands.snapshot(expected, extra)
    for attempt in state["attempts"]:
        if attempt["state"] == "owned":
            t = next(t for t in g["tasks"] if t["id"] == attempt["task"])
            expected_spec = {"id": t["id"], "lane": attempt["child"], "scopeRefs": t["scopeRefs"], "authorizationRefs": t["authorizationRefs"]}
            require(any(r["spec"] == expected_spec for r in snap["rows"]), "owned_mapping_changed_reconcile")
    settle(state, snap, receipts, verify_artifacts)
    task, outcome = next_task(g, state, snap)
    if not execute:
        return dict(outcome, dryRun=True, scope="local_attempts_direct_children_declared_resources")
    if reconcile:
        intent, child = reconcile
        matches = [a for a in state["attempts"] if a["id"] == intent and a["state"] in {"unknown", "mapping_pending"}]
        require(len(matches) == 1 and (not matches[0]["child"] or matches[0]["child"] == child), "unknown_intent_mismatch")
        a = matches[0]; task = next(t for t in g["tasks"] if t["id"] == a["task"])
        native = commands.thread(child)
        require(not native["archived"] and native["provider"] == g["provider"] and native["visibility"] == "visible", "child_identity_changed")
        commands.final_fence(expected, extra_child=child)
        a["child"], a["state"] = child, "mapping_pending"; journal.save(state)
        revision = commands.map_child(task, child, snap, journal.path.parent)
        a["state"] = "owned"; journal.save(state)
        return dict(outcome, action="reconciled_creation", assignmentRevision=revision)
    # Settlement is persisted even when no new work can be dispatched.
    journal.save(state)
    if task is None:
        return outcome
    commands.final_fence(expected, previous=snap)
    a = reserve(state, task); journal.save(state)  # before the possibly remote side effect
    try:
        child = commands.spawn(task)
        native = commands.thread(child)
        require(not native["archived"] and native["provider"] == g["provider"] and native["visibility"] == "visible", "child_identity_changed")
        a["child"], a["state"] = child, "mapping_pending"; journal.save(state)
        # Publication can race a later hold/source change; never replay spawn.
        commands.final_fence(expected, extra_child=child)
        revision = commands.map_child(task, child, snap, journal.path.parent)
        a["state"] = "owned"; journal.save(state)
        return dict(outcome, action="dispatched_new_child", assignmentRevision=revision)
    except (Refused, OSError):
        return dict(outcome, action="reconcile_unknown_attempt")


def main():
    global OPERATION_END
    OPERATION_END = time.monotonic() + 60
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--grant", required=True)
    parser.add_argument("--journal", required=True)
    parser.add_argument("--assignment-revision", required=True, type=int)
    parser.add_argument("--execute", action="store_true", help="use only within the existing owner delegation grant")
    parser.add_argument("--verification", help="private current coordinator verification receipts JSON array")
    parser.add_argument("--reconcile-intent")
    parser.add_argument("--child")
    parser.add_argument("--private", action="store_true", help="include private task/assignment IDs in output")
    args = parser.parse_args()
    try:
        require(os.name == "posix" and args.assignment_revision > 0 and bool(args.reconcile_intent) == bool(args.child), "unsupported_or_invalid_invocation")
        g = load_grant(args.grant)
        journal = Journal(args.journal)
        receipts = decode(private_read(args.verification, 100 * 1024), 100 * 1024) if args.verification else []
        require(isinstance(receipts, list) and len(receipts) <= 32, "verification_bound")
        def run():
            return advance(g, journal, Commands(g), args.assignment_revision, args.execute, receipts, (args.reconcile_intent, args.child) if args.child else None)
        if args.execute:
            with journal.locked(): result = run()
        else:
            result = run()
        if not args.private:
            for key in ("blocked", "independentReady", "pendingIntents"):
                if key in result and isinstance(result[key], list):
                    result[key] = len(result[key])
        print(json.dumps(result, sort_keys=True))
        return 0
    except (Refused, OSError, KeyError, TypeError, ValueError, AttributeError, StopIteration) as error:
        print(json.dumps({"action": "workflow_unavailable", "reason": str(error) if isinstance(error, Refused) else "evidence_unavailable", "nextAction": "inspect_private_grant_sources_journal_and_current_assignment_evidence"}))
        return 1


if __name__ == "__main__":
    sys.exit(main())
