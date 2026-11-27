#!/usr/bin/env python3
"""Recovery smoke must wait for completed held checks, not journal appends."""
import copy
from contextlib import contextmanager
import importlib.util
from pathlib import Path
import signal
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("smoke_recovery", Path(__file__).with_name("smoke-recovery.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class HeldRestartTest(unittest.TestCase):
    def test_only_real_checks_on_the_requested_hold_count(self):
        def record(**changes):
            return {"kind": "tick", "controlBefore": {"completed": True}, **changes}
        records = [record(), record(collapsed={"count": 2}),
                   record(kind="daemon_start"), record(kind="daemon_stop"),
                   record(kind="check_refused"), record(kind="control"),
                   record(dryRun=True), record(outcome="dry_run"),
                   record(controlBefore={"awaitingUser": True}), record(controlBefore=None)]
        self.assertEqual(smoke.held_checks({"records": records}, "completed"), 4)
        self.assertEqual(smoke.held_checks({"records": records}, "awaitingUser"), 1)

    def test_lifecycle_records_cannot_release_held_restart(self):
        for flag in ("awaitingUser", "completed"):
            for ordinary in (False, True):
                with self.subTest(flag=flag, ordinary=ordinary), tempfile.TemporaryDirectory() as directory:
                    loop = smoke.Loop.__new__(smoke.Loop)
                    loop.state = Path(directory)
                    loop.kind = "process"
                    loop.calls = lambda: "unchanged fictional calls"
                    loop.receiver = SimpleNamespace(count=lambda: 1)
                    smoke.save(loop.state / "control.json", {flag: True})
                    smoke.save(loop.state / "sends.json", {"used": 2})
                    journal = {"nextSequence": 1, "records": []}
                    endpoint = loop.state / "daemon-endpoint.json"
                    ticks = [0]

                    def append(kind):
                        record = {"kind": kind, "controlBefore": {flag: True}, "outcome": "held"}
                        # Successive identical checks may collapse into one record.
                        if kind == "tick" and journal["records"][-1]["kind"] == "tick":
                            last = journal["records"][-1]
                            last["collapsed"] = {"count": last.get("collapsed", {}).get("count", 0) + 1}
                        else:
                            journal["records"].append(record)
                        journal["nextSequence"] += 1

                    real_load = smoke.load
                    def load(path):
                        if path.name == "incidents.json":
                            return copy.deepcopy(journal)
                        return real_load(path)

                    @contextmanager
                    def daemon(exit_signal):
                        ticks[0] = 0
                        endpoint.write_bytes(str(journal["nextSequence"]).encode())
                        append("daemon_start")
                        yield SimpleNamespace(poll=lambda: None)
                        self.assertGreaterEqual(ticks[0], 2 if ordinary else 1,
                                                "lifecycle evidence released restart before held checks")
                        if exit_signal == signal.SIGTERM:
                            append("daemon_stop")
                            endpoint.unlink()

                    def interval(_):
                        ticks[0] += 1
                        append("tick")

                    loop.daemon = daemon
                    with patch.object(smoke, "load", load), patch.object(smoke.time, "sleep", interval):
                        loop.restart_held(flag, ordinary_intervals=ordinary)


if __name__ == "__main__":
    unittest.main()
