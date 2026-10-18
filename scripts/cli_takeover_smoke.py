#!/usr/bin/env python3
"""Exercise installed CLIs and real gateway requests with isolated takeover files.

Unlike smoke_test.sh's HTTP fixtures, this checks each actual client process,
its startup configuration, repeated takeover, and byte-exact rollback. No real
client settings or upstream credentials are copied or changed.
"""
import argparse
import hashlib
import stat
import ipaddress
from urllib.parse import urlparse
import json
import os
from pathlib import Path
import shutil
import socket
import signal
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def run(args, env, cwd, timeout=120):
    process = subprocess.Popen(args, env=env, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        # Native launchers may spawn a runtime. Stop its entire isolated process
        # group before rollback so a child cannot keep using restored files.
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
        raise RuntimeError(f"{Path(args[0]).name} timed out after {timeout}s: {stderr[-1500:]} {stdout[-1500:]}")
    if process.returncode:
        raise RuntimeError(f"{Path(args[0]).name} exited {process.returncode}: {stderr[-2000:]} {stdout[-1500:]}")
    return stdout


def fingerprint(path):
    if not path.exists():
        return None
    return {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'mode': stat.S_IMODE(path.stat().st_mode), 'symlink': os.readlink(path) if path.is_symlink() else None}


# Additional products share the existing lifecycle; only loader paths and launch
# contracts differ. Keep endpoint/model values in generated takeover plans.
NEW_CLIENTS = {
    'pi': ('pi', 'PI_CODING_AGENT_DIR', {'models.json': b'{}\n', 'settings.json': b'{}\n'}),
    'qwen': ('qwen', 'QWEN_HOME', {'settings.json': b'{}\n'}),
    'kimi': ('kimi', 'KIMI_CODE_HOME', {'config.toml': b'# isolated validation\n'}),
    'copilot': ('copilot', 'COPILOT_HOME', {}),
    'kilo': ('kilo', 'KILO_CONFIG_DIR', {'kilo.jsonc': b'{"permission":{"*":"deny"}}\n'}),
    'codebuddy': ('codebuddy', 'CODEBUDDY_CONFIG_DIR', {'settings.json': b'{}\n', 'models.json': b'{}\n'}),
    'crush': ('crush', 'CRUSH_GLOBAL_CONFIG', {'crush.json': b'{}\n'}),
    'iflow': ('iflow', None, {'settings.json': b'{}\n'}),
    'trae': ('trae-cli', None, {'trae_config.yaml': b'{}\n'}),
    'grok': ('grok', 'GROK_HOME', {'config.toml': b'# isolated validation\n'}),
    'deepseek-harness': ('dsh', 'DSH_HOME', {'cordis.patch.yml': b'[]\n'}),
}


def open_http(request, timeout):
    # Loopback management traffic must not inherit an external HTTP proxy.
    url = request.full_url if isinstance(request, urllib.request.Request) else request
    host = urlparse(url).hostname
    try:
        local = host == 'localhost' or ipaddress.ip_address(host).is_loopback
    except ValueError:
        local = False
    if local:
        return urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=timeout)
    return urllib.request.urlopen(request, timeout=timeout)


def new_client_command(product, binary, prompt, paths, work):
    if product == 'pi':
        return [binary, '-p', '--no-tools', '--no-extensions', '--no-mcp', '--no-skills', '--no-context-files', '--no-session', '--offline', prompt]
    if product == 'qwen':
        return [binary, '-p', prompt, '--output-format', 'json', '--max-session-turns', '1', '--max-tool-calls', '0', '--max-wall-time', '90s']
    if product == 'kimi':
        return [binary, '-p', prompt, '--output-format', 'stream-json']
    if product == 'copilot':
        # Official noninteractive mode requires this flag. The empty workspace
        # and no-tools prompt bound the smoke; no broad path permission is set.
        return ['/bin/bash', '-lc', 'exec "$@"', 'clipal-validation', binary, '-p', prompt, '--silent', '--allow-all-tools', '--disable-builtin-mcps', '--no-custom-instructions', '--no-auto-update']
    if product == 'kilo':
        return [binary, 'run', '--pure', '--format', 'json', prompt]
    if product == 'codebuddy':
        return [binary, '-p', prompt, '--output-format', 'json', '--max-turns', '1', '--tools', '', '--no-session-persistence', '--setting-sources', 'user']
    if product == 'crush':
        return [binary, 'run', '--quiet', '--cwd', str(work), prompt]
    if product == 'iflow':
        return [binary, '-p', prompt, '--max-turns', '1', '--timeout', '90', '--telemetry=false', '--stream=false']
    if product == 'trae':
        return [binary, 'run', prompt, '--config-file', str(paths[0]), '--working-dir', str(work), '--max-steps', '2', '--console-type', 'simple', '--trajectory-file', str(work / 'trae-trajectory.json')]
    if product == 'grok':
        return [binary, '-p', prompt, '--output-format', 'json', '--max-turns', '1', '--no-subagents', '--disable-web-search']
    if product == 'deepseek-harness':
        return [binary, 'headless', '--json', prompt]
    raise ValueError('Unknown client')


def validate_client_response(product, output, marker, work):
    """Accept assistant output, never a prompt echo or unsuccessful final state."""
    if product == 'trae':
        trajectory = json.loads((work / 'trae-trajectory.json').read_text())
        if trajectory.get('success') is not True:
            raise RuntimeError('TRAE did not complete: ' + str(trajectory.get('final_result')))
        replies = [step.get('response', {}).get('content') or '' for step in trajectory.get('llm_interactions', [])]
    elif product in {'pi', 'copilot', 'crush', 'iflow'}:
        replies = output.splitlines()
    elif product in NEW_CLIENTS:
        try:
            value = json.loads(output)
            events = value if isinstance(value, list) else [value]
        except json.JSONDecodeError:
            events = [json.loads(line) for line in output.splitlines() if line.strip()]
        replies = []
        for event in events:
            if event.get('type') == 'error' or event.get('is_error'):
                raise RuntimeError('Client returned an error event')
            if event.get('type') == 'result':
                if event.get('subtype') != 'success':
                    raise RuntimeError('Client result is not successful')
                replies.append(event.get('result', ''))
            if event.get('role') == 'assistant':
                replies.append(event.get('content', ''))
            message = event.get('message', {})
            if isinstance(message, dict) and message.get('role') == 'assistant':
                content = message.get('content', '')
                replies.extend([content] if isinstance(content, str) else [part.get('text', '') for part in content if part.get('type') == 'text'])
            if product == 'kilo' and event.get('type') == 'text':
                replies.append(event.get('part', {}).get('text', ''))
            if product == 'grok' and event.get('stopReason') == 'end_turn':
                replies.append(event.get('text', ''))
            if product == 'deepseek-harness' and event.get('type') == 'final':
                replies.append(event.get('text', ''))
    else:
        # Preserve the existing seven-client smoke output contract.
        replies = [output]
    matched = any((reply.strip() == marker if product in NEW_CLIENTS else marker in reply) for reply in replies if isinstance(reply, str))
    if not matched:
        raise RuntimeError(f'{product} did not return its assistant verification marker')



def finalize_report(report, root, keep_temp, output):
    # Persist only after cleanup so machine-readable success reflects every
    # cleanup failure, including removal of the private validation directory.
    if not keep_temp:
        try:
            shutil.rmtree(root)
        except OSError as error:
            report.setdefault('cleanup_errors', []).append('Cannot remove private workspace: ' + str(error))
            report['success'] = False
    if output:
        Path(output).write_text(json.dumps(report, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, help='Clipal binary built from the checkout')
    parser.add_argument('--upstream', default='http://127.0.0.1:3333/clipal', help='Running gateway with working Claude/OpenAI/Google providers; makes real model requests')
    parser.add_argument('--model', help='Verified gateway model ID for clients with selectable models; otherwise discover from the upstream catalog')
    parser.add_argument('--client-manifest', action='append', default=[], help='Install manifests with products/{id}/{binary,version,version_args}')
    parser.add_argument('--context-window', type=int, help='Explicit Kimi validation context budget; never inferred from a model name')
    parser.add_argument('--codex-model', help='Optional model supported by the upstream; defaults to Codex selection')
    parser.add_argument('--claude-model', help='Optional model supported by the upstream; defaults to Claude selection')
    parser.add_argument('--products', default='codex,claude,antigravity', help='Comma-separated clients to verify')
    parser.add_argument('--request-timeout', type=int, default=120, help='Per-client request deadline in seconds')
    parser.add_argument('--output', help='Write the verification report to this JSON file')
    parser.add_argument('--keep-temp', action='store_true')
    args = parser.parse_args()
    products = args.products.split(',')
    commands = {'codex':'codex', 'claude':'claude', 'antigravity':'agy', 'opencode':'opencode', 'continue':'cn', 'aider':'aider', 'goose':'goose'}
    commands.update({product: item[0] for product, item in NEW_CLIENTS.items()})
    installed = {}
    for manifest in args.client_manifest:
        installed.update(json.loads(Path(manifest).read_text())['products'])
    if 'kimi' in products and (args.context_window is None or args.context_window <= 0):
        raise ValueError('Pass a confirmed positive --context-window for Kimi validation')
    if not products or any(p not in commands for p in products):
        raise ValueError('Unsupported product selection')
    binaries = {commands[name]: installed.get(name, {}).get('binary') or shutil.which(commands[name]) for name in products}
    if not all(binaries.values()):
        raise RuntimeError('Install selected clients before running this smoke test')
    model = args.model
    if any(p in products for p in ['opencode','continue','aider','goose'] + list(NEW_CLIENTS)) and not model:
        try:
            with open_http(args.upstream.rstrip('/') + '/v1/models', timeout=15) as response:
                catalog = json.load(response)
            model = next(item['id'] for item in catalog.get('data', []) if item.get('id'))
        except Exception as error:
            raise RuntimeError('Gateway model catalog unavailable; pass --model with a verified model ID') from error
    root = Path(tempfile.mkdtemp(prefix='clipal-cli-takeover-'))
    home, config, work = (root / name for name in ['home', 'config', 'workspace'])
    for directory in [home, config, work]:
        directory.mkdir()
    env = {
        'HOME': str(home), 'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
        'SHELL': '/bin/bash', 'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_DATA_HOME': str(root / 'data'), 'XDG_CACHE_HOME': str(root / 'cache'), 'TMPDIR': str(root / 'tmp'), 'CI': '1', 'NO_COLOR': '1', 'TERM': 'dumb', 'NO_PROXY': '127.0.0.1,localhost',
        'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC': '1', 'DISABLE_TELEMETRY': '1',
        'DISABLE_ERROR_REPORTING': '1', 'DISABLE_AUTOUPDATER': '1', 'DISABLE_UPDATES': '1',
    }
    Path(env['TMPDIR']).mkdir()
    # Include paths explicitly: background services need not share a client's env.
    codex_dir, claude_dir = root / 'codex-custom', root / 'claude-custom'
    codex_dir.mkdir()
    claude_dir.mkdir()
    env.update(CODEX_HOME=str(codex_dir), CLAUDE_CONFIG_DIR=str(claude_dir))
    client_dirs = {'opencode':root/'opencode-custom', 'continue':root/'continue-custom', 'aider':root/'aider-custom', 'goose':root/'goose-root'/'config'}
    env.update(OPENCODE_CONFIG_DIR=str(client_dirs['opencode']), CONTINUE_GLOBAL_DIR=str(client_dirs['continue']), GOOSE_PATH_ROOT=str(root/'goose-root'))
    originals = {
        codex_dir / 'validation.config.toml': b'# profile under test\n',
        claude_dir / 'settings.json': b'{"env":{"KEEP":"yes"}}\n',
        claude_dir / '.claude.json': b'{"hasCompletedOnboarding":false,"theme":"dark"}\n',
        home / '.gemini' / 'antigravity-cli' / 'settings.json': b'{"theme":"dark"}\n',
        home / '.bashrc': b'# isolated non-login startup\n',
        home / '.bash_profile': b'# isolated login startup without bashrc\n',
    }
    other_files = {'opencode':client_dirs['opencode']/'opencode.jsonc', 'continue':client_dirs['continue']/'config.yaml', 'aider':client_dirs['aider']/'.aider.conf.yml', 'goose':client_dirs['goose']/'custom_providers'/'clipal.json'}
    seeds = {'opencode':b'{"$schema":"https://opencode.ai/config.json","permission":{"*":"deny"}}\n',
             'continue':b'name: Validation\nversion: 1.0.0\nschema: v1\n',
             'aider':b'analytics: false\n',
             'goose':b'{"name":"clipal","engine":"openai","display_name":"Clipal","base_url":"http://original.invalid","models":[],"requires_auth":false}\n'}
    for product, path in other_files.items():
        originals[path] = seeds[product]
    client_files = {product:[path] for product,path in other_files.items()}
    if 'opencode' in products:
        lower = client_dirs['opencode'] / 'opencode.json'
        client_files['opencode'].append(lower)
        originals[lower] = (json.dumps({'$schema':'https://opencode.ai/config.json', 'disabled_providers':['clipal'], 'small_model':'clipal/'+model}) + '\n').encode()
        originals[other_files['opencode']] = (json.dumps({'$schema':'https://opencode.ai/config.json', 'permission':{'*':'deny'}, 'model':'clipal/'+model, 'provider':{'clipal':{'models':{model:{'id':'clipal-audit-stale-model', 'name':'Preserved alias'}}}}}) + '\n').encode()
    for product, (_, variable, files) in NEW_CLIENTS.items():
        directory = root / (product + '-custom') if variable or product == 'trae' else home / '.iflow'
        client_dirs[product] = directory
        if variable:
            env[variable] = str(directory)
        client_files[product] = []
        for filename, seed in files.items():
            path = directory / filename
            originals[path] = seed
            client_files[product].append(path)
    client_files['copilot'] = [home / '.bashrc', home / '.bash_profile']
    for path, data in originals.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        path.chmod(0o600)
    real_home = Path.home()
    untouched = [real_home / '.codex' / 'config.toml', real_home / '.claude' / 'settings.json',
                 real_home / '.claude.json', real_home / '.gemini' / 'antigravity-cli' / 'settings.json',
                 real_home / '.zshrc', real_home / '.bashrc', real_home / '.bash_profile']
    untouched += [real_home/'.config'/'opencode'/'opencode.json', real_home/'.config'/'opencode'/'opencode.jsonc', real_home/'.continue'/'config.yaml', real_home/'.aider.conf.yml', real_home/'.config'/'goose'/'custom_providers'/'clipal.json']
    for product, (_, _, files) in NEW_CLIENTS.items():
        definition_home = {'pi':'.pi/agent','qwen':'.qwen','kimi':'.kimi-code','copilot':'.copilot','codebuddy':'.codebuddy','crush':'.config/crush','iflow':'.iflow','grok':'.grok','deepseek-harness':'.dsh'}.get(product)
        if definition_home:
            untouched.extend(real_home / definition_home / name for name in files)
    untouched += [real_home / name for name in ['.pi/agent/auth.json', '.copilot/config.json', '.copilot/providers.json', '.kimi-code/credentials.json', '.dsh/settings.yaml']]
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
        try:
            with open_http(request, timeout=40) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f'{path}: HTTP {error.code}: {error.read().decode()[:1000]}') from error

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
        for product in ['opencode','continue','aider','goose']:
            if product in products:
                api(f'/api/integrations/{product}/target', {'config_dir':str(client_dirs[product]),'model':model})
        for product in NEW_CLIENTS:
            if product in products:
                target = {'config_dir': str(client_dirs[product]), 'model': model}
                if product == 'kimi':
                    target['context_window'] = args.context_window
                if product == 'copilot':
                    target.update(shell='bash', shell_dir=str(home))
                if product == 'trae':
                    target['config_file'] = str(client_files[product][0])
                api(f'/api/integrations/{product}/target', target)
        if 'opencode' in products:
            # The real client must see the lower file's restriction too.
            resolved = json.loads(run([binaries['opencode'], 'debug', 'config', '--pure'],env,work,40))
            if 'clipal' not in resolved.get('disabled_providers',[]):
                raise RuntimeError('OpenCode did not merge the regression fixture')
            before_blocked = {path:path.read_bytes() for path in client_files['opencode']}
            blocked = next(item for item in api('/api/integrations') if item['product']=='opencode')
            if blocked.get('verification') != 'blocked':
                raise RuntimeError('Clipal missed the merged provider restriction')
            try:
                api('/api/integrations/opencode/apply',{})
            except RuntimeError as error:
                if 'disabled_providers' not in str(error):
                    raise
            else:
                raise RuntimeError('Clipal applied a provider disabled by a lower layer')
            if any(path.read_bytes()!=data for path,data in before_blocked.items()):
                raise RuntimeError('Blocked takeover modified a configuration layer')
            lower = client_dirs['opencode'] / 'opencode.json'
            allowed = json.loads(lower.read_text())
            allowed['disabled_providers'] = []
            originals[lower] = (json.dumps(allowed)+'\n').encode()
            lower.write_bytes(originals[lower])
        for product, command in commands.items():
            if product not in products:
                continue
            metadata = installed.get(product, {})
            version = metadata.get('version', '')
            try:
                version = run([binaries[command]] + metadata.get('version_args', ['--version']), env, work, 30).strip() if metadata.get('version_args', ['--version']) else metadata['version']
                api(f'/api/integrations/{product}/apply', {})
                active.append(product)
                api(f'/api/integrations/{product}/apply', {})
                if product == 'opencode':
                    resolved = json.loads(run([binaries['opencode'],'debug','config','--pure'],env,work,40))
                    actual = resolved['provider']['clipal']['models'][model]
                    if actual.get('id') != model or actual.get('name') != 'Preserved alias':
                        raise RuntimeError('Explicit model selection did not replace the stale wire ID while preserving metadata')
                    if resolved.get('model') != 'clipal/'+model or resolved.get('small_model') != 'clipal/'+model:
                        raise RuntimeError('The real client did not resolve the managed model selections')
                marker = 'CLIPAL_TAKEOVER_' + product.upper() + '_OK'
                prompt = 'Reply with exactly ' + marker + '. Do not use tools.'
                if product == 'trae':
                    prompt = 'This is a connectivity verification, not a repository change. Reply with exactly ' + marker + ' and then call task_done to finish. Do not read or write files or execute any other tools.'
                if product == 'codex':
                    cmd = [binaries[command], '--profile', 'validation', 'exec', '--skip-git-repo-check', '--ephemeral', '--json']
                    if args.codex_model:
                        cmd += ['--model', args.codex_model]
                    cmd += [prompt]
                elif product == 'claude':
                    cmd = [binaries[command], '-p', prompt, '--output-format', 'json', '--no-session-persistence']
                    if args.claude_model:
                        cmd += ['--model', args.claude_model]
                elif product == 'opencode':
                    cmd = [binaries[command], 'run', '--pure', '--format', 'json', '--title', 'Clipal validation', prompt]
                elif product == 'continue':
                    cmd = [binaries[command], '--config', str(other_files[product]), '--readonly', '-p', '--format', 'json', prompt]
                elif product == 'aider':
                    cmd = [binaries[command], '--config', str(other_files[product]), '--no-git', '--no-check-update', '--no-show-model-warnings', '--no-analytics', '--no-auto-commits', '--no-pretty', '--no-stream', '--yes-always', '--message', prompt]
                elif product == 'goose':
                    cmd = [binaries[command], 'run', '--no-session', '--provider', 'clipal', '--model', model, '--max-turns', '1', '--quiet', '--text', prompt]
                elif product in NEW_CLIENTS:
                    cmd = new_client_command(product, binaries[command], prompt, client_files[product], work)
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
                request_error = None
                outcome = {}
                try:
                    previous_request = api('/api/status')['clients']['openai'].get('last_request')
                    output = run(cmd, env, work, args.request_timeout)
                    (root / (product + '-response.log')).write_text(output)
                    validate_client_response(product, output, marker, work)
                    client = {'claude':'claude', 'antigravity':'google'}.get(product, 'openai')
                    outcome = api('/api/status')['clients'][client].get('last_request') or {}
                    if product in NEW_CLIENTS and outcome == previous_request:
                        raise RuntimeError(f'{product} did not make a new request through the isolated gateway')
                    if outcome.get('status') != 200:
                        raise RuntimeError(f'{product} gateway outcome was not HTTP 200: {outcome}')
                except Exception as error:
                    request_error = str(error)
                    (root / (product + '-error.log')).write_text(request_error)
                api(f'/api/integrations/{product}/rollback', {})
                active.remove(product)
                relevant = [path for path in originals if (product == 'codex' and path.parent == codex_dir) or
                            (product == 'claude' and path.parent == claude_dir) or
                            (product == 'antigravity' and path.is_relative_to(home)) or
                            (product in client_files and path in client_files[product])]
                for path in relevant:
                    if path.stat().st_mode & 0o777 != 0o600:
                        raise RuntimeError(f'{product} rollback changed permissions: {path}')
                    if path.read_bytes() != originals[path]:
                        raise RuntimeError(f'{product} rollback changed {path}')
                # A new cycle must capture user edits made after the first rollback.
                modified = {}
                for path in relevant:
                    data = originals[path] + (b'\n' if path.suffix in ['.json', '.jsonc'] else b'# second cycle\n')
                    path.write_bytes(data)
                    modified[path] = data
                api(f'/api/integrations/{product}/apply', {})
                active.append(product)
                api(f'/api/integrations/{product}/rollback', {})
                active.remove(product)
                if any(path.read_bytes() != data for path, data in modified.items()):
                    raise RuntimeError(f'{product} second-cycle rollback lost edits')
                report['clients'][product] = {'version': version, 'marker': marker, 'http_status': outcome.get('status'), 'rollback': 'byte-exact', 'second_cycle': 'preserved', **({'model':model} if product in other_files or product in NEW_CLIENTS else {}), **({'installation':metadata} if metadata else {})}
                if request_error:
                    report['clients'][product]['error'] = request_error
                if product == 'opencode':
                    report['clients'][product].update(explicit_alias='verified', merged_restriction='blocked', read_only_layers='preserved')
                print(product + (': request FAILED: ' + request_error if request_error else ': request HTTP 200') + ', rollback and second cycle verified', flush=True)
            except Exception as error:
                report['clients'][product] = {'version': version, 'model': model, 'error': str(error), 'success': False}
                print(product + ': FAILED: ' + str(error), flush=True)
                if product in active:
                    try:
                        api(f'/api/integrations/{product}/rollback', {})
                        active.remove(product)
                        restored = all(path.exists() and path.read_bytes() == originals[path] and stat.S_IMODE(path.stat().st_mode) == 0o600 for path in client_files.get(product, []))
                        report['clients'][product]['rollback'] = 'byte-exact' if restored else 'FAILED'
                    except Exception as rollback_error:
                        report['clients'][product]['rollback_error'] = str(rollback_error)
        report['real_user_files_unchanged'] = all(fingerprint(path) == before[str(path)] for path in untouched)
        if not report['real_user_files_unchanged']:
            raise RuntimeError('Real user client configuration changed during the test')
        report['success'] = all('error' not in result for result in report['clients'].values())
    except Exception as error:
        report['error'] = str(error)
        raise
    finally:
        try:
            for item in api('/api/integrations'):
                if item['product'] in products and item.get('backup_available') and item['product'] not in active:
                    active.append(item['product'])
        except Exception as error:
            report.setdefault('cleanup_errors', []).append('Cannot inspect remaining baselines: ' + str(error))
        for product in reversed(active):
            try:
                api(f'/api/integrations/{product}/rollback', {})
            except Exception as error:
                report.setdefault('cleanup_errors', []).append(str(error))
        report['real_user_files_unchanged'] = all(fingerprint(path) == before[str(path)] for path in untouched)
        if report.get('cleanup_errors') or not report['real_user_files_unchanged']:
            report['success'] = False
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()
        logfile.close()
        finalize_report(report, root, args.keep_temp, args.output)
    print(json.dumps(report, indent=2))
    if not report['success']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
