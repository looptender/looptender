"""Unit tests for scripts/ci-macos-race.py coverage and verification."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('ci_macos_race', Path(__file__).with_name('ci-macos-race.py'))
macos_race = importlib.util.module_from_spec(spec)
spec.loader.exec_module(macos_race)


class MacOSRaceTests(unittest.TestCase):
    def test_current_tree_is_covered(self):
        count = macos_race.verify_coverage()
        self.assertGreater(count, 100)

    def test_uncovered_darwin_file_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'cmd/looptender').mkdir(parents=True)
            (root / 'cmd/looptender/service_darwin_test.go').write_text('package main\nfunc TestFoo(t *testing.T) {}\n')
            (root / 'internal/newpkg').mkdir(parents=True)
            (root / 'internal/newpkg/feature_darwin.go').write_text('package newpkg\n')
            with self.assertRaisesRegex(ValueError, 'internal/newpkg'):
                macos_race.verify_coverage(root)

    def test_test2json_verification_requires_exact_start_and_finish(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / 'out.jsonl'
            good = [
                {'Action': 'run', 'Test': 'TestOne'},
                {'Action': 'pass', 'Test': 'TestOne'},
                {'Action': 'run', 'Test': 'TestTwo'},
                {'Action': 'skip', 'Test': 'TestTwo'},
            ]
            log.write_text(''.join(json.dumps(e) + '\n' for e in good))
            macos_race.verify_test2json_execution(log, ['TestOne', 'TestTwo'])
            log.write_text(''.join(json.dumps(e) + '\n' for e in good[:2]))
            with self.assertRaises(ValueError):
                macos_race.verify_test2json_execution(log, ['TestOne', 'TestTwo'])


if __name__ == '__main__':
    unittest.main()
