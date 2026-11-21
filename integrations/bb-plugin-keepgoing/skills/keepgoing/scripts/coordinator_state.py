"""Agent-owned delegation journal/policy. Never imported by the supervisor."""
import contextlib
import hashlib
import json
import os
import re
from pathlib import Path
import stat
import tempfile
import uuid

MAX_BYTES = 256 * 1024
MAX_ATTEMPTS = 128


class Refused(Exception):
    """Fixed reason codes only; private command output is never propagated."""


def require(condition, reason):
    if not condition:
        raise Refused(reason)


def decode(raw, limit=MAX_BYTES):
    require(len(raw) <= limit, "input_bound")
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "duplicate_field")
            result[key] = value
        return result
    def bad_constant(_):
        raise Refused("invalid_number")
    try:
        return json.loads(raw, object_pairs_hook=pairs, parse_constant=bad_constant)
    except (ValueError, UnicodeError, TypeError):
        raise Refused("invalid_json") from None


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def private_read(path, limit=MAX_BYTES):
    path = Path(path)
    require(path.is_absolute(), "absolute_path_required")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        s = os.fstat(fd)
        require(stat.S_ISREG(s.st_mode) and s.st_uid == os.getuid() and not s.st_mode & 0o077, "private_regular_file_required")
        require(s.st_size <= limit, "input_bound")
        with os.fdopen(fd, "rb", closefd=False) as stream:
            raw = stream.read(limit + 1)
        require(len(raw) <= limit, "input_bound")
        return raw
    finally:
        os.close(fd)


def validate(state):
    require(isinstance(state, dict) and set(state) == {"version", "grantId", "context", "grantRevision", "attempts", "completed", "spent"}, "journal_schema")
    require(type(state["version"]) is int and state["version"] == 1 and type(state["grantRevision"]) is int and state["grantRevision"] >= 1, "journal_schema")
    require(isinstance(state["attempts"], list) and len(state["attempts"]) <= MAX_ATTEMPTS and isinstance(state["completed"], dict) and len(state["completed"]) <= 32, "journal_bound")
    require(type(state["spent"]) is int and state["spent"] == len(state["attempts"]), "journal_history")
    ids, tasks, children = set(), set(), set()
    for attempt in state["attempts"]:
        require(isinstance(attempt, dict) and set(attempt) == {"id", "task", "scope", "resources", "grantRevision", "state", "child"}, "journal_schema")
        require(attempt["state"] in {"unknown", "mapping_pending", "owned", "complete"}, "journal_state")
        require(isinstance(attempt["id"], str) and re.fullmatch(r"[0-9a-f]{32}", attempt["id"]) is not None and attempt["id"] not in ids, "journal_identity")
        require(isinstance(attempt["task"], str) and re.fullmatch(r"[a-z][a-z0-9_-]{0,63}", attempt["task"]) is not None and attempt["task"] not in tasks and len(tasks) < 32, "journal_identity")
        require(isinstance(attempt["resources"], list) and len(attempt["resources"]) <= 8 and all(isinstance(x, str) and 0 < len(x) <= 128 for x in attempt["resources"]), "journal_schema")
        require(isinstance(attempt["scope"], str) and re.fullmatch(r"[0-9a-f]{64}", attempt["scope"]) is not None and type(attempt["grantRevision"]) is int and 1 <= attempt["grantRevision"] <= state["grantRevision"], "journal_schema")
        child = attempt["child"]
        require(isinstance(child, str) and (not child or re.fullmatch(r"thr_[A-Za-z0-9_-]{1,128}", child) is not None) and (child or attempt["state"] == "unknown"), "journal_identity")
        if child:
            require(child not in children, "duplicate_owner")
            children.add(child)
        ids.add(attempt["id"])
        tasks.add(attempt["task"])
    for task, receipt in state["completed"].items():
        require(task in tasks and isinstance(receipt, dict) and set(receipt) == {"digest", "assignmentRevision", "handoffRevision"}, "journal_receipt")
        require(isinstance(receipt["digest"], str) and len(receipt["digest"]) == 64 and all(type(receipt[k]) is int and receipt[k] > 0 for k in ("assignmentRevision", "handoffRevision")), "journal_receipt")
    require({a["task"] for a in state["attempts"] if a["state"] == "complete"} == set(state["completed"]), "journal_history")


class Journal:
    def __init__(self, path):
        self.path = Path(path)
        require(self.path.is_absolute(), "absolute_path_required")

    @contextlib.contextmanager
    def locked(self):
        try:
            import fcntl
        except ImportError:
            raise Refused("unix_locking_required") from None
        parent = self.path.parent
        s = parent.lstat()
        require(stat.S_ISDIR(s.st_mode) and not s.st_mode & 0o077 and s.st_uid == os.getuid(), "private_directory_required")
        fd = os.open(str(self.path) + ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            s = os.fstat(fd)
            require(stat.S_ISREG(s.st_mode) and s.st_uid == os.getuid() and not s.st_mode & 0o077, "private_lock_required")
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise Refused("workflow_busy") from None
            yield self
        finally:
            os.close(fd)

    def load(self, grant):
        if not self.path.exists() and not self.path.is_symlink():
            require(grant["revision"] == 1, "missing_journal_no_reset")
            state = {"version": 1, "grantId": grant["id"], "context": grant["context"], "grantRevision": 1, "attempts": [], "completed": {}, "spent": 0}
        else:
            state = decode(private_read(self.path))
        validate(state)
        require(state["grantId"] == grant["id"] and state["context"] == grant["context"], "journal_binding_changed")
        require(grant["revision"] >= state["grantRevision"], "stale_grant")
        tasks = {t["id"]: t for t in grant["tasks"]}
        for a in state["attempts"]:
            if a["state"] != "complete":
                require(a["task"] in tasks and a["scope"] == tasks[a["task"]]["scopeDigest"], "unfinished_scope_changed")
        state["grantRevision"] = grant["revision"]
        return state

    def save(self, state):
        validate(state)
        raw = json.dumps(state, sort_keys=True, separators=(",", ":")).encode()
        require(len(raw) <= MAX_BYTES, "journal_full")
        # Refuse malformed existing bytes instead of overwriting them as repair.
        if self.path.exists() or self.path.is_symlink():
            prior = decode(private_read(self.path))
            validate(prior)
            require(prior["grantId"] == state["grantId"] and prior["context"] == state["context"] and prior["grantRevision"] <= state["grantRevision"] and prior["spent"] <= state["spent"], "journal_transition")
            for old, new in zip(prior["attempts"], state["attempts"]):
                require(all(old[k] == new[k] for k in ("id", "task", "scope", "resources", "grantRevision")), "journal_transition")
                require(not old["child"] or old["child"] == new["child"], "journal_transition")
                require(new["state"] in {"unknown": {"unknown", "mapping_pending"}, "mapping_pending": {"mapping_pending", "owned"}, "owned": {"owned", "complete"}, "complete": {"complete"}}[old["state"]], "journal_transition")
            require(all(state["completed"].get(k) == v for k, v in prior["completed"].items()), "journal_transition")
        name = None
        try:
            with tempfile.NamedTemporaryFile(dir=self.path.parent, prefix=".delegation-", delete=False) as stream:
                name = stream.name
                os.fchmod(stream.fileno(), 0o600)
                stream.write(raw)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(name, self.path)
            name = None
            fd = os.open(self.path.parent, os.O_RDONLY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        finally:
            if name:
                os.unlink(name)


def reserve(state, task):
    require(len(state["attempts"]) < MAX_ATTEMPTS and not any(a["task"] == task["id"] for a in state["attempts"]), "task_already_reserved")
    attempt = {"id": uuid.uuid4().hex, "task": task["id"], "scope": task["scopeDigest"], "resources": task["resources"], "grantRevision": state["grantRevision"], "state": "unknown", "child": ""}
    state["attempts"].append(attempt)
    state["spent"] += 1
    return attempt


def next_task(grant, state, snapshot):
    """Return one independent new task; retain every unresolved obligation."""
    attempts = state["attempts"]
    blocked = []
    for row in snapshot["rows"]:
        h = row.get("handoff")
        child = snapshot["children"].get(row["spec"]["lane"])
        if not child or child.get("archived"):
            reason = "reconcile_ownership"
        elif child["execution"] == "error":
            reason = "reconcile_execution"
        elif not h or row.get("acknowledged", 0) != row.get("handoffRevision"):
            reason = "request_current_handoff"
        elif h["outcome"] == "complete":
            reason = "verify_outcome" if row["spec"]["id"] not in state["completed"] else None
        else:
            reason = {"superseded": "reconcile_successor", "waiting": "review_gate", "unfinished": "review_existing_obligation"}[h["outcome"]]
        if reason:
            blocked.append({"assignment": row["spec"]["id"], "action": reason})
    used = {a["task"] for a in attempts}
    resources = {r for a in attempts if a["state"] != "complete" for r in a["resources"]}
    candidates = [t for t in grant["tasks"] if t["id"] not in used and not t["personGate"] and grant["direction"] > t["parkedAt"] and all(d in state["completed"] for d in t["dependsOn"]) and not resources.intersection(t["resources"])]
    base = {"blocked": blocked, "independentReady": [t["id"] for t in candidates], "completed": len(state["completed"]), "attempted": len(attempts)}
    # Unknown includes lost mapping publication, never just lost spawn replies.
    if any(a["state"] in {"unknown", "mapping_pending"} for a in attempts):
        return None, dict(base, action="reconcile_unknown_attempt")
    if len(attempts) >= grant["maxAttempts"]:
        return None, dict(base, action="attempt_budget")
    observed = {k for k, c in snapshot["children"].items() if not c["archived"] and c["execution"] in {"active", "pending", "starting", "stopping"}}
    reserved = {a["child"] for a in attempts if a["state"] != "complete" and a["child"]}
    if len(observed | reserved) >= grant["maxChildren"]:
        return None, dict(base, action="local_capacity")
    return (candidates[0], dict(base, action="dispatch_new_child")) if candidates else (None, dict(base, action="reconcile_or_wait"))


def settle(state, snapshot, receipts, verify_artifacts):
    rows = {r["spec"]["id"]: r for r in snapshot["rows"]}
    for receipt in receipts:
        require(isinstance(receipt, dict) and set(receipt) == {"task", "recordId", "assignmentRevision", "handoffRevision", "accepted", "artifacts"}, "verification_schema")
        task = receipt["task"]
        old = state["completed"].get(task)
        if old:
            require(old["digest"] == digest(receipt), "changed_duplicate_receipt")
            continue
        matches = [a for a in state["attempts"] if a["task"] == task and a["state"] == "owned"]
        require(len(matches) == 1 and task in rows, "verification_owner")
        a, row = matches[0], rows[task]
        h = row.get("handoff")
        child = snapshot["children"].get(a["child"])
        require(receipt["accepted"] is True and receipt["recordId"] == snapshot["recordId"] and receipt["assignmentRevision"] == row["revision"] and receipt["handoffRevision"] == row["handoffRevision"] == row.get("acknowledged") and h and h["outcome"] == "complete" and h["assignmentRevision"] == row["revision"] and h["evidenceRefs"] and not h["remainingRefs"] and row["spec"]["lane"] == a["child"], "verification_not_current")
        require(child and not child["archived"] and child["execution"] == "idle" and not child["pending"], "verification_child_not_settled")
        verify_artifacts(receipt["artifacts"])
        a["state"] = "complete"
        state["completed"][task] = {"digest": digest(receipt), "assignmentRevision": row["revision"], "handoffRevision": row["handoffRevision"]}
