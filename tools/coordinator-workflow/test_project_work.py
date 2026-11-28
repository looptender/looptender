"""Fictional continuity journeys. No provider, service, runtime or host calls."""
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from fixture_environment import isolated_environment
from test_coverage import fixture, NOW

SCRIPTS = Path(__file__).resolve().parents[2] / 'integrations/bb-plugin-keepgoing/skills/keepgoing/scripts'
sys.path.insert(0, str(SCRIPTS))
import project_work as work
from coordinator_state import Refused


def register(root):
    i, c, r = fixture(root)
    for row in i['items']:
        row.update(title='Fictional Sessionpipe ' + row['id'], owner=None,
                   acceptanceRefs=['example:acceptance/' + row['id']],
                   nextAction='Review the fictional artifact and record follow-through',
                   evidenceRefs=['example:research-review'], blockingObligation=None)
        if row['assignment']:
            row['owner'] = {'kind': 'assignment', 'id': row['assignment']['id']}
        if row['state'] == 'waiting':
            row['blockingObligation'] = {'id': 'implementation', 'revision': 1}
    return i, c, r


@unittest.skipUnless(sys.platform in {'darwin', 'linux'}, 'Darwin/Linux only')
class ProjectWorkTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.i, self.c, self.r = register(self.root)
        self.path = self.root / 'WORK.md'
        self.env = isolated_environment(self.root)
        self.write_work()

    def raw(self, i=None):
        return work.markdown(i or self.i, 'looptender-work', '# Fictional work\n\nPrivate full text, accents é and 中文.\nA checkbox [x] grants nothing.')

    def write_work(self):
        self.path.write_bytes(self.raw()); self.path.chmod(0o600)

    def project(self, context=True, previous=None):
        return work.reconcile(self.i, work.sha(self.raw()), work.coverage.pin_sources(self.i),
                              self.c if context else None, self.r if context else None,
                              previous=previous, now=NOW)

    def codes(self, report):
        return {x['class'] for x in report['findings']}

    def cli(self, *args, timeout=3):
        return subprocess.run([sys.executable, '-B', str(SCRIPTS / 'project_work.py'), *map(str, args)],
                              env=self.env, text=True, capture_output=True, timeout=timeout)

    def test_completed_child_does_not_close_sessionpipe_follow_through(self):
        row = self.i['items'][0]
        row.update(state='open', acceptance=None)
        before = copy.deepcopy((self.i, self.c, self.r))
        report = self.project()
        self.assertIn('child_complete_follow_through_open', self.codes(report))
        self.assertEqual(report['coverage']['verdict'], 'continue_authorized_work')
        self.assertFalse(report['objectiveComplete'])
        self.assertEqual(before, (self.i, self.c, self.r))

    def test_fulfilled_wait_keeps_independent_work_and_never_changes_wait(self):
        before = copy.deepcopy(self.i)
        report = self.project()
        self.assertIn('wait_obligation_resolved', self.codes(report))
        self.assertIn('unjustified_coordinator_wait', self.codes(report))
        self.assertIn('visibility', [r['id'] for r in report['items']])
        self.assertEqual(report['coverage']['verdict'], 'continue_authorized_work')
        self.assertEqual(before, self.i)
        self.assertEqual(self.i['items'][1]['state'], 'waiting')
        self.assertTrue(all(f['autoAction'] == 'none' for f in report['findings']))

    def test_missing_binding_captures_and_sources_still_report_local_work(self):
        self.i.update(binding=None, assignmentRecord=None, assignmentRevision=None, workSources=[], authoritySources=[])
        for row in self.i['items']:
            row.update(workSource=None, acceptance=None)
        report = self.project(context=False)
        self.assertIn('unfinished_unowned_work', self.codes(report))
        self.assertIn('coverage_inputs_unavailable', self.codes(report))
        self.assertIn('work_source_missing', self.codes(report))
        self.assertEqual(len(report['items']), 3)
        self.assertEqual(report['assessability']['runtimeCoverage'], 'cannot_assess')
        self.assertIsNone(report['coverage'])

    def test_missing_is_not_empty_and_empty_is_not_complete(self):
        result = self.cli('check', '--work', self.root / 'missing.md')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(result.stdout)['verdict'], 'cannot_assess')
        self.i = work.template('example')
        self.write_work()
        result = self.cli('check', '--work', self.path, '--private')
        self.assertEqual(result.returncode, 1)
        report = json.loads(result.stdout)
        self.assertIn('empty_declared_inventory', self.codes(report))
        self.assertFalse(report['objectiveComplete'])

    def test_stale_acknowledgement_does_not_count_child_completion(self):
        self.i['items'][0].update(state='open', acceptance=None)
        for key, value in [('acknowledged', 0), ('handoffRevision', 2), ('revision', 2)]:
            with self.subTest(key=key):
                saved = copy.deepcopy(self.r)
                self.r['revisions'][-1]['rows'][0][key] = value
                report = self.project()
                self.assertIn('assignment_mapping_stale', self.codes(report))
                self.assertNotIn('child_complete_follow_through_open', self.codes(report))
                self.r = saved

    def test_dependency_revision_cycle_missing_and_unmet(self):
        for dep, expected in [({'id': 'implementation', 'revision': 2}, 'dependency_revision_changed'),
                              ({'id': 'absent', 'revision': 1}, 'dependency_missing'),
                              ({'id': 'visibility', 'revision': 1}, 'dependency_cycle'),
                              ({'id': 'delivery', 'revision': 1}, 'dependency_unmet')]:
            with self.subTest(expected=expected):
                self.i['items'][2]['dependsOn'] = [dep]
                self.assertIn(expected, self.codes(self.project()))
        self.i['items'][2]['dependsOn'] = None
        self.assertIn('dependency_evidence_unavailable', self.codes(self.project()))

    def test_wait_needs_current_accepted_obligation_and_dependencies(self):
        for mutation in ('missing', 'revision', 'unaccepted', 'source', 'dependency', 'gate'):
            saved = copy.deepcopy(self.i)
            with self.subTest(mutation=mutation):
                target = self.i['items'][0]
                if mutation == 'missing': self.i['items'][1]['blockingObligation']['id'] = 'absent'
                elif mutation == 'revision': self.i['items'][1]['blockingObligation']['revision'] = 2
                elif mutation == 'unaccepted': target['acceptance']['accepted'] = False
                elif mutation == 'source': target['acceptance']['workSourceDigest'] = 'f' * 64
                elif mutation == 'dependency': target['dependsOn'] = [{'id': 'visibility', 'revision': 1}]
                else: target['personGate'] = {'state': 'held', 'refs': ['example:person']}
                self.assertNotIn('wait_obligation_resolved', self.codes(self.project()))
            self.i = saved

    def test_person_gate_and_runtime_hold_win_with_independent_work_visible(self):
        self.i['items'][1]['personGate'] = {'state': 'held', 'refs': ['example:release-person']}
        for control in work.coverage.CONTROLS:
            with self.subTest(control=control):
                self.c['controls'] = {k: k == 'enabled' for k in work.coverage.CONTROLS}
                self.c['controls'][control] = control != 'enabled'
                report = self.project()
                self.assertEqual(report['verdict'], 'runtime_held')
                self.assertIn('person_gate_held', self.codes(report))
                self.assertNotIn('unjustified_coordinator_wait', self.codes(report))
                self.assertEqual(len(report['items']), 3)

    def test_stale_and_partial_captures_never_certify_no_work(self):
        for key, value in [('observedAt', '2000-01-01T00:00:00Z'), ('health', 'partial')]:
            prior = copy.deepcopy(self.c)
            self.c[key] = value
            report = self.project()
            self.assertEqual(report['coverage']['verdict'], 'cannot_assess')
            self.assertIn('unfinished_unowned_work', self.codes(report))
            self.c = prior

    def test_acceptance_supports_research_review_artifacts_without_commits(self):
        report = self.project()
        self.assertEqual(report['coverage']['items'][0]['disposition'], 'declared_closed')
        self.assertIn('wait_obligation_resolved', self.codes(report))
        self.assertFalse(report['objectiveComplete'])

    def test_schema_rejects_duplicates_unknown_fields_contradictions(self):
        mutations = [lambda i: i['items'].append(copy.deepcopy(i['items'][0])),
                     lambda i: i.update(extra=True),
                     lambda i: i['items'][0].update(owner=None),
                     lambda i: i['items'][2].update(owner={'kind': 'assignment', 'id': 'other'}),
                     lambda i: i['items'][1].update(blockingObligation=None),
                     lambda i: i['items'][2].update(blockingObligation={'id': 'implementation', 'revision': 1}),
                     lambda i: i['items'][0].update(workSource=None),
                     lambda i: i['items'][0].update(state='open'),
                     lambda i: i['items'][2].update(acceptanceRefs=[]),
                     lambda i: i.update(revision=True),
                     lambda i: i.update(assignmentRecord=None)]
        for mutate in mutations:
            candidate = copy.deepcopy(self.i); mutate(candidate)
            with self.subTest(candidate=candidate.get('extra')):
                with self.assertRaises((Refused, TypeError)):
                    work.parse_work(self.raw(candidate))
        for raw in [self.raw().replace(b'"version": 1', b'"version": 1, "version": 1', 1),
                    self.raw() + self.raw(), self.raw().replace(b'```\n', b'', 1),
                    b'\xff', b'no machine section', self.raw() + b'\0', b'x' * (work.WORK_LIMIT + 1)]:
            with self.assertRaises(Refused): work.parse_work(raw)

    def test_revision_drift_and_removed_unfinished_item_preserved_in_findings(self):
        previous = (copy.deepcopy(self.i), self.raw())
        self.i['items'][2]['nextAction'] = 'Review newly reported fictional evidence'
        report = self.project(previous=previous)
        self.assertTrue({'checkpoint_stale', 'register_revision_not_advanced', 'item_revision_not_advanced'} <= self.codes(report))
        self.i['items'].pop()
        self.assertIn('unfinished_item_removed', self.codes(self.project(previous=previous)))

    def test_restart_checkpoint_preserves_full_text_and_archives_prior(self):
        output = self.root / 'HANDOFF.md'
        original = self.path.read_bytes()
        first = self.cli('checkpoint', '--work', self.path, '--output', output)
        self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
        old_bytes = output.read_bytes(); first_digest = json.loads(first.stdout)['checkpointSha256']
        self.assertEqual(work.parse_checkpoint(old_bytes)[1], original)
        self.i['revision'] += 1; self.i['items'][2]['revision'] += 1
        self.i['items'][2]['nextAction'] = 'Resume the next review after restart'
        self.write_work()
        # A new process reads the same complete private state.
        read = self.cli('read', '--work', self.path, '--private')
        self.assertEqual(json.loads(read.stdout)['markdown'].encode(), self.path.read_bytes())
        refused = self.cli('checkpoint', '--work', self.path, '--output', output)
        self.assertEqual(refused.returncode, 1)
        self.assertEqual(output.read_bytes(), old_bytes)
        updated = self.cli('checkpoint', '--work', self.path, '--output', output, '--expect-previous', first_digest, '--private')
        self.assertEqual(updated.returncode, 0, updated.stdout + updated.stderr)
        self.assertIn('checkpoint_stale', self.codes(json.loads(updated.stdout)))
        archive = output.with_name(output.name + '.' + first_digest + '.md')
        self.assertEqual(archive.read_bytes(), old_bytes)
        self.assertEqual(work.parse_checkpoint(output.read_bytes())[1], self.path.read_bytes())
        stale = self.cli('checkpoint', '--work', self.path, '--output', output, '--expect-previous', first_digest)
        self.assertEqual(stale.returncode, 1)
        self.assertEqual(archive.read_bytes(), old_bytes)

    def test_init_never_overwrites_and_default_output_keeps_private_text(self):
        p = self.root / 'new.md'
        result = self.cli('init', '--work', p, '--id', 'EXAMPLE')
        self.assertEqual(result.returncode, 0)
        self.assertEqual(p.stat().st_mode & 0o777, 0o600)
        prior = p.read_bytes()
        self.assertEqual(self.cli('init', '--work', p, '--id', 'EXAMPLE').returncode, 1)
        self.assertEqual(p.read_bytes(), prior)
        checked = self.cli('check', '--work', self.path)
        self.assertEqual(checked.returncode, 1)
        for private in ('Sessionpipe', 'example-scope', 'research-review', str(self.root)):
            self.assertNotIn(private, checked.stdout)

    def test_fifo_symlink_directory_permissions_oversize_and_parent_link_refuse_promptly(self):
        fifo = self.root / 'fifo'; os.mkfifo(fifo, 0o600)
        symlink = self.root / 'link'; symlink.symlink_to(self.path)
        oversized = self.root / 'oversized'; oversized.write_bytes(b'x' * (work.WORK_LIMIT + 1)); oversized.chmod(0o600)
        public = self.root / 'public'; public.write_bytes(self.raw()); public.chmod(0o644)
        parentlink = self.root / 'parentlink'; parentlink.symlink_to(self.root, target_is_directory=True)
        for p in (fifo, symlink, self.root, oversized, public, parentlink / 'WORK.md'):
            with self.subTest(path=p.name):
                result = self.cli('read', '--work', p, timeout=1.5)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertNotIn(str(self.root), result.stdout)
        # The same safe input path is used for captures, checkpoints and locks.
        for flag in ('--context', '--assignments', '--previous'):
            self.assertEqual(self.cli('check', '--work', self.path, flag, fifo, timeout=1.5).returncode, 1)
        self.assertEqual(self.cli('checkpoint', '--work', self.path, '--output', fifo, timeout=1.5).returncode, 1)
        output = self.root / 'handoff'; os.mkfifo(str(output) + '.lock', 0o600)
        self.assertEqual(self.cli('checkpoint', '--work', self.path, '--output', output, timeout=1.5).returncode, 1)
        self.assertFalse(output.exists())

    def test_source_fifo_drift_and_work_reread_are_unavailable_not_hangs(self):
        fifo = self.root / 'sourcefifo'; os.mkfifo(fifo, 0o600)
        self.i['workSources'][0]['path'] = str(fifo); self.write_work()
        result = self.cli('check', '--work', self.path, '--private', timeout=1.5)
        self.assertIn('source_unavailable', self.codes(json.loads(result.stdout)))
        self.i, self.c, self.r = register(self.root); self.write_work()
        (self.root / 'ROADMAP.md').write_text('Changed fictional scope')
        self.assertIn('source_drift', self.codes(self.project()))
        original = work.read_file
        calls = 0
        def changing(path, limit):
            nonlocal calls
            raw = original(path, limit)
            calls += 1
            return raw + b'\n' if calls > 1 else raw
        with patch.object(work, 'read_file', side_effect=changing):
            with self.assertRaisesRegex(Refused, 'work_changed_during_read'):
                work.inspect(self.path)

    def test_no_runtime_commands_even_with_traps_on_path(self):
        traps = self.root / 'bin'; traps.mkdir()
        marker = self.root / 'called'
        for name in ('bb', 'looptender', 'git', 'gh', 'claude', 'codex', 'curl'):
            p = traps / name
            p.write_text('#!/bin/sh\nprintf called > "' + str(marker) + '"\nexit 99\n'); p.chmod(0o700)
        self.env['PATH'] = str(traps)
        before = self.path.read_bytes()
        report = self.cli('check', '--work', self.path, '--private')
        self.assertEqual(report.returncode, 1)
        self.assertEqual(before, self.path.read_bytes())
        self.assertFalse(marker.exists())
        with patch('subprocess.Popen', side_effect=AssertionError('no commands')):
            self.project()

    def test_checkpoint_fault_keeps_old_snapshot_and_archive(self):
        output = self.root / 'HANDOFF.md'
        self.assertEqual(self.cli('checkpoint', '--work', self.path, '--output', output).returncode, 0)
        prior = output.read_bytes()
        with patch.object(work.os, 'replace', side_effect=OSError('fictional publish failure')):
            with self.assertRaises(OSError):
                work.save_checkpoint(output, prior, work.sha(prior))
        self.assertEqual(output.read_bytes(), prior)
        self.assertEqual(output.with_name(output.name + '.' + work.sha(prior) + '.md').read_bytes(), prior)
        self.assertEqual(list(self.root.glob('.handoff-*')), [])

    def test_unknown_source_does_not_erase_other_known_work(self):
        self.i['items'][1]['workSource'] = None
        report = self.project()
        self.assertEqual(len(report['items']), 3)
        self.assertIn('visibility', [r['id'] for r in report['items']])
        self.assertIn('unfinished_unowned_work', self.codes(report))
        self.assertIsNone(report['coverage'])
        self.assertEqual(report['assessability']['runtimeCoverage'], 'cannot_assess')

    def test_checkpoint_archive_nonregular_and_conflicting_bytes_refuse(self):
        output = self.root / 'HANDOFF.md'
        self.assertEqual(self.cli('checkpoint', '--work', self.path, '--output', output).returncode, 0)
        prior = output.read_bytes(); previous = work.sha(prior)
        archive = output.with_name(output.name + '.' + previous + '.md')
        for kind in ('fifo', 'symlink', 'directory', 'bytes'):
            with self.subTest(kind=kind):
                if kind == 'fifo': os.mkfifo(archive, 0o600)
                elif kind == 'symlink': archive.symlink_to(output)
                elif kind == 'directory': archive.mkdir(mode=0o700)
                else: archive.write_bytes(b'fictional different archive'); archive.chmod(0o600)
                result = self.cli('checkpoint', '--work', self.path, '--output', output, '--expect-previous', previous, timeout=1.5)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(output.read_bytes(), prior)
                if kind == 'directory': archive.rmdir()
                else: archive.unlink()

    def test_first_publish_failure_leaves_no_partial_destination(self):
        output = self.root / 'new.md'
        with patch.object(work.os, 'link', side_effect=OSError('fictional link failure')):
            with self.assertRaises(OSError): work.exclusive(output, self.raw())
        self.assertFalse(output.exists())
        self.assertEqual(list(self.root.glob('.project-work-*')), [])

    def test_strict_escaped_fields_and_blank_actions(self):
        malformed = self.raw().replace(b'"version": 1', b'"version": 1, "vers\\u0069on": 1', 1)
        with self.assertRaisesRegex(Refused, 'duplicate_field'): work.parse_work(malformed)
        self.i['items'][2]['nextAction'] = '   '
        with self.assertRaisesRegex(Refused, 'work_item_text'): work.validate(self.i)

    def test_changed_pins_during_read_refuse_report(self):
        first = work.coverage.pin_sources(self.i)
        with patch.object(work.coverage, 'pin_sources', side_effect=[first, {}]):
            with self.assertRaisesRegex(Refused, 'source_changed_during_read'): work.inspect(self.path)

    def test_checkpoint_cannot_hide_current_history_with_another_previous(self):
        output = self.root / 'HANDOFF.md'
        self.assertEqual(self.cli('checkpoint', '--work', self.path, '--output', output).returncode, 0)
        prior = output.read_bytes()
        other = self.root / 'other.md'; other.write_bytes(prior); other.chmod(0o600)
        result = self.cli('checkpoint', '--work', self.path, '--output', output, '--previous', other, '--expect-previous', work.sha(prior))
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(result.stdout)['reason'], 'checkpoint_previous_must_match_output')
        self.assertEqual(output.read_bytes(), prior)

    def test_cli_bridge_uses_actual_bound_captures(self):
        self.i['items'][0].update(state='open', acceptance=None)
        now = datetime.now(timezone.utc).isoformat()
        self.c['observedAt'] = now
        self.r['revisions'][-1]['inventory']['observedAt'] = now
        self.write_work()
        context = self.root / 'context.json'; assignments = self.root / 'assignments.json'
        context.write_text(json.dumps(self.c)); context.chmod(0o600)
        assignments.write_text(json.dumps(self.r)); assignments.chmod(0o600)
        result = self.cli('check', '--work', self.path, '--context', context, '--assignments', assignments, '--private')
        self.assertEqual(result.returncode, 1)
        report = json.loads(result.stdout)
        self.assertIn('child_complete_follow_through_open', self.codes(report))
        self.assertIn('unjustified_coordinator_wait', self.codes(report))
        self.assertEqual(report['coverage']['verdict'], 'continue_authorized_work')
        self.assertEqual(self.path.read_bytes(), self.raw())
        self.c['binding']['goalRevision'] += 1
        context.write_text(json.dumps(self.c))
        result = self.cli('check', '--work', self.path, '--context', context, '--assignments', assignments, '--private')
        self.assertEqual(json.loads(result.stdout)['coverage']['verdict'], 'cannot_assess')

    def test_checkpoint_rejects_contradictory_report_binding(self):
        report = self.project()
        checkpoint = {'version': 1, 'registerId': self.i['id'], 'revision': self.i['revision'],
                      'workSha256': work.sha(self.raw()), 'workMarkdown': self.raw().decode(), 'report': report}
        for mutation in ('completion', 'digest', 'items', 'unknown'):
            value = copy.deepcopy(checkpoint)
            if mutation == 'completion': value['report']['objectiveComplete'] = True
            elif mutation == 'digest': value['report']['evidence']['workSha256'] = 'f' * 64
            elif mutation == 'items': value['report']['items'] = []
            else: value['report']['unexpected'] = True
            with self.subTest(mutation=mutation), self.assertRaises(Refused):
                work.parse_checkpoint(work.markdown(value, 'looptender-handoff', '# Snapshot'))

    def test_unsupported_platform_refuses_before_writes(self):
        target = self.root / 'unsupported.md'
        with patch.object(work.sys, 'platform', 'win32'), patch('builtins.print'):
            self.assertEqual(work.main(['init', '--work', str(target), '--id', 'example']), 1)
        self.assertFalse(target.exists())


if __name__ == '__main__':
    unittest.main()
