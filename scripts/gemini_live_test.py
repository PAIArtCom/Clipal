import json
import os
from pathlib import Path
import shutil
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import unittest
from unittest.mock import patch

import gemini_live as live


def model(name, methods=None):
    return {'name': 'models/' + name, 'supportedGenerationMethods': methods if methods is not None else ['generateContent', 'streamGenerateContent']}


def response(text='CLIPAL_LIVE_OK', finish='STOP', thought=False):
    return json.dumps({'candidates': [{'content': {'parts': [{'text': text, 'thought': thought}]}, 'finishReason': finish}]})


class HTTPFixture:
    """One local HTTP fixture for gateway and forward-proxy regression tests."""
    def __init__(self, reject=False):
        self.requests = []
        self.routing = {'mode': 'auto', 'pinned_provider': ''}
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def handle_request(self):
                length = int(self.headers.get('Content-Length', '0'))
                data = json.loads(self.rfile.read(length)) if length else None
                fixture.requests.append((self.command, self.path, data))
                content_type = 'application/json'
                if reject:
                    status, body = 502, '{}'
                elif self.path == '/api/client-config/gemini':
                    if self.command == 'PUT':
                        fixture.routing = data
                    status, body = 200, json.dumps(fixture.routing)
                elif self.path.endswith('/gemini/v1beta/models'):
                    status, body = 200, json.dumps({'models': [model('future-flash')]})
                elif ':streamGenerateContent' in self.path:
                    status, body = 200, 'data: ' + response() + '\n\n'
                    content_type = 'text/event-stream'
                else:
                    status, body = 200, response()
                raw = body.encode()
                self.send_response(status)
                self.send_header('Content-Type', content_type)
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            do_GET = do_POST = do_PUT = handle_request

            def log_message(self, *args):
                pass

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.url = 'http://127.0.0.1:' + str(self.server.server_port)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()


def proxy_environment(proxy_url):
    # Keep runtime discovery, but isolate account/model/proxy settings from the host.
    env = {key: value for key, value in os.environ.items()
           if not key.lower().endswith('_proxy') and not key.startswith('CLIPAL_LIVE_')}
    env['HTTP_PROXY'] = proxy_url
    env['HTTPS_PROXY'] = proxy_url
    return env


class TransportTests(unittest.TestCase):
    def test_loopback_ignores_proxy_but_remote_uses_it(self):
        with HTTPFixture() as target, HTTPFixture(reject=True) as proxy:
            with patch.dict(os.environ, proxy_environment(proxy.url), clear=True):
                # Rebuild the default opener so host-side urllib state cannot hide proxy use.
                with patch.object(live.urllib.request, '_opener', None):
                    self.assertTrue(live.run(target.url)['passed'])
                    self.assertEqual(len(target.requests), 3)
                    self.assertEqual(proxy.requests, [])
                    status, _, _, _ = live.request('http://remote.invalid', '/gemini/v1beta/models')
                    self.assertEqual(status, 502)
                    self.assertEqual(proxy.requests[0][1], 'http://remote.invalid/gemini/v1beta/models')

    def test_loopback_host_boundaries(self):
        for base, direct in [('http://127.0.0.1', True), ('http://127.23.45.67', True),
                             ('http://[::1]:3333', True), ('http://LOCALHOST.:3333', True),
                             ('http://localhost.evil.invalid', False), ('http://192.168.1.1', False),
                             ('https://remote.invalid', False)]:
            with self.subTest(base=base):
                opener = unittest.mock.MagicMock()
                reply = opener.open.return_value.__enter__.return_value
                reply.status, reply.read.return_value = 200, b'{}'
                with patch.object(live.urllib.request, 'build_opener', return_value=opener) as build, \
                     patch.object(live.urllib.request, 'urlopen') as default:
                    default.return_value.__enter__.return_value = reply
                    live.request(base, '/test')
                self.assertEqual(build.called, direct)
                self.assertEqual(default.called, not direct)

    @unittest.skipUnless(Path('/bin/bash').exists() and shutil.which('curl'), 'requires bash and curl')
    def test_installed_service_shell_with_and_without_provider(self):
        for provider in ('', 'new-paid-account'):
            with self.subTest(provider=provider), HTTPFixture() as target, HTTPFixture(reject=True) as proxy:
                command = ['/bin/bash', str(Path(__file__).with_name('live_gemini_oauth_smoke.sh')),
                           '--base-url', target.url]
                if provider:
                    command += ['--provider', provider]
                result = subprocess.run(command, env=proxy_environment(proxy.url), capture_output=True,
                                        text=True, timeout=20)
                self.assertEqual(result.returncode, 0, result.stderr)
                report = json.loads(result.stdout)
                self.assertTrue(report['passed'])
                self.assertEqual(report['provider'], provider)
                self.assertEqual(len(report['attempts'][0]['checks']), 2)
                self.assertEqual(proxy.requests, [])
                self.assertEqual(target.routing, {'mode': 'auto', 'pinned_provider': ''})
                writes = [data for method, _, data in target.requests if method == 'PUT']
                self.assertEqual(writes, [{'mode': 'manual', 'pinned_provider': provider},
                                          {'mode': 'auto', 'pinned_provider': ''}] if provider else [])


class LiveTests(unittest.TestCase):
    def test_future_catalog_drives_both_requests(self):
        paths = []

        def call(base, path, data=None):
            paths.append(path)
            if len(paths) == 1:
                return 200, json.dumps({'models': [model('future-flash-z')]}), 'application/json', 0
            if 'streamGenerateContent' in path:
                return 200, 'data: ' + response() + '\n\n', 'text/event-stream', 0
            return 200, response(), 'application/json', 0

        result = live.run('http://local', call=call)
        self.assertTrue(result['passed'])
        self.assertEqual(paths, ['/gemini/v1beta/models', '/clipal/v1beta/models/future-flash-z:generateContent', '/clipal/v1beta/models/future-flash-z:streamGenerateContent?alt=sse'])

    def test_catalog_filters_methods_and_non_text_models(self):
        data = {'models': [model('future-image'), model('future-audio'), model('future-embedding'), model('future-text', ['generateContent']), model('future-flash'), model('future-flash')]}
        self.assertEqual(live.candidates(data), ['future-flash'])
        self.assertEqual(set(live.candidates(data, stream=False)), {'future-text', 'future-flash'})

    def test_no_candidates_never_uses_a_default(self):
        for data in ({}, {'models': []}, {'models': [model('future-image')]}):
            with self.subTest(data=data), self.assertRaises(live.LiveFailure):
                live.candidates(data)

    def test_explicit_model_must_be_listed_and_compatible(self):
        data = {'models': [model('future-flash')]}
        self.assertEqual(live.candidates(data, 'models/future-flash'), ['future-flash'])
        with self.assertRaises(live.LiveFailure):
            live.candidates(data, 'missing-old-model')

    def test_list_failure_never_generates(self):
        paths = []

        def call(base, path, data=None):
            paths.append(path)
            return 403, '{}', 'application/json', 0

        with self.assertRaises(live.LiveFailure):
            live.run('http://local', call=call)
        self.assertEqual(paths, ['/gemini/v1beta/models'])

    def test_retired_candidate_is_recorded_and_next_listed_model_is_verified(self):
        paths = []

        def call(base, path, data=None):
            paths.append(path)
            if len(paths) == 1:
                return 200, json.dumps({'models': [model('flash-a'), model('flash-b')]}), 'application/json', 0
            if 'flash-a:' in path:
                return 200, response('This model is no longer available.', None), 'application/json', 0
            return 200, response(), 'application/json', 0

        result = live.run('http://local', stream=False, call=call)
        self.assertEqual(result['selected_model'], 'flash-b')
        self.assertEqual(result['attempts'][0]['checks'][0]['result'], 'retired')

    def test_auth_and_invalid_output_do_not_trigger_model_fallback(self):
        for status, body in [(403, response('no longer available')), (200, response('wrong')), (200, response(finish=None))]:
            paths = []

            def call(base, path, data=None):
                paths.append(path)
                if len(paths) == 1:
                    return 200, json.dumps({'models': [model('flash-a'), model('flash-b')]}), 'application/json', 0
                return status, body, 'application/json', 0

            with self.subTest(status=status, body=body), self.assertRaises(live.LiveFailure):
                live.run('http://local', stream=False, call=call)
            self.assertEqual(len(paths), 2)

    def test_stream_requires_valid_frames_content_type_and_terminal_stop(self):
        for body, content_type in [('data: ' + response(finish=None), 'text/event-stream'), ('data: ' + response(), 'application/json'), ('data: {broken}', 'text/event-stream'), ('', 'text/event-stream')]:
            with self.subTest(body=body), self.assertRaises((live.LiveFailure, ValueError)):
                live.verify(body, content_type, 'CLIPAL_LIVE_OK', True)
        chunks = 'data: ' + response('CLIPAL_', None) + '\n\ndata: ' + response('LIVE_OK') + '\n\n'
        self.assertEqual(live.verify(chunks, 'text/event-stream', 'CLIPAL_LIVE_OK', True)['text'], 'CLIPAL_LIVE_OK')
        with self.assertRaises(live.LiveFailure):
            live.verify(response(thought=True), 'application/json', 'CLIPAL_LIVE_OK')

    def test_explicit_retired_model_does_not_switch(self):
        def call(base, path, data=None):
            if data is None:
                return 200, json.dumps({'models': [model('flash-a'), model('flash-b')]}), 'application/json', 0
            return 410, 'model has been retired', 'application/json', 0
        with self.assertRaisesRegex(live.LiveFailure, 'explicitly selected'):
            live.run('http://local', 'flash-a', False, call=call)

    def test_malformed_catalog_and_candidate_shapes_fail_cleanly(self):
        for data in (None, [], {'models': [{'name': None}, {'name': 'future-text', 'supportedGenerationMethods': None}]}):
            with self.subTest(data=data), self.assertRaises(live.LiveFailure):
                live.candidates(data)
        for data in ([], {'candidates': None}, {'candidates': [None]}, {'candidates': [{'content': None}]}):
            with self.subTest(data=data), self.assertRaises(live.LiveFailure):
                live.verify(json.dumps(data), 'application/json', 'CLIPAL_LIVE_OK')

    def test_sse_supports_multiline_data_and_rejects_truncated_frame(self):
        body = response()
        with self.assertRaises(live.LiveFailure):
            live.verify('data: ' + body, 'text/event-stream', 'CLIPAL_LIVE_OK', True)
        split = body.index('"content"')
        frames = ': heartbeat\n\ndata: ' + body[:split] + '\ndata: ' + body[split:] + '\n\n'
        self.assertEqual(live.verify(frames, 'text/event-stream', 'CLIPAL_LIVE_OK', True)['text'], 'CLIPAL_LIVE_OK')

    def test_routing_read_failure_cannot_report_success(self):
        calls = []

        def call(base, path, data=None, method=None):
            calls.append(method)
            if len(calls) == 1:
                return 200, '{"mode":"auto","pinned_provider":""}', 'application/json', 0
            if len(calls) == 2:
                return 200, '{}', 'application/json', 0
            return 503, '{"error":"unavailable"}', 'application/json', 0

        with patch.object(live, 'request', side_effect=call), patch.object(live, 'run', return_value={'passed': True}), patch('sys.argv', ['gemini_live.py', '--base-url', 'http://local', '--provider', 'paid-account']):
            with self.assertRaises(live.LiveFailure):
                live.main()

    def test_stop_must_finish_the_stream_and_not_a_different_candidate(self):
        stopped = json.loads(response())
        broken = json.loads(response('', 'MAX_TOKENS'))
        with self.assertRaises(live.LiveFailure):
            live.verify('data: ' + json.dumps(stopped) + '\n\ndata: ' + json.dumps(broken) + '\n\n', 'text/event-stream', 'CLIPAL_LIVE_OK', True)
        stopped['candidates'].append(broken['candidates'][0])
        with self.assertRaises(live.LiveFailure):
            live.verify(json.dumps(stopped), 'application/json', 'CLIPAL_LIVE_OK')

    def test_installed_service_routing_restored_on_failure_without_overwriting_user_edit(self):
        for concurrent_edit in (False, True):
            writes = []
            reads = [0]

            def call(base, path, data=None, method=None):
                if method == 'PUT':
                    writes.append(data)
                    return 200, '{}', 'application/json', 0
                reads[0] += 1
                state = {'mode': 'auto', 'pinned_provider': ''} if reads[0] == 1 else {'mode': 'manual', 'pinned_provider': 'other' if concurrent_edit else 'paid-account'}
                return 200, json.dumps(state), 'application/json', 0

            with patch.object(live, 'request', side_effect=call), patch.object(live, 'run', side_effect=live.LiveFailure('failed')), patch('sys.argv', ['gemini_live.py', '--base-url', 'http://local', '--provider', 'paid-account']):
                with self.assertRaises(live.LiveFailure):
                    live.main()
            self.assertEqual(len(writes), 1 if concurrent_edit else 2)
            if not concurrent_edit:
                self.assertEqual(writes[-1], {'mode': 'auto', 'pinned_provider': ''})


if __name__ == '__main__':
    unittest.main()
