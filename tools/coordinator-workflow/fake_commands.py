"""Fictional BB/CLI process, invoked only by the offline tests."""
import copy
import hashlib
import json
import os
from pathlib import Path
import sys
import time

if "--warm" in sys.argv[1:]:
    sys.exit(0)
path = Path(os.environ["COORDINATOR_FAKE_WORLD"])
w = json.loads(path.read_text())
kind, args = sys.argv[1], sys.argv[2:]
raw = sys.stdin.buffer.read()
w["events"].append({"kind": kind, "args": args, "stdinDigest": hashlib.sha256(raw).hexdigest(), "stdinLength": len(raw), "caller": os.environ.get("BB_THREAD_ID"), "bbCli": os.environ.get("BB_CLI")})


def save(): path.write_text(json.dumps(w))
def emit(value): save(); print(json.dumps(value)); sys.exit(0)
def option(name): return args[args.index(name) + 1]

def inventory():
    return {"observedAt": "2030-01-01T00:00:00Z", "coverage": "observed_direct_children", "lanes": [{"thread": k, "execution": v["thread"]["status"], "archived": v["thread"]["archivedAt"] is not None} for k, v in w["children"].items()]}

if kind == "bb":
    if args[:2] == ["thread", "show"]:
        emit(w["parent"] if args[2] == w["parent"]["thread"]["id"] else w["children"].get(args[2], {"ok": False, "error": {"code": "thread_not_found"}}))
    if args[:2] == ["thread", "list"]: emit([{"id": k} for k in w["children"]])
    if args[:3] == ["thread", "interactions", "list"]: emit(w.get("interactions", {}).get(args[3], []))
    if args[:2] == ["thread", "spawn"]:
        if w.get("mode") == "slow_spawn": save(); time.sleep(30)
        if w.get("mode") == "blocked_spawn":
            save()
            Path(str(path) + ".started").write_text(str(os.getpid()))
            while not Path(str(path) + ".release").exists(): time.sleep(0.01)
        if w.get("mode") == "oversize_spawn": save(); sys.stdout.write("x" * (2 * 1024 * 1024)); sys.stdout.flush(); time.sleep(30)
        if w.get("mode") == "stderr_spawn": sys.stderr.write("PRIVATE-ERROR" * 200000); sys.stderr.flush()
        child = "thr_example_lane" + str(len(w["children"]) + 1)
        p = copy.deepcopy(w["parent"])
        p["thread"].update(id=child, parentThreadId=option("--parent-thread"), status="active", providerId=option("--provider"), visibility=option("--visibility"), projectId=option("--project"), environmentId=option("--environment"))
        p["environment"].update(id=option("--environment"), hostId=option("--host"))
        if w.get("mode") == "wrong_parent": p["thread"]["parentThreadId"] = "thr_example_other_owner"
        w["children"][child] = p
        if w.get("mode") == "hold_after_submit": w["status"]["control"]["paused"] = True; w["status"]["controlState"] = "paused"
        if w.get("mode") == "lost_spawn": emit({})
        emit({"id": child, "status": "active"})
    # Any tell/reuse/retry/archive/stop would be a test failure, not a harmless fake.
    save(); print(json.dumps({"ok": False, "error": {"code": "forbidden_fake_command"}})); sys.exit(17)

if args[0] == "status":
    if w.get("mode") == "hold_on_final_status":
        n = sum(e["args"][0] == "status" for e in w["events"] if e["kind"] == "keepgoing")
        if n >= 3: w["status"]["control"]["stop"] = True; w["status"]["controlState"] = "stop requested"
    emit(w["status"])
if args[:2] == ["assignment", "show"]: emit(w["record"])
if args[:2] == ["assignment", "inventory"]:
    value = inventory()
    if w.get("mode") == "partial_inventory": value["coverage"] = "partial"
    emit(value)
if args[:2] == ["assignment", "review"]:
    require_revision = int(option("--revision"))
    if require_revision != w["record"]["revisions"][-1]["revision"]: emit({"ok": False, "error": {"code": "stale_revision"}})
    specs = json.loads(Path(option("--file")).read_text())["assignments"]
    old = w["record"]["revisions"][-1]
    current = copy.deepcopy(old); current["revision"] += 1; current["inventory"] = inventory()
    rows = {r["spec"]["id"]: r for r in old["rows"]}
    current["rows"] = [rows[s["id"]] if s["id"] in rows and rows[s["id"]]["spec"] == s else {"spec": s, "revision": 1, "handoffRevision": 0, "acknowledged": 0} for s in specs]
    w["record"]["revisions"].append(current)
    if w.get("mode") == "lost_mapping": save(); sys.exit(19)
    emit({"availability": "available"})
save(); sys.exit(23)
