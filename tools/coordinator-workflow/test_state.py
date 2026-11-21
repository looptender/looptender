"""Behavioral policy/journal tests; no installed BB or provider is called."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parents[2] / "integrations/bb-plugin-keepgoing/skills/keepgoing/scripts"
sys.path.insert(0, str(SCRIPTS))
from coordinator_state import Journal, Refused, decode, digest, next_task, reserve, settle, validate


def task(name, resources=None, parked=0):
    return {"id": name, "scopeDigest": digest(name), "resources": resources or [], "dependsOn": [], "personGate": False, "parkedAt": parked}


def grant():
    return {"id": "example-grant", "context": {"parent": "thr_example_coordinator"}, "revision": 1, "direction": 1, "maxAttempts": 4, "maxChildren": 2, "tasks": [task("docs", ["example-worktree"]), task("test", ["other-worktree"]), task("next", ["example-worktree"])]}


def snapshot():
    return {"recordId": "assignment_example", "rows": [], "children": {}}


class StateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "journal.json"
        self.journal = Journal(self.path)
        self.g = grant()
        self.state = self.journal.load(self.g)

    def own(self, task_index=0):
        a = reserve(self.state, self.g["tasks"][task_index])
        self.journal.save(self.state)
        a["child"], a["state"] = "thr_example_" + a["task"], "mapping_pending"
        self.journal.save(self.state)
        a["state"] = "owned"
        self.journal.save(self.state)
        return a

    def test_unknown_survives_restart_and_new_grant_with_all_reservations(self):
        reserve(self.state, self.g["tasks"][0]); self.journal.save(self.state)
        self.g["revision"] = 2
        self.g["maxAttempts"] = 8
        script = "import json,sys;sys.path.insert(0,sys.argv[1]);from coordinator_state import *;g=json.loads(sys.argv[3]);j=Journal(sys.argv[2]);s=j.load(g);print(json.dumps([s,next_task(g,s,{'rows':[],'children':{}})[1]]))"
        result = subprocess.run([sys.executable, "-c", script, str(SCRIPTS), str(self.path), json.dumps(self.g)], capture_output=True, text=True, timeout=5, check=True)
        state, outcome = json.loads(result.stdout)
        self.assertEqual(state["spent"], 1)
        self.assertEqual(state["attempts"][0]["resources"], ["example-worktree"])
        self.assertEqual(outcome["action"], "reconcile_unknown_attempt")
        self.assertEqual(outcome["independentReady"], ["test"])
        self.assertEqual(self.journal.load(self.g)["attempts"][0]["state"], "unknown")

    def test_capacity_deduplicates_exact_owned_child_and_preserves_resource(self):
        a = self.own()
        snap = snapshot(); snap["children"][a["child"]] = {"execution": "active", "archived": False}
        selected, _ = next_task(self.g, self.state, snap)
        self.assertEqual(selected["id"], "test")  # same child is one slot, not two
        snap["children"]["thr_example_other"] = {"execution": "pending", "archived": False}
        self.assertEqual(next_task(self.g, self.state, snap)[1]["action"], "local_capacity")
        # Unknown creation has no child ID, but cannot evade capacity via restart.
        a["state"] = "mapping_pending"
        self.assertIsNone(next_task(self.g, self.state, snapshot())[0])

    def test_complete_requires_current_verified_artifact_then_dispatches_independent_next(self):
        a = self.own()
        snap = snapshot()
        snap["children"][a["child"]] = {"execution": "idle", "archived": False, "pending": False, "queued": 0}
        snap["rows"] = [{"spec": {"id": "docs", "lane": a["child"]}, "revision": 1, "handoffRevision": 1, "acknowledged": 1, "handoff": {"outcome": "complete", "assignmentRevision": 1, "evidenceRefs": ["example-result"], "remainingRefs": []}}]
        result = Path(self.tmp.name) / "result.txt"
        receipt = {"task": "docs", "recordId": snap["recordId"], "assignmentRevision": 1, "handoffRevision": 1, "accepted": True, "artifacts": [{"path": str(result), "sha256": hashlib.sha256(b"verified example\n").hexdigest()}]}
        def verify(artifacts):
            for item in artifacts:
                if not Path(item["path"]).exists() or hashlib.sha256(Path(item["path"]).read_bytes()).hexdigest() != item["sha256"]:
                    raise Refused("artifact_changed")
        with self.assertRaises(Refused): settle(self.state, snap, [receipt], verify)
        self.assertEqual(self.state["completed"], {})
        result.write_text("verified example\n")
        stale = copy.deepcopy(receipt); stale["handoffRevision"] = 2
        with self.assertRaises(Refused): settle(self.state, snap, [stale], verify)
        snap["children"][a["child"]]["pending"] = True
        with self.assertRaises(Refused): settle(self.state, snap, [receipt], verify)
        snap["children"][a["child"]]["pending"] = False
        settle(self.state, snap, [receipt], verify); self.journal.save(self.state)
        settle(self.state, snap, [receipt], verify)
        self.assertEqual(len(self.state["completed"]), 1)
        # Same resource becomes available only after verification, even if BB was idle earlier.
        self.g["tasks"] = [self.g["tasks"][0], self.g["tasks"][2]]
        selected, _ = next_task(self.g, self.state, snap)
        self.assertEqual(selected["id"], "next")
        reserve(self.state, selected)
        settle(self.state, snap, [receipt], verify)
        self.assertEqual(next_task(self.g, self.state, snap)[1]["action"], "reconcile_unknown_attempt")
        self.assertEqual(len(self.state["completed"]), 1)
        # An external re-review cannot reuse old acceptance or released resources.
        snap["rows"][0]["revision"] = 2
        with self.assertRaisesRegex(Refused, "verified_obligation_changed_reconcile"):
            settle(self.state, snap, [receipt], verify)

    def test_existing_obligation_is_never_implicitly_replaced(self):
        snap = snapshot()
        snap["rows"] = [{"spec": {"id": "docs", "lane": "thr_example_old"}, "handoff": None}]
        selected, outcome = next_task(self.g, self.state, snap)
        self.assertEqual(selected["id"], "test")
        self.assertEqual(outcome["blocked"], [{"assignment": "docs", "action": "reconcile_ownership"}])
        self.g["tasks"] = [self.g["tasks"][0], self.g["tasks"][2]]
        self.assertIsNone(next_task(self.g, self.state, snap)[0])  # old obligation still owns worktree-a

    def test_park_new_owner_and_error_archive_obligations_are_not_dropped(self):
        self.g["tasks"] = [task("docs", parked=1)]
        snap = snapshot()
        for name, archived, execution in [("old-error", False, "error"), ("old-archive", True, "idle")]:
            child = "thr_example_" + name
            snap["children"][child] = {"execution": execution, "archived": archived}
            snap["rows"].append({"spec": {"id": name, "lane": child}, "handoff": None})
        self.assertIsNone(next_task(self.g, self.state, snap)[0])
        self.g["direction"] = 2; self.g["revision"] = 2
        selected, outcome = next_task(self.g, self.state, snap)
        self.assertEqual(selected["id"], "docs")
        self.assertEqual([r["action"] for r in outcome["blocked"]], ["reconcile_execution", "reconcile_ownership"])
        self.assertEqual(len(snap["rows"]), 2)

    def test_budget_and_immutable_unfinished_scope(self):
        self.own(); self.g["maxAttempts"] = 1
        self.assertEqual(next_task(self.g, self.state, snapshot())[1]["action"], "attempt_budget")
        self.g["revision"] = 2; self.g["tasks"][0]["scopeDigest"] = digest("different scope")
        with self.assertRaisesRegex(Refused, "unfinished_scope_changed"): self.journal.load(self.g)
        self.g = grant(); self.g["id"] = "different-owner-grant"
        with self.assertRaisesRegex(Refused, "journal_binding_changed"): self.journal.load(self.g)

    def test_corruption_no_eviction_private_files_and_nonblocking_lock(self):
        self.own()
        raw = self.path.read_bytes()
        changed = copy.deepcopy(self.state); changed["attempts"] = []; changed["spent"] = 0
        with self.assertRaises(Refused): self.journal.save(changed)
        self.assertEqual(raw, self.path.read_bytes())
        self.path.write_bytes(b'{"version":1,"version":1}')
        with self.assertRaises(Refused): self.journal.load(self.g)
        self.assertEqual(self.path.read_bytes(), b'{"version":1,"version":1}')
        self.path.write_bytes(raw); self.path.chmod(0o644)
        with self.assertRaises(Refused): self.journal.load(self.g)
        self.path.chmod(0o600)
        with self.journal.locked():
            with self.assertRaisesRegex(Refused, "workflow_busy"):
                with Journal(self.path).locked(): pass
        self.path.unlink(); self.g["revision"] = 2
        with self.assertRaisesRegex(Refused, "missing_journal_no_reset"): self.journal.load(self.g)


if __name__ == "__main__": unittest.main()
