"""Regressions for ci-windows-inventory.py: a Windows-only test outside every pinned pattern must fail."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('ci-windows-inventory.py')
spec = importlib.util.spec_from_file_location('inv', SCRIPT)
inv = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inv)


class InventoryTests(unittest.TestCase):
    def test_build_constraints_and_file_suffixes(self):
        only = lambda name, text: inv.windows_only(Path(name), text)
        self.assertTrue(only('a_windows_test.go', 'package p\n'))
        self.assertTrue(only('a_test.go', '//go:build windows\n\npackage p\n'))
        self.assertTrue(only('a_test.go', '//go:build windows && !race\n\npackage p\n'))
        self.assertFalse(only('a_test.go', 'package p\n'))
        self.assertFalse(only('a_unix_test.go', '//go:build !windows\n\npackage p\n'))
        self.assertFalse(only('a_test.go', '//go:build windows || linux\n\npackage p\n'))
        self.assertFalse(only('a_linux_test.go', 'package p\n'))
        self.assertFalse(only('a_test.go', '// //go:build windows\npackage p\n'))

    def test_selection_by_package_and_pattern(self):
        tests = [('./pkg/a', 'TestWindowsOne', 'f'), ('./pkg/a', 'TestRenamed', 'f'), ('./pkg/b', 'TestWindowsTwo', 'f')]
        commands = [(['./pkg/a', './pkg/c'], '^TestWindows'), (['./pkg/b'], None)]
        self.assertEqual([t[1] for t in inv.uncovered(tests, commands)], ['TestRenamed'])
        # A pattern listed for another package does not cover this one.
        self.assertEqual(len(inv.uncovered([('./pkg/a', 'TestWindowsOne', 'f')], [(['./pkg/b'], '^TestWindows')])), 1)

    def test_real_workflow_parses_and_covers_the_real_tests(self):
        commands = inv.steps(inv.WORKFLOW.read_text())
        self.assertGreater(len(commands), 20)
        self.assertTrue(any(p == '^TestClient' for _, p in commands))
        tests = inv.inventory()
        self.assertGreater(len(tests), 20)
        self.assertEqual(inv.uncovered(tests, commands), [])

    def test_script_exits_nonzero_and_names_the_test_when_one_is_unselected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / '.github/workflows').mkdir(parents=True)
            (root / 'pkg').mkdir()
            (root / 'pkg/w_windows_test.go').write_text('package pkg\nimport "testing"\nfunc TestKept(t *testing.T) {}\nfunc TestRenamedAway(t *testing.T) {}\n')
            workflow = ('jobs:\n  windows-acl:\n    steps:\n      - run: go test ./pkg -run \'^TestKept$\' -count=1\n'
                        '  windows-processes:\n    steps:\n      - run: go test ./other -count=1\n')
            (root / '.github/workflows/checks.yml').write_text(workflow)
            result = subprocess.run([sys.executable, str(SCRIPT)], capture_output=True, text=True,
                                    env={**os.environ, 'CI_WINDOWS_INVENTORY_ROOT': str(root)})
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn('TestRenamedAway', result.stderr)
            self.assertNotIn('TestKept ', result.stderr)


if __name__ == '__main__':
    unittest.main()
