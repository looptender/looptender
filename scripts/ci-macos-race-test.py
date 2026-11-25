"""Unit tests for scripts/ci-macos-race.py coverage and verification."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('ci_macos_race', Path(__file__).with_name('ci-macos-race.py'))
macos_race = importlib.util.module_from_spec(spec)
spec.loader.exec_module(macos_race)

plan_spec = importlib.util.spec_from_file_location('ci_plan', Path(__file__).with_name('ci-plan.py'))
ci_plan = importlib.util.module_from_spec(plan_spec)
plan_spec.loader.exec_module(ci_plan)


class MacOSRaceTests(unittest.TestCase):
    def test_current_tree_is_covered(self):
        count = macos_race.verify_coverage()
        self.assertGreater(count, 100)

    def test_all_macos_race_packages_are_in_ci_plan(self):
        covered = set(macos_race.PLATFORM_PACKAGES) | set(macos_race.UNIX_TEST_GLOBS)
        for pkg in sorted(covered):
            prefix = pkg.removeprefix('./') + '/'
            self.assertIn(prefix, ci_plan.MACOS_SENSITIVE_INTERNAL)

    def test_collect_tests_excludes_non_darwin_build_tags_and_suffixes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            cmd = root / 'cmd/looptender'
            cmd.mkdir(parents=True)
            (cmd / 'service_darwin_test.go').write_text('package main\nfunc TestDarwinOnly(t *testing.T) {}\n')
            (cmd / 'service_binding_linux_test.go').write_text('//go:build linux\n\npackage main\nfunc TestLinuxTagged(t *testing.T) {}\n')
            (cmd / 'service_linux_test.go').write_text('package main\nfunc TestLinuxSuffix(t *testing.T) {}\n')
            (cmd / 'service_windows_test.go').write_text('package main\nfunc TestWindowsSuffix(t *testing.T) {}\n')
            (cmd / 'control_freeze_other_test.go').write_text('//go:build !darwin && !linux\n\npackage main\nfunc TestNotDarwinOrLinux(t *testing.T) {}\n')
            (cmd / 'control_freeze_test.go').write_text('//go:build darwin || linux\n\npackage main\nfunc TestDarwinOrLinux(t *testing.T) {}\n')
            (cmd / 'service_cgo_test.go').write_text('//go:build darwin && cgo\n\npackage main\nfunc TestDarwinCgo(t *testing.T) {}\n')
            (cmd / 'service_nocgo_test.go').write_text('//go:build linux || (darwin && !cgo)\n\npackage main\nfunc TestDarwinNoCgo(t *testing.T) {}\n')
            (cmd / 'service_test.go').write_text('package main\nfunc TestPortableService(t *testing.T) {}\n')
            got = macos_race.collect_tests(root, macos_race.CMD_TEST_GLOBS)
            self.assertEqual(got, ['TestDarwinCgo', 'TestDarwinOnly', 'TestDarwinOrLinux', 'TestPortableService'])

    def test_uncovered_darwin_file_or_guard_fails(self):
        samples = [
            ('internal/newpkg/feature_darwin.go', 'package newpkg\n'),
            ('internal/newpkg/feature.go', '//go:build darwin || linux\n\npackage newpkg\n'),
            ('internal/newpkg/guard.go', 'package newpkg\nimport "runtime"\nfunc f() bool { return runtime.GOOS != "darwin" }\n'),
        ]
        for rel, body in samples:
            with self.subTest(rel=rel), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / 'cmd/looptender').mkdir(parents=True)
                (root / 'cmd/looptender/service_darwin_test.go').write_text('package main\nfunc TestFoo(t *testing.T) {}\n')
                target = root / rel
                target.parent.mkdir(parents=True)
                target.write_text(body)
                with self.assertRaisesRegex(ValueError, 'internal/newpkg'):
                    macos_race.verify_coverage(root)

    def test_uncovered_cmd_darwin_guard_test_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'cmd/looptender').mkdir(parents=True)
            (root / 'cmd/looptender/service_darwin_test.go').write_text('package main\nfunc TestFoo(t *testing.T) {}\n')
            (root / 'cmd/looptender/extra_guard_test.go').write_text(
                'package main\nimport ("runtime"; "testing")\nfunc TestExtra(t *testing.T) { if runtime.GOOS != "darwin" { t.Skip() } }\n')
            with self.assertRaisesRegex(ValueError, 'extra_guard_test.go'):
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
