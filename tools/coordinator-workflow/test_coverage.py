"""Read-only coverage fixtures: no installed BB/provider/runtime is consulted."""
import copy
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from fixture_environment import isolated_environment

SCRIPTS = Path(__file__).resolve().parents[2] / "integrations/bb-plugin-keepgoing/skills/keepgoing/scripts"
sys.path.insert(0, str(SCRIPTS))
import coordinator_coverage as coverage
from coordinator_state import Refused

NOW = datetime(2026, 1, 1, 12, tzinfo=timezone.utc)
AT = "2026-01-01T12:00:00Z"


def fixture(root):
    root = Path(root)
    doc = root / "ROADMAP.md"; doc.write_text("Fictional declared work; checkboxes are not acceptance.\n")
    auth = root / "owner.txt"; auth.write_text("Existing authority for implementation and visibility.\n")
    hashed = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
    source = {"id": "roadmap", "revision": 1, "path": str(doc), "sha256": hashed(doc), "itemIds": ["implementation", "delivery", "visibility"]}
    authority = {"id": "owner-direction", "revision": 1, "path": str(auth), "sha256": hashed(auth)}
    binding = {"loop": "example", "config": "/private/example/looptender.yaml", "runtime": "/private/example/runtime", "coordinator": "thr_example_parent", "owner": "thr_example_parent", "generation": 1, "goal": "goal_example", "goalRevision": 1}
    def item(key):
        return {"id": key, "revision": 1, "workSource": {k: source[k] for k in ("id", "revision", "sha256")}, "authorization": {"state": "authorized", "refs": ["owner-direction"]}, "scopeRefs": ["example:" + key], "dependsOn": [], "personGate": {"state": "none", "refs": []}, "state": "open", "waitRefs": [], "assignment": None, "acceptance": None}
    items = [item(k) for k in source["itemIds"]]
    items[0].update(state="closed", assignment={"id": "implementation", "revision": 1, "handoffRevision": 1}, acceptance={"accepted": True, "itemRevision": 1, "workSourceRevision": 1, "workSourceDigest": source["sha256"], "evidenceRefs": ["example:acceptance-mapping"]})
    items[1].update(state="waiting", waitRefs=["example:pending-ci-on-explicit-delivery"])
    inventory = {"version": 1, "id": "example-scope", "revision": 1, "binding": binding, "assignmentRecord": "assignment_example", "assignmentRevision": 2, "scopeComplete": True, "workSources": [source], "authoritySources": [authority], "items": items}
    context = {"version": 1, "binding": copy.deepcopy(binding), "observedAt": AT, "health": "complete", "coordinatorState": "waiting", "controls": {k: k == "enabled" for k in coverage.CONTROLS}, "objectiveAcceptance": None}
    record = {"version": 1, "id": "assignment_example", "revisions": [{"revision": 2, "binding": copy.deepcopy(binding), "inventory": {"coverage": "observed_direct_children", "observedAt": AT, "lanes": [{"thread": "thr_example_impl", "execution": "idle", "archived": False}]}, "rows": [{"spec": {"id": "implementation", "lane": "thr_example_impl", "scopeRefs": ["example:implementation"], "authorizationRefs": ["owner-direction"]}, "revision": 1, "handoffRevision": 1, "acknowledged": 1, "handoff": {"assignment": "implementation", "assignmentRevision": 1, "outcome": "complete", "remainingRefs": []}}]}]}
    return inventory, context, record


class CoverageTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.i, self.c, self.r = fixture(self.root)

    def run_projection(self):
        return coverage.project(self.i, self.c, self.r, coverage.pin_sources(self.i), NOW)

    def codes(self, report):
        return {f["class"] for f in report["findings"]}

    def test_completed_lanes_and_pending_ci_do_not_hide_undelegated_work(self):
        prior = copy.deepcopy((self.i, self.c, self.r))
        report = self.run_projection()
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.assertFalse(report["waitJustified"])
        self.assertIn("unjustified_coordinator_wait", self.codes(report))
        self.assertEqual(next(r for r in report["items"] if r["id"] == "visibility")["disposition"], "independent_open")
        self.assertIn("suspected_stranded_objective", self.codes(report))
        self.assertFalse(report["objectiveComplete"])
        self.assertEqual(report["assessability"]["objectiveAcceptance"], "cannot_assess")
        self.assertEqual(prior, (self.i, self.c, self.r))
        finding = next(f for f in report["findings"] if f["class"] == "unjustified_coordinator_wait")
        self.assertEqual(finding["evidence"]["workSource"], self.i["items"][2]["workSource"])
        self.assertEqual(finding["autoAction"], "none")
        self.assertEqual(finding["confidence"], "declared_evidence")
        self.assertIn("captures_not_live_authenticated", finding["unknowns"])

    def test_known_work_visible_with_unknown_unrelated_coverage(self):
        self.i["items"][1]["personGate"]["state"] = "unknown"
        report = self.run_projection()
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.assertEqual(report["assessability"]["workAvailability"], "partially_assessable")
        self.assertIn("person_gate_evidence_unavailable", self.codes(report))
        self.assertNotIn("suspected_stranded_objective", self.codes(report))

    def test_missing_row_and_source_are_explicitly_unknown(self):
        self.i["items"].pop()
        report = self.run_projection()
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertIn("scope_item_uncovered", self.codes(report))
        self.assertFalse(report["waitJustified"])
        self.i["workSources"] = []
        report = self.run_projection()
        self.assertIn("work_source_missing", self.codes(report))
        self.assertEqual(next(f for f in report["findings"] if f["class"] == "work_source_missing")["recommendedAction"], "select_and_pin_already_authorized_work_source")

    def test_explicit_empty_is_not_missing_or_completion(self):
        self.i["workSources"][0]["itemIds"] = []
        self.i["items"] = []
        self.r["revisions"][-1]["rows"] = []
        self.r["revisions"][-1]["inventory"]["lanes"] = []
        report = self.run_projection()
        self.assertEqual(report["verdict"], "completion_review_required")
        self.assertEqual(report["assessability"]["workAvailability"], "assessable_declared_scope")
        self.assertFalse(report["objectiveComplete"])
        self.assertNotIn("suspected_stranded_objective", self.codes(report))

    def test_dependency_and_person_gate_are_local(self):
        self.i["items"][2]["dependsOn"] = [{"id": "delivery", "revision": 1}]
        report = self.run_projection()
        self.assertEqual(report["verdict"], "declared_wait_supported")
        self.assertEqual(coverage.exit_code(report), 0)
        self.i["items"][2]["dependsOn"] = []
        self.i["items"][2]["personGate"] = {"state": "held", "refs": ["example:merge-only-gate"]}
        self.assertEqual(self.run_projection()["verdict"], "declared_wait_supported")
        # A gate on delivery never creates one on visibility.
        self.i["items"][1]["personGate"] = self.i["items"][2]["personGate"]
        self.i["items"][2]["personGate"] = {"state": "none", "refs": []}
        self.assertEqual(self.run_projection()["verdict"], "continue_authorized_work")

    def test_cycle_missing_dependency_and_revision_drift(self):
        for kind in ("cycle", "missing", "stale"):
            with self.subTest(kind=kind):
                self.i, self.c, self.r = fixture(self.root)
                self.i["items"][2]["dependsOn"] = [{"id": "delivery" if kind != "missing" else "absent", "revision": 2 if kind == "stale" else 1}]
                if kind == "cycle":
                    self.i["items"][1]["dependsOn"] = [{"id": "visibility", "revision": 1}]
                report = self.run_projection()
                self.assertEqual(report["verdict"], "cannot_assess")
                self.assertIn("dependency_cycle" if kind == "cycle" else "dependency_evidence_unavailable", self.codes(report))

    def test_holds_and_stale_bindings_win(self):
        for field in ("stop", "paused", "awaitingUser", "completed", "superseded", "pendingTransfer"):
            with self.subTest(field=field):
                self.c["controls"][field] = True
                report = self.run_projection()
                self.assertEqual(report["verdict"], "runtime_held")
                self.assertNotIn("unjustified_coordinator_wait", self.codes(report))
                self.assertEqual(next(r for r in report["items"] if r["id"] == "visibility")["disposition"], "independent_open")
                self.c["controls"][field] = False
        for mutate in (lambda: self.c["binding"].update(goalRevision=2), lambda: self.c.update(observedAt="2025-01-01T00:00:00Z"), lambda: self.c.update(health="partial")):
            self.i, self.c, self.r = fixture(self.root); mutate()
            report = self.run_projection(); self.assertEqual(report["verdict"], "cannot_assess")
            self.assertNotIn("suspected_stranded_objective", self.codes(report))

    def test_acceptance_is_explicit_and_revision_bound(self):
        self.i["items"][0]["acceptance"] = None
        report = self.run_projection()
        self.assertIn("item_acceptance_unavailable", self.codes(report))
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.i["items"][2]["state"] = "closed"  # commit/green CI alone is no mapping
        report = self.run_projection()
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertFalse(report["objectiveComplete"])
        self.i, self.c, self.r = fixture(self.root)
        self.i["items"][0]["acceptance"]["workSourceRevision"] = 2
        self.assertIn("item_acceptance_unavailable", self.codes(self.run_projection()))

    def test_claim_missing_source_drift_and_no_reapproval_inference(self):
        self.i["items"][0]["assignment"] = None
        self.assertIn("missing_claimed_item", self.codes(self.run_projection()))
        self.i, self.c, self.r = fixture(self.root)
        Path(self.i["workSources"][0]["path"]).write_text("An ordinary roadmap progress edit.\n")
        report = self.run_projection()
        self.assertIn("source_drift", self.codes(report))
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertEqual(self.i["items"][2]["authorization"]["state"], "authorized")
        self.assertNotIn("request_approval", json.dumps(report))

    def test_assignment_revision_and_partial_snapshot_unknown(self):
        for field, value in (("revision", 3), ("inventory", {"coverage": "partial", "observedAt": AT, "lanes": []})):
            self.i, self.c, self.r = fixture(self.root)
            self.r["revisions"][-1][field] = value
            report = self.run_projection()
            self.assertIn("assignment_inventory_unavailable", self.codes(report))
            self.assertFalse(report["waitJustified"])
            self.assertIn("unjustified_coordinator_wait", self.codes(report))

    def test_dense_dependency_graph_is_bounded(self):
        template = copy.deepcopy(self.i["items"][2])
        self.i["items"] = []
        for n in range(32):
            row = copy.deepcopy(template); row["id"] = "task%d" % n
            row["dependsOn"] = [{"id": "task%d" % d, "revision": 1} for d in range(n)]
            self.i["items"].append(row)
        self.i["workSources"][0]["itemIds"] = [r["id"] for r in self.i["items"]]
        self.r["revisions"][-1]["rows"] = []
        report = self.run_projection()
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.assertEqual(len(report["items"]), 32)

    def test_unaccepted_dependency_never_unlocks_a_dependent(self):
        self.i["items"][0]["acceptance"] = None
        self.i["items"][2]["dependsOn"] = [{"id": "implementation", "revision": 1}]
        report = self.run_projection()
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertIn("dependency_acceptance_unavailable", self.codes(report))
        self.assertNotIn("unjustified_coordinator_wait", self.codes(report))
        self.i, self.c, self.r = fixture(self.root)
        self.i["items"][1]["dependsOn"] = None
        report = self.run_projection()
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.assertIn("dependency_evidence_unavailable", self.codes(report))

    def test_missing_lane_inventory_is_not_empty_and_never_certifies_wait(self):
        self.i["items"][2]["dependsOn"] = [{"id": "delivery", "revision": 1}]
        self.r["revisions"][-1]["inventory"].pop("lanes")
        report = self.run_projection()
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertFalse(report["waitJustified"])
        self.assertNotIn("suspected_stranded_objective", self.codes(report))

    def test_declared_authority_unknown_and_scope_expansion_do_not_grant_work(self):
        for state in ("unknown", "scope_expansion"):
            self.i["items"][2]["authorization"]["state"] = state
            report = self.run_projection()
            self.assertEqual(report["verdict"], "cannot_assess")
            self.assertFalse(report["waitJustified"])
            self.assertNotIn("unjustified_coordinator_wait", self.codes(report))

    def test_objective_acceptance_is_not_derived_from_assignment_or_ci(self):
        self.c["objectiveAcceptance"] = {"goal": "goal_example", "goalRevision": 1, "scopeRevision": 1, "inventoryDigest": coverage.digest(self.i), "accepted": True, "evidenceRefs": ["example:explicit-objective-mapping"]}
        report = self.run_projection()
        self.assertEqual(report["assessability"]["objectiveAcceptance"], "cannot_assess")  # open work contradicts acceptance
        for row in self.i["items"]:
            row["state"] = "closed"
            row["acceptance"] = {"accepted": True, "itemRevision": row["revision"], "workSourceRevision": 1, "workSourceDigest": row["workSource"]["sha256"], "evidenceRefs": ["example:explicit-item-mapping"]}
        self.c["objectiveAcceptance"]["inventoryDigest"] = coverage.digest(self.i)
        report = self.run_projection()
        self.assertEqual(report["assessability"]["objectiveAcceptance"], "mapped_declaration_only")
        self.assertFalse(report["objectiveComplete"])
        self.i["revision"] = 2
        report = self.run_projection()
        self.assertEqual(report["assessability"]["objectiveAcceptance"], "cannot_assess")

    def write_inputs(self):
        self.c["observedAt"] = datetime.now(timezone.utc).isoformat()
        self.r["revisions"][-1]["inventory"]["observedAt"] = self.c["observedAt"]
        paths = [self.root / n for n in ("inventory.json", "context.json", "assignments.json")]
        for path, value in zip(paths, (self.i, self.c, self.r)):
            path.write_text(json.dumps(value)); path.chmod(0o600)
        return paths

    def test_usable_command_no_external_commands_or_mutations_and_privacy(self):
        paths = self.write_inputs()
        prior = {p: p.read_bytes() for p in self.root.iterdir() if p.is_file()}
        args = [sys.executable, "-B", str(SCRIPTS / "coordinator_coverage.py"), "--inventory", str(paths[0]), "--context", str(paths[1]), "--assignments", str(paths[2])]
        env = isolated_environment(self.root / "isolated")
        before_files = {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        # PATH has no BB/gh/provider. The command has no such subprocess path.
        p = subprocess.run(args, capture_output=True, text=True, env=env, timeout=5)
        self.assertEqual(p.returncode, 1, p.stderr)
        report = json.loads(p.stdout)
        self.assertEqual(before_files, {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob("*") if p.is_file()})
        self.assertEqual(report["verdict"], "continue_authorized_work")
        self.assertNotIn("thr_example", p.stdout)
        self.assertNotIn(str(self.root), p.stdout)
        self.assertNotIn("visibility", p.stdout)
        self.assertEqual(prior, {p: p.read_bytes() for p in prior})
        previous = self.root / "receipt.json"; previous.write_text(p.stdout); previous.chmod(0o600)
        p = subprocess.run(args + ["--previous", str(previous), "--private"], capture_output=True, text=True, env=env, timeout=5)
        self.assertEqual(p.returncode, 1)
        self.assertTrue(json.loads(p.stdout)["unchanged"])
        self.assertIn("visibility", p.stdout)
        self.assertEqual(prior, {p: p.read_bytes() for p in prior})
        original_open = os.open
        def readonly_open(path, flags, *a, **kw):
            self.assertEqual(flags & (os.O_CREAT | os.O_WRONLY | os.O_RDWR | os.O_TRUNC | os.O_APPEND), 0)
            return original_open(path, flags, *a, **kw)
        with patch("os.open", side_effect=readonly_open), patch("subprocess.Popen", side_effect=AssertionError("no commands")), patch("coordinator.Journal.save", side_effect=AssertionError("no journal writes")), patch("coordinator.Commands.spawn", side_effect=AssertionError("no spawn")):
            report = coverage.inspect(paths, datetime.now(timezone.utc))
            self.assertEqual(report["verdict"], "continue_authorized_work")

    def test_strict_inputs_and_read_races(self):
        paths = self.write_inputs()
        with patch.object(coverage, "private_read", wraps=coverage.private_read) as read:
            original = coverage.pin_sources
            calls = [0]
            def changed(i):
                value = original(i); calls[0] += 1
                if calls[0] == 2:
                    value[("workSources", "roadmap")]["observedDigest"] = "0" * 64
                return value
            with patch.object(coverage, "pin_sources", side_effect=changed):
                with self.assertRaisesRegex(Refused, "evidence_changed_during_read"):
                    coverage.inspect(paths, datetime.now(timezone.utc))
            self.assertEqual(read.call_count, 6)
        paths[0].write_text('{"version":1,"version":1}')
        with self.assertRaisesRegex(Refused, "duplicate_field"):
            coverage.inspect(paths, datetime.now(timezone.utc))
        paths = self.write_inputs(); paths[0].chmod(0o644)
        with self.assertRaisesRegex(Refused, "private_regular_file_required"):
            coverage.inspect(paths, datetime.now(timezone.utc))
        paths[0].chmod(0o600)
        link = self.root / "link"; link.symlink_to(paths[0])
        with self.assertRaises(OSError):
            coverage.inspect([link, *paths[1:]], datetime.now(timezone.utc))
    @unittest.skipUnless(hasattr(os, "mkfifo") and hasattr(os, "O_NONBLOCK"), "Unix descriptor boundary")
    def test_fifo_inputs_and_sources_refuse_before_blocking(self):
        paths = self.write_inputs()
        entry = [sys.executable, "-B", str(SCRIPTS / "coordinator_coverage.py")]
        args = ["--inventory", str(paths[0]), "--context", str(paths[1]), "--assignments", str(paths[2])]
        # Run the actual CLI in an owned child with no FIFO writer. The race
        # variant swaps the path immediately before acquisition, defeating any
        # pre-open path-type check while still exercising descriptor validation.
        race = """import os,runpy,sys
sys.dont_write_bytecode = True
script, target = sys.argv[1:3]
sys.argv = [script] + sys.argv[3:]
original = os.open
def replacing_open(path, flags, *a, **kw):
    if str(path) == target and not replacing_open.changed:
        replacing_open.changed = True
        os.unlink(target)
        os.mkfifo(target, 0o600)
    return original(path, flags, *a, **kw)
replacing_open.changed = False
os.open = replacing_open
sys.path.insert(0, os.path.dirname(script))
runpy.run_path(script, run_name="__main__")
"""
        for target in (paths[1], Path(self.i["workSources"][0]["path"])):
            for replace in (False, True):
                with self.subTest(kind="private" if target == paths[1] else "source", replacement=replace):
                    original = target.read_bytes()
                    try:
                        if replace:
                            cmd = [sys.executable, "-B", "-c", race, entry[-1], str(target), *args]
                        else:
                            target.unlink(); os.mkfifo(target, 0o600)
                            cmd = entry + args
                        result = subprocess.run(cmd, capture_output=True, text=True, timeout=3)
                        self.assertEqual(result.returncode, 1, result.stderr)
                        report = json.loads(result.stdout)
                        self.assertEqual(report["verdict"], "cannot_assess")
                        self.assertFalse(report["objectiveComplete"])
                        self.assertNotIn(str(target), result.stdout)
                    finally:
                        target.unlink(); target.write_bytes(original); target.chmod(0o600)

    def test_descriptor_readers_preserve_regular_symlink_and_size_guards(self):
        path = self.root / "private.json"; raw = b'{"fictional":true}'
        path.write_bytes(raw); path.chmod(0o600)
        self.assertEqual(coverage.private_read(path), raw)
        self.assertEqual(coverage.workflow.file_hash(path, collect=True), (hashlib.sha256(raw).hexdigest(), raw))
        for reader in (coverage.private_read, coverage.workflow.file_hash):
            with self.subTest(reader=reader.__name__):
                with self.assertRaisesRegex(Refused, "absolute_path_required"):
                    reader("relative.json")
                with self.assertRaises((OSError, Refused)):
                    reader(self.root)
                link = self.root / (reader.__name__ + "-link"); link.symlink_to(path)
                with self.assertRaises(OSError):
                    reader(link)
                with self.assertRaisesRegex(Refused, "input_bound|source_bound"):
                    reader(path, 1)
        path.chmod(0o644)
        with self.assertRaisesRegex(Refused, "private_regular_file_required"):
            coverage.private_read(path)

    def test_contradictory_handoff_cannot_supply_acceptance(self):
        self.r["revisions"][-1]["rows"][0]["handoff"]["remainingRefs"] = ["example:unfinished"]
        self.i["items"][2]["dependsOn"] = [{"id": "implementation", "revision": 1}]
        report = self.run_projection()
        self.assertIn("assignment_handoff_unavailable", self.codes(report))
        self.assertEqual(report["verdict"], "cannot_assess")
        self.assertFalse(report["waitJustified"])

    def test_command_rejects_extra_mutation_flags_and_missing_inputs(self):
        paths = self.write_inputs()
        entry = [sys.executable, "-B", str(SCRIPTS / "coordinator_coverage.py")]
        args = ["--inventory", str(paths[0]), "--context", str(paths[1]), "--assignments", str(paths[2])]
        env = isolated_environment(self.root / "isolated")
        for flag in ("--execute", "--child", "--reconcile-intent"):
            p = subprocess.run(entry + args + [flag], capture_output=True, text=True, env=env, timeout=5)
            self.assertEqual(p.returncode, 2)
        paths[1].write_text(json.dumps({"version": 1}))
        p = subprocess.run(entry + args, capture_output=True, text=True, env=env, timeout=5)
        r = json.loads(p.stdout); self.assertEqual(r["verdict"], "cannot_assess")
        self.assertIn("context_schema", r["missingInputs"])
        paths[0].write_bytes(b" " * (coverage.LIMIT + 1))
        p = subprocess.run(entry + args, capture_output=True, text=True, env=env, timeout=5)
        self.assertIn("input_bound", json.loads(p.stdout)["missingInputs"])



if __name__ == "__main__":
    unittest.main()
