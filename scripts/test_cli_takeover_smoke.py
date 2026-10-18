"""Negative controls for the installed-client verification harness."""
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import cli_takeover_smoke as smoke


class SmokeVerificationTests(unittest.TestCase):
    def test_prompt_echo_and_error_are_not_assistant_success(self):
        marker = 'CLIPAL_TEST_OK'
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            for output in [json.dumps([{'type': 'system', 'prompt': marker}]), json.dumps([{'type': 'result', 'subtype': 'error', 'result': marker}])]:
                with self.assertRaises(RuntimeError):
                    smoke.validate_client_response('qwen', output, marker, work)
            valid = json.dumps([{'type': 'result', 'subtype': 'success', 'result': marker}])
            smoke.validate_client_response('qwen', valid, marker, work)
            smoke.validate_client_response('deepseek-harness', json.dumps({'type': 'final', 'text': marker}), marker, work)
            with self.assertRaises(RuntimeError):
                smoke.validate_client_response('deepseek-harness', json.dumps({'type': 'text', 'text': marker}), marker, work)

    def test_raw_clients_require_exact_marker_not_echoed_prompt(self):
        marker = 'CLIPAL_TEST_OK'
        for product in ['pi', 'copilot', 'crush', 'iflow']:
            with self.subTest(product=product):
                with self.assertRaises(RuntimeError):
                    smoke.validate_client_response(product, 'Reply with exactly ' + marker + '. Do not use tools.', marker, Path('.'))
                smoke.validate_client_response(product, 'diagnostic\n' + marker + '\n', marker, Path('.'))

    def test_trae_must_finish_and_have_real_assistant_content(self):
        marker = 'CLIPAL_TEST_OK'
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            path = work / 'trae-trajectory.json'
            for success, content in [(False, marker), (True, '')]:
                path.write_text(json.dumps({'success': success, 'task': marker, 'llm_interactions': [{'response': {'content': content}}]}))
                with self.assertRaises(RuntimeError):
                    smoke.validate_client_response('trae', marker, marker, work)
            path.write_text(json.dumps({'success': True, 'llm_interactions': [{'response': {'content': marker}}]}))
            smoke.validate_client_response('trae', marker, marker, work)

    def test_arguments_do_not_expand_shell_metacharacters(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            marker = work / 'executed'
            payload = "'\"$(touch " + str(marker) + ")`touch " + str(marker) + '`\nsecond line'
            result = smoke.run([sys.executable, '-c', 'import sys; print(sys.argv[1],end="")', payload], {'PATH': '/usr/bin:/bin'}, work)
            self.assertEqual(result, payload)
            self.assertFalse(marker.exists())

    @unittest.skipUnless(hasattr(os, 'fork'), 'Unix process-group behavior')
    def test_timeout_stops_descendants_before_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            marker = work / 'late-write'
            program = 'import os,time,pathlib; child=os.fork(); time.sleep(.3 if child==0 else 10); pathlib.Path(' + repr(str(marker)) + ').touch()'
            with self.assertRaisesRegex(RuntimeError, 'timed out'):
                smoke.run([sys.executable, '-c', program], {'PATH': '/usr/bin:/bin'}, work, timeout=.1)
            time.sleep(.35)
            self.assertFalse(marker.exists())

    def test_fingerprint_catches_permission_only_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'config'
            path.write_text('unchanged')
            path.chmod(0o600)
            before = smoke.fingerprint(path)
            path.chmod(0o640)
            self.assertNotEqual(before, smoke.fingerprint(path))

    def test_cleanup_failure_is_persisted_as_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'workspace'
            root.mkdir()
            output = Path(directory) / 'result.json'
            report = {'success': True}
            with mock.patch.object(smoke.shutil, 'rmtree', side_effect=OSError('injected removal failure')):
                smoke.finalize_report(report, root, False, output)
            saved = json.loads(output.read_text())
            self.assertFalse(saved['success'])
            self.assertIn('injected removal failure', saved['cleanup_errors'][0])
            smoke.finalize_report({'success': True}, root, False, output)
            self.assertTrue(json.loads(output.read_text())['success'])
            self.assertFalse(root.exists())

    def test_loopback_does_not_inherit_external_proxy(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'isolated')

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with mock.patch.dict(os.environ, {'HTTP_PROXY': 'http://127.0.0.1:1', 'http_proxy': 'http://127.0.0.1:1', 'NO_PROXY': '', 'no_proxy': ''}):
                with smoke.open_http('http://127.0.0.1:' + str(server.server_port), timeout=3) as response:
                    self.assertEqual(response.read(), b'isolated')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    unittest.main()
