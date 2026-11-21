"""Only new Python children; never imports the native runner or invokes Go."""
import json
import os
import pathlib
import tempfile
import time
import unittest
from owned_process_evidence import run_logged


class EvidenceTests(unittest.TestCase):
    def test_failure_and_success_records(self):
        cases = {
            'timeout': (['/usr/bin/python3', '-c', "import os,time;os.write(1,b'partial');time.sleep(2)"], TimeoutError),
            'cap': (['/usr/bin/python3', '-c', "import os;os.write(1,b'x'*4097)"], ValueError),
            'spawn': (['/fictional/nonexistent-owned-program'], FileNotFoundError),
            'pin': (['/fictional/must-not-launch'], ValueError),
            'success': (['/usr/bin/python3', '-c', "print('complete')"], None),
        }
        for name, (command, error) in cases.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                root = pathlib.Path(tmp)
                entries, snapshots = [], []
                path = root / 'result.json'
                def save():
                    with path.open('w') as f:
                        json.dump(entries, f)
                        f.flush()
                        os.fsync(f.fileno())
                    snapshots.append(json.loads(path.read_text()))
                def check():
                    self.assertEqual(snapshots[0][0]['state'], 'started')
                    self.assertIsNone(snapshots[0][0]['pid'])
                    if name == 'pin':
                        raise ValueError('fictional pin failure')
                def invoke():
                    return run_logged(command, {'PATH': '/usr/bin:/bin', 'HOME': tmp,
                                                'TMPDIR': tmp, 'LANG': 'C', 'LC_ALL': 'C'},
                                      tmp, entries, save, check, time.monotonic()+3,
                                      timeout=0.5)
                if error:
                    with self.assertRaises(error):
                        invoke()
                else:
                    invoke()
                stored = json.loads(path.read_text())
                self.assertEqual(len(stored), 1)
                row = stored[0]
                self.assertEqual(row['command'], command)
                self.assertEqual(row['state'], 'failed' if error else 'finished')
                if name == 'timeout':
                    self.assertTrue(row['timeout'])
                    self.assertEqual(row['stdout'], 'partial')
                if name == 'cap':
                    self.assertTrue(row['truncated'])
                    self.assertEqual(row['stdout'], 'x'*4096)
                if name in ('timeout', 'cap', 'success'):
                    self.assertIsNotNone(row['pid'])
                    self.assertIsNotNone(row['exit'])
                    self.assertIn(row['cleanup'], ('reaped', 'owned-command-killed-and-reaped'))
                else:
                    self.assertIsNone(row['pid'])
                    self.assertEqual(row['cleanup'], 'not-spawned')
                if name == 'success':
                    self.assertEqual(row['stdout'], 'complete\n')
                    self.assertEqual(row['exit'], 0)

    def test_final_save_does_not_replace_original_failure(self):
        entries, calls = [], []
        def save():
            calls.append(1)
            if len(calls) == 2:
                raise OSError('fictional fsync failure')
        def fail():
            raise ValueError('original pin failure')
        with self.assertRaisesRegex(ValueError, 'original pin failure'):
            run_logged(['/fictional/never-run'], {}, '/', entries, save, fail,
                       time.monotonic()+1)
        self.assertIn('fsync failure', entries[0]['evidence_error'])


if __name__ == '__main__':
    unittest.main()
