#!/usr/bin/env python3
"""Exercise installed CLIs and real gateway requests with isolated takeover files.

Unlike smoke_test.sh's HTTP fixtures, this checks each actual client process,
its startup configuration, repeated takeover, and byte-exact rollback. No real
client settings or upstream credentials are copied or changed.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def run(args, env, cwd, timeout=120):
    result = subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} exited {result.returncode}: {result.stderr[-2000:]} {result.stdout[-1500:]}")
    return result.stdout


def fingerprint(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.exists() else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, help='Clipal binary built from the checkout')
    parser.add_argument('--upstream', default='http://127.0.0.1:3333/clipal', help='Running gateway with working Claude/OpenAI/Google providers; makes real model requests')
    parser.add_argument('--codex-model', help='Optional model supported by the upstream; defaults to Codex selection')
    parser.add_argument('--claude-model', help='Optional model supported by the upstream; defaults to Claude selection')
    parser.add_argument('--products', default='codex,claude,antigravity', help='Comma-separated clients to verify')
    parser.add_argument('--request-timeout', type=int, default=120, help='Per-client request deadline in seconds')
    parser.add_argument('--output', help='Write the verification report to this JSON file')
    parser.add_argument('--keep-temp', action='store_true')
    args = parser.parse_args()
    products = args.products.split(',')
    if not products or any(p not in ['codex', 'claude', 'antigravity'] for p in products):
        raise ValueError('Unsupported product selection')
    binaries = {name: shutil.which(name) for name in ['codex', 'claude', 'agy']}
    if not all(binaries.values()):
        raise RuntimeError('Install codex, claude, and agy before running this smoke test')
    root = Path(tempfile.mkdtemp(prefix='clipal-cli-takeover-'))
    home, config, work = (root / name for name in ['home', 'config', 'workspace'])
    for directory in [home, config, work]:
        directory.mkdir()
    env = {
        'HOME': str(home), 'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
        'SHELL': '/bin/bash', 'NO_PROXY': '127.0.0.1,localhost',
        'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC': '1', 'DISABLE_TELEMETRY': '1',
        'DISABLE_ERROR_REPORTING': '1', 'DISABLE_AUTOUPDATER': '1', 'DISABLE_UPDATES': '1',
    }
    # Include paths explicitly: background services need not share a client's env.
    codex_dir, claude_dir = root / 'codex-custom', root / 'claude-custom'
    codex_dir.mkdir()
    claude_dir.mkdir()
    env.update(CODEX_HOME=str(codex_dir), CLAUDE_CONFIG_DIR=str(claude_dir))
    originals = {
        codex_dir / 'validation.config.toml': b'# profile under test\n',
        claude_dir / 'settings.json': b'{"env":{"KEEP":"yes"}}\n',
        claude_dir / '.claude.json': b'{"hasCompletedOnboarding":false,"theme":"dark"}\n',
        home / '.gemini' / 'antigravity-cli' / 'settings.json': b'{"theme":"dark"}\n',
        home / '.bashrc': b'# isolated non-login startup\n',
        home / '.bash_profile': b'# isolated login startup without bashrc\n',
    }
    for path, data in originals.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        path.chmod(0o600)
    real_home = Path.home()
    untouched = [real_home / '.codex' / 'config.toml', real_home / '.claude' / 'settings.json',
                 real_home / '.claude.json', real_home / '.gemini' / 'antigravity-cli' / 'settings.json',
                 real_home / '.zshrc', real_home / '.bashrc', real_home / '.bash_profile']
    before = {str(path): fingerprint(path) for path in untouched}
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        port = listener.getsockname()[1]
    (config / 'config.yaml').write_text(f'listen_addr: 127.0.0.1\nport: {port}\nlog_level: info\nupstream_proxy_mode: direct\n')
    # Forward through the user's existing gateway; this test never reads tokens.
    for client in ['claude', 'openai', 'google']:
        (config / f'{client}.yaml').write_text('mode: auto\nproviders:\n  - name: live-gateway\n    base_url: ' + json.dumps(args.upstream) + '\n    api_key: clipal\n    priority: 1\n')
    base = f'http://127.0.0.1:{port}'
    report = {'root': str(root), 'clients': {}, 'upstream': args.upstream, 'success': False}

    def api(path, body=None):
        request = urllib.request.Request(base + path)
        if body is not None:
            request.data = json.dumps(body).encode()
            request.add_header('Content-Type', 'application/json')
            request.add_header('X-Clipal-UI', '1')
            request.add_header('Origin', base)
        with urllib.request.urlopen(request, timeout=40) as response:
            return json.load(response)

    for path in config.glob('*.yaml'):
        path.chmod(0o600)
    active = []
    logfile = (root / 'server.log').open('w')
    server = subprocess.Popen([str(Path(args.binary).resolve()), '--config-dir', str(config)], env=env, cwd=work, stdout=logfile, stderr=subprocess.STDOUT)
    try:
        for _ in range(150):
            if server.poll() is not None:
                raise RuntimeError('Isolated gateway exited: ' + (root / 'server.log').read_text()[-2000:])
            try:
                api('/health')
                break
            except (OSError, urllib.error.URLError):
                time.sleep(.1)
        else:
            raise RuntimeError('Isolated gateway did not become healthy')
        for product, directory, profile in [('codex', codex_dir, 'validation'), ('claude', claude_dir, '')]:
            api(f'/api/integrations/{product}/target', {'config_dir': str(directory), 'profile': profile})
        for product, command in [('codex', 'codex'), ('claude', 'claude'), ('antigravity', 'agy')]:
            if product not in products:
                continue
            version = run([binaries[command], '--version'], env, work, 30).strip()
            api(f'/api/integrations/{product}/apply', {})
            active.append(product)
            api(f'/api/integrations/{product}/apply', {})
            marker = 'CLIPAL_TAKEOVER_' + product.upper() + '_OK'
            prompt = 'Reply with exactly ' + marker + '. Do not use tools.'
            if product == 'codex':
                cmd = [binaries[command], '--profile', 'validation', 'exec', '--skip-git-repo-check', '--ephemeral', '--json']
                if args.codex_model:
                    cmd += ['--model', args.codex_model]
                cmd += [prompt]
            elif product == 'claude':
                cmd = [binaries[command], '-p', prompt, '--output-format', 'json', '--no-session-persistence']
                if args.claude_model:
                    cmd += ['--model', args.claude_model]
            else:
                def shell_args(argv):
                    return ['/bin/bash', '-lc', 'exec "$@"', 'clipal-validation'] + argv
                choices = run(shell_args([binaries[command], 'models']), env, work, 40)
                selectable = {line.split('\t', 1)[0] for line in choices.splitlines() if '\t' in line}
                available = api('/clipal/v1beta/models')['models']
                ids = {entry['name'].removeprefix('models/') for entry in available}
                common = sorted(selectable & ids, key=lambda item: (not item.endswith('-low'), item))
                if not common:
                    raise RuntimeError('No common model in the CLI and upstream catalogs')
                cmd = shell_args([binaries[command], '-p', prompt, '--model', common[0], '--output-format', 'json', '--print-timeout', '90s'])
            output = run(cmd, env, work, args.request_timeout)
            if marker not in output:
                raise RuntimeError(f'{product} did not return its verification marker: {output[-1500:]}')
            client = {'codex': 'openai', 'claude': 'claude', 'antigravity': 'google'}[product]
            outcome = api('/api/status')['clients'][client].get('last_request') or {}
            if outcome.get('status') != 200:
                raise RuntimeError(f'{product} gateway outcome was not HTTP 200: {outcome}')
            api(f'/api/integrations/{product}/rollback', {})
            active.remove(product)
            relevant = [path for path in originals if (product == 'codex' and path.parent == codex_dir) or
                        (product == 'claude' and path.parent == claude_dir) or
                        (product == 'antigravity' and path.is_relative_to(home))]
            for path in relevant:
                if path.read_bytes() != originals[path]:
                    raise RuntimeError(f'{product} rollback changed {path}')
            # A new cycle must capture user edits made after the first rollback.
            modified = {}
            for path in relevant:
                data = originals[path] + (b'\n' if path.suffix == '.json' else b'# second cycle\n')
                path.write_bytes(data)
                modified[path] = data
            api(f'/api/integrations/{product}/apply', {})
            active.append(product)
            api(f'/api/integrations/{product}/rollback', {})
            active.remove(product)
            if any(path.read_bytes() != data for path, data in modified.items()):
                raise RuntimeError(f'{product} second-cycle rollback lost edits')
            report['clients'][product] = {'version': version, 'marker': marker, 'http_status': outcome['status'], 'rollback': 'byte-exact', 'second_cycle': 'preserved'}
            print(product + ': request HTTP 200, rollback and second cycle verified', flush=True)
        report['real_user_files_unchanged'] = all(fingerprint(path) == before[str(path)] for path in untouched)
        if not report['real_user_files_unchanged']:
            raise RuntimeError('Real user client configuration changed during the test')
        report['success'] = True
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        for product in reversed(active):
            try:
                api(f'/api/integrations/{product}/rollback', {})
            except Exception as error:
                report.setdefault('cleanup_errors', []).append(str(error))
        report['real_user_files_unchanged'] = all(fingerprint(path) == before[str(path)] for path in untouched)
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()
        logfile.close()
        if args.output:
            Path(args.output).write_text(json.dumps(report, indent=2) + '\n')
        if not args.keep_temp:
            shutil.rmtree(root)
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
