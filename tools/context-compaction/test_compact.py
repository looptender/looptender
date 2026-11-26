"""Fictional compact journeys; no installed BB/provider or real conversation."""
import copy
import json
import os
import shlex
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[2] / 'integrations/bb-plugin-keepgoing/skills/keepgoing/scripts'
sys.path.insert(0, str(SCRIPTS))
import compact as c
from coordinator_state import Refused, digest


class Fake(c.CompactCommands):
    def __init__(self, g):
        super().__init__(g)
        b = g['binding']
        self.calls = []
        self.fail_send = False
        self.ambiguous = False
        self.rows = []
        self.inventory = {'coverage': 'observed_direct_children', 'lanes': []}
        self.usage = {'estimated': False, 'usedTokens': 120000, 'modelContextWindow': 200000}
        self.s = {'loop': b['loop'], 'stateDir': b['runtime'], 'target': 'primary', 'targetConfigured': True, 'targetAgent': {'bbThread': b['thread'], 'provider': b['provider']}, 'goal': {'id': b['goal'], 'revision': 1}, 'controlState': 'enabled', 'control': {'enabled': True, 'paused': False, 'stop': False, 'awaitingUser': False, 'ownership': {'threadId': b['owner'], 'generation': 1}}, 'deadlineReached': False}
        self.parent = {'thread': {'id': b['thread'], 'projectId': b['project'], 'environmentId': b['environment'], 'providerId': b['provider'], 'archivedAt': None, 'deletedAt': None, 'status': 'idle', 'queuedMessageCount': 0, 'activeBackgroundAgentCount': 0}, 'environment': {'id': b['environment'], 'hostId': b['host']}}
        self.interactions = []
        self.on_status = None
        self.on_send = None

    def call(self, executable, args, **kw):
        self.calls.append((executable, args))
        if executable == 'keepgoing':
            if args[0] == 'status':
                if self.on_status: self.on_status(self)
                return copy.deepcopy(self.s)
            if args[:2] == ['assignment', 'inventory']: return copy.deepcopy(self.inventory)
        if args[:2] == ['thread', 'show']: return copy.deepcopy(self.parent)
        if args[:3] == ['thread', 'interactions', 'list']: return copy.deepcopy(self.interactions)
        if args[:2] == ['thread', 'context']: return {'usage': copy.deepcopy(self.usage)}
        if args[:2] == ['thread', 'log']:
            seq = int(args[args.index('--after-seq') + 1]); return [copy.deepcopy(r) for r in self.rows if r['seq'] > seq][:100]
        if args[:2] == ['thread', 'compact']:
            if self.on_send: self.on_send(self)
            if self.fail_send: raise Refused('lost_reply')
            return {'ok': True, 'threadId': 'thr_example_wrong' if self.ambiguous else self.grant['binding']['thread']}
        raise AssertionError(args)

    def completion(self):
        b = self.grant['binding']
        self.rows = [{'seq': seq, 'threadId': b['thread'], 'type': typ, 'scope': {'kind': 'turn', 'turnId': 'turn_example'}, 'data': {'item': {'id': 'compact_example', 'type': 'contextCompaction', 'text': 'PRIVATE fictional text never retained'}}} for seq, typ in [(11, 'item/started'), (12, 'item/completed')]]
        self.usage['usedTokens'] = 40000

    def sends(self): return [a for _, a in self.calls if a[:2] == ['thread', 'compact']]


class Journey(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve(); self.root.chmod(0o700)
        self.env = patch.dict(os.environ, {'BB_THREAD_ID': 'thr_example_operator'}, clear=True); self.env.start(); self.addCleanup(self.env.stop)
        runtime = self.root / 'runtime'; (runtime / 'targets/primary').mkdir(parents=True)
        cursor = runtime / 'targets/primary/bb-cursor.json'; cursor.write_text(json.dumps({'thread': 'thr_example_target', 'lastSeq': 10})); cursor.chmod(0o600)
        config = self.root / 'config.yaml'; config.write_text('fictional configuration')
        exe = self.root / 'fixture-cli'; exe.write_text('fictional pinned executable'); exe.chmod(0o700)
        pin = {'path': str(exe), 'sha256': c.file_hash(str(exe))[0]}
        self.g = {'version': 1, 'id': 'grant_example', 'request': 'phase_one', 'caller': 'thr_example_operator', 'binding': {'loop': 'example-loop', 'config': str(config), 'runtime': str(runtime), 'thread': 'thr_example_target', 'project': 'proj_example', 'environment': 'env_example', 'host': 'host_example', 'provider': 'example-provider', 'owner': 'thr_example_owner', 'generation': 1, 'goal': 'goal_example', 'goalRevision': 1}, 'bb': pin, 'effectiveBB': pin, 'keepgoing': pin, 'maxAttempts': 2, 'cooldownSeconds': 3600, 'continuity': {}}
        for role in c.ROLES:
            p = config if role == 'ownership' else self.root / (role + '.md')
            if role != 'ownership': p.write_text('PRIVATE fictional continuity ' + role)
            self.g['continuity'][role] = {'path': str(p), 'sha256': c.file_hash(str(p))[0]}
        self.grant = self.root / 'grant.json'; self.grant.write_text(json.dumps(self.g)); self.grant.chmod(0o600)
        self.g = c.load_grant(str(self.grant)); self.commands = Fake(self.g); self.journal = c.CompactJournal(str(self.root / 'journal.json'))

    def run_step(self, execute=True, receipt=None, now=10000):
        with self.journal.locked(): return c.advance(self.g, self.journal, self.commands, execute, receipt, now)

    def state(self): return json.loads(self.journal.path.read_text())

    def receipt(self):
        a = self.state()['attempts'][-1]
        return {'attempt': a['id'], 'priorContinuity': digest(a['continuity']), 'currentContinuity': digest(self.g['continuity']), 'accepted': True}

    def test_acceptance_completion_receipt_and_restart_no_replay(self):
        self.assertEqual(self.run_step(False)['action'], 'eligible_manual_request')
        self.assertFalse(self.journal.path.exists()); self.assertFalse(self.commands.sends())
        # Reservation is on disk BEFORE the mutating command.
        self.commands.on_send = lambda _: self.assertEqual(self.state()['attempts'][-1]['state'], 'unknown')
        self.assertEqual(self.run_step()['action'], 'request_accepted_not_completed')
        self.assertEqual(self.state()['attempts'][0]['state'], 'accepted')
        self.g['request'] = 'new_direction'
        # A newer grant direction cannot unreserve a submitted attempt.
        self.assertEqual(self.run_step()['action'], 'hold_pending_attempt')
        self.commands.completion()
        self.assertEqual(self.run_step(receipt=self.receipt())['action'], 'completion_and_receipt_recorded')
        self.assertEqual(self.state()['attempts'][0]['after']['usedTokens'], 40000)
        self.assertEqual(len(self.commands.sends()), 1)
        raw = self.journal.path.read_text(); self.assertNotIn('PRIVATE', raw)
        with self.assertRaisesRegex(Refused, 'cooldown'): self.run_step(now=11000)
        self.commands.rows = []
        self.assertEqual(self.run_step(now=13600)['action'], 'request_accepted_not_completed')
        self.commands.completion(); self.run_step(receipt=self.receipt(), now=13601)
        self.g['request'] = 'third'
        with self.assertRaisesRegex(Refused, 'attempt_cap'): self.run_step(now=20000)

    def test_lost_or_wrong_reply_never_retries(self):
        for mode in ['lost', 'wrong']:
            with self.subTest(mode=mode):
                self.journal = c.CompactJournal(str(self.root / (mode + '.json')))
                self.commands = Fake(self.g)
                self.commands.fail_send = mode == 'lost'; self.commands.ambiguous = mode == 'wrong'
                self.assertEqual(self.run_step()['state'], 'unknown')
                n = len(self.commands.sends()); self.run_step(); self.assertEqual(len(self.commands.sends()), n)
                self.commands.completion(); self.run_step(receipt=self.receipt())
                with self.assertRaisesRegex(Refused, 'request_already_attempted'): self.run_step(now=20000)

    def test_safety_gates_are_send_free(self):
        cases = [lambda: self.commands.s.update(controlState='paused'), lambda: self.commands.parent['thread'].update(status='active'), lambda: self.commands.parent['thread'].update(queuedMessageCount=1), lambda: self.commands.parent['thread'].pop('activeBackgroundAgentCount'), lambda: self.commands.parent['environment'].update(hostId='host_example_other'), lambda: self.commands.s['goal'].update(revision=2), lambda: self.commands.s['control']['ownership'].update(generation=2), lambda: self.commands.s.update(deadlineReached=True), lambda: self.commands.s.update(sendAllowance={'reached': True}), lambda: self.commands.inventory.update(coverage='partial'), lambda: self.commands.inventory.update(lanes=[{'thread': 'thr_example_child', 'archived': False, 'execution': 'active'}]), lambda: self.commands.interactions.append({'threadId': 'thr_example_target', 'status': 'pending'}), lambda: self.commands.usage.pop('estimated')]
        for change in cases:
            self.commands = Fake(self.g); change()
            with self.assertRaises(Refused): self.run_step()
            self.assertFalse(self.commands.sends()); self.assertFalse(self.journal.path.exists())

    def test_stop_after_reservation_retains_unknown_without_send(self):
        original = self.journal.save
        def stop(s):
            original(s); self.commands.s['control']['stop'] = True
        with patch.object(self.journal, 'save', stop):
            with self.assertRaisesRegex(Refused, 'runtime_hold'): self.run_step()
        self.assertFalse(self.commands.sends()); self.assertEqual(self.state()['attempts'][-1]['state'], 'unknown')
        self.assertEqual(self.run_step()['action'], 'hold_pending_attempt')

    def test_failed_reservation_sends_nothing(self):
        with patch.object(self.journal, 'save', side_effect=OSError('fixture')):
            with self.assertRaises(OSError): self.run_step()
        self.assertFalse(self.commands.sends())

    def test_completion_must_be_unique_current_paired_and_receipted(self):
        self.run_step(); self.commands.completion(); good = copy.deepcopy(self.commands.rows)
        for rows in [good[:1], good[1:], [dict(good[0], seq=1), dict(good[1], seq=2)], good + [dict(good[1], seq=13)], [good[0], dict(good[1], scope={'kind': 'turn', 'turnId': 'turn_example_other'})]]:
            self.commands.rows = rows
            with self.assertRaises(Refused): self.run_step(receipt=self.receipt())
            self.assertEqual(self.state()['attempts'][0]['state'], 'accepted')
        self.commands.rows = good
        r = self.receipt(); r['accepted'] = False
        with self.assertRaisesRegex(Refused, 'continuity_receipt_required'): self.run_step(receipt=r)
        self.assertEqual(len(self.commands.sends()), 1)

    def test_changed_sources_or_executable_refuse(self):
        for pin in [self.g['bb'], self.g['continuity']['goal']]:
            path = Path(pin['path']); original = path.read_bytes(); path.write_text('changed')
            with self.assertRaises(Refused): self.run_step()
            path.write_bytes(original)
        self.assertFalse(self.commands.sends())

    def test_corrupt_journal_preserved_and_lock_contention(self):
        self.journal.path.write_text('{'); self.journal.path.chmod(0o600)
        with self.assertRaises(Refused): self.run_step()
        self.assertEqual(self.journal.path.read_text(), '{'); self.journal.path.unlink()
        with self.journal.locked():
            with self.assertRaisesRegex(Refused, 'workflow_busy'): self.run_step()
        self.assertFalse(self.commands.sends())

    def test_grant_binding_and_missing_fields_refuse(self):
        for key in ['caller', 'cooldownSeconds', 'maxAttempts']:
            g = copy.deepcopy(self.g); g.pop('context'); g[key] = {'caller': 'thr_example_other', 'cooldownSeconds': 1, 'maxAttempts': True}[key]
            self.grant.write_text(json.dumps(g))
            with self.assertRaises(Refused): c.load_grant(str(self.grant))
        self.g['context']['caller'] = 'thr_example_rebound'
        self.run_step(); self.g['context']['caller'] = 'thr_example_operator'
        with self.assertRaisesRegex(Refused, 'journal_binding_changed'): self.run_step()

    def test_pending_replay_and_cooldown_do_no_status_work(self):
        self.run_step(); count = len(self.commands.calls)
        self.run_step(); self.assertEqual(len(self.commands.calls), count)
        self.commands.completion(); self.run_step(receipt=self.receipt())
        self.g['request'] = 'new_request'; count = len(self.commands.calls)
        with self.assertRaisesRegex(Refused, 'cooldown'): self.run_step(now=11000)
        self.assertEqual(len(self.commands.calls), count)

    def test_recent_compact_or_incomplete_event_scan_sends_nothing(self):
        self.commands.completion()
        with self.assertRaisesRegex(Refused, 'recent_compaction_reconcile_first'): self.run_step()
        self.commands.rows = [{'seq': i, 'threadId': self.g['binding']['thread'], 'type': 'provider/message', 'data': {'text': 'PRIVATE fictional text'}} for i in range(11, 2011)]
        with self.assertRaisesRegex(Refused, 'event_coverage_bound'): self.run_step()
        self.assertFalse(self.commands.sends()); self.assertFalse(self.journal.path.exists())
        self.commands.rows = [{'seq': 11, 'threadId': 'thr_example_wrong', 'type': 'turn/started', 'data': {}}]
        with self.assertRaisesRegex(Refused, 'event_sequence_changed'): self.run_step()

    def test_changed_bounds_or_retired_runtime_refuses_without_repair(self):
        self.run_step(); before = self.journal.path.read_bytes()
        self.g['context']['maxAttempts'] = 8
        with self.assertRaisesRegex(Refused, 'journal_binding_changed'): self.run_step()
        self.assertEqual(self.journal.path.read_bytes(), before)

    def test_real_command_wrapper_unknown_restart_and_reconcile(self):
        fake = Path(__file__).with_name('fake_cli.py')
        executable = self.root / 'pinned-cli'
        executable.write_text('#!/bin/sh\nexec ' + shlex.quote(sys.executable) + ' -B ' + shlex.quote(str(fake)) + ' "$@"\n')
        executable.chmod(0o700)
        pin = {'path': str(executable), 'sha256': c.file_hash(str(executable))[0]}
        for k in ('bb', 'effectiveBB', 'keepgoing'): self.g[k] = pin
        self.g.pop('context'); self.grant.write_text(json.dumps(self.g))
        world = self.root / 'world.json'
        w = {'status': self.commands.s, 'parent': self.commands.parent, 'inventory': self.commands.inventory, 'usage': self.commands.usage, 'rows': [], 'lostReply': True}
        world.write_text(json.dumps(w))
        args = [sys.executable, '-B', str(SCRIPTS / 'compact.py'), '--grant', str(self.grant), '--journal', str(self.journal.path), '--execute']
        env = dict(os.environ, COMPACT_FIXTURE=str(world))
        first = subprocess.run(args, env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(json.loads(first.stdout)['state'], 'unknown')
        second = subprocess.run(args, env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(json.loads(second.stdout)['action'], 'hold_pending_attempt')
        w = json.loads(world.read_text())
        self.assertEqual(sum(a[:2] == ['thread', 'compact'] for a in w['calls']), 1)
        self.commands.completion(); w['rows'] = self.commands.rows; w['lostReply'] = False; world.write_text(json.dumps(w))
        receipt = self.root / 'receipt.json'; receipt.write_text(json.dumps(self.receipt())); receipt.chmod(0o600)
        result = subprocess.run(args + ['--receipt', str(receipt)], env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['action'], 'completion_and_receipt_recorded')
        self.assertEqual(sum(a[:2] == ['thread', 'compact'] for a in json.loads(world.read_text())['calls']), 1)
        self.assertNotIn('PRIVATE', result.stdout + self.journal.path.read_text())

    def test_changed_roster_on_final_snapshot_refuses(self):
        count = [0]
        def changed(cmd):
            count[0] += 1
            if count[0] == 2: cmd.inventory['coverage'] = 'partial'
        self.commands.on_status = changed
        with self.assertRaises(Refused): self.run_step()
        self.assertFalse(self.commands.sends()); self.assertFalse(self.journal.path.exists())


if __name__ == '__main__': unittest.main()
