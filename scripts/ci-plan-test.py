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
    def test_draft_pull_request_skips_macos_unless_macos_menu_changed(self):
        for files in (['cmd/looptender/main.go'], ['integrationsmacos/x.swift'], ['notintegrations/macos/x.swift']):
            with self.subTest(files=files):
                got = plan.plan('pull_request', 'true', NEVER, lambda: files)
                self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.UBUNTU_ONLY))
                self.assertEqual(got[4]['run_race'], True)

    def test_docs_only_pr_skips_race_windows_plugin_and_macos_on_draft_and_ready(self):
        for draft in ('true', 'false'):
            for files in (['docs/testing.md', 'README.md'], ['changelog.d/fix.md'], ['LICENSES/MIT.txt']):
                with self.subTest(draft=draft, files=files):
                    got = plan.plan('pull_request', draft, NEVER, lambda: files)
                    self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.UBUNTU_ONLY))
                    self.assertEqual(got[4]['scope'], 'docs')
                    self.assertFalse(got[4]['run_race'])
                    self.assertTrue(got[4]['run_checks'])
                    self.assertFalse(got[4]['run_bb_plugin'])
                    self.assertFalse(got[4]['run_windows'])

    def test_bb_plugin_only_pr_runs_only_bb_plugin_job(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['integrations/bb-plugin-keepgoing/src/index.ts', 'docs/testing.md'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.UBUNTU_ONLY))
        self.assertEqual(got[4]['scope'], 'bb-plugin')
        self.assertTrue(got[4]['run_bb_plugin'])
        self.assertFalse(got[4]['run_checks'])
        self.assertFalse(got[4]['run_race'])
        self.assertFalse(got[4]['run_windows'])

    def test_dockui_only_pr_skips_race_windows_and_macos(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['cmd/looptender/dockui/dock.js', 'docs/dock.md'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.UBUNTU_ONLY))
        self.assertEqual(got[4]['scope'], 'dockui')
        self.assertTrue(got[4]['run_bb_plugin'])
        self.assertTrue(got[4]['run_checks'])
        self.assertFalse(got[4]['run_race'])
        self.assertFalse(got[4]['run_windows'])

    def test_macos_only_pr_skips_race_windows_and_plugin_and_runs_swift_on_macos(self):
        for draft in ('true', 'false'):
            got = plan.plan('pull_request', draft, NEVER, lambda: ['integrations/macos/Sources/KeepgoingMenu/App.swift', 'README.md'])
            self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
            self.assertEqual(got[4]['scope'], 'macos-only')
            self.assertTrue(got[4]['run_checks'])
            self.assertTrue(got[4]['run_swift'])
            self.assertFalse(got[4]['run_race'])
            self.assertFalse(got[4]['run_windows'])
            self.assertFalse(got[4]['run_bb_plugin'])

    def test_draft_with_macos_menu_and_go_runs_macos_other_checks_and_ubuntu_race(self):
        got = plan.plan('pull_request', 'true', NEVER, lambda: ['cmd/looptender/main.go', 'integrations/macos/Package.swift'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
        self.assertTrue(got[4]['run_race'])
        self.assertTrue(got[4]['run_swift'])

    def test_ready_portable_internal_pr_skips_macos_until_merge_queue(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['internal/decide/decide.go'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.UBUNTU_ONLY))
        self.assertEqual(got[4]['scope'], 'go-no-macos')
        self.assertTrue(got[4]['run_race'])
        self.assertTrue(got[4]['run_windows'])
        self.assertFalse(got[4]['run_bb_plugin'])

    def test_ready_unix_only_pr_skips_windows_but_runs_macos(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['cmd/looptender/router_processes_unix.go'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
        self.assertFalse(got[4]['run_windows'])
        self.assertTrue(got[4]['run_macos_race'])

    def test_ready_testexec_or_darwin_cmd_pr_runs_macos_race(self):
        for changed in ('internal/testexec/testexec.go', 'cmd/looptender/legacy_retire.go', 'cmd/looptender/panel_claude_quota_test.go'):
            with self.subTest(changed=changed):
                got = plan.plan('pull_request', 'false', NEVER, lambda: [changed])
                self.assertEqual(got[3], plan.FULL)
                self.assertTrue(got[4]['run_macos_race'])

    def test_ready_for_review_skips_duplicate_when_no_macos_needed_and_sha_passed(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['docs/testing.md'],
                        action='ready_for_review', passed_pull_request=lambda: 888)
        self.assertTrue(got[1])
        self.assertIn('888', got[2])

    def test_ready_for_review_runs_when_macos_is_needed(self):
        got = plan.plan('pull_request', 'false', NEVER, lambda: ['cmd/looptender/main.go'],
                        action='ready_for_review', passed_pull_request=lambda: 888)
        self.assertEqual((got[1], got[3]), (False, plan.FULL))

    def test_pr_whose_files_are_unknown_empty_or_too_many_runs_everything(self):
        def broken():
            raise OSError('api down')
        for files in (broken, lambda: [], lambda: ['a.go'] * plan.FILE_LIST_LIMIT, None):
            with self.subTest(files=files):
                got = plan.plan('pull_request', 'true', NEVER, files)
                self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
                self.assertEqual(got[4]['scope'], 'full')

    def test_an_error_body_or_odd_shape_is_doubt_never_a_list_of_names(self):
        import json, os
        for body in ({'message': 'Bad credentials', 'filename': 'integrations/macos/x.swift'}, [{'message': 'Not Found'}],
                     [{'filename': 5}], ['x'], 'text', None, [{}], [[{'filename': 'a'}], {'filename': 'b'}]):
            with self.subTest(body=body):
                os.environ['CI_PLAN_FILES_JSON'] = json.dumps(body)
                try:
                    with self.assertRaises(ValueError):
                        plan.pull_request_files('o/r', 1)
                    got = plan.plan('pull_request', 'true', NEVER, lambda: plan.pull_request_files('o/r', 1))
                    self.assertEqual((got[0], got[3]), (plan.UBUNTU_ONLY, plan.FULL))
                finally:
                    del os.environ['CI_PLAN_FILES_JSON']

    def test_file_list_from_the_api_includes_renames_and_pages(self):
        import json, os
        os.environ['CI_PLAN_FILES_JSON'] = json.dumps([[{'filename': 'a.go'}, {'filename': 'integrations/macos/B.swift', 'previous_filename': 'integrations/macos/A.swift'}], [{'filename': 'c.md'}]])
        try:
            self.assertEqual(plan.pull_request_files('o/r', 1), ['a.go', 'integrations/macos/B.swift', 'integrations/macos/A.swift', 'c.md'])
        finally:
            del os.environ['CI_PLAN_FILES_JSON']

    def test_everything_else_runs_everything(self):
        for event, draft in [('pull_request', ''), ('pull_request', 'unknown'), ('merge_group', ''), ('merge_group', 'true'),
                             ('workflow_dispatch', ''), ('push', '')]:
            with self.subTest(event=event, draft=draft):
                got = plan.plan(event, draft, NEVER, lambda: ['README.md'])
                self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
                self.assertTrue(got[4]['run_macos_race'])

    def test_merge_queue_never_skips_or_narrows(self):
        got = plan.plan('merge_group', 'true', lambda: 123, lambda: ['README.md'])
        self.assertEqual((got[0], got[1], got[3]), (plan.UBUNTU_ONLY, False, plan.FULL))
        self.assertEqual(got[4]['scope'], 'full')
        self.assertTrue(got[4]['run_macos_race'])
        self.assertTrue(got[4]['run_swift'])

    def test_push_is_a_duplicate_only_when_the_same_commit_passed_the_queue(self):
        self.assertEqual(plan.plan('push', '', lambda: 77)[:2], (plan.UBUNTU_ONLY, True))
        self.assertEqual(plan.plan('push', '', lambda: None)[:2], (plan.UBUNTU_ONLY, False))

    def test_any_error_on_a_push_runs_everything(self):
        def broken():
            raise OSError('api down')
        self.assertEqual(plan.plan('push', '', broken)[:2], (plan.UBUNTU_ONLY, False))

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


class OutputTests(unittest.TestCase):
    def test_outputs_go_to_the_github_output_file_and_only_the_reason_to_stdout(self):
        import contextlib, io, os, tempfile
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / 'output'
            env = {'EVENT': 'pull_request', 'DRAFT': 'true', 'GITHUB_OUTPUT': str(out), 'REPO': 'o/r', 'PR': '1',
                   'CI_PLAN_FILES_JSON': '[["main.go"]]'}
            captured = io.StringIO()
            old = dict(os.environ)
            os.environ.update(env)
            try:
                with contextlib.redirect_stdout(captured):
                    plan.main()
            finally:
                os.environ.clear(); os.environ.update(old)
            lines = out.read_text().splitlines()
            self.assertEqual(lines, [
                'os=["ubuntu-24.04"]',
                'checks_os=["ubuntu-24.04"]',
                'duplicate=false',
                'scope=go-no-macos',
                'run_race=true',
                'run_checks=true',
                'run_bb_plugin=false',
                'run_windows=true',
                'run_swift=false',
                'run_smoke=true',
                'run_macos_race=false',
            ])
            self.assertNotIn('=', captured.getvalue().split(':')[0])
            self.assertTrue(all('=' in l for l in lines))


def needs(**results):
    plan_out = results.pop('_duplicate', 'false')
    outputs = {'duplicate': plan_out}
    for key in list(results):
        if key.startswith('_run_'):
            outputs[key[1:]] = results.pop(key)
    base = {'plan': {'result': 'success', 'outputs': outputs}}
    base.update({k: {'result': v} for k, v in results.items()})
    return base


class GateTests(unittest.TestCase):
    def test_all_success_passes(self):
        self.assertTrue(gate.verdict(needs(race='success', checks='success'))[0])

    def test_any_failure_cancelled_or_unplanned_skip_fails(self):
        for bad in ('failure', 'cancelled', 'skipped'):
            with self.subTest(bad=bad):
                self.assertFalse(gate.verdict(needs(race='success', checks=bad))[0])

    def test_skipped_is_accepted_for_a_duplicate_or_explicit_plan_skip(self):
        self.assertTrue(gate.verdict(needs(race='skipped', checks='skipped', _duplicate='true'))[0])
        self.assertTrue(gate.verdict(needs(race='skipped', checks='success', _run_race='false'))[0])
        self.assertTrue(gate.verdict(needs(**{'race': 'skipped', 'bb-plugin': 'skipped', 'windows-acl': 'skipped', 'windows-processes': 'skipped', 'checks': 'success',
                                              '_run_race': 'false', '_run_bb_plugin': 'false', '_run_windows': 'false'}))[0])
        self.assertFalse(gate.verdict(needs(race='skipped', checks='skipped', _run_race='false'))[0])
        self.assertFalse(gate.verdict(needs(race='failure', checks='skipped', _duplicate='true'))[0])

    def test_a_failed_or_missing_plan_fails(self):
        self.assertFalse(gate.verdict({'plan': {'result': 'failure'}, 'race': {'result': 'success'}})[0])
        self.assertFalse(gate.verdict({'race': {'result': 'success'}, 'checks': {'result': 'success'}})[0])

    def test_a_gate_that_waits_for_nothing_but_the_plan_fails(self):
        self.assertFalse(gate.verdict(needs())[0])


if __name__ == '__main__':
    unittest.main()
