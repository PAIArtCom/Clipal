#!/usr/bin/env python3
"""Discover and verify text models through Clipal's Gemini ingress (stdlib only)."""
import argparse
import ipaddress
import json
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class LiveFailure(RuntimeError):
    pass


def candidates(document, requested='', stream=True):
    if not isinstance(document, dict):
        raise LiveFailure('models response is not an object')
    models = document.get('models')
    if not isinstance(models, list):
        raise LiveFailure('models response has no model list')
    found = set()
    for item in models:
        if not isinstance(item, dict):
            continue
        name = item.get('name')
        methods = item.get('supportedGenerationMethods', [])
        if not isinstance(name, str) or not isinstance(methods, list):
            continue
        name = name.removeprefix('models/')
        if not name or 'generateContent' not in methods:
            continue
        if stream and 'streamGenerateContent' not in methods:
            continue
        # The catalog may also advertise image/audio/video/embedding-only models.
        description = (name + ' ' + str(item.get('displayName', ''))).lower()
        if any(word in description for word in ('image', 'imagen', 'veo', 'video', 'audio', 'tts', 'embedding', 'disabled')):
            continue
        found.add(name)
    if requested:
        requested = requested.removeprefix('models/')
        if requested not in found:
            raise LiveFailure('requested model is not a listed text generation candidate: ' + requested)
        return [requested]
    # Prefer inexpensive text models, without encoding a model generation/version.
    ordered = sorted(found, key=lambda name: ('flash' not in name.lower(), name))
    if not ordered:
        raise LiveFailure('model list contains no compatible text generation candidates')
    return ordered


def verify(body, content_type, token, stream=False):
    if stream:
        if 'text/event-stream' not in content_type.lower():
            raise LiveFailure('stream missing text/event-stream content-type')
        events, lines = [], []
        for line in body.splitlines():
            if not line:
                if lines:
                    data = '\n'.join(lines)
                    if data != '[DONE]':
                        events.append(json.loads(data))
                    lines = []
            elif line.startswith('data:'):
                lines.append(line[5:].removeprefix(' '))
        if lines:
            raise LiveFailure('stream ended before an SSE frame was complete')
        if not events:
            raise LiveFailure('stream contains no data frames')
    else:
        events = [json.loads(body)]
    texts, finishes = [], []
    stopped = False
    for event in events:
        if not isinstance(event, dict):
            raise LiveFailure('response event is not an object')
        if event.get('error'):
            raise LiveFailure('response contains an error: ' + json.dumps(event['error']))
        choices = event.get('candidates', [])
        if not isinstance(choices, list) or len(choices) > 1:
            raise LiveFailure('expected a single response candidate')
        for candidate in choices:
            if not isinstance(candidate, dict) or candidate.get('index', 0) != 0:
                raise LiveFailure('invalid response candidate')
            if stopped:
                raise LiveFailure('candidate data arrived after terminal STOP')
            content = candidate.get('content', {})
            if not isinstance(content, dict) or not isinstance(content.get('parts', []), list):
                raise LiveFailure('invalid response content')
            for part in content.get('parts', []):
                if not isinstance(part, dict) or not isinstance(part.get('text', ''), str):
                    raise LiveFailure('invalid response part')
                if not part.get('thought'):
                    texts.append(part.get('text', ''))
            finish = candidate.get('finishReason')
            if finish:
                if finish != 'STOP':
                    raise LiveFailure('generation ended with ' + str(finish))
                finishes.append(finish)
                stopped = True
    text = ''.join(texts).strip()
    if text != token or not stopped:
        raise LiveFailure('generation did not return the expected text and STOP: ' + repr(text[:400]))
    return {'text': text, 'finish_reasons': finishes, 'event_count': len(events)}


def retired(status, body):
    # Only explicit lifecycle errors permit another candidate. Never mask auth,
    # network, quota, malformed responses, or arbitrary model validation failures.
    if status not in (200, 400, 404, 410):
        return False
    return bool(re.search(r'no longer available|has been (?:retired|discontinued)|model[^\n]{0,100}(?:is retired|is no longer supported)', body, re.I))


def request(base, path, data=None, method=None):
    raw = None if data is None else json.dumps(data).encode()
    req = urllib.request.Request(base + path, data=raw, method=method, headers={
        'Content-Type': 'application/json', 'X-Clipal-UI': '1',
        'User-Agent': 'GeminiCLI/Clipal-live-test', 'Authorization': 'Bearer clipal-placeholder-token'})
    # Local gateway traffic must not be sent to an environment or system proxy.
    # Classify literal addresses only; resolving remote names would change routing.
    host = urllib.parse.urlsplit(base).hostname or ''
    try:
        loopback = ipaddress.ip_address(host).is_loopback
    except ValueError:
        loopback = host.lower().rstrip('.') == 'localhost'
    open_request = (urllib.request.build_opener(urllib.request.ProxyHandler({})).open
                    if loopback else urllib.request.urlopen)
    started = time.monotonic()
    for attempt in range(5):
        try:
            with open_request(req, timeout=120) as response:
                return response.status, response.read().decode(), response.headers.get('Content-Type', ''), round(time.monotonic() - started, 2)
        except urllib.error.HTTPError as error:
            body = error.read().decode()
            if data is None or error.code not in (429, 503) or attempt == 4:
                return error.code, body, error.headers.get('Content-Type', ''), round(time.monotonic() - started, 2)
            try:
                delay = max(1, min(30, int(float(error.headers.get('Retry-After', '2')))))
            except ValueError:
                delay = 2
            print('HTTP %s; retrying in %ss (%s/5)' % (error.code, delay, attempt + 1), file=sys.stderr)
            time.sleep(delay)


def run(base, model='', stream=True, token='CLIPAL_LIVE_OK', call=request):
    status, body, _, elapsed = call(base, '/gemini/v1beta/models')
    report = {'models_http_status': status, 'models_elapsed_seconds': elapsed, 'attempts': []}
    if status != 200:
        raise LiveFailure('model listing failed: HTTP ' + str(status))
    listed = candidates(json.loads(body), model, stream)
    report['candidates'] = listed
    payload = {'contents': [{'role': 'user', 'parts': [{'text': 'Reply with exactly ' + token + ' and nothing else.'}]}],
               'generationConfig': {'maxOutputTokens': 4096}}
    for selected in listed:
        attempt = {'model': selected, 'checks': []}
        report['attempts'].append(attempt)
        unavailable = False
        for streaming in ([False, True] if stream else [False]):
            suffix = ':streamGenerateContent?alt=sse' if streaming else ':generateContent'
            status, body, content_type, elapsed = call(base, '/clipal/v1beta/models/' + urllib.parse.quote(selected, safe='') + suffix, payload)
            check = {'check': 'stream' if streaming else 'generate', 'http_status': status, 'elapsed_seconds': elapsed}
            attempt['checks'].append(check)
            if retired(status, body):
                check['result'] = 'retired'
                unavailable = True
                break
            if status != 200:
                raise LiveFailure('generation failed: HTTP ' + str(status) + ' model=' + selected)
            check.update(verify(body, content_type, token, streaming))
            check['result'] = 'passed'
        if not unavailable:
            report.update({'selected_model': selected, 'passed': True})
            return report
        if model:
            raise LiveFailure('explicitly selected model is retired: ' + selected)
    raise LiveFailure('all listed text generation candidates returned retirement errors')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', required=True)
    parser.add_argument('--provider', default='', help='Temporarily pin this Gemini provider; restore routing afterward')
    parser.add_argument('--model', default='', help='Require a specific listed text model (default: discover)')
    parser.add_argument('--skip-stream', action='store_true')
    parser.add_argument('--token', default='CLIPAL_LIVE_OK')
    args = parser.parse_args()
    base = args.base_url.rstrip('/')
    original = None
    report = None
    try:
        if args.provider:
            status, body, _, _ = request(base, '/api/client-config/gemini')
            if status != 200:
                raise LiveFailure('cannot read Gemini routing')
            config = json.loads(body)
            original = {k: config.get(k, '') for k in ('mode', 'pinned_provider')}
            status, _, _, _ = request(base, '/api/client-config/gemini', {'mode': 'manual', 'pinned_provider': args.provider}, 'PUT')
            if status != 200:
                raise LiveFailure('cannot pin Gemini provider')
        report = run(base, args.model, not args.skip_stream, args.token)
        report['provider'] = args.provider
    finally:
        if original is not None:
            status, body, _, _ = request(base, '/api/client-config/gemini')
            if status != 200:
                raise LiveFailure("cannot verify routing for restoration; test is incomplete")
            current = json.loads(body)
            if current.get('mode') == 'manual' and current.get('pinned_provider') == args.provider:
                status, _, _, _ = request(base, '/api/client-config/gemini', original, 'PUT')
                if status != 200:
                    raise LiveFailure('failed to restore Gemini routing')
            elif report is not None:
                raise LiveFailure('routing changed during the test; selected account is unverified')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    try:
        main()
    except (LiveFailure, ValueError, OSError) as error:
        print('live Gemini test failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
