"""Table tests for the run plan and the gate decision."""
import importlib.util
from pathlib import Path
import unittest


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


plan, gate = load('ci-plan'), load('ci-gate')
NEVER = lambda: None


class PlanTests(unittest.TestCase):
    def test_draft_pull_request_skips_macos_only(self):
        self.assertEqual(plan.plan('pull_request', 'true', NEVER)[:2], (plan.UBUNTU_ONLY, False))

    def test_everything_else_runs_everything(self):
        for event, draft in [('pull_request', 'false'), ('pull_request', ''), ('merge_group', ''), ('merge_group', 'true'),
                             ('workflow_dispatch', ''), ('push', '')]:
            with self.subTest(event=event, draft=draft):
                self.assertEqual(plan.plan(event, draft, NEVER)[:2], (plan.FULL, False))

    def test_merge_queue_never_skips_or_narrows(self):
        # Even with a draft flag or a passed run to find: the queue runs everything.
        self.assertEqual(plan.plan('merge_group', 'true', lambda: 123)[:2], (plan.FULL, False))

    def test_push_is_a_duplicate_only_when_the_same_commit_passed_the_queue(self):
        self.assertEqual(plan.plan('push', '', lambda: 77)[:2], (plan.FULL, True))
        self.assertEqual(plan.plan('push', '', lambda: None)[:2], (plan.FULL, False))

    def test_any_error_on_a_push_runs_everything(self):
        def broken():
            raise OSError('api down')
        self.assertEqual(plan.plan('push', '', broken)[:2], (plan.FULL, False))

    def test_only_a_successful_merge_group_run_of_the_same_commit_counts(self):
        import os
        sha = 'a' * 40
        def answer(*runs):
            import json
            os.environ['CI_PLAN_RUNS_JSON'] = json.dumps({'workflow_runs': list(runs)})
            try:
                return plan.passed_merge_group('o/r', sha)
            finally:
                del os.environ['CI_PLAN_RUNS_JSON']
        good = {'id': 5, 'head_sha': sha, 'event': 'merge_group', 'conclusion': 'success'}
        self.assertEqual(answer(good), 5)
        self.assertIsNone(answer())
        for change in ({'head_sha': 'b' * 40}, {'event': 'pull_request'}, {'conclusion': 'failure'}, {'conclusion': None}):
            self.assertIsNone(answer({**good, **change}), change)


def needs(**results):
    plan_out = results.pop('_duplicate', 'false')
    base = {'plan': {'result': 'success', 'outputs': {'duplicate': plan_out}}}
    base.update({k: {'result': v} for k, v in results.items()})
    return base


class GateTests(unittest.TestCase):
    def test_all_success_passes(self):
        self.assertTrue(gate.verdict(needs(race='success', checks='success'))[0])

    def test_any_failure_cancelled_or_skipped_fails(self):
        for bad in ('failure', 'cancelled', 'skipped'):
            with self.subTest(bad=bad):
                self.assertFalse(gate.verdict(needs(race='success', checks=bad))[0])

    def test_skipped_is_accepted_only_for_a_duplicate_run(self):
        self.assertTrue(gate.verdict(needs(race='skipped', checks='skipped', _duplicate='true'))[0])
        self.assertFalse(gate.verdict(needs(race='failure', checks='skipped', _duplicate='true'))[0])
        self.assertFalse(gate.verdict(needs(race='cancelled', checks='skipped', _duplicate='true'))[0])

    def test_a_failed_or_missing_plan_fails(self):
        self.assertFalse(gate.verdict({'plan': {'result': 'failure'}, 'race': {'result': 'success'}})[0])
        self.assertFalse(gate.verdict({'race': {'result': 'success'}, 'checks': {'result': 'success'}})[0])

    def test_a_gate_that_waits_for_nothing_but_the_plan_fails(self):
        self.assertFalse(gate.verdict(needs())[0])


if __name__ == '__main__':
    unittest.main()
