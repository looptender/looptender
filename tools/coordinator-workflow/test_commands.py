"""Run the packaged consumer with fake executables through its real adapter."""
import copy
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
from fixture_environment import isolated_environment

SCRIPTS = Path(__file__).resolve().parents[2] / "integrations/bb-plugin-keepgoing/skills/keepgoing/scripts"
ENTRY = SCRIPTS / "coordinator.py"
FAKE = Path(__file__).with_name("fake_commands.py")


class CommandTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir="/tmp"); self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name).resolve(); self.directory.chmod(0o700)
        self.world_file = self.directory / "world.json"
        self.journal = self.directory / "journal.json"
        self.grant_file = self.directory / "grant.json"
        self.env = isolated_environment(self.directory / "environment")
        self.env.update(BB_THREAD_ID="thr_example_coordinator", COORDINATOR_FAKE_WORLD=str(self.world_file))
        self.executables = {}
        for name in ("bb", "keepgoing"):
            p = self.directory / name
            p.write_text("#!/bin/sh\nexec " + shlex.quote(sys.executable) + " -B " + shlex.quote(str(FAKE)) + " " + name + ' "$@"\n')
            p.chmod(0o700); self.executables[name] = {"path": str(p), "sha256": self.hash(p)}
            # Warm the host's script admission outside the adapter's timeout.
        self.env["BB_CLI"] = self.executables["bb"]["path"]
        runtime = self.directory / "runtime"; runtime.mkdir(mode=0o700)
        config = self.directory / "loop.yaml"; config.write_text("fictional configuration\n")
        owner = self.directory / "owner.txt"; owner.write_text("Owner permits the fictional docs and tests deliverables, visible children and the stated local bounds.\n")
        self.binding = {"loop": "example-loop", "config": str(config), "runtime": str(runtime), "coordinator": self.env["BB_THREAD_ID"], "owner": self.env["BB_THREAD_ID"], "generation": 1, "goal": "goal_example", "goalRevision": 1}
        parent = {"thread": {"id": self.env["BB_THREAD_ID"], "parentThreadId": None, "projectId": "proj_example", "environmentId": "env_example", "providerId": "example-provider", "visibility": "visible", "status": "idle", "queuedMessageCount": 0, "archivedAt": None, "deletedAt": None}, "environment": {"id": "env_example", "hostId": "host_example"}}
        self.world = {"parent": parent, "children": {}, "events": [], "interactions": {}, "record": {"version": 1, "id": "assignment_example", "revisions": [{"revision": 1, "binding": self.binding, "inventory": {"coverage": "observed_direct_children", "lanes": []}, "rows": []}]}, "status": {"loop": "example-loop", "target": "primary", "targetConfigured": True, "targetAgent": {"bbThread": self.env["BB_THREAD_ID"]}, "controlState": "enabled", "control": {"enabled": True, "paused": False, "stop": False, "awaitingUser": False, "ownership": {"threadId": self.env["BB_THREAD_ID"], "generation": 1}}, "goal": {"id": "goal_example", "revision": 1}, "assignments": {"availability": "available"}, "deadlineReached": False}}
        sources = [{"ref": "config", "path": str(config), "sha256": self.hash(config)}, {"ref": "owner", "path": str(owner), "sha256": self.hash(owner)}]
        tasks = []
        for name, resource in (("docs", "worktree-a"), ("tests", "worktree-b"), ("next", "worktree-a")):
            p = self.directory / (name + ".txt"); p.write_text("PRIVATE fictional deliverable " + name)
            sources.append({"ref": name, "path": str(p), "sha256": self.hash(p)})
            tasks.append({"id": name, "scopeRefs": [name], "authorizationRefs": ["owner"], "briefRef": name, "resources": [resource], "dependsOn": [], "personGate": False, "parkedAt": 0})
        self.grant = {"version": 1, "id": "example-grant", "revision": 1, "direction": 1, "instructionRef": "owner", "binding": self.binding, "recordId": "assignment_example", "project": "proj_example", "environment": "env_example", "host": "host_example", "provider": "example-provider", "model": "example-model", "effort": "medium", "bb": self.executables["bb"], "effectiveBB": self.executables["bb"], "keepgoing": self.executables["keepgoing"], "sources": sources, "maxAttempts": 4, "maxChildren": 2, "tasks": tasks}
        self.save_world(); self.save_grant()
        for name in self.executables:
            # A real warm invocation, never an installed BB binary.
            subprocess.run([self.executables[name]["path"], "--warm"], env=self.env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        self.world = self.read_world(); self.world["events"] = []; self.save_world()

    @staticmethod
    def hash(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()
    def save_world(self): self.world_file.write_text(json.dumps(self.world))
    def read_world(self): return json.loads(self.world_file.read_text())
    def save_grant(self): self.grant_file.write_text(json.dumps(self.grant)); self.grant_file.chmod(0o600)
    def read_journal(self): return json.loads(self.journal.read_text())
    def revision(self):
        if hasattr(self, "real_cli"):
            return self.actual_record()["revisions"][-1]["revision"]
        return self.read_world()["record"]["revisions"][-1]["revision"]
    def actual(self, *args):
        p = subprocess.run([self.real_cli, *args, "-c", self.binding["config"], "--json"], env=self.env, capture_output=True, text=True, timeout=15)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return json.loads(p.stdout) if p.stdout.lstrip().startswith(("{", "[")) else None
    def actual_record(self): return self.actual("assignment", "show", "--private")
    def process(self, execute=True, extra=(), timeout=25):
        args = [sys.executable, "-B", str(ENTRY), "--grant", str(self.grant_file), "--journal", str(self.journal), "--assignment-revision", str(self.revision()), "--private"]
        if execute: args.append("--execute")
        return subprocess.run(args + list(extra), env=self.env, capture_output=True, text=True, timeout=timeout)
    def run_flow(self, **kw):
        p = self.process(**kw)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn("PRIVATE", p.stdout + p.stderr)
        return json.loads(p.stdout)
    def spawn_events(self): return [e for e in self.read_world()["events"] if e["args"][:2] == ["thread", "spawn"]]

    def finish(self, task):
        self.world = self.read_world()
        x = copy.deepcopy(self.world["record"]["revisions"][-1]); x["revision"] += 1
        row = next(r for r in x["rows"] if r["spec"]["id"] == task)
        row["handoffRevision"] = row["acknowledged"] = 1
        row["handoff"] = {"outcome": "complete", "assignmentRevision": row["revision"], "evidenceRefs": [task + "-artifact"], "remainingRefs": []}
        child = row["spec"]["lane"]; self.world["children"][child]["thread"]["status"] = "idle"
        self.world["record"]["revisions"].append(x); self.save_world()
        artifact = self.directory / (task + "-artifact.txt"); artifact.write_text("Verified fictional " + task + "\n")
        # The fictional coordinator checks a real artifact before attesting.
        self.assertEqual(artifact.read_text(), "Verified fictional " + task + "\n")
        receipt = {"task": task, "recordId": self.grant["recordId"], "assignmentRevision": row["revision"], "handoffRevision": 1, "accepted": True, "artifacts": [{"path": str(artifact), "sha256": self.hash(artifact)}]}
        path = self.directory / "verification.json"; path.write_text(json.dumps([receipt])); path.chmod(0o600)
        return path

    def test_dispatch_settle_next_and_duplicate_receipt_actual_command_artifacts(self):
        dry = self.run_flow(execute=False)
        self.assertEqual(dry["action"], "dispatch_new_child"); self.assertFalse(self.journal.exists()); self.assertEqual(self.spawn_events(), [])
        first = self.run_flow(); self.assertEqual(first["action"], "dispatched_new_child")
        self.run_flow()
        self.assertEqual(len(self.spawn_events()), 2)
        self.assertEqual(self.run_flow()["action"], "local_capacity")
        receipt = self.finish("docs")
        out = self.run_flow(extra=("--verification", str(receipt)))
        self.assertEqual(out["action"], "dispatched_new_child"); self.assertEqual(out["completed"], 1)
        self.assertEqual(len(self.spawn_events()), 3)
        self.run_flow(extra=("--verification", str(receipt)))
        self.assertEqual(len(self.spawn_events()), 3); self.assertEqual(len(self.read_journal()["completed"]), 1)
        rows = self.read_world()["record"]["revisions"][-1]["rows"]
        self.assertEqual({r["spec"]["id"] for r in rows}, {"docs", "tests", "next"})
        self.assertEqual(len({r["spec"]["lane"] for r in rows}), 3)
        for event in self.spawn_events():
            args = event["args"]
            self.assertIn("--visibility", args); self.assertEqual(args[args.index("--visibility") + 1], "visible")
            self.assertEqual(args[args.index("--parent-thread") + 1], self.env["BB_THREAD_ID"])
            self.assertEqual(event["caller"], self.env["BB_THREAD_ID"]); self.assertEqual(event["bbCli"], self.env["BB_CLI"])
            self.assertNotIn("--permission-mode", args); self.assertGreater(event["stdinLength"], 0)
        self.assertFalse(any(e["args"][:2] in (["thread", "tell"], ["thread", "retry"], ["thread", "archive"]) for e in self.read_world()["events"]))

    def test_existing_environment_selects_host_and_rejects_conflicting_selectors(self):
        sys.path.insert(0, str(SCRIPTS)); from coordinator import Commands
        from coordinator_state import Refused
        self.assertEqual(self.run_flow()["action"], "dispatched_new_child")
        event = self.spawn_events()[0]
        args = event["args"]
        self.assertEqual(args[args.index("--environment") + 1], "env_example")
        self.assertNotIn("--host", args); self.assertNotIn("--machine", args)
        child = next(iter(self.read_world()["children"].values()))
        self.assertEqual(child["environment"]["hostId"], self.grant["host"])
        before = self.world_file.read_bytes()
        for alias in ("--host", "--machine"):
            with self.subTest(alias=alias), patch.dict(os.environ, self.env, clear=True):
                with self.assertRaisesRegex(Refused, "command_failed"):
                    Commands(self.grant).call("bb", args + [alias, self.grant["host"]])
            self.assertEqual(self.world_file.read_bytes(), before)
        # Omission of a redundant selector must not weaken returned-host checks.
        self.world = self.read_world(); self.world["mode"] = "wrong_host"; self.save_world()
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        self.assertEqual(self.read_journal()["attempts"][-1]["state"], "unknown")
        self.assertEqual(self.grant["host"], "host_example")
        self.assertEqual(len(self.read_world()["children"]), 2)
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        self.assertEqual(len(self.read_world()["children"]), 2)

    def test_lost_spawn_new_process_new_grant_and_explicit_reconciliation(self):
        self.world["mode"] = "lost_spawn"; self.save_world()
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        a = self.read_journal()["attempts"][0]
        self.grant["revision"] = 2; self.grant["maxAttempts"] = 8; self.save_grant()
        # A new process cannot bypass the reserved unknown creation.
        p = self.process(); self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(json.loads(p.stdout)["action"], "reconcile_unknown_attempt")
        self.assertEqual(len(self.spawn_events()), 1)
        self.assertEqual(self.read_journal()["spent"], 1)
        self.world = self.read_world(); self.world["mode"] = "normal"; self.save_world()
        child = next(iter(self.world["children"]))
        out = self.run_flow(extra=("--reconcile-intent", a["id"], "--child", child))
        self.assertEqual(out["action"], "reconciled_creation")
        self.assertEqual(self.read_journal()["attempts"][0]["state"], "owned"); self.assertEqual(len(self.spawn_events()), 1)
        self.run_flow(); self.assertEqual(len(self.spawn_events()), 2)

    def test_mapping_lost_reply_repairs_exact_row_without_second_publication(self):
        self.world["mode"] = "lost_mapping"; self.save_world()
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        a = self.read_journal()["attempts"][0]; self.assertEqual(a["state"], "mapping_pending")
        before = sum(e["args"][:2] == ["assignment", "review"] for e in self.read_world()["events"])
        self.world = self.read_world(); self.world["mode"] = "normal"; self.save_world()
        self.run_flow(extra=("--reconcile-intent", a["id"], "--child", a["child"]))
        after = sum(e["args"][:2] == ["assignment", "review"] for e in self.read_world()["events"])
        self.assertEqual(before, after); self.assertEqual(len(self.spawn_events()), 1)

    def test_final_hold_and_partial_inventory_prevent_submission_but_submitted_race_is_unknown(self):
        for mode in ("hold_on_final_status", "partial_inventory"):
            self.world = self.read_world(); self.world["mode"] = mode; self.world["events"] = []; self.save_world()
            p = self.process(); self.assertNotEqual(p.returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world = self.read_world(); self.world["mode"] = "hold_after_submit"; self.world["status"]["control"].update(stop=False, paused=False); self.world["status"]["controlState"] = "enabled"; self.save_world()
        out = self.run_flow(); self.assertEqual(out["action"], "reconcile_unknown_attempt")
        self.assertEqual(len(self.spawn_events()), 1)  # demonstrate, do NOT claim atomic no-create-after-hold
        self.assertTrue(self.read_world()["status"]["control"]["paused"])
        self.assertEqual(self.read_journal()["spent"], 1)
        self.process(); self.assertEqual(len(self.spawn_events()), 1)

    def test_binding_drift_pending_input_and_source_drift_do_not_create(self):
        original = copy.deepcopy(self.world)
        for mutate in (lambda w: w["status"]["control"]["ownership"].update(generation=2), lambda w: w["status"]["goal"].update(revision=2)):
            self.world = copy.deepcopy(original); mutate(self.world); self.save_world()
            self.assertNotEqual(self.process().returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world = original; self.save_world()
        Path(self.grant["sources"][1]["path"]).write_text("different instruction")
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(self.spawn_events(), [])

    def test_concurrent_advance_and_stream_cap_have_no_duplicate_dispatch(self):
        # Hold the same real journal lock in another process.
        sys.path.insert(0, str(SCRIPTS)); from coordinator_state import Journal
        with Journal(self.journal).locked():
            p = self.process(); self.assertNotEqual(p.returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world["mode"] = "oversize_spawn"; self.save_world()
        start = time.monotonic(); out = self.run_flow()
        self.assertLess(time.monotonic() - start, 8); self.assertEqual(out["action"], "reconcile_unknown_attempt")
        self.assertEqual(self.read_journal()["spent"], 1)
        self.assertEqual(len(self.spawn_events()), 1)

    def test_two_concurrent_advances_submit_exactly_one_child(self):
        self.world["mode"] = "blocked_spawn"; self.save_world()
        args = [sys.executable, "-B", str(ENTRY), "--grant", str(self.grant_file), "--journal", str(self.journal), "--assignment-revision", "1", "--execute"]
        first = subprocess.Popen(args, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            end = time.monotonic() + 8
            while not Path(str(self.world_file) + ".started").exists() and time.monotonic() < end:
                time.sleep(0.01)
            self.assertTrue(Path(str(self.world_file) + ".started").exists())
            second = self.process()
            self.assertNotEqual(second.returncode, 0)
            self.assertEqual(json.loads(second.stdout)["reason"], "workflow_busy")
            Path(str(self.world_file) + ".release").touch()
            stdout, stderr = first.communicate(timeout=15)
            self.assertEqual(first.returncode, 0, stdout + stderr)
            self.assertEqual(json.loads(stdout)["action"], "dispatched_new_child")
            self.assertEqual(len(self.spawn_events()), 1)
            self.assertEqual(self.read_journal()["spent"], 1)
        finally:
            if first.poll() is None: first.kill(); first.communicate(timeout=3)

    def test_pending_input_parent_drift_and_untrusted_child_refuse_without_replay(self):
        self.world["parent"]["environment"]["hostId"] = "host_example_other"; self.save_world()
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world["parent"]["environment"]["hostId"] = "host_example"
        self.world["parent"]["thread"]["queuedMessageCount"] = 1; self.save_world()
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world["parent"]["thread"]["queuedMessageCount"] = 0
        self.world["interactions"][self.env["BB_THREAD_ID"]] = [{"threadId": self.env["BB_THREAD_ID"], "status": "pending"}]; self.save_world()
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(self.spawn_events(), [])
        self.world["interactions"] = {}; self.save_world()
        self.run_flow()
        original = self.read_world(); child = next(iter(original["children"]))
        for field, value in (("queuedMessageCount", 1),):
            self.world = copy.deepcopy(original); self.world["children"][child]["thread"][field] = value; self.save_world()
            # A queued unfinished child remains reserved; no completion is inferred.
            out = self.run_flow(execute=False); self.assertEqual(out["completed"], 0)
        self.world = copy.deepcopy(original)
        self.world["record"]["revisions"][-1]["rows"][0]["spec"]["scopeRefs"] = ["different-scope"]; self.save_world()
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(len(self.spawn_events()), 1)
        # Test typed pending input with an independent task still available.
        self.world = copy.deepcopy(original)
        self.world["interactions"][child] = [{"threadId": child, "status": "pending"}]; self.save_world()
        before = len(self.spawn_events())
        self.assertNotEqual(self.process().returncode, 0); self.assertEqual(len(self.spawn_events()), before)
        self.world = copy.deepcopy(original); self.world["mode"] = "wrong_parent"; self.save_world()
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        before = len(self.spawn_events())
        self.assertEqual(self.run_flow()["action"], "reconcile_unknown_attempt")
        self.assertEqual(len(self.spawn_events()), before)

    def test_real_adapter_timeout_cleanup_and_stderr_discard(self):
        sys.path.insert(0, str(SCRIPTS)); from coordinator import Commands
        from coordinator_state import Refused
        self.world["mode"] = "slow_spawn"; self.save_world()
        commands = Commands(self.grant)
        started = time.monotonic()
        with patch.dict(os.environ, self.env, clear=True):
            with self.assertRaisesRegex(Refused, "command_timeout"):
                commands.call("bb", ["thread", "spawn"], timeout=0.2)
        self.assertLess(time.monotonic() - started, 3)
        # Huge private stderr never blocks or enters returned stdout.
        self.world = self.read_world(); self.world["mode"] = "stderr_spawn"; self.save_world()
        out = self.run_flow()
        self.assertEqual(out["action"], "dispatched_new_child")
        self.assertEqual(len(self.spawn_events()), 2)  # one timed-out local request plus one explicitly separate fixture request

    @unittest.skipUnless(os.environ.get("KEEPGOING_TEST_BINARY"), "actual CLI fixture requires the explicitly built test binary")
    def test_actual_cli_assignment_writer_and_saved_controls(self):
        self.real_cli = str(Path(os.environ["KEEPGOING_TEST_BINARY"]).resolve())
        config = Path(self.binding["config"])
        config.write_text("version: 1\nloop:\n  name: example-loop\n  state_dir: runtime\n  adapter: bb\n  provider: codex\n  group: example-account\n  bb: {thread: thr_example_coordinator, cli: " + json.dumps(self.executables["bb"]["path"]) + "}\n  prompt: Continue the fictional task.\n  assignment_lifecycle: true\n")
        config.chmod(0o600)
        self.world["parent"]["thread"]["providerId"] = "codex"; self.save_world()
        self.actual("enable")
        goal = self.directory / "goal.json"; goal.write_text(json.dumps({"title": "Fictional objective", "objectiveRefs": ["example-objective"], "acceptanceRefs": ["example-acceptance"]})); goal.chmod(0o600)
        self.actual("goal", "create", "--file", str(goal))
        plan = self.directory / "initial-plan.json"; plan.write_text('{"assignments":[]}'); plan.chmod(0o600)
        self.actual("assignment", "review", "--file", str(plan))
        r = self.actual_record(); self.binding = r["revisions"][-1]["binding"]
        self.grant.update(binding=self.binding, recordId=r["id"], provider="codex", keepgoing={"path": self.real_cli, "sha256": self.hash(self.real_cli)})
        self.grant["sources"][0]["sha256"] = self.hash(config); self.save_grant()
        controls = Path(self.binding["runtime"]) / "control.json"; before = controls.read_bytes()
        self.assertEqual(self.run_flow()["action"], "dispatched_new_child")
        self.assertEqual(self.run_flow()["action"], "dispatched_new_child")
        r = self.actual_record(); row = next(x for x in r["revisions"][-1]["rows"] if x["spec"]["id"] == "docs")
        self.world = self.read_world(); self.world["children"][row["spec"]["lane"]]["thread"]["status"] = "idle"; self.save_world()
        from datetime import datetime, timezone
        handoff = self.directory / "handoff.json"
        handoff.write_text(json.dumps({"assignment": "docs", "assignmentRevision": 1, "declaredBy": row["spec"]["lane"], "declaredAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "outcome": "complete", "availability": "available", "blocker": "none", "evidenceRefs": ["example-artifact"], "remainingRefs": [], "dependencyRefs": [], "nextOwner": ""})); handoff.chmod(0o600)
        self.actual("assignment", "handoff", "--file", str(handoff), "--id", r["id"], "--revision", str(self.revision()))
        ack = self.directory / "ack.json"; ack.write_text('{"assignment":"docs","handoffRevision":1}'); ack.chmod(0o600)
        self.actual("assignment", "acknowledge", "--file", str(ack), "--id", r["id"], "--revision", str(self.revision()))
        artifact = self.directory / "accepted.txt"; artifact.write_text("Verified fictional docs output\n")
        verification = self.directory / "verification.json"; verification.write_text(json.dumps([{"task": "docs", "recordId": r["id"], "assignmentRevision": 1, "handoffRevision": 1, "accepted": True, "artifacts": [{"path": str(artifact), "sha256": self.hash(artifact)}]}])); verification.chmod(0o600)
        self.assertEqual(self.run_flow(extra=("--verification", str(verification)))["action"], "dispatched_new_child")
        self.assertEqual(len(self.actual_record()["revisions"][-1]["rows"]), 3)
        self.assertEqual(controls.read_bytes(), before)
        self.assertEqual(len(self.spawn_events()), 3)


if __name__ == "__main__": unittest.main()
