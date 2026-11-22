"""Prove poisoned host config/credential paths do not reach fixture children."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from fixture_environment import isolated_environment


class EnvironmentTests(unittest.TestCase):
    def test_poisoned_host_aliases_and_credentials_are_absent_in_child(self):
        with tempfile.TemporaryDirectory(dir='/tmp') as temporary:
            root = Path(temporary).resolve()
            poison = {'HOME': '/no-such-fictional-owner-home', 'PATH': '/no-such-fictional-bin',
                      'TMPDIR': '/no-such-fictional-tmp', 'XDG_CONFIG_HOME': '/no-such-fictional-config',
                      'BB_CLI': '/no-such-fictional-bb', 'BB_THREAD_ID': 'thr_example_wrong_owner',
                      'OPENAI_API_KEY': 'fictional-credential-sentinel', 'ANTHROPIC_API_KEY': 'fictional-credential-sentinel',
                      'CODEX_HOME': '/no-such-fictional-codex', 'CLAUDE_CONFIG_DIR': '/no-such-fictional-claude',
                      'AWS_SHARED_CREDENTIALS_FILE': '/no-such-fictional-credentials', 'PYTHONPATH': '/no-such-fictional-import'}
            for prefix in ('LOOPTENDER_', 'KEEPGOING_'):
                for suffix in ('REGISTRY_DIR', 'SESSION_DIR', 'ROUTES_FILE', 'NET_PROBE', 'WEBHOOK_URL', 'WEBHOOK_SECRET'):
                    poison[prefix + suffix] = 'fictional-poison-sentinel'
            with patch.dict(os.environ, poison, clear=True):
                env = isolated_environment(root)
                child = subprocess.run([sys.executable, '-B', '-c', 'import os,json; print(json.dumps(dict(os.environ)))'],
                                       env=env, capture_output=True, text=True, timeout=5, check=True)
            actual = json.loads(child.stdout)
            self.assertFalse(any('sentinel' in value or 'no-such-fictional' in value for value in actual.values()))
            for key in ('BB_CLI', 'BB_THREAD_ID', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR', 'AWS_SHARED_CREDENTIALS_FILE', 'PYTHONPATH'):
                self.assertNotIn(key, actual)
            for prefix in ('LOOPTENDER_', 'KEEPGOING_'):
                for suffix in ('REGISTRY_DIR', 'SESSION_DIR', 'ROUTES_FILE'):
                    self.assertTrue(Path(actual[prefix + suffix]).is_relative_to(root))
            self.assertEqual(actual['LOOPTENDER_ROUTES_FILE'], actual['KEEPGOING_ROUTES_FILE'])
