"""Ordering checks: numeric process identity must not be signalled after reap."""
import os
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[2] / 'integrations/bb-plugin-keepgoing/skills/keepgoing/scripts'
sys.path.insert(0, str(SCRIPTS))
from coordinator import Commands
from coordinator_state import Refused


class ProcessCleanupTests(unittest.TestCase):
    def fake_process(self, payload, exit_code, events):
        read, write = os.pipe()
        os.write(write, payload); os.close(write)
        stdin_read, stdin_write = os.pipe(); os.close(stdin_read)
        class Process:
            pid = 12345  # fictional identity; all signalling is intercepted
            returncode = None
            stdout = os.fdopen(read, 'rb')
            stdin = os.fdopen(stdin_write, 'wb')
            def wait(self, timeout):
                events.append('reap')
                self.returncode = exit_code
                return exit_code
        return Process()

    def test_success_and_failed_exit_never_signal_after_reap(self):
        for code in (0, 3):
            with self.subTest(code=code):
                events = []
                process = self.fake_process(b'{}', code, events)
                with patch('coordinator.subprocess.Popen', return_value=process), patch('coordinator.os.killpg', side_effect=lambda *_: events.append('signal')):
                    commands = Commands({'bb': {'path': '/fictional/never-executed'}})
                    if code:
                        with self.assertRaisesRegex(Refused, 'command_failed'):
                            commands.call('bb', [])
                    else:
                        self.assertEqual(commands.call('bb', []), {})
                self.assertEqual(events, ['reap'])

    def test_overflow_signals_before_reaping_unreaped_leader(self):
        events = []
        process = self.fake_process(b'{"fictional":true}', -9, events)
        def signal_before_reap(*_):
            self.assertIsNone(process.returncode)
            events.append('signal')
        with patch('coordinator.subprocess.Popen', return_value=process), patch('coordinator.os.killpg', side_effect=signal_before_reap):
            with self.assertRaisesRegex(Refused, 'command_output_bound'):
                Commands({'bb': {'path': '/fictional/never-executed'}}).call('bb', [], limit=1)
        self.assertEqual(events, ['signal', 'reap'])
