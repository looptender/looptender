"""Regressions for ci-go-test-ran.py: a pinned pattern that matches nothing must fail."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('ci-go-test-ran.py')
spec = importlib.util.spec_from_file_location('ran', SCRIPT)
ran = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ran)


class RanTests(unittest.TestCase):
    def test_result_lines(self):
        self.assertTrue(ran.ran_any(['ok  \texample.com/a\t0.1s\n']))
        self.assertTrue(ran.ran_any(['ok  \texample.com/a\t0.1s [no tests to run]\n', 'ok  \texample.com/b\t0.2s\n']))
        self.assertFalse(ran.ran_any(['ok  \texample.com/a\t0.1s [no tests to run]\n']))
        self.assertFalse(ran.ran_any(['no result lines at all\n']))

    def test_real_go_pattern_that_matches_nothing_fails(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'go.mod').write_text('module example.com/fixture\n\ngo 1.24\n')
            (root / 'f_test.go').write_text('package fixture\nimport "testing"\nfunc TestKept(t *testing.T) {}\n')
            def run(pattern):
                return subprocess.run([sys.executable, str(SCRIPT), 'go', 'test', '-v', '-count=1', '-run', pattern, '.'],
                                      cwd=root, capture_output=True, text=True)
            ok = run('^(TestKept|TestGone)$')
            self.assertEqual(ok.returncode, 0, ok.stdout + ok.stderr)
            bad = run('^(TestRenamed|TestGone)$')
            self.assertEqual(bad.returncode, 1, bad.stdout + bad.stderr)
            self.assertIn('no test ran', bad.stderr)


if __name__ == '__main__':
    unittest.main()
