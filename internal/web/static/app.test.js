const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('first-paint theme agrees with runtime theme for saved and system preferences', () => {
    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    const bootstrap = html.match(/<script>([\s\S]*?)<\/script>/)[1];
    for (const theme of [null, 'system', 'light', 'dark']) {
        for (const systemDark of [false, true]) {
            let dark = false;
            const classList = {
                toggle(name, enabled) { dark = enabled; },
                add() { dark = true; },
                remove() { dark = false; }
            };
            const document = { documentElement: { classList } };
            const window = { matchMedia: () => ({ matches: systemDark }) };
            vm.runInNewContext(bootstrap, {
                document, window,
                localStorage: { getItem: key => key === 'theme' ? theme : null }
            });
            const expected = theme === 'dark' || ((theme === null || theme === 'system') && systemDark);
            assert.equal(dark, expected, `first paint: ${theme}, system dark: ${systemDark}`);
            const state = loadApp({ context: { document, window } });
            state.applyTheme(theme || 'system');
            assert.equal(dark, expected, `runtime: ${theme}, system dark: ${systemDark}`);
        }
    }
});

test('first-paint theme follows the system when storage is unavailable', () => {
    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    const bootstrap = html.match(/<script>([\s\S]*?)<\/script>/)[1];
    let dark = false;
    vm.runInNewContext(bootstrap, {
        document: { documentElement: { classList: { toggle(name, enabled) { dark = enabled; } } } },
        window: { matchMedia: () => ({ matches: true }) },
        localStorage: { getItem() { throw new Error('storage blocked'); } }
    });
    assert.equal(dark, true);
});

function createPopupWindow() {
    return {
        closed: false,
        close() {
            this.closed = true;
        },
        location: {
            href: 'about:blank',
            assign(url) {
                this.href = url;
            },
            replace(url) {
                this.href = url;
            }
        }
    };
}

function loadApp(options = {}) {
    const source = ['environment-options.js', 'form-controls.js', 'app.js']
        .map(file => fs.readFileSync(path.join(__dirname, file), 'utf8')).join('\n');
    const storage = new Map(Object.entries(options.localStorageData || {}));
    const location = {
        href: 'http://localhost/',
        assign(url) {
            this.href = url;
        }
    };
    const context = {
        console,
        localStorage: {
            getItem(key) {
                return storage.has(key) ? storage.get(key) : null;
            },
            setItem(key, value) {
                storage.set(String(key), String(value));
            },
            removeItem(key) {
                storage.delete(String(key));
            }
        },
        document: {
            documentElement: {
                lang: 'en',
                classList: {
                    add() {},
                    remove() {}
                }
            },
            getElementById() {
                return null;
            }
        },
        window: {
            matchMedia() {
                return {
                    matches: false,
                    addEventListener() {}
                };
            },
            open(url) {
                const popup = createPopupWindow();
                if (url) {
                    popup.location.href = url;
                }
                return popup;
            },
            location
        },
        setTimeout,
        clearTimeout,
        setInterval() {
            return 1;
        },
        clearInterval() {},
        queueMicrotask,
        URL,
        AbortController,
        fetch: async () => ({ ok: true, json: async () => ({}) }),
        confirm: () => true
    };

    if (options.context) {
        if (options.context.localStorage) {
            Object.assign(context.localStorage, options.context.localStorage);
        }
        if (options.context.document) {
            Object.assign(context.document, options.context.document);
            if (options.context.document.documentElement) {
                Object.assign(context.document.documentElement, options.context.document.documentElement);
            }
        }
        if (options.context.window) {
            const windowOverrides = { ...options.context.window };
            if (windowOverrides.location) {
                Object.assign(context.window.location, windowOverrides.location);
                delete windowOverrides.location;
            }
            Object.assign(context.window, windowOverrides);
        }
        Object.assign(context, {
            ...options.context,
            localStorage: context.localStorage,
            document: context.document,
            window: context.window
        });
    }

    vm.runInNewContext(`${source}\n;globalThis.__appFactory = app;`, context, {
        filename: 'app.js'
    });

    const state = context.__appFactory();
    state.__context = context;
    if (typeof state.$nextTick !== 'function') {
        state.$nextTick = callback => callback();
    }
    return state;
}

class FakeFormData {
    constructor() {
        this.parts = [];
    }

    append(name, value, filename) {
        this.parts.push({ name, value, filename });
    }
}

test('applyLocalProviderReorder reorders providers and renumbers priority immediately', () => {
    const state = loadApp();
    state.providers = [
        { name: 'p1', priority: 1, enabled: true },
        { name: 'p2', priority: 2, enabled: true },
        { name: 'p3', priority: 3, enabled: true }
    ];

    state.applyLocalProviderReorder(1, 0);

    assert.deepEqual(
        state.providers.map(provider => `${provider.name}:${provider.priority}`),
        ['p2:1', 'p1:2', 'p3:3']
    );
});

function createProviderList(names) {
    const template = { matches: () => false };
    const list = {
        children: [template, ...names.map(name => ({
            matches: selector => selector === '.provider-row',
            getAttribute: attribute => attribute === 'data-id' ? name : null
        }))],
        appendChild(row) {
            this.children.splice(this.children.indexOf(row), 1);
            this.children.push(row);
        }
    };
    return list;
}

test('syncSortableDomOrder restores keyed rows without counting the Alpine template', () => {
    const list = createProviderList(['p1', 'p2', 'p3']);
    const template = list.children[0];
    const originalRows = list.children.slice(1);
    const state = loadApp({ context: { document: { getElementById: () => list } } });
    state.syncSortableDomOrder(['p3', 'p1', 'p2']);
    assert.equal(list.children[0], template);
    assert.deepEqual(list.children.slice(1), [originalRows[2], originalRows[0], originalRows[1]]);
    state.syncSortableDomOrder(['p2', 'p3', 'p1']);
    assert.deepEqual(list.children.slice(1), [originalRows[1], originalRows[2], originalRows[0]]);
});

test('afterProviderRender aligns keyed rows to current providers on nextTick', async () => {
    const list = createProviderList(['p1', 'p2']);
    const state = loadApp({ context: { document: { getElementById: () => list } } });
    state.providers = [{ name: 'p2', priority: 1 }, { name: 'p1', priority: 2 }];
    await state.afterProviderRender();
    assert.deepEqual(list.children.slice(1).map(row => row.getAttribute('data-id')), ['p2', 'p1']);
});

test('apiCall omits JSON content type for FormData uploads', async () => {
    const state = loadApp({
        context: {
            FormData: FakeFormData,
            fetch: async (url, options) => ({
                ok: true,
                json: async () => ({ url, headers: options.headers })
            })
        }
    });

    const result = await state.apiCall('/api/oauth/import/cli-proxy-api', {
        method: 'POST',
        body: new FakeFormData()
    }, true);

    assert.equal(result.headers['X-Clipal-UI'], '1');
    assert.equal(Object.prototype.hasOwnProperty.call(result.headers, 'Content-Type'), false);
});

test('saveProvider includes OpenAI override fields in payload', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'openai';
    state.clientConfig.override_support = {
        model: true,
        openai: {
            reasoning_effort: true
        },
        claude: {
            thinking_budget_tokens: false
        }
    };
    state.providerForm = {
        name: 'openai-primary',
        base_url: 'https://example.com',
        model: 'gpt-5.4',
        reasoning_effort: 'high',
        thinking_budget_tokens: 0,
        api_keys_text: 'key-1',
        priority: 1,
        enabled: true
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = () => {};
    state.closeModals = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

    await state.saveProvider();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/providers/openai');
    assert.deepEqual(calls[0].options, {
        name: 'openai-primary',
        base_url: 'https://example.com',
        proxy_mode: 'default',
        priority: 1,
        enabled: true,
        overrides: {
            model: 'gpt-5.4',
            openai: {
                reasoning_effort: 'high'
            }
        },
        api_key: 'key-1'
    });
});

test('saveProvider includes Claude thinking budget override in payload', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'claude';
    state.clientConfig.override_support = {
        model: true,
        openai: {
            reasoning_effort: false
        },
        claude: {
            thinking_budget_tokens: true
        }
    };
    state.providerForm = {
        name: 'claude-primary',
        base_url: 'https://example.com',
        model: 'claude-sonnet-4-5',
        reasoning_effort: '',
        thinking_budget_tokens: 2048,
        api_keys_text: 'key-1\nkey-2',
        priority: 2,
        enabled: false
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = () => {};
    state.closeModals = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

    await state.saveProvider();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/providers/claude');
    assert.deepEqual(calls[0].options, {
        name: 'claude-primary',
        base_url: 'https://example.com',
        proxy_mode: 'default',
        priority: 2,
        enabled: false,
        overrides: {
            model: 'claude-sonnet-4-5',
            claude: {
                thinking_budget_tokens: 2048
            }
        },
        api_keys: ['key-1', 'key-2']
    });
});

test('providerOverrideSupport centralizes support matrix', () => {
    const state = loadApp();
    const plain = value => JSON.parse(JSON.stringify(value));

    state.clientConfig.override_support = {
        model: true,
        openai: {
            reasoning_effort: true
        },
        claude: {
            thinking_budget_tokens: false
        }
    };
    assert.deepEqual(plain(state.providerOverrideSupport()), {
        model: true,
        openai: {
            reasoning_effort: true
        },
        claude: {
            thinking_budget_tokens: false
        }
    });

    state.clientConfig.override_support = {
        model: true,
        openai: {
            reasoning_effort: false
        },
        claude: {
            thinking_budget_tokens: true
        }
    };
    assert.deepEqual(plain(state.providerOverrideSupport()), {
        model: true,
        openai: {
            reasoning_effort: false
        },
        claude: {
            thinking_budget_tokens: true
        }
    });

    state.clientConfig.override_support = {
        model: false,
        openai: {
            reasoning_effort: false
        },
        claude: {
            thinking_budget_tokens: false
        }
    };
    assert.deepEqual(plain(state.providerOverrideSupport()), {
        model: false,
        openai: {
            reasoning_effort: false
        },
        claude: {
            thinking_budget_tokens: false
        }
    });
});

test('openAddProviderModal sets next priority for provider form', () => {
    const state = loadApp();
    state.selectedClient = 'openai';
    state.oauthProviders = [{ provider: 'codex' }];
    state.clientConfig.override_support = {
        model: true,
        openai: {
            reasoning_effort: true
        },
        claude: {
            thinking_budget_tokens: false
        }
    };
    state.providers = [
        { name: 'p1', priority: 1, enabled: true },
        { name: 'p2', priority: 3, enabled: true }
    ];

    state.openAddProviderModal();

    assert.equal(state.showAddProviderModal, true);
    assert.equal(state.providerForm.priority, 4);
    assert.equal(state.providerForm.proxy_mode, 'default');
    assert.equal(state.providerForm.auth_type, 'api_key');
    assert.equal(state.providerForm.oauth_provider, 'codex');
});

test('loadOAuthProviders switches to the available oauth source for current client', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'google';
    state.providerForm.auth_type = 'oauth';
    state.providerForm.oauth_provider = 'codex';
    state.apiCall = async url => {
        calls.push(url);
        return [{ provider: 'antigravity' }];
    };

    await state.loadOAuthProviders(true);

    assert.deepEqual(calls, ['/api/oauth/providers?client_type=google']);
    assert.deepEqual(JSON.parse(JSON.stringify(state.oauthProviders)), [{ provider: 'antigravity' }]);
    assert.equal(state.providerForm.auth_type, 'oauth');
    assert.equal(state.providerForm.oauth_provider, 'antigravity');
});

test('oauthImportFileHint names supported credential file types for the selected oauth provider', () => {
    const state = loadApp();

    state.providerForm.oauth_provider = 'codex';
    assert.match(state.oauthImportFileHint(), /Codex CLI auth\.json/);
    assert.match(state.oauthImportFileHint(), /~\/\.codex\/auth\.json/);
    assert.match(state.oauthImportFileHint(), /CLIProxyAPI/);
    assert.match(state.oauthImportFileHint(), /sub2api/);
    assert.match(state.oauthImportFileHint(), /Codex accounts/);

    state.providerForm.oauth_provider = 'claude';
    assert.match(state.oauthImportFileHint(), /Claude OAuth JSON/);
    assert.match(state.oauthImportFileHint(), /Claude accounts/);

    state.providerForm.oauth_provider = 'antigravity';
    assert.match(state.oauthImportFileHint(), /Antigravity OAuth JSON/);
    assert.match(state.oauthImportFileHint(), /Antigravity accounts/);
    assert.equal(state.providerSupportsOAuthMetadata({auth_type: 'oauth', oauth_provider: 'gemini', oauth_ref: 'old'}), false);
});

test('editProvider hydrates override fields directly into the form', () => {
    const state = loadApp();

    state.editProvider({
        name: 'openai-primary',
        base_url: 'https://example.com',
        proxy_mode: 'custom',
        proxy_url_hint: 'http://127.0.0.1:7890',
        overrides: {
            model: 'gpt-5.4',
            openai: {
                reasoning_effort: 'high'
            }
        },
        key_count: 1,
        priority: 1,
        enabled: true
    });

    assert.equal(state.showEditProviderModal, true);
    assert.equal(state.providerForm.proxy_mode, 'custom');
    assert.equal(state.providerForm.proxy_url, '');
    assert.equal(state.providerForm.proxy_url_hint, 'http://127.0.0.1:7890');
    assert.equal(state.providerForm.model, 'gpt-5.4');
    assert.equal(state.providerForm.reasoning_effort, 'high');
    assert.equal(state.providerForm.thinking_budget_tokens, 0);
});

test('editProvider rejects oauth providers', () => {
    const state = loadApp();
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);

    state.editProvider({
        name: 'codex-sean-example-com',
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: 'codex-sean-example-com',
        enabled: true
    });

    assert.equal(state.showEditProviderModal, false);
    assert.equal(alerts.length, 1);
});

test('setProviderAuthType falls back to api_key when oauth is unavailable', () => {
    const state = loadApp();
    state.oauthProviders = [];
    state.providerForm.auth_type = 'api_key';

    state.setProviderAuthType('oauth');

    assert.equal(state.providerForm.auth_type, 'api_key');
});

test('formatCompactTokenCount keeps at most three digits across all units', () => {
    const state = loadApp();

    assert.equal(state.formatCompactTokenCount(999), '999');
    assert.equal(state.formatCompactTokenCount(1000), '1K');
    assert.equal(state.formatCompactTokenCount(1234), '1.23K');
    assert.equal(state.formatCompactTokenCount(12345), '12.3K');
    assert.equal(state.formatCompactTokenCount(123456), '123K');
    assert.equal(state.formatCompactTokenCount(999500), '1M');
    assert.equal(state.formatCompactTokenCount(3644514), '3.64M');
    assert.equal(state.formatCompactTokenCount(724117614), '724M');
    assert.equal(state.formatCompactTokenCount(1250000000), '1.25B');
    assert.equal(state.formatCompactTokenCount(1234000000000), '1.23T');
    assert.equal(state.formatCompactTokenCount(1234000000000000), '1.23P');
    assert.equal(state.formatCompactTokenCount(1234000000000000000), '1.23E');
    assert.equal(state.formatCompactTokenCount(1234000000000000000000), '1.23Z');
    assert.equal(state.formatCompactTokenCount(1234000000000000000000000), '1.23Y');
});

test('providerUsage labels attach reasoning/thoughts to their parent metrics', () => {
    const state = loadApp();
    const provider = {
        usage: {
            has_usage: true,
            total_tokens: 724117614,
            input_tokens: 720473100,
            output_tokens: 3644514,
            reasoning_tokens: 120045,
            thoughts_tokens: 9876
        }
    };

    assert.equal(state.providerUsageTotalMain(provider), '724M');
    assert.equal(state.providerUsageTotalTitle(provider), '724,117,614 = 720,473,100 + 3,644,514 + incl. Thoughts 9,876');
    assert.equal(state.providerUsageInOutTitle(provider), '720,473,100 / 3,644,514 · incl. Reasoning 120,045');
});

test('providerUsage breakdowns prefer structured entries and preserve parent semantics', () => {
    const state = loadApp();
    const provider = {
        usage: {
            has_usage: true,
            total_tokens: 25,
            input_tokens: 7,
            output_tokens: 1,
            usage_breakdowns: [
                { kind: 'thoughts', parent: 'total', tokens: 17 },
                { kind: 'reasoning', parent: 'output', tokens: 0 }
            ]
        }
    };

    assert.deepEqual(
        JSON.parse(JSON.stringify(state.providerUsageBreakdowns(provider, 'total'))),
        [{ kind: 'thoughts', parent: 'total', tokens: 17 }]
    );
    assert.equal(state.providerUsageTotalTitle(provider), '25 = 7 + 1 + incl. Thoughts 17');
    assert.equal(state.providerUsageInOutTitle(provider), '7 / 1');
});

test('providerUsage titles fall back to plain formulas without breakdowns', () => {
    const state = loadApp();
    const provider = {
        usage: {
            has_usage: true,
            total_tokens: 92,
            input_tokens: 23,
            output_tokens: 69
        }
    };

    assert.equal(state.providerUsageTotalTitle(provider), '92 = 23 + 69');
    assert.equal(state.providerUsageInOutTitle(provider), '23 / 69');
});

test('providerSpend labels format micros and preserve exact hover text', () => {
    const state = loadApp();
    const provider = {
        usage: {
            has_cost: true,
            spend_today_micros: 12500,
            spend_week_micros: 4250000
        }
    };

    assert.equal(state.providerSpendToday(provider), '$0.0125');
    assert.equal(state.providerSpendTodayTitle(provider), '$0.012500');
    assert.equal(state.providerSpendWeek(provider), '$4.25');
    assert.equal(state.providerSpendWeekTitle(provider), '$4.250000');
});

test('providerSpend labels compact large amounts and keep precise hover text', () => {
    const state = loadApp();
    const provider = {
        usage: {
            has_cost: true,
            spend_today_micros: 1234567890,
            spend_week_micros: 9876543210000
        }
    };

    assert.equal(state.providerSpendToday(provider), '$1.23K');
    assert.equal(state.providerSpendTodayTitle(provider), '$1,234.567890');
    assert.equal(state.providerSpendWeek(provider), '$9.88M');
    assert.equal(state.providerSpendWeekTitle(provider), '$9,876,543.210000');
});

test('formatUSDMicros does not round spend values into the next display tier', () => {
    const state = loadApp();

    assert.equal(state.formatUSDMicros(999999), '$0.999');
    assert.equal(state.formatUSDMicros(9999500), '$9.99');
    assert.equal(state.formatUSDMicros(999999000), '$999');
    assert.equal(state.formatUSDMicros(999999000000), '$999K');
});

test('providerOAuthRateLimitSections builds weekly and code review rows', () => {
    const state = loadApp();
    const provider = {
        auth_type: 'oauth',
        oauth_plan_type: 'free',
        oauth_rate_limits: {
            primary: {
                used_percent: 100,
                window_minutes: 10080,
                resets_at: '2026-04-28T12:00:00Z'
            },
            additional: [
                {
                    limit_id: 'code_review',
                    limit_name: 'Code review',
                    primary: {
                        used_percent: 75,
                        window_minutes: 10080,
                        resets_at: '2026-04-27T12:00:00Z'
                    }
                }
            ]
        }
    };

    assert.equal(state.providerOAuthPlanLabel(provider), 'Free');
    assert.equal(state.providerOAuthRateLimitPercentLabel(provider.oauth_rate_limits.primary), '100% used');

    const sections = state.providerOAuthRateLimitSections(provider);

    const summaries = sections.map(section => `${section.key}:${section.label}:${state.providerOAuthRateLimitPercentLabel(section.window)}`);

    assert.deepEqual(
        JSON.parse(JSON.stringify(summaries)),
        [
            'primary:Weekly limit:100% used',
            'additional-0-primary:Code review weekly limit:75% used'
        ]
    );
});

test('loadProviderOAuthMetadata fetches provider-scoped metadata on demand', async () => {
    const state = loadApp();
    const calls = [];
    const provider = {
        name: 'codex-sean-example-com',
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: 'codex-sean-example-com'
    };
    state.selectedClient = 'openai';
    state.apiCall = async (url, options, background, suppressAlert) => {
        calls.push({ url, options, background, suppressAlert });
        return {
            oauth_plan_type: 'free',
            oauth_rate_limits: {
                primary: {
                    used_percent: 80,
                    window_minutes: 10080,
                    resets_at: '2026-04-28T12:00:00Z'
                }
            }
        };
    };

    assert.equal(state.providerOAuthMetadataButtonLabel(provider), 'Load');

    await state.loadProviderOAuthMetadata(provider);

    assert.deepEqual(
        JSON.parse(JSON.stringify(calls)),
        [
            {
                url: '/api/providers/openai/codex-sean-example-com/oauth-metadata',
                options: {},
                background: true,
                suppressAlert: true
            }
        ]
    );
    assert.equal(provider.oauth_plan_type, 'free');
    assert.equal(provider.oauth_rate_limits.primary.used_percent, 80);
    assert.equal(state.providerHasLoadedOAuthMetadata(provider), true);
    assert.equal(state.providerOAuthMetadataError(provider), '');
    assert.equal(state.providerOAuthMetadataButtonLabel(provider), 'Refresh');
});

test('quota labels identify usage instead of reversing remaining quota', () => {
    const state = loadApp();
    const provider = {
        auth_type: 'oauth',
        oauth_provider: 'antigravity',
        oauth_rate_limits: {
            additional: [{
                limit_id: 'gemini-weekly',
                limit_name: 'Gemini Models Weekly Limit Remaining',
                primary: { used_percent: 5 }
            }]
        }
    };
    const [section] = state.providerOAuthRateLimitSections(provider);
    assert.equal(section.label, 'Gemini Models Weekly Limit');
    assert.equal(state.providerOAuthRateLimitPercentLabel(section.window), '5% used');
    state.locale = 'zh-CN';
    assert.equal(state.providerOAuthRateLimitPercentLabel(section.window), '已用 5%');
    assert.equal(state.providerOAuthRateLimitPercentLabel({ used_percent: 0 }), '已用 0%');
    assert.equal(state.providerOAuthRateLimitPercentLabel({ used_percent: 100 }), '已用 100%');
});

for (const service of ['claude', 'antigravity']) {
    test(`loadProviderOAuthMetadata supports ${service} plan and quota loading, retry and refresh`, async () => {
        const state = loadApp();
        const calls = [];
        const provider = {
            name: `${service}-account`,
            auth_type: 'oauth',
            oauth_provider: service,
            oauth_ref: `${service}-account`
        };
        state.selectedClient = service === 'claude' ? 'claude' : 'google';
        state.apiCall = async (url, options, background, suppressAlert) => {
            calls.push({ url, options, background, suppressAlert });
            return {
                oauth_plan_type: service === 'claude' ? 'pro' : 'Google AI Pro',
                oauth_rate_limits: {
                    additional: [{ limit_id: 'gemini-pro', limit_name: 'Gemini Pro', primary: { used_percent: 0 } }]
                }
            };
        };

        assert.equal(state.providerSupportsOAuthMetadata(provider), true);
        assert.equal(state.providerSupportsOAuthMetadata({ ...provider, oauth_ref: '' }), false);
        assert.equal(state.providerSupportsOAuthMetadata({ ...provider, auth_type: 'api_key' }), false);
        await state.loadProviderOAuthMetadata(provider);
        assert.deepEqual(JSON.parse(JSON.stringify(calls)), [{
            url: `/api/providers/${state.selectedClient}/${provider.name}/oauth-metadata`,
            options: {}, background: true, suppressAlert: true
        }]);
        assert.equal(state.providerOAuthPlanLabel(provider), service === 'claude' ? 'Pro' : 'Google AI Pro');
        const sections = state.providerOAuthRateLimitSections(provider);
        assert.equal(sections.length, 1);
        assert.equal(sections[0].window.used_percent, 0);
        assert.equal(state.providerHasLoadedOAuthMetadata(provider), true);
        assert.equal(state.providerOAuthMetadataButtonLabel(provider), 'Refresh');

        state.apiCall = async () => { throw new Error('Quota service unavailable'); };
        await state.loadProviderOAuthMetadata(provider);
        assert.equal(state.providerOAuthMetadataError(provider), 'Quota service unavailable');
        assert.equal(state.providerOAuthRateLimitSections(provider).length, 1, 'failed refresh preserves prior data');
        assert.equal(state.providerOAuthMetadataBusy(provider), false);

        state.apiCall = async () => ({ oauth_plan_type: 'Google AI Pro' });
        await state.loadProviderOAuthMetadata(provider);
        assert.equal(state.providerOAuthMetadataError(provider), '');
        assert.equal(state.providerOAuthRateLimitSections(provider).length, 0, 'missing quotas never become invented rows');
        assert.equal(state.providerHasLoadedOAuthMetadata(provider), true);
    });
}

test('providerOAuthRateLimitSections localizes Claude additional limit ids', () => {
    const state = loadApp();
    state.locale = 'zh-CN';
    const provider = {
        auth_type: 'oauth',
        oauth_provider: 'claude',
        oauth_rate_limits: {
            additional: [
                {
                    limit_id: 'claude_oauth_apps',
                    limit_name: 'OAuth apps',
                    primary: {
                        used_percent: 15,
                        window_minutes: 10080,
                        resets_at: '2026-06-07T05:00:00Z'
                    }
                },
                {
                    limit_id: 'claude_extra_usage',
                    limit_name: 'Extra usage',
                    primary: {
                        used_percent: 50,
                        window_minutes: 0
                    }
                }
            ]
        }
    };

    const sections = state.providerOAuthRateLimitSections(provider);

    assert.deepEqual(
        JSON.parse(JSON.stringify(sections.map(section => section.label))),
        [
            'OAuth 应用周限额',
            '额外用量限额'
        ]
    );
});

test('providerOAuthAuthStatusLabel prefers backend status for oauth cards', () => {
    const state = loadApp();

    const label = state.providerOAuthAuthStatusLabel({
        auth_type: 'oauth',
        oauth_auth_status: 'refresh_due'
    });

    assert.equal(label, 'Refresh due');
});

test('providerOAuthAuthStatus falls back to reauth-needed when no credential metadata exists', () => {
    const state = loadApp();

    const status = state.providerOAuthAuthStatus({
        auth_type: 'oauth',
        oauth_auth_status: '',
        oauth_expires_at: '',
        oauth_last_refresh: ''
    });

    assert.equal(status, 'reauth_needed');
});

test('providerOAuthRefreshSummary returns never when last refresh is unavailable', () => {
    const state = loadApp();

    const summary = state.providerOAuthRefreshSummary({
        auth_type: 'oauth',
        oauth_last_refresh: ''
    });

    assert.equal(summary, 'Never');
});

test('deleteProvider deletes oauth providers through the provider API', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'openai';
    state.apiCall = async (url, options) => {
        calls.push({ url, method: options.method });
        return {};
    };
    state.showAlert = () => {};
    state.askConfirm = async () => true;
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};
    state.selectedProviderName = 'codex-sean-example-com';
    state.providerDetailOpen = true;

    await state.deleteProvider({
        name: 'codex-sean-example-com',
        display_name: 'sean@example.com',
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: 'codex-sean-example-com'
    });

    assert.deepEqual(calls, [
        {
            url: '/api/providers/openai/codex-sean-example-com',
            method: 'DELETE'
        }
    ]);
    // The inspector no longer points at the deleted provider.
    assert.equal(state.selectedProviderName, '');
    assert.equal(state.providerDetailOpen, false);
});

test('oauthSessionSuccessMessage reflects provider action', () => {
    const state = loadApp();
    state.selectedClient = 'openai';

    const created = state.oauthSessionSuccessMessage({
        provider: 'codex',
        provider_name: 'codex-sean-example-com',
        provider_action: 'created',
        display_name: 'sean@example.com'
    });
    const reused = state.oauthSessionSuccessMessage({
        provider: 'codex',
        provider_name: 'codex-sean-example-com',
        provider_action: 'reused',
        display_name: 'sean@example.com'
    });
    const relinked = state.oauthSessionSuccessMessage({
        provider: 'codex',
        provider_name: 'codex-sean-example-com',
        provider_action: 'relinked',
        display_name: 'sean@example.com'
    });

    assert.match(created, /provider codex-sean-example-com/i);
    assert.match(reused, /refreshed existing provider codex-sean-example-com/i);
    assert.match(relinked, /relinked provider codex-sean-example-com/i);
});

test('toggleProvider updates oauth providers through the standard provider API', async () => {
    const state = loadApp();
    const calls = [];
    const alerts = [];
    const provider = {
        name: 'codex-sean-example-com',
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: 'codex-sean-example-com',
        enabled: true
    };
    state.selectedClient = 'openai';
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = (...args) => alerts.push(args);
    state.refreshStatus = async () => {};

    await state.toggleProvider(provider, { target: { checked: false } });

    assert.deepEqual(calls, [
        {
            url: '/api/providers/openai/codex-sean-example-com',
            options: {
                enabled: false
            }
        }
    ]);
    assert.equal(provider.enabled, false);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'success');
});

test('saveProvider includes custom proxy settings when configured', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'openai';
    state.providerForm = {
        name: 'openai-proxy',
        base_url: 'https://example.com',
        proxy_mode: 'custom',
        proxy_url: 'http://127.0.0.1:7890',
        proxy_url_hint: '',
        model: '',
        reasoning_effort: '',
        thinking_budget_tokens: 0,
        api_keys_text: 'key-1',
        priority: 1,
        enabled: true
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = () => {};
    state.closeModals = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

    await state.saveProvider();

    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0].options, {
        name: 'openai-proxy',
        base_url: 'https://example.com',
        proxy_mode: 'custom',
        proxy_url: 'http://127.0.0.1:7890',
        priority: 1,
        enabled: true,
        api_key: 'key-1'
    });
});

test('saveProvider omits unsupported override fields for gemini', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'google';
    state.providerForm = {
        name: 'gemini-primary',
        base_url: 'https://example.com',
        proxy_mode: 'default',
        proxy_url: '',
        proxy_url_hint: '',
        model: 'gemini-2.5-pro',
        reasoning_effort: 'high',
        thinking_budget_tokens: 2048,
        api_keys_text: 'key-1',
        priority: 1,
        enabled: true
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = () => {};
    state.closeModals = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

    await state.saveProvider();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/providers/google');
    assert.deepEqual(calls[0].options, {
        name: 'gemini-primary',
        base_url: 'https://example.com',
        proxy_mode: 'default',
        priority: 1,
        enabled: true,
        api_key: 'key-1'
    });
});

test('saveProvider starts OAuth authorization flow for oauth provider', async () => {
    const state = loadApp();
    const calls = [];
    state.selectedClient = 'openai';
    state.providerForm = {
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: '',
        name: '',
        base_url: '',
        proxy_mode: 'default',
        proxy_url: '',
        proxy_url_hint: '',
        model: '',
        reasoning_effort: '',
        thinking_budget_tokens: 0,
        api_keys_text: '',
        priority: 1,
        enabled: true
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: options && options.body ? JSON.parse(options.body) : null });
        if (url === '/api/oauth/providers/start') {
            return {
                session_id: 'sess-1',
                provider: 'codex',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            };
        }
        if (url === '/api/oauth/sessions/sess-1') {
            return {
                status: 'completed',
                provider: 'codex'
            };
        }
        if (url === '/api/oauth/sessions/sess-1/link') {
            return {
                status: 'completed',
                provider: 'codex',
                provider_name: 'codex-sean-example-com',
                display_name: 'sean@example.com'
            };
        }
        throw new Error(`unexpected apiCall ${url}`);
    };
    state.showAlert = () => {};
    state.closeModals = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

    await state.saveProvider();

    assert.deepEqual(
        calls.map(call => call.url),
        ['/api/oauth/providers/start', '/api/oauth/sessions/sess-1']
    );
    assert.deepEqual(calls[0].options, {
        client_type: 'openai',
        provider: 'codex',
        theme: 'system',
        request_environment: { country: 'US', timezone: 'America/New_York', source: 'default' },
        proxy_mode: 'default'
    });
});

test('importCLIProxyAPIFiles uploads selected json files and refreshes providers after success', async () => {
    const state = loadApp({
        context: {
            FormData: FakeFormData
        }
    });
    const calls = [];
    const alerts = [];
    let closed = 0;
    let loadProvidersCalls = 0;
    let refreshStatusCalls = 0;
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    state.apiCall = async (url, options) => {
        calls.push({
            url,
            parts: options.body.parts.map(part => ({
                name: part.name,
                filename: part.filename,
                value: typeof part.value === 'object' ? part.value.name : part.value
            }))
        });
        return {
            imported_count: 1,
            linked_count: 1,
            skipped_count: 1,
            failed_count: 0,
            message: 'imported 1 account(s), linked 1 provider(s), skipped 1 entry(s)',
            results: [
                {
                    file: 'codex-a.json',
                    status: 'imported',
                    message: 'imported account and created provider codex-a'
                },
                {
                    file: 'codex-b.json',
                    status: 'skipped',
                    message: 'duplicate account in selected files'
                }
            ]
        };
    };
    state.showAlert = (...args) => alerts.push(args);
    state.closeModals = () => {
        closed++;
    };
    state.loadProviders = async () => {
        loadProvidersCalls++;
    };
    state.refreshStatus = async () => {
        refreshStatusCalls++;
    };

    await state.importCLIProxyAPIFiles([
        { name: 'codex-a.json' },
        { name: 'codex-b.json' }
    ]);

    assert.deepEqual(calls, [
        {
            url: '/api/oauth/import/cli-proxy-api',
            parts: [
                { name: 'client_type', filename: undefined, value: 'openai' },
                { name: 'provider', filename: undefined, value: 'codex' },
                { name: 'files', filename: 'codex-a.json', value: 'codex-a.json' },
                { name: 'files', filename: 'codex-b.json', value: 'codex-b.json' }
            ]
        }
    ]);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'info');
    assert.match(alerts[0][1], /linked 1 provider/i);
    assert.match(alerts[0][1], /codex-a\.json: imported account and created provider codex-a/i);
    assert.match(alerts[0][1], /codex-b\.json: duplicate account in selected files/i);
    assert.equal(closed, 1);
    assert.equal(loadProvidersCalls, 1);
    assert.equal(refreshStatusCalls, 1);
});

test('oauthImportResultDetails includes all result entries without truncation', () => {
    const state = loadApp();
    const details = state.oauthImportResultDetails([
        { file: 'a.json', status: 'skipped', message: 'reason-a' },
        { file: 'b.json', status: 'failed', message: 'reason-b' },
        { file: 'c.json', status: 'skipped', message: 'reason-c' },
        { file: 'd.json', status: 'failed', message: 'reason-d' },
        { file: 'e.json', status: 'skipped', message: 'reason-e' },
        { file: 'f.json', status: 'failed', message: 'reason-f' }
    ]);

    assert.match(details, /a\.json: reason-a/);
    assert.match(details, /b\.json: reason-b/);
    assert.match(details, /c\.json: reason-c/);
    assert.match(details, /d\.json: reason-d/);
    assert.match(details, /e\.json: reason-e/);
    assert.match(details, /f\.json: reason-f/);
    assert.doesNotMatch(details, /more entries/i);
});

test('triggerOAuthImportPicker clicks the hidden input', () => {
    const state = loadApp();
    let clicked = 0;
    state.$refs = {
        oauthImportInput: {
            value: 'stale',
            click() {
                clicked++;
            }
        }
    };

    state.triggerOAuthImportPicker();

    assert.equal(state.$refs.oauthImportInput.value, '');
    assert.equal(clicked, 1);
});

test('importCLIProxyAPIFiles uploads a single selected json file', async () => {
    const state = loadApp({
        context: {
            FormData: FakeFormData
        }
    });
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        oauth_provider: 'codex'
    };
    const calls = [];
    state.apiCall = async (url, options) => {
        calls.push({
            url,
            parts: options.body.parts.map(part => ({
                name: part.name,
                filename: part.filename,
                value: typeof part.value === 'object' ? part.value.name : part.value
            }))
        });
        return {
            imported_count: 0,
            linked_count: 0,
            skipped_count: 1,
            failed_count: 0,
            results: []
        };
    };
    state.showAlert = () => {};

    await state.importCLIProxyAPIFiles([
        { name: 'codex-account.json' }
    ]);

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/oauth/import/cli-proxy-api');
    assert.deepEqual(calls[0].parts, [
        { name: 'client_type', filename: undefined, value: 'openai' },
        { name: 'provider', filename: undefined, value: 'codex' },
        { name: 'files', filename: 'codex-account.json', value: 'codex-account.json' }
    ]);
});

test('startOAuthProviderAuthorization opens the real auth URL in a new window and keeps Clipal on the current page', async () => {
    const popup = createPopupWindow();
    const state = loadApp({
        context: {
            window: {
                open(url) {
                    calls.push(`open:${url}`);
                    popup.location.href = url || '';
                    return popup;
                }
            }
        }
    });
    const calls = [];
    const resumed = [];
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    state.apiCall = async (url, options) => {
        calls.push(url);
        assert.equal(state.__context.window.location.href, 'http://localhost/');
        assert.deepEqual(JSON.parse(options.body), {
            client_type: 'openai',
            provider: 'codex',
            theme: 'system',
            request_environment: { country: 'US', timezone: 'America/New_York', source: 'default' },
            proxy_mode: 'default'
        });
        return {
            session_id: 'sess-popup',
            provider: 'codex',
            auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb',
            expires_at: '2099-04-21T13:00:00Z'
        };
    };
    state.resumePendingOAuthSession = async (pending, options) => {
        resumed.push({ pending, options });
        return null;
    };
    state.showAlert = () => {};

    await state.startOAuthProviderAuthorization();

    assert.deepEqual(calls, [
        '/api/oauth/providers/start',
        'open:https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
    ]);
    assert.equal(
        popup.location.href,
        'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
    );
    assert.equal(state.__context.window.location.href, 'http://localhost/');
    assert.deepEqual(JSON.parse(state.__context.localStorage.getItem('clipal.pendingOAuthSession')), {
        session_id: 'sess-popup',
        provider: 'codex',
        client_type: 'openai',
        started_at: JSON.parse(state.__context.localStorage.getItem('clipal.pendingOAuthSession')).started_at,
        expires_at: '2099-04-21T13:00:00Z',
        auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
    });
    assert.equal(state.oauthAuthorization.phase, 'waiting');
    assert.equal(state.oauthAuthorization.popup_blocked, false);
    assert.deepEqual(JSON.parse(JSON.stringify(resumed)), [
        {
            pending: {
                session_id: 'sess-popup',
                provider: 'codex',
                client_type: 'openai',
                started_at: resumed[0].pending.started_at,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            },
            options: {
                showError: false,
                phase: 'waiting'
            }
        }
    ]);
});

test('startOAuthProviderAuthorization sends chosen region and custom proxy settings', async () => {
    const state = loadApp();
    state.theme = 'dark';
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex',
        request_country: 'JP',
        request_timezone: 'Asia/Tokyo',
        request_environment_source: 'user',
        proxy_mode: 'custom',
        proxy_url: ' http://127.0.0.1:7890 '
    };
    state.apiCall = async (url, options) => {
        assert.equal(url, '/api/oauth/providers/start');
        assert.deepEqual(JSON.parse(options.body), {
            client_type: 'openai',
            provider: 'codex',
            theme: 'dark',
            request_environment: { country: 'JP', timezone: 'Asia/Tokyo', source: 'user' },
            proxy_mode: 'custom',
            proxy_url: 'http://127.0.0.1:7890'
        });
        return {
            session_id: 'sess-proxy',
            provider: 'codex',
            auth_url: 'https://auth.openai.com/oauth/authorize'
        };
    };
    state.resumePendingOAuthSession = async () => null;
    state.showAlert = () => {};

    await state.startOAuthProviderAuthorization();
});

test('startOAuthProviderAuthorization keeps waiting state in Clipal when popup is blocked', async () => {
    const state = loadApp({
        context: {
            window: {
                open() {
                    return null;
                }
            }
        }
    });
    const resumed = [];
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    state.apiCall = async () => ({
        session_id: 'sess-blocked',
        provider: 'codex',
        auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb',
        expires_at: '2099-04-21T13:05:00Z'
    });
    state.resumePendingOAuthSession = async (pending, options) => {
        resumed.push({ pending, options });
        return null;
    };
    state.showAlert = () => {};

    await state.startOAuthProviderAuthorization();

    assert.equal(state.__context.window.location.href, 'http://localhost/');
    assert.equal(state.oauthAuthorization.phase, 'blocked');
    assert.equal(state.oauthAuthorization.popup_blocked, true);
    assert.equal(state.showAddProviderModal, true);
    assert.deepEqual(JSON.parse(JSON.stringify(resumed)), [
        {
            pending: {
                session_id: 'sess-blocked',
                provider: 'codex',
                client_type: 'openai',
                started_at: resumed[0].pending.started_at,
                expires_at: '2099-04-21T13:05:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            },
            options: {
                showError: false,
                phase: 'blocked'
            }
        }
    ]);
});

test('submitPendingOAuthAuthorizationCode posts pasted input and completes the OAuth flow', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-manual',
                provider: 'antigravity',
                client_type: 'google',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?state=sess-manual'
            })
        }
    });
    const calls = [];
    const alerts = [];
    let loadProvidersCalls = 0;
    let refreshStatusCalls = 0;
    state.selectedClient = 'claude';
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'antigravity'
    };
    state.applyOAuthAuthorizationState(state.loadPendingOAuthSession(), {
        phase: 'waiting'
    });
    state.oauthAuthorization.manual_code = 'http://127.0.0.1:39393/oauth2callback?code=manual-code&state=sess-manual';
    state.apiCall = async (url, options) => {
        calls.push({ url, payload: JSON.parse(options.body) });
        return {
            status: 'completed',
            provider: 'antigravity',
            provider_name: 'antigravity-sean-example-com',
            display_name: 'sean@example.com'
        };
    };
    state.showAlert = (...args) => alerts.push(args);
    state.loadProviders = async () => {
        loadProvidersCalls++;
    };
    state.refreshStatus = async () => {
        refreshStatusCalls++;
    };

    await state.submitPendingOAuthAuthorizationCode();

    assert.deepEqual(calls, [
        {
            url: '/api/oauth/sessions/sess-manual/code',
            payload: {
                code: 'http://127.0.0.1:39393/oauth2callback?code=manual-code&state=sess-manual',
                client_type: 'google'
            }
        }
    ]);
    assert.equal(state.showAddProviderModal, false);
    assert.equal(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
    assert.equal(loadProvidersCalls, 1);
    assert.equal(refreshStatusCalls, 1);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'success');
});

test('submitPendingOAuthAuthorizationCode keeps OAuth session active on invalid manual input', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-manual-error',
                provider: 'antigravity',
                client_type: 'google',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?state=sess-manual-error'
            })
        }
    });
    const resumed = [];
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'antigravity'
    };
    state.applyOAuthAuthorizationState(state.loadPendingOAuthSession(), {
        phase: 'waiting'
    });
    state.oauthAuthorization.manual_code = 'http://127.0.0.1:39393/oauth2callback?foo=bar';
    state.apiCall = async () => {
        throw new Error('invalid authorization response: callback URL missing code');
    };
    state.resumePendingOAuthSession = async (pending, options) => {
        resumed.push({ pending, options });
        return null;
    };
    state.showAlert = () => {};

    await state.submitPendingOAuthAuthorizationCode();

    assert.equal(state.showAddProviderModal, true);
    assert.equal(state.oauthAuthorization.phase, 'waiting');
    assert.equal(state.oauthAuthorization.manual_submit_busy, false);
    assert.equal(
        state.oauthAuthorization.manual_submit_error,
        'invalid authorization response: callback URL missing code'
    );
    assert.notEqual(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
    assert.deepEqual(JSON.parse(JSON.stringify(resumed)), [
        {
            pending: {
                session_id: 'sess-manual-error',
                provider: 'antigravity',
                client_type: 'google',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?state=sess-manual-error'
            },
            options: {
                showError: false,
                phase: 'waiting'
            }
        }
    ]);
});

test('oauth authorization manual-entry copy is provider-aware for Claude', () => {
    const state = loadApp();
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'claude'
    };
    state.applyOAuthAuthorizationState({
        session_id: 'sess-claude',
        provider: 'claude',
        client_type: 'claude',
        started_at: 1710000000000,
        expires_at: '2099-04-21T13:00:00Z',
        auth_url: 'https://claude.com/cai/oauth/authorize?state=sess-claude'
    }, {
        phase: 'waiting'
    });

    assert.equal(state.oauthAuthorizationManualEntryLabel(), 'Callback URL');
    assert.match(state.oauthAuthorizationManualCodeHint(), /full callback URL/i);
    assert.match(state.oauthAuthorizationManualCodeHint(), /code and state/i);
    assert.equal(
        state.oauthAuthorizationManualCodePlaceholder(),
        'Paste the full callback URL with code and state'
    );
    assert.equal(state.oauthAuthorizationSubmitLabel(), 'Submit Callback URL');
});

test('resumePendingOAuthSession completes stored session and clears pending storage', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-1',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
    });
    const calls = [];
    const alerts = [];
    let loadProvidersCalls = 0;
    let refreshStatusCalls = 0;
    state.selectedClient = 'claude';
    state.apiCall = async (url, options) => {
        calls.push(url);
        if (url === '/api/oauth/sessions/sess-1') {
            return {
                status: 'completed',
                provider: 'codex'
            };
        }
        if (url === '/api/oauth/sessions/sess-1/link') {
            assert.deepEqual(JSON.parse(options.body), {
                client_type: 'openai'
            });
            return {
                status: 'completed',
                provider: 'codex',
                provider_name: 'codex-sean-example-com',
                display_name: 'sean@example.com'
            };
        }
        throw new Error(`unexpected apiCall ${url}`);
    };
    state.showAlert = (...args) => alerts.push(args);
    state.closeModals = () => {};
    state.loadProviders = async () => {
        loadProvidersCalls++;
    };
    state.refreshStatus = async () => {
        refreshStatusCalls++;
    };

    await state.resumePendingOAuthSession();

    assert.deepEqual(calls, ['/api/oauth/sessions/sess-1', '/api/oauth/sessions/sess-1/link']);
    assert.equal(state.selectedClient, 'openai');
    assert.equal(loadProvidersCalls, 1);
    assert.equal(refreshStatusCalls, 1);
    assert.equal(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'success');
});

test('resumePendingOAuthSession ignores completed poll results after cancellation', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-race',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
    });
    const calls = [];
    const alerts = [];
    let resolvePoll;
    state.apiCall = async (url) => {
        calls.push(url);
        if (url === '/api/oauth/sessions/sess-race') {
            return await new Promise(resolve => {
                resolvePoll = resolve;
            });
        }
        throw new Error(`unexpected apiCall ${url}`);
    };
    state.showAlert = (...args) => alerts.push(args);

    const promise = state.resumePendingOAuthSession();
    await Promise.resolve();
    assert.equal(typeof resolvePoll, 'function');

    state.stopOAuthPolling();
    resolvePoll({
        status: 'completed',
        provider: 'codex'
    });

    const result = await promise;
    assert.equal(result, null);
    assert.deepEqual(calls, ['/api/oauth/sessions/sess-race']);
    assert.equal(alerts.length, 0);
    assert.notEqual(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
});

test('manual submit reuses in-flight oauth link request and shows success once', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-link-race',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
    });
    const calls = [];
    const alerts = [];
    let loadProvidersCalls = 0;
    let refreshStatusCalls = 0;
    let resolveLink;
    state.selectedClient = 'openai';
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    state.applyOAuthAuthorizationState(state.loadPendingOAuthSession(), {
        phase: 'waiting'
    });
    state.oauthAuthorization.manual_code = 'manual-code';
    state.apiCall = async (url, options) => {
        calls.push(url);
        if (url === '/api/oauth/sessions/sess-link-race') {
            return {
                status: 'completed',
                provider: 'codex'
            };
        }
        if (url === '/api/oauth/sessions/sess-link-race/code') {
            return {
                status: 'completed',
                provider: 'codex'
            };
        }
        if (url === '/api/oauth/sessions/sess-link-race/link') {
            assert.deepEqual(JSON.parse(options.body), {
                client_type: 'openai'
            });
            return await new Promise(resolve => {
                resolveLink = resolve;
            });
        }
        throw new Error(`unexpected apiCall ${url}`);
    };
    state.showAlert = (...args) => alerts.push(args);
    state.closeModals = () => {};
    state.loadProviders = async () => {
        loadProvidersCalls++;
    };
    state.refreshStatus = async () => {
        refreshStatusCalls++;
    };

    const pollPromise = state.resumePendingOAuthSession();
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(typeof resolveLink, 'function');

    const manualPromise = state.submitPendingOAuthAuthorizationCode();
    await Promise.resolve();
    assert.equal(calls.filter(url => url === '/api/oauth/sessions/sess-link-race/link').length, 1);

    resolveLink({
        status: 'completed',
        provider: 'codex',
        provider_name: 'codex-sean-example-com',
        display_name: 'sean@example.com'
    });

    const [pollResult, manualResult] = await Promise.all([pollPromise, manualPromise]);
    assert.equal(pollResult, null);
    assert.equal(manualResult.provider_name, 'codex-sean-example-com');
    assert.equal(calls.filter(url => url === '/api/oauth/sessions/sess-link-race/link').length, 1);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'success');
    assert.equal(loadProvidersCalls, 1);
    assert.equal(refreshStatusCalls, 1);
});

test('resumePendingOAuthSession does not downgrade a completed OAuth session when popup state becomes cross-origin', async () => {
    const popup = createPopupWindow();
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-success',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        },
        context: {
            window: {
                open() {
                    return popup;
                }
            }
        }
    });
    const alerts = [];
    let loadProvidersCalls = 0;
    let refreshStatusCalls = 0;
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    const pending = state.loadPendingOAuthSession();
    state.applyOAuthAuthorizationState(pending, {
        phase: 'waiting'
    });
    state.openOAuthAuthorizationPopup(pending.auth_url);
    Object.defineProperty(state, 'oauthAuthorizationPopup', {
        configurable: true,
        get() {
            throw new Error("Failed to read a named property '__v_isRef' from 'Window': An attempt was made to break through the security policy of the user agent.");
        },
        set() {
            throw new Error("Failed to read a named property '__v_isRef' from 'Window': An attempt was made to break through the security policy of the user agent.");
        }
    });
    state.apiCall = async (url, options) => {
        if (url === '/api/oauth/sessions/sess-success') {
            return {
                status: 'completed',
                provider: 'codex'
            };
        }
        assert.equal(url, '/api/oauth/sessions/sess-success/link');
        assert.deepEqual(JSON.parse(options.body), {
            client_type: 'openai'
        });
        return {
            status: 'completed',
            provider: 'codex',
            provider_name: 'codex-sean-example-com',
            display_name: 'sean@example.com'
        };
    };
    state.showAlert = (...args) => alerts.push(args);
    state.loadProviders = async () => {
        loadProvidersCalls++;
    };
    state.refreshStatus = async () => {
        refreshStatusCalls++;
    };

    await state.resumePendingOAuthSession();

    assert.equal(state.showAddProviderModal, false);
    assert.equal(state.oauthAuthorization.phase, 'idle');
    assert.equal(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
    assert.equal(popup.closed, true);
    assert.equal(loadProvidersCalls, 1);
    assert.equal(refreshStatusCalls, 1);
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0][0], 'success');
});

test('init selects pending OAuth target client and resumes polling after initial load', async () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-init',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
    });
    const resumed = [];
    state.refreshStatus = async () => {};
    state.loadServiceStatus = async () => {};
    state.loadProviders = async () => {};
    state.loadOAuthProviders = async () => {};
    state.loadGlobalConfig = async () => {};
    state.loadIntegrations = async () => {};
    state.initSortable = () => {};
    state.resumePendingOAuthSession = async pending => {
        resumed.push(pending);
    };

    await state.init();

    assert.equal(state.selectedClient, 'openai');
    assert.deepEqual(JSON.parse(JSON.stringify(resumed)), [
        {
            session_id: 'sess-init',
            provider: 'codex',
            client_type: 'openai',
            started_at: 1710000000000,
            expires_at: '2099-04-21T13:00:00Z',
            auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
        }
    ]);
});

test('cancelOAuthAuthorization clears pending state and keeps add modal open', () => {
    const popup = createPopupWindow();
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-cancel',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
        ,
        context: {
            window: {
                open() {
                    return popup;
                }
            }
        }
    });
    state.showAddProviderModal = true;
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex'
    };
    state.applyOAuthAuthorizationState(state.loadPendingOAuthSession(), {
        phase: 'waiting'
    });
    state.openOAuthAuthorizationPopup('https://auth.openai.com/oauth/authorize');

    state.cancelOAuthAuthorization();

    assert.equal(state.showAddProviderModal, true);
    assert.equal(state.oauthAuthorization.phase, 'idle');
    assert.equal(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
    assert.equal(popup.closed, true);
});

test('cancelOAuthAuthorization best-effort cancels backend oauth session', () => {
    const state = loadApp({
        localStorageData: {
            'clipal.pendingOAuthSession': JSON.stringify({
                session_id: 'sess-cancel',
                provider: 'codex',
                client_type: 'openai',
                started_at: 1710000000000,
                expires_at: '2099-04-21T13:00:00Z',
                auth_url: 'https://auth.openai.com/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%2Fcb'
            })
        }
    });
    const calls = [];
    state.apiCall = (url, options, background, suppressAlert) => {
        calls.push({ url, options, background, suppressAlert });
        return Promise.resolve({ status: 'error' });
    };
    state.showAddProviderModal = true;
    state.applyOAuthAuthorizationState(state.loadPendingOAuthSession(), {
        phase: 'waiting'
    });

    state.cancelOAuthAuthorization();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/oauth/sessions/sess-cancel/cancel');
    assert.equal(calls[0].options.method, 'POST');
    assert.equal(calls[0].background, true);
    assert.equal(calls[0].suppressAlert, true);
    assert.equal(state.__context.localStorage.getItem('clipal.pendingOAuthSession'), null);
});

test('saveGlobalConfig normalizes and clears non-custom upstream proxy settings', async () => {
    const state = loadApp();
    const calls = [];
    state.globalConfig = {
        ...state.globalConfig,
        upstream_proxy_mode: 'DIRECT',
        upstream_proxy_url: 'http://127.0.0.1:7890'
    };
    state.apiCall = async (url, options) => {
        calls.push({ url, options: JSON.parse(options.body) });
        return {};
    };
    state.showAlert = () => {};
    state.refreshStatus = async () => {};

    await state.saveGlobalConfig();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/config/global/update');
    assert.equal(calls[0].options.upstream_proxy_mode, 'direct');
    assert.equal(calls[0].options.upstream_proxy_url, '');
});

test('data import selection and preview use the shared data API', async () => {
    const state = loadApp();
    const calls = [];
    state.apiCall = async (url, options) => {
        calls.push({ url, body: JSON.parse(options.body) });
        return { format: 'cliproxyapi', mode: 'merge', credentials: 1, providers: 0, usage_providers: 0 };
    };
    state.dataImportMode = 'replace';
    await state.selectDataImportFiles({
        target: {
            files: [{ name: 'credential.json', text: async () => '{"type":"codex"}' }]
        }
    });
    await state.previewDataImport();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/data/import/preview');
    assert.equal(calls[0].body.format, 'auto');
    assert.equal(calls[0].body.files[0].name, 'credential.json');
    assert.equal(calls[0].body.files[0].data, '{"type":"codex"}');
    assert.equal(state.dataImportMode, 'merge');
    assert.equal(state.dataImportPlan.credentials, 1);
    assert.equal(state.showDataImportPreviewModal, true);
});

test('data import selection preserves large integers as text and resets automatic mode', async () => {
    const state = loadApp();
    state.dataImportMode = 'merge';
    const raw = '{"request_count":9007199254740993}';
    await state.selectDataImportFiles({
        target: { files: [{ name: 'backup.json', text: async () => raw }] }
    });
    assert.equal(state.dataImportFiles[0].data, raw);
    assert.equal(state.dataImportMode, '');
});

test('data import picker uses the OAuth file-picker pattern and reports selected file names', () => {
    const state = loadApp();
    let clicked = 0;
    state.$refs = {
        dataImportInput: {
            value: 'stale',
            click() { clicked++; }
        }
    };

    state.triggerDataImportPicker();

    assert.equal(state.$refs.dataImportInput.value, '');
    assert.equal(clicked, 1);
    assert.equal(state.dataImportFilesLabel(), 'No files selected');
    state.dataImportFiles = [{ name: 'backup.json' }, { name: 'oauth.json' }];
    assert.equal(state.dataImportFilesLabel(), 'backup.json, oauth.json');

    const markup = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    assert.match(markup, /class="btn btn-secondary btn-sm" @click="triggerDataImportPicker\(\)"/);
    assert.match(markup, /x-ref="dataImportInput" type="file" hidden/);
});

test('full backup export and import use separate settings panels', () => {
    const markup = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    const panelStart = '<section class="settings-panel settings-panel--wide">';
    const exportPanelStart = markup.indexOf(panelStart);
    const exportPanelEnd = markup.indexOf('</section>', exportPanelStart);
    const importPanelStart = markup.indexOf(panelStart, exportPanelEnd);
    const importPanelEnd = markup.indexOf('</section>', importPanelStart);
    const exportPanel = markup.slice(exportPanelStart, exportPanelEnd);
    const importPanel = markup.slice(importPanelStart, importPanelEnd);

    assert.ok(exportPanelStart >= 0);
    assert.ok(exportPanelEnd > exportPanelStart);
    assert.ok(importPanelStart > exportPanelEnd);
    assert.ok(importPanelEnd > importPanelStart);
    assert.equal((markup.match(/@click="exportConfig\(\)"/g) || []).length, 1);
    assert.doesNotMatch(markup, /settings-transfer-export/);
    assert.match(exportPanel, /<button type="button" class="btn btn-primary" @click="exportConfig\(\)"/);
    assert.match(exportPanel, /t\('settings\.exportFullBackup'\)/);
    assert.match(exportPanel, /t\('settings\.exportBackupCopy'\)/);
    assert.match(exportPanel, /t\('settings\.exportSaveHint'\)/);
    assert.doesNotMatch(importPanel, /exportConfig/);
    assert.match(importPanel, /t\('settings\.importTitle'\)/);
    assert.match(importPanel, /@click="previewDataImport\(\)"/);
    assert.match(importPanel, /@click="applyDataImport\(\)"/);

    const state = loadApp();
    assert.equal(state.t('settings.exportFullBackup'), 'Export Full Backup');
    assert.match(state.t('settings.exportBackupCopy'), /provider API keys/i);
    assert.match(state.t('settings.exportBackupCopy'), /OAuth access and refresh tokens/i);
    assert.match(state.t('settings.exportBackupCopy'), /usage data/i);
    assert.equal(state.t('settings.importTitle'), 'Import Data');
});

test('full backup export opens a save dialog and writes the selected file', async () => {
    const pickerCalls = [];
    const writes = [];
    const alerts = [];
    const state = loadApp({
        context: {
            fetch: async () => ({ ok: true, blob: async () => 'backup-data' }),
            window: {
                showSaveFilePicker: async options => {
                    pickerCalls.push(options);
                    return {
                        createWritable: async () => ({
                            write: async data => writes.push(data),
                            close: async () => writes.push('closed')
                        })
                    };
                }
            }
        }
    });
    state.showAlert = (type, message) => alerts.push({ type, message });

    await state.exportConfig();

    assert.equal(pickerCalls.length, 1);
    assert.equal(pickerCalls[0].suggestedName, 'clipal-data.json');
    assert.deepEqual([...pickerCalls[0].types[0].accept['application/json']], ['.json']);
    assert.deepEqual(writes, ['backup-data', 'closed']);
    assert.deepEqual(alerts, [{ type: 'success', message: 'Backup saved as clipal-data.json.' }]);
});

test('full backup export falls back to browser download and explains where to look', async () => {
    const alerts = [];
    const anchor = { click() { this.clicked = true; } };
    const state = loadApp({
        context: {
            fetch: async () => ({ ok: true, blob: async () => 'backup-data' }),
            document: {
                body: {
                    appendChild(node) { this.appended = node; },
                    removeChild(node) { this.removed = node; }
                },
                createElement: tag => {
                    assert.equal(tag, 'a');
                    return anchor;
                }
            },
            window: {
                URL: {
                    createObjectURL: blob => {
                        assert.equal(blob, 'backup-data');
                        return 'blob:clipal-backup';
                    },
                    revokeObjectURL(url) { this.revoked = url; }
                }
            }
        }
    });
    state.showAlert = (type, message) => alerts.push({ type, message });

    await state.exportConfig();

    assert.equal(anchor.href, 'blob:clipal-backup');
    assert.equal(anchor.download, 'clipal-data.json');
    assert.equal(anchor.clicked, true);
    assert.equal(state.__context.window.URL.revoked, 'blob:clipal-backup');
    assert.deepEqual(alerts, [{
        type: 'success',
        message: 'Download started for clipal-data.json. Check your browser downloads for its location.'
    }]);
});

test('full backup export treats a cancelled save dialog as a no-op', async () => {
    const alerts = [];
    const state = loadApp({
        context: {
            fetch: async () => {
                throw new Error('export should not be requested after cancellation');
            },
            window: {
                showSaveFilePicker: async () => {
                    const error = new Error('cancelled');
                    error.name = 'AbortError';
                    throw error;
                }
            }
        }
    });
    state.showAlert = (type, message) => alerts.push({ type, message });

    await state.exportConfig();

    assert.deepEqual(alerts, []);
});

test('data import apply reports returned apply details', async () => {
    const state = loadApp();
    const alerts = [];
    const calls = [];
    state.dataImportPlan = { id: 'plan' };
    state.showDataImportPreviewModal = true;
    state.dataImportFiles = [{ name: 'backup.json', data: '{}' }];
    state.apiCall = async (url, options) => {
        calls.push({ url, body: JSON.parse(options.body) });
        return { credentials: 2, providers: 3, usage_providers: 4 };
    };
    state.showAlert = (type, message) => alerts.push({ type, message });
    state.loadGlobalConfig = async () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};
    await state.applyDataImport();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/data/import/apply');
    assert.equal(calls[0].body.plan_id, 'plan');
    assert.equal(alerts.length, 1);
    assert.match(alerts[0].message, /2/);
    assert.match(alerts[0].message, /3/);
    assert.match(alerts[0].message, /4/);
    assert.equal(state.showDataImportPreviewModal, false);
});

test('data import apply shows the server error exactly once', async () => {
    const state = loadApp();
    const alerts = [];
    const calls = [];
    state.dataImportPlan = { id: 'plan' };
    state.showDataImportPreviewModal = true;
    state.dataImportFiles = [{ name: 'backup.json', data: '{}' }];
    state.apiCall = async (...args) => {
        calls.push(args);
        throw new Error('import data or mode changed after preview; preview again');
    };
    state.showAlert = (type, message) => alerts.push({ type, message });

    await state.applyDataImport();

    assert.equal(calls.length, 1);
    assert.equal(calls[0][3], true);
    assert.deepEqual(alerts, [{
        type: 'error',
        message: 'import data or mode changed after preview; preview again'
    }]);
    assert.equal(state.dataImportPlan, null);
    assert.equal(state.showDataImportPreviewModal, false);
});

test('data import preview cancellation discards the reviewed plan', () => {
    const state = loadApp();
    state.dataImportPlan = { id: 'plan' };
    state.showDataImportPreviewModal = true;

    state.closeDataImportPreview();

    assert.equal(state.dataImportPlan, null);
    assert.equal(state.showDataImportPreviewModal, false);
});

test('OAuth environment separates configured preferences from diagnosis', async () => {
    const state = loadApp();
    state.selectedClient = 'claude';
    const requests = [];
    state.apiCall = async (url, options = {}) => {
        requests.push({ url, method: options.method || 'GET' });
        if (url.endsWith('/oauth-environment')) return { account_id: 'acct', environment: { device_id: 'stable', dns: 'auto' } };
        return { environment: { device_id: 'stable', country: 'JP', dns: 'auto' }, checks: [] };
    };
    await state.openEnvironment({ name: 'account / owner', auth_type: 'oauth' });
    assert.equal(state.environmentProfile.dns, 'auto');
    assert.equal(state.environmentBusy, false);
    assert.equal(state.environmentAccount, 'acct');
    await state.diagnoseEnvironment();
    assert.equal(state.environmentProfile.country, 'JP');
    assert.deepEqual(requests.map(r => r.method), ['GET', 'POST']);
    assert.equal(requests[0].url, '/api/providers/claude/account%20%2F%20owner/oauth-environment');
    assert.equal(typeof state.saveEnvironment, 'function');
});

test('OAuth environment ignores stale account responses and recovers from API errors', async () => {
    const state = loadApp();
    let completeFirst;
    state.apiCall = url => url.includes('/first/')
        ? new Promise(resolve => { completeFirst = resolve; })
        : Promise.resolve({ account_id: 'second', environment: { device_id: 'second-id', dns: 'auto' } });
    const first = state.openEnvironment({ name: 'first', auth_type: 'oauth' });
    await state.openEnvironment({ name: 'second', auth_type: 'oauth' });
    completeFirst({ account_id: 'first', environment: { device_id: 'wrong' } });
    await first;
    assert.equal(state.environmentProfile.device_id, 'second-id');
    state.apiCall = async () => { throw new Error('unavailable'); };
    await state.diagnoseEnvironment();
    assert.equal(state.environmentBusy, false);
    assert.equal(state.environmentProfile.device_id, 'second-id');
});

test('OAuth diagnostic explanations follow the management locale', () => {
    const state = loadApp();
    const check = { detail: 'google DoH succeeded on the selected entry.' };
    state.locale = 'zh-CN';
    assert.match(state.environmentCheckDetail(check), /Google DoH 已通过/);
    state.environmentDiagnostics = { bootstrap_dns: 'DoH is not used for proxy requests; system DNS resolves the first-hop proxy hostname' };
    assert.match(state.environmentBootstrapDetail(), /代理域名第一跳/);
    state.environmentDiagnostics = { bootstrap_dns: 'DoH is not used for proxy requests' };
    assert.match(state.environmentBootstrapDetail(), /由代理解析/);
    state.environmentDiagnostics = { bootstrap_dns: 'DoH is not used' };
    assert.equal(state.environmentBootstrapDetail(), '未使用 DoH');
    state.environmentDiagnostics = { bootstrap_dns: 'DoH service hostname bootstrap with fixed-IP fallback (no explicit proxy)' };
    assert.match(state.environmentBootstrapDetail(), /服务域名引导（固定 IP 后备）/);
    state.environmentDiagnostics = { bootstrap_dns: 'Automatic DoH route; system hostname fallback on connection failure (no explicit proxy)' };
    assert.match(state.environmentBootstrapDetail(), /优先使用 DoH.*回退系统域名通路/);
    assert.doesNotMatch(state.environmentCheckDetail(check), /目标连接使用解析结果/);
    assert.match(state.environmentCheckDetail({ detail: 'Target hostnames are delegated to the standard proxy; public DoH is not required.' }), /不依赖公共 DoH/);
    assert.match(state.environmentCheckDetail({ detail: 'Optional DoH reference lookup failed; proxy requests do not depend on it.' }), /不依赖此查询/);
    assert.match(state.environmentCheckDetail({ detail: 'cloudflare DoH reference lookup succeeded; it does not verify proxy DNS or target egress.' }), /不能证明代理实际 DNS/);
    state.locale = 'en';
    assert.equal(state.environmentCheckDetail(check), check.detail);
});

test('public DoH reference is optional per dialog and never saved as account configuration', async () => {
    const state = loadApp();
    const profile = { device_id: 'stable', country: 'US', timezone: 'America/New_York', source: 'user', dns: 'auto' };
    const requests = [];
    state.apiCall = async (url, options = {}) => {
        requests.push({ url, options });
        if (url.endsWith('/oauth-environment')) return { account_identity: 'account-a', environment: { ...profile } };
        return { environment: { ...profile }, checks: [{ name: 'doh_reference', status: 'pending', detail: 'Optional DoH reference lookup failed; proxy requests do not depend on it.' }] };
    };
    await state.openEnvironment({ name: 'owner', auth_type: 'oauth' });
    assert.equal(state.environmentCheckDoH, false);
    await state.diagnoseEnvironment();
    assert.deepEqual(JSON.parse(requests[1].options.body), { check_doh: false });
    state.environmentCheckDoH = true;
    await state.diagnoseEnvironment();
    assert.deepEqual(JSON.parse(requests[2].options.body), { check_doh: true });
    assert.equal(state.environmentBusy, false);
    assert.equal(state.environmentProfile.dns, 'auto');
    assert.equal(state.environmentProfile.device_id, 'stable');
    await state.saveEnvironment();
    assert.deepEqual(JSON.parse(requests[3].options.body), { country: 'US', timezone: 'America/New_York', account_identity: 'account-a' });
    await state.openEnvironment({ name: 'other', auth_type: 'oauth' });
    assert.equal(state.environmentCheckDoH, false);
    assert.match(state.environmentDNSLabel(), /proxy|代理/);
    state.environmentProfile.dns = 'google';
    assert.match(state.environmentDNSLabel(), /Google/);
    assert.match(state.environmentDNSLabel(), /proxy|代理/);
});


test('OAuth account region confirmation sends identity and preserves DNS policy', async () => {
    const state = loadApp();
    state.selectedClient = 'openai';
    const calls = [];
    state.apiCall = async (url, options = {}) => {
        calls.push({ url, options });
        if (options.method === 'PUT') {
            return { environment: { country: 'US', timezone: 'America/New_York', source: 'user', needs_confirmation: false, dns: 'google' } };
        }
        return {
            account_identity: 'account-a', account_id: 'a',
            environment: { country: 'US', timezone: 'America/Los_Angeles', dns: 'google', needs_confirmation: true },
            upstream_region: { country_code: 'JP' }
        };
    };
    await state.openEnvironment({ name: 'owner', auth_type: 'oauth' });
    assert.equal(state.environmentForm.timezone, 'America/Los_Angeles');
    assert.equal(state.environmentUpstream.country_code, 'JP');
    assert.match(state.environmentSourceLabel(), /确认|confirmation/);
    state.environmentForm.timezone = 'America/New_York';
    await state.saveEnvironment();
    const payload = JSON.parse(calls[1].options.body);
    assert.deepEqual(payload, { country: 'US', timezone: 'America/New_York', account_identity: 'account-a' });
    assert.equal('dns' in payload, false);
    assert.equal(state.environmentProfile.dns, 'google');
    assert.equal(state.environmentProfile.source, 'user');
    assert.equal(state.environmentBusy, false);
});

test('askConfirm resolves with the dialog choice and closes the dialog', async () => {
    const state = loadApp();

    const accepted = state.askConfirm({ message: 'Delete?', danger: true });
    assert.equal(state.confirmDialog.visible, true);
    assert.equal(state.confirmDialog.danger, true);
    state.resolveConfirm(true);
    assert.equal(await accepted, true);
    assert.equal(state.confirmDialog.visible, false);

    const rejected = state.askConfirm({ message: 'Delete?' });
    state.resolveConfirm(false);
    assert.equal(await rejected, false);
});

test('deleteProvider does not call the API when confirmation is declined', async () => {
    const state = loadApp();
    const calls = [];
    state.apiCall = async url => {
        calls.push(url);
        return {};
    };
    state.askConfirm = async () => false;

    await state.deleteProvider({ name: 'p1', key_count: 2 });

    assert.deepEqual(calls, []);
});

test('saveGlobalConfig asks before newly exposing the Web UI remotely', async () => {
    const state = loadApp();
    const calls = [];
    state.apiCall = async url => {
        calls.push(url);
        return {};
    };
    state.showAlert = () => {};
    state.refreshStatus = async () => {};
    state.savedAllowRemoteWebUI = false;
    state.globalConfig.allow_remote_web_ui = true;

    state.askConfirm = async () => false;
    await state.saveGlobalConfig();
    assert.deepEqual(calls, []);

    state.askConfirm = async () => true;
    await state.saveGlobalConfig();
    assert.deepEqual(calls, ['/api/config/global/update']);
    assert.equal(state.savedAllowRemoteWebUI, true);

    let asked = false;
    state.askConfirm = async () => {
        asked = true;
        return true;
    };
    await state.saveGlobalConfig();
    assert.equal(asked, false);
});

test('globalDirty tracks edits against the saved snapshot', async () => {
    const state = loadApp();
    state.apiCall = async () => ({ listen_addr: '127.0.0.1', port: 3333 });
    assert.equal(state.globalDirty, false);

    await state.loadGlobalConfig();
    assert.equal(state.globalDirty, false);

    state.globalConfig.port = 4444;
    assert.equal(state.globalDirty, true);

    state.globalConfig.port = 3333;
    assert.equal(state.globalDirty, false);
});

test('setTab asks before discarding unsaved settings and restores the snapshot', async () => {
    const state = loadApp();
    state.apiCall = async () => ({ listen_addr: '127.0.0.1', port: 3333 });
    await state.loadGlobalConfig();
    state.activeTab = 'settings';
    state.globalConfig.port = 9999;

    state.askConfirm = async () => false;
    assert.equal(await state.setTab('status'), false);
    assert.equal(state.activeTab, 'settings');
    assert.equal(state.globalConfig.port, 9999);

    state.askConfirm = async () => true;
    assert.equal(await state.setTab('status'), true);
    assert.equal(state.activeTab, 'status');
    assert.equal(state.globalConfig.port, 3333);
    assert.equal(state.globalDirty, false);
});

test('saveGlobalConfig reports a restart when listen address or port changed', async () => {
    const state = loadApp();
    state.apiCall = async url => (url === '/api/config/global'
        ? { listen_addr: '127.0.0.1', port: 3333 }
        : {});
    state.refreshStatus = async () => {};
    const alerts = [];
    state.showAlert = (type, message) => alerts.push({ type, message });
    await state.loadGlobalConfig();

    state.globalConfig.port = 4444;
    await state.saveGlobalConfig();

    assert.deepEqual(alerts.map(item => item.type), ['success', 'info']);
    assert.equal(state.globalDirty, false);
});

test('hash routing parses tab and client and serializes current state', async () => {
    const state = loadApp({ context: { window: { location: { hash: '#/providers/openai' } } } });
    assert.deepEqual({ ...state.parseHash() }, { tab: 'providers', client: 'openai' });

    state.activeTab = 'status';
    assert.equal(state.hashForState(), '#/status');
    state.activeTab = 'providers';
    state.selectedClient = 'google';
    assert.equal(state.hashForState(), '#/providers/google');

    state.__context.window.location.hash = '#/providers/gemini';
    assert.deepEqual({ ...state.parseHash() }, { tab: 'providers', client: 'google' });
    state.__context.window.location.hash = '#/data';
    state.askConfirm = async () => true;
    await state.applyHash();
    assert.equal(state.activeTab, 'data');
});

test('index.html attributes contain no escaped quotes', () => {
    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    assert.equal(html.includes('\\"'), false);
});

test('providerRuntime reports current and degraded providers only', () => {
    const state = loadApp();
    state.selectedClient = 'claude';
    state.status = {
        clients: {
            claude: {
                current_provider: 'a',
                providers: [
                    { name: 'a', state: 'available', enabled: true },
                    { name: 'b', state: 'cooling_down', enabled: true, deactivated_in: '2m0s', key_count: 2, available_key_count: 0 },
                    { name: 'c', state: 'available', enabled: true },
                    { name: 'd', state: 'disabled', enabled: false },
                    { name: 'e', state: 'recovery_probe', enabled: true }
                ]
            }
        }
    };

    assert.equal(state.providerRuntime({ name: 'a' }).state, 'current');
    const cooling = state.providerRuntime({ name: 'b' });
    assert.equal(cooling.state, 'cooling_down');
    assert.match(cooling.label, /2m0s/);
    assert.equal(state.providerRuntime({ name: 'c' }), null);
    assert.equal(state.providerRuntime({ name: 'd' }), null);
    assert.equal(state.providerRuntime({ name: 'e' }).tone, 'info');
    assert.equal(state.providerRuntime({ name: 'missing' }), null);
});

test('activity helpers classify request outcomes and summarize metadata', () => {
    const state = loadApp();
    assert.equal(state.activityTone('switch', {}), 'warn');
    assert.equal(state.activityTone('request', { status: 200 }), 'success');
    assert.equal(state.activityTone('request', { status: 429 }), 'danger');
    assert.equal(state.activityTone('request', { status: 0 }), 'danger');
    assert.equal(
        state.activityRequestMeta({ provider: 'p1', status: 200, protocol: 'anthropic', bytes: 12 }),
        'p1 · 200 · anthropic · 12 B'
    );
    assert.equal(state.activityRequestMeta(null), '');
});

test('saveGlobalConfig keeps edits made while the save is in flight as unsaved', async () => {
    const state = loadApp();
    let release;
    state.apiCall = async url => {
        if (url === '/api/config/global') return { listen_addr: '127.0.0.1', port: 3333 };
        await new Promise(resolve => { release = resolve; });
        return {};
    };
    state.refreshStatus = async () => {};
    state.showAlert = () => {};
    await state.loadGlobalConfig();

    state.globalConfig.port = 4444;
    const saving = state.saveGlobalConfig();
    await new Promise(resolve => setTimeout(resolve, 0));
    state.globalConfig.port = 5555;
    release();
    await saving;

    assert.equal(state.savedGlobalConfig.port, 4444);
    assert.equal(state.globalDirty, true);
});

test('providerRuntime shows degraded health even for the current provider', () => {
    const state = loadApp();
    state.selectedClient = 'claude';
    state.status = {
        clients: {
            claude: {
                current_provider: 'a',
                providers: [{ name: 'a', state: 'unavailable', enabled: true, key_count: 1, available_key_count: 0 }]
            }
        }
    };

    const runtime = state.providerRuntime({ name: 'a' });
    assert.equal(runtime.state, 'unavailable');
    assert.equal(runtime.tone, 'danger');
    assert.equal(runtime.current, true);
    assert.match(runtime.title, /^In use\n/);
});

test('askConfirm makes the background inert and restores focus on close', async () => {
    const container = { inert: false };
    const toolbar = { inert: false };
    const trigger = { focus() { trigger.focused = true; } };
    const state = loadApp({
        context: {
            document: {
                activeElement: trigger,
                querySelector: selector => ({ '.container': container, '.toolbar': toolbar }[selector] || null)
            }
        }
    });

    const pending = state.askConfirm({ message: 'Sure?' });
    assert.equal(container.inert, true);
    assert.equal(toolbar.inert, true);
    state.resolveConfirm(true);
    await pending;

    assert.equal(container.inert, false);
    assert.equal(toolbar.inert, false);
    assert.equal(trigger.focused, true);
});

test('Escape opens provider discard confirmation without cancelling it on the same event', async () => {
    const state = loadApp();
    state.openAddProviderModal();
    state.providerForm.name = 'draft';
    const event = { stopped: false, stopImmediatePropagation() { this.stopped = true; } };
    const pending = state.requestCloseProvider(event);
    // Simulate the next window listener in DOM registration order.
    if (!event.stopped && state.confirmDialog.visible) state.resolveConfirm(false);
    assert.equal(event.stopped, true);
    assert.equal(state.confirmDialog.visible, true);

    const secondEscape = { stopped: false, stopImmediatePropagation() { this.stopped = true; } };
    await state.requestCloseProvider(secondEscape);
    assert.equal(secondEscape.stopped, false);
    state.resolveConfirm(false);
    assert.equal(await pending, false);
    assert.equal(state.showAddProviderModal, true);
    assert.equal(state.providerForm.name, 'draft');
});


test('mode and pin changes preserve the saved selection on failure and commit only on success', async () => {
    for (const [initialMode, action] of [['auto', s => s.setClientMode('manual')], ['manual', s => s.setClientMode('auto')], ['manual', s => s.pinProvider('backup')]]) {
        const state = loadApp();
        state.providers = [{ name: 'primary', enabled: true, priority: 1 }, { name: 'backup', enabled: true, priority: 2 }];
        state.clientConfig = { mode: initialMode, pinned_provider: initialMode === 'auto' ? '' : 'primary' };
        const before = JSON.stringify(state.clientConfig);
        state.showAlert = () => {};
        state.refreshStatus = async () => {};
        let finish;
        let payload;
        state.apiCall = async (_, options) => {
            payload = JSON.parse(options.body);
            await new Promise((resolve, reject) => { finish = reject; });
        };
        const pending = action(state);
        assert.equal(state.clientConfigSaving, true);
        assert.equal(JSON.stringify(state.clientConfig), before);
        const client = state.selectedClient;
        await state.selectClient('google');
        await state.pinProvider('backup');
        assert.equal(state.selectedClient, client);
        finish(new Error('save failed'));
        await pending;
        assert.equal(JSON.stringify(state.clientConfig), before);
        assert.equal(state.clientConfigSaving, false);
        state.apiCall = async () => ({});
        await action(state);
        assert.equal(JSON.stringify(state.clientConfig), JSON.stringify(payload));
    }
});

test('provider discard confirmation preserves edits on cancel and clears them only on acceptance', async () => {
    const state = loadApp();
    state.openAddProviderModal();
    assert.equal(state.providerDirty, false);
    state.providerForm.name = 'draft';
    state.providerForm.api_keys_text = 'unsaved-key';
    assert.equal(state.hasUnsavedInput, true);
    state.askConfirm = async () => false;
    await state.requestCloseProvider();
    assert.equal(state.showAddProviderModal, true);
    assert.equal(state.providerForm.api_keys_text, 'unsaved-key');
    state.askConfirm = async () => true;
    await state.requestCloseProvider();
    assert.equal(state.showAddProviderModal, false);
    assert.equal(state.providerForm.api_keys_text, '');
    assert.equal(state.providerDirty, false);
    state.openAddProviderModal();
    state.askConfirm = () => { throw new Error('clean form must not prompt'); };
    await state.requestCloseProvider();
    assert.equal(state.showAddProviderModal, false);
});

test('provider save failure preserves input and successful save closes without discard prompt', async () => {
    const state = loadApp();
    state.openAddProviderModal();
    state.providerForm.name = 'draft';
    state.providerForm.api_keys_text = 'unsaved-key';
    state.apiCall = async () => { throw new Error('save failed'); };
    await state.saveProvider();
    assert.equal(state.providerDirty, true);
    assert.equal(state.providerSaving, false);
    assert.equal(state.providerForm.api_keys_text, 'unsaved-key');
    state.apiCall = async () => ({});
    state.showAlert = () => {};
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};
    state.askConfirm = () => { throw new Error('saved form must not prompt'); };
    await state.saveProvider();
    assert.equal(state.showAddProviderModal, false);
    assert.equal(state.hasUnsavedInput, false);
});

test('environment draft survives cancellation and failed save, then becomes clean after successful save', async () => {
    const state = loadApp();
    const data = { environment: { country: 'US', timezone: 'America/New_York' }, account_identity: 'test' };
    state.apiCall = async () => data;
    await state.openEnvironment({ name: 'test', auth_type: 'oauth' });
    assert.equal(state.environmentDirty, false);
    state.environmentForm.country = 'JP';
    state.askConfirm = async () => false;
    await state.closeEnvironment();
    assert.equal(state.environmentVisible, true);
    state.apiCall = async () => { throw new Error('save failed'); };
    await state.saveEnvironment();
    assert.equal(state.environmentDirty, true);
    assert.equal(state.environmentForm.country, 'JP');
    state.apiCall = async () => ({ ...data, environment: { country: 'JP', timezone: 'Asia/Tokyo' } });
    await state.saveEnvironment();
    assert.equal(state.environmentDirty, false);
    state.askConfirm = () => { throw new Error('saved form must not prompt'); };
    await state.closeEnvironment();
    assert.equal(state.environmentVisible, false);
});

test('status polling retains last good data, suppresses toasts, marks stale and recovers', async () => {
    const state = loadApp();
    let fail = false;
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    state.__context.fetch = async () => {
        if (fail) throw new Error('offline');
        return { ok: true, json: async () => ({ version: 'test', clients: {} }) };
    };
    await state.refreshStatus();
    assert.equal(state.status.version, 'test');
    assert.equal(state.statusNeedsAttention('runtime'), false);
    state.statusClock = state.statusHealth.runtime.lastSuccess + 15000;
    assert.equal(state.statusNeedsAttention('runtime'), true);
    fail = true;
    await state.refreshStatus();
    await state.refreshStatus();
    assert.equal(state.status.version, 'test');
    assert.equal(state.statusHealth.runtime.failed, true);
    assert.equal(alerts.length, 0);
    fail = false;
    await state.refreshStatus();
    assert.equal(state.statusNeedsAttention('runtime'), false);
});

test('initial status failure reports no successful update and retry does not overlap requests', async () => {
    const state = loadApp();
    let rejectRequest;
    let calls = 0;
    state.apiCall = async () => {
        calls++;
        return new Promise((_, reject) => { rejectRequest = reject; });
    };
    const pending = state.refreshStatus();
    await state.refreshStatus();
    assert.equal(calls, 1);
    rejectRequest(new Error('offline'));
    await pending;
    assert.equal(state.statusNeedsAttention('runtime'), true);
    assert.equal(state.statusUpdatedLabel('runtime'), state.t('feedback.never'));
    assert.equal(state.statusHealth.runtime.busy, false);
});

test('status timeout releases the polling lock and services keep their last good state', async () => {
    let timeout;
    const state = loadApp({ context: { setTimeout: callback => { timeout = callback; return 1; }, clearTimeout() {} } });
    state.serviceStatus.running = true;
    state.__context.fetch = async (_, options) => new Promise((_, reject) => {
        options.signal.addEventListener('abort', () => reject(new Error('timed out')));
    });
    const pending = state.loadServiceStatus(true);
    timeout();
    await pending;
    assert.equal(state.serviceStatus.running, true);
    assert.equal(state.statusHealth.service.failed, true);
    assert.equal(state.statusHealth.service.busy, false);
    assert.equal(state.toasts.length, 0);
});

test('reload protection includes provider and environment drafts', async () => {
    const listeners = {};
    const state = loadApp({ context: { window: { addEventListener: (name, handler) => { listeners[name] = handler; } } } });
    state.apiCall = async url => url.startsWith('/api/providers/') ? [] : {};
    state.loadOAuthProviders = async () => {};
    state.loadIntegrations = async () => {};
    await state.init();
    const event = () => ({ prevented: false, preventDefault() { this.prevented = true; } });
    const clean = event();
    listeners.beforeunload(clean);
    assert.equal(clean.prevented, false);
    state.openAddProviderModal();
    state.providerForm.name = 'draft';
    const provider = event();
    listeners.beforeunload(provider);
    assert.equal(provider.prevented, true);
    state.closeModals();
    state.environmentVisible = true;
    state.environmentFormSnapshot = JSON.stringify(state.environmentForm);
    state.environmentForm.country = 'JP';
    const environment = event();
    listeners.beforeunload(environment);
    assert.equal(environment.prevented, true);
});


test('navigation cannot retarget an unsaved provider draft without confirmation', async () => {
    const state = loadApp();
    state.openAddProviderModal();
    state.providerForm.name = 'draft';
    state.askConfirm = async () => false;
    assert.equal(await state.setTab('status'), false);
    assert.equal(state.activeTab, 'providers');
    const client = state.selectedClient;
    await state.selectClient('google');
    assert.equal(state.selectedClient, client);
    assert.equal(state.providerForm.name, 'draft');
    state.askConfirm = async () => true;
    assert.equal(await state.setTab('status'), true);
    assert.equal(state.showAddProviderModal, false);
});

test('provider search narrows the list without changing priority and locks reordering', () => {
    const state = loadApp();
    state.providers = [
        { name: 'primary', display_name: 'Work', enabled: true, priority: 1, base_url: 'https://example.test' },
        { name: 'backup', enabled: true, priority: 2, auth_type: 'oauth' },
        { name: 'off', enabled: false, priority: 3 }
    ];
    const original = JSON.stringify(state.providers);
    assert.equal(state.providerSearchAvailable, false);
    state.providerSearch = 'WORK';
    assert.equal(state.providerSearchAvailable, true);
    assert.deepEqual(state.visibleProviders.map(p => p.name), ['primary']);
    state.providerSearch = 'example.test';
    assert.deepEqual(state.visibleProviders.map(p => p.name), ['primary']);
    assert.equal(state.canReorderProviders, false);
    state.providerSearch = '';
    assert.equal(state.visibleProviders.length, 3);
    assert.equal(state.canReorderProviders, true);
    assert.equal(JSON.stringify(state.providers), original);
    state.providers = Array.from({ length: 9 }, (_, i) => ({ name: 'p' + i, priority: i + 1 }));
    assert.equal(state.providerSearchAvailable, true);
});
test('keyboard moves persist the full order and roll back failed saves', async () => {
    const list = createProviderList(['a', 'b', 'c']);
    const domOrder = () => list.children.slice(1).map(row => row.getAttribute('data-id'));
    const state = loadApp({ context: { document: { getElementById: () => list } } });
    state.providers = [{ name: 'a', priority: 1 }, { name: 'b', priority: 2 }, { name: 'c', priority: 3 }];
    let payload;
    state.apiCall = async (_, options) => { payload = JSON.parse(options.body); };
    await state.moveProvider(state.providers[1], -1);
    assert.deepEqual(payload.providers, ['b', 'a', 'c']);
    assert.deepEqual(domOrder(), ['b', 'a', 'c']);
    assert.equal(state.providers[0].priority, 1);
    const saved = JSON.stringify(state.providers);
    state.apiCall = async () => { throw new Error('failed'); };
    await state.moveProvider(state.providers[0], 1);
    assert.equal(JSON.stringify(state.providers), saved);
    assert.deepEqual(domOrder(), ['b', 'a', 'c']);
    assert.equal(state.providerReordering, false);
    state.providerSearch = 'a';
    state.apiCall = async () => { throw new Error('must not save filtered order'); };
    await state.moveProvider(state.providers[0], 1);
    assert.equal(JSON.stringify(state.providers), saved);
    state.providerSearch = '';
    await state.moveProvider(state.providers[0], -1);
    assert.equal(JSON.stringify(state.providers), saved);
});

test('reordering locks client switches and mode changes until the save completes', async () => {
    const state = loadApp();
    state.providers = [{ name: 'a', priority: 1, enabled: true }, { name: 'b', priority: 2, enabled: true }];
    let resolve;
    let requests = 0;
    state.apiCall = async () => { requests++; await new Promise(done => { resolve = done; }); };
    const pending = state.moveProvider(state.providers[0], 1);
    await new Promise(done => setImmediate(done));
    const client = state.selectedClient;
    await state.selectClient('google');
    await state.setClientMode('manual');
    assert.equal(state.selectedClient, client);
    assert.equal(requests, 1);
    resolve();
    await pending;
    assert.equal(state.providerReordering, false);
});

test('serving label shows capability candidates and falls back when unknown', () => {
    const state = loadApp();
    assert.equal(state.runtimeCandidatesLabel(), 'Not yet known');
    state.status.clients[state.selectedClient] = { current_provider: 'a', current_providers: { text: 'a', image: 'b' } };
    assert.equal(state.runtimeCandidatesLabel(), 'text: a · image: b');
    state.status.clients[state.selectedClient] = { current_provider: 'a', current_providers: { text: 'a', image: 'a' } };
    assert.equal(state.runtimeCandidatesLabel(), 'a');
});

test('provider rows show disabled, standby, in-use and degraded states', () => {
    const state = loadApp();
    state.status.clients[state.selectedClient] = {
        current_provider: 'live',
        providers: [{ name: 'cool', state: 'cooling_down', deactivated_in: '3m' }]
    };
    assert.equal(state.providerRowState({ name: 'off', enabled: false }).tone, 'off');
    assert.equal(state.providerRowState({ name: 'idle', enabled: true }).label, 'Standby');
    assert.equal(state.providerRowState({ name: 'live', enabled: true }).tone, 'primary');
    const cool = state.providerRowState({ name: 'cool', enabled: true });
    assert.equal(cool.tone, 'warn');
    assert.match(cool.label, /3m/);
    assert.equal(state.providerNeedsAttention({ name: 'cool', enabled: true }), true);
    assert.equal(state.providerNeedsAttention({ name: 'live', enabled: true }), false);
});

test('degraded-state notices show the backend message as returned and never the provider name', () => {
    const state = loadApp();
    state.status.clients[state.selectedClient] = {
        current_provider: 'live',
        providers: [
            { name: 'cool', state: 'cooling_down', label: 'cool (cooling down)', detail: 'Rate limited. Retry in 12m.',
                skip_reason: 'deactivated', deactivated_reason: 'rate_limit', deactivated_in: '12m', deactivated_message: '429 Too Many Requests' },
            { name: 'trip', state: 'cooling_down', label: 'trip (cooling down)', detail: 'Circuit breaker is open. Retry in 30s.',
                skip_reason: 'circuit_open', circuit_state: 'open', circuit_open_in: '30s', deactivated_message: 'stale' },
            { name: 'bare', state: 'unavailable', label: 'bare (unavailable)' }
        ]
    };
    const cool = state.providerRowState({ name: 'cool', enabled: true });
    assert.equal(cool.label, 'Cooling down · 12m');
    assert.equal(cool.reason.summary, 'Rate limited. Retry in 12m.');
    assert.equal(cool.reason.message, '429 Too Many Requests');
    assert.doesNotMatch(cool.title, /cool/);
    // The upstream message belongs to a deactivation only.
    const trip = state.providerRowState({ name: 'trip', enabled: true });
    assert.equal(trip.reason.summary, 'Circuit breaker is open. Retry in 30s.');
    assert.equal(trip.reason.message, '');
    assert.equal(state.providerRowState({ name: 'bare', enabled: true }).reason, null);
    assert.equal(state.providerRowState({ name: 'live', enabled: true }).reason, null);
    state.locale = 'zh-CN';
    assert.equal(state.providerRowState({ name: 'cool', enabled: true }).reason.summary, 'Rate limited. Retry in 12m.');
});

test('provider row meta names the upstream host or OAuth and selects into the inspector', () => {
    const state = loadApp();
    assert.equal(state.providerRowMeta({ name: 'a', base_url: 'https://api.example.test/v1' }), 'api.example.test');
    assert.equal(state.providerRowMeta({ name: 'codex-a', display_name: 'a@example.test', auth_type: 'oauth' }), 'codex-a');
    const first = { name: 'a' };
    const second = { name: 'b' };
    state.providers = [first, second];
    // Without a choice the inspector shows the first provider; the narrow-screen detail stays closed.
    assert.equal(state.selectedProvider.name, 'a');
    assert.equal(state.providerDetailOpen, false);
    state.providerRowClick(second, { target: { closest: () => null } });
    assert.equal(state.providerSelected(second), true);
    assert.equal(state.providerSelected(first), false);
    assert.equal(state.providerDetailOpen, true);
    // Clicks on the drag handle or switch do not change the selection.
    state.providerRowClick(first, { target: { closest: () => ({}) } });
    assert.equal(state.selectedProvider.name, 'b');
    state.closeProviderDetail();
    assert.equal(state.providerDetailOpen, false);
    // A filtered-out or deleted selection falls back to the first visible provider.
    state.providers = [first];
    assert.equal(state.selectedProvider.name, 'a');
    state.providers = [];
    assert.equal(state.selectedProvider, null);
});

test('floating metric tooltip flips above the trigger and stays inside the viewport', () => {
    const listeners = [];
    let toolbarBottom = 100;
    const state = loadApp({ context: {
        window: {
            innerWidth: 800, innerHeight: 500,
            addEventListener(...args) { listeners.push(['add', ...args]); },
            removeEventListener(...args) { listeners.push(['remove', ...args]); }
        },
        document: {
            getElementById: () => ({ getBoundingClientRect: () => ({ width: 280, height: 70 }) }),
            querySelector: () => ({ getBoundingClientRect: () => ({ bottom: toolbarBottom }) })
        }
    } });
    const tip = state.__context.floatingTooltip();
    let clipped = false;
    let hovered = false;
    let focused = false;
    let alignEnd = false;
    tip.$el = {
        classList: { contains: name => name === 'tooltip' || (name === 'tooltip--end' && alignEnd) },
        getBoundingClientRect: () => ({ left: 750, right: 790, top: 430, bottom: 470 }),
        closest: () => ({ getBoundingClientRect: () => ({ top: 100, bottom: clipped ? 400 : 490 }) }),
        contains: () => focused,
        matches: () => hovered
    };
    tip.$id = () => 'metric-tooltip-1';
    tip.$nextTick = callback => callback();
    tip.init();
    tip.show();
    assert.equal(tip.open, true);
    assert.equal(tip.position.left, '512px');
    assert.equal(tip.position.top, '352px');
    alignEnd = true;
    tip.place();
    assert.equal(tip.position.left, '510px');
    focused = true;
    tip.leave();
    assert.equal(tip.open, true, 'mouse leave retains the keyboard tooltip');
    focused = false;
    hovered = true;
    tip.blur();
    assert.equal(tip.open, true, 'blur retains the hovered tooltip');
    hovered = false;
    tip.blur();
    assert.equal(tip.open, false);
    tip.show();
    clipped = true;
    listeners[0][2]();
    assert.equal(tip.open, false, 'scrolling the trigger out of its body hides the tooltip');
    clipped = false;
    tip.show();
    toolbarBottom = 480;
    listeners[0][2]();
    assert.equal(tip.open, false, 'the sticky toolbar occludes the trigger even inside the viewport');
    tip.destroy();
    assert.equal(listeners.length, 4);
    for (let index = 0; index < 2; index++) {
        assert.equal(listeners[index][1], listeners[index + 2][1]);
        assert.equal(listeners[index][2], listeners[index + 2][2]);
        assert.equal(listeners[index][3], listeners[index + 2][3]);
    }
});

test('floating metric tooltip clamps the left edge and ignores metrics without data', () => {
    const state = loadApp({ context: {
        window: { innerWidth: 800, innerHeight: 500 },
        document: {
            getElementById: () => ({ getBoundingClientRect: () => ({ width: 280, height: 70 }) }),
            querySelector: () => null
        }
    } });
    const tip = state.__context.floatingTooltip();
    let enabled = false;
    tip.$el = {
        classList: { contains: name => name === 'tooltip' && enabled },
        getBoundingClientRect: () => ({ left: -10, right: 100, top: 150, bottom: 180 }),
        closest: () => null
    };
    tip.$id = () => 'metric-tooltip-1';
    tip.$nextTick = callback => callback();
    tip.show();
    assert.equal(tip.open, false);
    enabled = true;
    tip.show();
    assert.equal(tip.position.left, '8px');
    assert.equal(tip.position.top, '188px');
});

test('short wide windows use the same provider navigation as narrow screens', () => {
    let scrollTop = 900;
    let focused = false;
    const state = loadApp({ context: {
        window: {
            innerWidth: 1280, innerHeight: 320,
            matchMedia(query) {
                return { matches: query === '(max-width: 760px), (max-height: 360px)' };
            },
            scrollTo({ top }) { scrollTop = top; }
        },
        document: { querySelector: () => ({ focus() { focused = true; } }) }
    } });
    state.providers = [{ name: 'short-window-provider' }];
    state.selectProvider(state.providers[0]);
    assert.equal(state.providerDetailVisible, true);
    assert.equal(scrollTop, 0);
    assert.equal(focused, true);
});

test('compact provider layout shows the list and empty-state feedback when search has no matches', () => {
    const state = loadApp();
    state.providers = Array.from({ length: 9 }, (_, index) => ({ name: `provider-${index}` }));
    assert.equal(state.providerDetailVisible, false);
    state.selectProvider(state.providers[4]);
    assert.equal(state.providerDetailVisible, true);
    state.providerSearch = 'no-match';
    assert.equal(state.selectedProvider, null);
    assert.equal(state.providerDetailVisible, false);
    // Clearing the query restores the chosen provider and its Back navigation.
    state.providerSearch = '';
    assert.equal(state.selectedProvider.name, 'provider-4');
    assert.equal(state.providerDetailVisible, true);
    state.closeProviderDetail();
    assert.equal(state.providerDetailVisible, false);
    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    assert.match(html, /class="provider-split" :class="\{'is-detail-open': providerDetailVisible\}"/);
});

test('status polling is scoped to the tab that shows the data', () => {
    const state = loadApp();
    state.activeTab = 'providers';
    assert.equal(state.statusPolledOn('runtime'), true);
    assert.equal(state.statusPolledOn('service'), false);
    state.activeTab = 'services';
    assert.equal(state.statusPolledOn('runtime'), false);
    assert.equal(state.statusPolledOn('service'), true);
    state.activeTab = 'status';
    assert.equal(state.statusPolledOn('runtime'), true);
    assert.equal(state.statusPolledOn('service'), true);
    state.activeTab = 'settings';
    assert.equal(state.statusPolledOn('runtime'), false);
});

test('system status refreshes service readiness on arrival and retains failure feedback', async () => {
    const state = loadApp();
    state.activeTab = 'settings';
    state.serviceStatus.running = true;
    state.statusHealth.service.lastSuccess = Date.now() - 60000;
    const calls = [];
    state.apiCall = async url => {
        calls.push(url);
        if (url === '/api/service/status') throw new Error('offline');
        return {};
    };
    await state.setTab('status');
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls.sort(), ['/api/service/status', '/api/status']);
    assert.equal(state.setupServiceReady(), true);
    assert.equal(state.statusNeedsAttention('service'), true);
    assert.equal(state.statusPolledOn('service'), true);
    state.apiCall = async () => ({ running: false });
    await state.loadServiceStatus(true);
    assert.equal(state.setupServiceReady(), false);
    assert.equal(state.statusNeedsAttention('service'), false);
});

test('OAuth rows without plan metadata still expose credential details', () => {
    const state = loadApp();
    const provider = { name: 'claude-account', auth_type: 'oauth', oauth_provider: 'claude', oauth_auth_status: 'reauth_needed' };
    state.providers = [provider];
    state.providerRowClick(provider, { target: { closest: () => null } });
    assert.equal(state.providerSelected(provider), true);
    assert.equal(state.providerOAuthAuthStatusLabel(provider), 'Reauth needed');
    assert.equal(state.providerOAuthRefreshSummary(provider), 'Never');
});

test('integration change summary reports counts, no-op and oversized files', () => {
    const state = loadApp();
    assert.equal(state.integrationChangesSummary({ changes: state.integrationChanges('a', 'b') }), 'Changes: +1 / −1 lines');
    assert.equal(state.integrationChangesSummary({ changes: state.integrationChanges('a', 'a') }), 'No changes to apply');
    assert.match(state.integrationChangesSummary({ changes: { limited: true, rows: [] } }), /too large/);
});
test('integration diff handles insertions, removals, repeated lines and large files', () => {
    const state = loadApp();
    let diff = state.integrationChanges('keep\nold\nkeep', 'keep\nnew\nkeep');
    assert.equal(diff.added, 1);
    assert.equal(diff.removed, 1);
    assert.equal(diff.rows[0].text, '− old');
    assert.equal(diff.rows[1].text, '+ new');
    assert.equal(state.integrationChanges('same', 'same').rows.length, 0);
    assert.equal(state.integrationChanges('', '<script>bad()</script>').rows[0].text, '+ <script>bad()</script>');
    assert.equal(state.integrationChanges('removed', '').removed, 1);
    assert.equal(state.integrationChanges('x\n'.repeat(1000), 'y\n'.repeat(1000)).limited, true);
    assert.equal(state.integrationChanges('x'.repeat(100001), '').limited, true);
});

test('integration preview uses planned content for apply and original backup for rollback', () => {
    const state = loadApp();
    const item = { product: 'codex', current_content: 'current', planned_content: 'planned', backup_content: 'original', backup_available: true };
    let normalized = state.normalizeIntegration({ ...item, state: 'not_configured' });
    assert.equal(normalized.changes.rows[1].text, '+ planned');
    normalized = state.normalizeIntegration({ ...item, state: 'configured' });
    assert.equal(normalized.changes.rows[1].text, '+ original');
});


test('drag handle and keyboard moves use the same reorder path; search blocks dragging', async () => {
    let options;
    const state = loadApp({ context: {
        document: { getElementById: () => createProviderList(['a', 'b']) },
        Sortable: { create: (_, supplied) => { options = supplied; return { sort() {}, destroy() {} }; } }
    } });
    state.providers = [{ name: 'a' }, { name: 'b' }];
    state.initSortable();
    assert.equal(options.handle, '.provider-drag-handle');
    assert.equal(options.onMove(), true);
    state.providerSearch = 'a';
    assert.equal(options.onMove(), false);
    state.providerSearch = '';
    let moved;
    state.reorderProviders = async (from, to) => { moved = [from, to]; };
    await options.onEnd({ oldDraggableIndex: 0, newDraggableIndex: 1, oldIndex: 2, newIndex: 3 });
    assert.deepEqual(moved, [0, 1]);
});

test('takeover cards abbreviate home paths, tone states and toggle previews per product', () => {
    const state = loadApp();
    assert.equal(state.displayHomePath('/Users/sean/.claude/settings.json'), '~/.claude/settings.json');
    assert.equal(state.displayHomePath('/home/dev/.codex/config.toml'), '~/.codex/config.toml');
    assert.equal(state.displayHomePath('C:\\Users\\bob\\.codex\\config.toml'), '~\\.codex\\config.toml');
    assert.equal(state.displayHomePath('/etc/clipal.yaml'), '/etc/clipal.yaml');
    assert.equal(state.integrationStateTone('configured'), 'success');
    assert.equal(state.integrationStateTone('error'), 'danger');
    assert.equal(state.integrationStateTone('not_configured'), 'idle');
    state.toggleIntegrationPreview('claude');
    assert.equal(state.integrationPreviewOpen.claude, true);
    assert.equal(!!state.integrationPreviewOpen.codex, false);
    state.toggleIntegrationPreview('claude');
    assert.equal(state.integrationPreviewOpen.claude, false);
});

test('mode summary and OAuth auth tone follow the current state', () => {
    const state = loadApp();
    state.clientConfig = { mode: 'auto' };
    assert.match(state.modeHelpSummary(), /priority/);
    state.clientConfig = { mode: 'manual' };
    assert.match(state.modeHelpSummary(), /pinned/);
    const oauth = status => ({ auth_type: 'oauth', oauth_auth_status: status });
    assert.equal(state.providerOAuthAuthStatusTone(oauth('ready')), 'success');
    assert.equal(state.providerOAuthAuthStatusTone(oauth('refresh_due')), 'warn');
    assert.equal(state.providerOAuthAuthStatusTone(oauth('reauth_needed')), 'danger');
});

test('environment dropdowns localize countries and preserve stored country and timezone aliases', () => {
    const state = loadApp();
    const english = state.environmentCountryOptions('US');
    assert.equal(english.length, 249);
    assert.match(english.find(option => option.value === 'US').label, /United States/);
    state.locale = 'zh-CN';
    assert.match(state.environmentCountryOptions('JP').find(option => option.value === 'JP').label, /日本/);
    assert.equal(state.environmentCountryOptions('XK').filter(option => option.value === 'XK').length, 1);
    const zones = state.environmentTimezoneOptions('US/Eastern');
    for (const zone of ['US/Eastern', 'UTC', 'America/New_York', 'Asia/Shanghai', 'Asia/Tokyo', 'Europe/London']) {
        assert.ok(zones.includes(zone), zone);
    }
    assert.equal(zones.filter(zone => zone === 'US/Eastern').length, 1);
    state.environmentForm = { country: 'US', timezone: 'US/Eastern' };
    state.environmentForm.country = 'JP';
    state.environmentCountryOptions(state.environmentForm.country);
    state.environmentTimezoneOptions(state.environmentForm.timezone);
    assert.equal(state.environmentForm.timezone, 'US/Eastern');
});

test('country dropdown keeps English fallback names without Intl.DisplayNames', () => {
    const state = loadApp({ context: { Intl: {} } });
    assert.match(state.environmentCountryOptions('JP').find(option => option.value === 'JP').label, /Japan/);
});

test('timezone labels show current offsets, including daylight saving and fractional hours', () => {
    const state = loadApp();
    const winter = new Date('2026-01-15T12:00:00Z');
    const summer = new Date('2026-07-15T12:00:00Z');
    assert.match(state.environmentTimezoneLabel('Asia/Shanghai', winter), /^UTC\+8 ·/);
    assert.match(state.environmentTimezoneLabel('America/New_York', winter), /^UTC-5 ·/);
    assert.match(state.environmentTimezoneLabel('America/New_York', summer), /^UTC-4 ·/);
    assert.match(state.environmentTimezoneLabel('Asia/Kathmandu', winter), /^UTC\+5:45 ·/);
    assert.match(state.environmentTimezoneLabel('Pacific/Chatham', winter), /^UTC\+13:45 ·/);
    assert.match(state.environmentTimezoneLabel('UTC', winter), /^UTC\+0 ·/);
    assert.equal(state.environmentTimezoneLabel('Unknown/SavedAlias', winter), 'Unknown/SavedAlias');
    state.locale = 'zh-CN';
    assert.match(state.environmentTimezoneLabel('Asia/Shanghai', winter), /中国/);
});

function loadChoicePicker(config) {
    const state = loadApp();
    const picker = state.__context.choicePicker(config);
    picker.$nextTick = callback => callback();
    picker.$refs = {};
    picker.place = () => {};
    return picker;
}

test('timezone search supports offsets, countries and identifiers while preserving saved aliases', () => {
    const state = loadApp();
    state.locale = 'zh-CN';
    const choices = state.environmentTimezoneChoices('US/Eastern');
    assert.equal(choices.filter(option => option.value === 'US/Eastern').length, 1);
    const picker = loadChoicePicker({ get: () => 'US/Eastern', options: () => choices });
    for (const query of ['+8', 'UTC+08', 'UTC+08:00', 'Shanghai', 'Asia/Shanghai', '中国', 'China', 'CN']) {
        picker.search(query);
        assert.ok(picker.matches.some(option => option.value === 'Asia/Shanghai'), query);
    }
    picker.search('GMT+08:00');
    assert.ok(picker.matches.some(option => option.value === 'Asia/Shanghai'));
    assert.ok(!picker.matches.some(option => option.value === 'Australia/Eucla'));
    picker.search('+5:45');
    assert.ok(picker.matches.some(option => option.value === 'Asia/Kathmandu'));
    picker.search('US/Eastern');
    assert.equal(picker.matches[0].value, 'US/Eastern');
});

test('country search accepts localized and English names, codes and accent-free text', () => {
    const state = loadApp();
    state.locale = 'zh-CN';
    const picker = loadChoicePicker({ get: () => 'US', options: () => state.environmentCountryOptions() });
    for (const query of ['JP', 'Japan', '日本']) {
        picker.search(query);
        assert.ok(picker.matches.some(option => option.value === 'JP'), query);
    }
    picker.search('reunion');
    assert.ok(picker.matches.some(option => option.value === 'RE'));
});

test('choice picker navigates, selects only listed options, and discards unconfirmed search text', () => {
    let value = 'b';
    let disabled = false;
    const choices = [{ value: 'a', label: 'Alpha' }, { value: 'b', label: 'Beta' }];
    const picker = loadChoicePicker({ get: () => value, set: next => { value = next; }, options: () => choices, disabled: () => disabled });
    picker.place = () => {};
    picker.show();
    assert.equal(picker.active, 1);
    picker.move(1);
    assert.equal(picker.active, 0);
    picker.move(-1);
    assert.equal(picker.active, 1);
    picker.edge(false);
    assert.equal(picker.active, 0);
    picker.edge(true);
    assert.equal(picker.active, 1);
    picker.search('not-a-choice');
    assert.equal(picker.active, -1);
    picker.choose(0);
    assert.equal(value, 'b');
    picker.move(1);
    assert.equal(picker.active, -1);
    picker.close();
    assert.equal(picker.selectedLabel, 'Beta');
    picker.show();
    picker.search('alpha');
    disabled = true;
    picker.choose(0);
    assert.equal(value, 'b');
    disabled = false;
    picker.choose(0);
    assert.equal(value, 'a');
    assert.equal(picker.open, false);
    assert.equal(picker.query, '');
    picker.search('beta');
    assert.equal(picker.open, true);
    assert.equal(picker.matches[0].value, 'b');
    assert.equal(value, 'a');
});

test('choosing an option never steals focus after the user tabs to another field', () => {
    const state = loadApp();
    const callbacks = [];
    let selectedText = 0;
    const input = { select() { selectedText++; } };
    const picker = state.__context.choicePicker({
        get: () => 'a', set() {}, options: () => [{ value: 'a', label: 'Alpha' }]
    });
    picker.$refs = { input };
    picker.$nextTick = callback => callbacks.push(callback);
    picker.choose(0);
    state.__context.document.activeElement = {};
    callbacks.shift()();
    assert.equal(selectedText, 0);
    picker.choose(0);
    state.__context.document.activeElement = input;
    callbacks.shift()();
    assert.equal(selectedText, 1);
});

test('mode saves lock provider mutations until completion', async () => {
    const state = loadApp();
    const provider = { name: 'primary', enabled: true, priority: 1 };
    state.providers = [provider];
    state.refreshStatus = async () => {};
    state.showAlert = () => {};
    let finish;
    let requests = 0;
    state.apiCall = async () => {
        requests++;
        await new Promise(resolve => { finish = resolve; });
    };
    const pending = state.pinProvider(provider.name);
    assert.equal(state.canMutateProviders, false);
    await state.deleteProvider(provider);
    state.editProvider(provider);
    state.openAddProviderModal();
    await state.saveProvider();
    assert.equal(state.showEditProviderModal, false);
    assert.equal(state.showAddProviderModal, false);
    assert.equal(requests, 1);
    finish();
    await pending;
    assert.equal(state.canMutateProviders, true);
});

test('late mode response reloads config after an in-flight deletion instead of restoring a deleted pin', async () => {
    const state = loadApp();
    state.providers = [{ name: 'primary', enabled: true, priority: 1 }];
    state.refreshStatus = async () => {};
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    let finish;
    let configReads = 0;
    state.apiCall = async (url, options = {}) => {
        if (options.method === 'PUT') {
            await new Promise(resolve => { finish = resolve; });
            return {};
        }
        if (url.startsWith('/api/providers/')) return [];
        configReads++;
        return { mode: 'auto', pinned_provider: '' };
    };
    const pending = state.pinProvider('primary');
    await state.loadProviders(true);
    finish();
    await pending;
    assert.equal(state.clientConfig.mode, 'auto');
    assert.equal(state.clientConfig.pinned_provider, '');
    assert.equal(configReads, 2);
    assert.equal(alerts.length, 0);
});

test('unit editor displays saved durations and byte counts without rewriting configuration', () => {
    const state = loadApp();
    let raw = '1h30m';
    let writes = 0;
    const field = state.__context.unitField({ get: () => raw, set: value => { raw = value; writes++; } });
    field.sync();
    assert.equal(field.value, '90');
    assert.equal(field.unit, 'm');
    assert.equal(raw, '1h30m');
    assert.equal(writes, 0);
    field.commit('45');
    field.sync();
    assert.equal(raw, '45m');
    field.unit = 's';
    field.commit('2.5');
    assert.equal(raw, '2.5s');
    raw = '0';
    field.sync();
    assert.equal(field.value, '0');
    assert.equal(raw, '0');

    raw = 67108864;
    const size = state.__context.unitField({ kind: 'bytes', get: () => raw, set: value => { raw = value; } });
    size.sync();
    assert.equal(size.value, '64');
    assert.equal(size.unit, 'MiB');
    size.commit('128');
    assert.equal(raw, 134217728);
    size.unit = 'B';
    size.commit('1.5');
    assert.equal(size.error, 'forms.wholeBytes');
    size.commit('1025');
    assert.equal(size.error, '');
    assert.equal(raw, 1025);
});

test('unit editor preserves optional blanks, fine durations, and uncommon valid saved formats', () => {
    const state = loadApp();
    let raw = '';
    let enabled = true;
    const field = state.__context.unitField({ optional: true, positive: true, disabled: () => !enabled, get: () => raw, set: value => { raw = value; } });
    field.sync();
    assert.equal(field.value, '');
    field.commit('0');
    assert.equal(field.error, 'forms.positiveDuration');
    field.commit('');
    assert.equal(raw, '');
    assert.equal(field.error, '');
    raw = '2ns';
    field.sync();
    assert.equal(field.unit, 'ns');
    assert.ok(field.choices.includes('ns'));
    raw = '+1h30m';
    field.sync();
    assert.equal(field.custom, '+1h30m');
    assert.equal(raw, '+1h30m');
    enabled = false;
    field.sync();
    assert.equal(field.disabled, true);
});

test('provider loading errors preserve the last list and recover without pretending the group is empty', async () => {
    const state = loadApp({ context: { console: { ...console, error() {} } } });
    state.providers = [{ name: 'saved' }];
    state.apiCall = async () => { throw new Error('offline'); };
    await state.loadProviders(true);
    assert.equal(state.providers[0].name, 'saved');
    assert.equal(state.providerLoadError, 'offline');
    assert.equal(state.canMutateProviders, false);
    state.apiCall = async url => url.startsWith('/api/providers/') ? [{ name: 'fresh' }] : { mode: 'auto' };
    await state.loadProviders(true);
    assert.equal(state.providers[0].name, 'fresh');
    assert.equal(state.providerLoadError, '');
    assert.equal(state.canMutateProviders, true);
});

test('an older client response cannot overwrite the selected client list', async () => {
    const state = loadApp();
    let finish;
    state.apiCall = async url => {
        if (url === '/api/providers/claude') return new Promise(resolve => { finish = resolve; });
        if (url.startsWith('/api/providers/')) return [{ name: 'openai-only' }];
        return { mode: 'auto' };
    };
    const older = state.loadProviders(true);
    state.selectedClient = 'openai';
    await state.loadProviders(true);
    finish([{ name: 'claude-only' }]);
    await older;
    assert.equal(state.providers[0].name, 'openai-only');
    assert.equal(state.providerLoadBusy, false);
});

test('service HTTP failures show errors and do not announce a requested action', async () => {
    const scheduled = [];
    const state = loadApp({ context: {
        fetch: async () => ({ ok: false, json: async () => ({ error: 'permission denied' }) }),
        setTimeout: (...args) => scheduled.push(args)
    } });
    state.serviceActionDisabledReason = () => '';
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    await state.serviceAction('start');
    assert.deepEqual(alerts, [['error', 'permission denied']]);
    assert.equal(state.serviceBusyAction, '');
    assert.equal(scheduled.length, 0);
});

test('interrupted service actions remain unconfirmed and recheck status without accepting duplicates', async () => {
    const scheduled = [];
    let calls = 0;
    let finish;
    const state = loadApp({ context: {
        fetch: async () => { calls++; await new Promise(resolve => { finish = resolve; }); throw new Error('network'); },
        setTimeout: (...args) => scheduled.push(args)
    } });
    state.serviceActionDisabledReason = () => '';
    state.fetchTrackedStatus = async () => ({ running: true });
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    const pending = state.serviceAction('start');
    await state.loadServiceStatus(true);
    assert.equal(state.serviceBusyAction, 'start');
    await state.serviceAction('start');
    assert.equal(calls, 1);
    finish();
    await pending;
    assert.equal(alerts[0][1], state.t('services.actionUnconfirmed'));
    assert.equal(scheduled.length, 4);
});

test('account environment loading can be closed and a late response cannot reopen or change it', async () => {
    const state = loadApp();
    let finish;
    state.apiCall = async () => new Promise(resolve => { finish = resolve; });
    const pending = state.openEnvironment({ name: 'account', auth_type: 'oauth' });
    assert.equal(state.environmentBusy, true);
    assert.equal(await state.closeEnvironment(), true);
    finish({ environment: { country: 'JP', timezone: 'Asia/Tokyo' }, account_identity: 'account' });
    await pending;
    assert.equal(state.environmentVisible, false);
    assert.equal(state.environmentBusy, false);
    assert.equal(state.environmentIdentity, '');
});

test('all dialog focus boundaries include inputs and selects and exclude hidden controls', () => {
    const state = loadApp();
    const first = { focus() { first.focused = true; }, getClientRects: () => [1] };
    const last = { focus() { last.focused = true; }, getClientRects: () => [1] };
    const hidden = { getClientRects: () => [] };
    const dialog = { querySelectorAll: () => [hidden, first, last], contains: () => true };
    let prevented = false;
    state.__context.document.activeElement = last;
    state.trapDialogFocus({ currentTarget: dialog, shiftKey: false, preventDefault() { prevented = true; } });
    assert.equal(first.focused, true);
    assert.equal(prevented, true);
    state.__context.document.activeElement = first;
    state.trapDialogFocus({ currentTarget: dialog, shiftKey: true, preventDefault() {} });
    assert.equal(last.focused, true);
});

test('unit editors reject values the backend would silently raise while allowing disabled timeouts', () => {
    const state = loadApp();
    let raw = '5m';
    const duration = state.__context.unitField({ minimum: 300, get: () => raw, set: value => { raw = value; } });
    duration.sync();
    duration.unit = 's';
    duration.commit('30');
    assert.equal(duration.error, 'forms.minimumTimeout');
    duration.commit('0');
    assert.equal(duration.error, '');
    duration.commit('300');
    assert.equal(duration.error, '');
    raw = 67108864;
    const size = state.__context.unitField({ kind: 'bytes', minimum: 67108864, get: () => raw, set: value => { raw = value; } });
    size.sync();
    size.commit('1');
    assert.equal(size.error, 'forms.minimumSize');
    size.commit('64');
    assert.equal(size.error, '');
});

test('client keyboard selection wraps and supports first and last while preserving save locks', async () => {
    const state = loadApp({ context: { document: { querySelector: () => null } } });
    const visited = [];
    state.loadProviders = async () => visited.push(state.selectedClient);
    state.loadOAuthProviders = async () => {};
    await state.moveClient(-1);
    assert.equal(state.selectedClient, 'google');
    await state.moveClient('first');
    assert.equal(state.selectedClient, 'claude');
    await state.moveClient('last');
    assert.equal(state.selectedClient, 'google');
    state.clientConfigSaving = true;
    await state.moveClient(1);
    assert.equal(state.selectedClient, 'google');
    assert.deepEqual(visited, ['google', 'claude', 'google']);
});

test('switching client groups resets the provider selection and respects save locks', async () => {
    const state = loadApp({ context: { document: { querySelector: () => null } } });
    const visited = [];
    state.loadProviders = async () => visited.push(state.selectedClient);
    state.loadOAuthProviders = async () => {};
    state.selectedProviderName = 'a';
    state.providerDetailOpen = true;
    await state.selectClient('google');
    assert.equal(state.selectedClient, 'google');
    assert.equal(state.selectedProviderName, '');
    assert.equal(state.providerDetailOpen, false);
    state.clientConfigSaving = true;
    await state.selectClient('claude');
    assert.equal(state.selectedClient, 'google');
    assert.deepEqual(visited, ['google']);
});

test('account preference saves report success and prevent closure only while writing', async () => {
    const state = loadApp();
    state.environmentVisible = true;
    state.environmentIdentity = 'account';
    state.environmentPath = '/api/providers/claude/account';
    let finish;
    state.apiCall = async () => new Promise(resolve => { finish = resolve; });
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    const pending = state.saveEnvironment();
    assert.equal(state.environmentSaving, true);
    assert.equal(await state.closeEnvironment(), false);
    finish({ environment: { country: 'JP', timezone: 'Asia/Tokyo' } });
    await pending;
    assert.equal(state.environmentSaving, false);
    assert.deepEqual(alerts, [['success', state.t('requestEnv.saved')]]);
    assert.equal(await state.closeEnvironment(), true);
});

test('account load failures offer retry state and late errors after close stay silent', async () => {
    const state = loadApp();
    const alerts = [];
    state.showAlert = (...args) => alerts.push(args);
    state.apiCall = async () => { throw new Error('offline'); };
    await state.openEnvironment({ name: 'account', auth_type: 'oauth' });
    assert.equal(state.environmentLoadError, 'offline');
    assert.equal(state.environmentBusy, false);
    assert.equal(state.environmentProvider.name, 'account');
    state.apiCall = async () => ({ environment: { country: 'US', timezone: 'America/New_York' }, account_identity: 'account' });
    await state.openEnvironment(state.environmentProvider);
    assert.equal(state.environmentLoadError, '');
    assert.equal(state.environmentIdentity, 'account');
    await state.closeEnvironment();
    let reject;
    state.apiCall = async () => new Promise((_, fail) => { reject = fail; });
    const pending = state.openEnvironment(state.environmentProvider);
    await state.closeEnvironment();
    reject(new Error('late error'));
    await pending;
    assert.equal(state.environmentLoadError, '');
    assert.equal(alerts.length, 0);
});

test('translations have identical keys and interpolation parameters in both languages', () => {
    const state = loadApp();
    const flatten = (object, prefix = '', result = {}) => {
        for (const [key, value] of Object.entries(object)) {
            const name = prefix ? `${prefix}.${key}` : key;
            if (typeof value === 'object') flatten(value, name, result);
            else result[name] = value;
        }
        return result;
    };
    const english = flatten(state.messages.en);
    const chinese = flatten(state.messages['zh-CN']);
    assert.deepEqual(Object.keys(english).sort(), Object.keys(chinese).sort());
    const parameters = value => [...value.matchAll(/\{(\w+)\}/g)].map(match => match[1]).sort();
    for (const key of Object.keys(english)) {
        assert.deepEqual(parameters(english[key]), parameters(chinese[key]), key);
    }
    state.locale = 'zh-CN';
    state.editingProviderKeyCount = 1;
    assert.match(state.providerEditKeyHint(), /API Key/);
});

test('provider save keeps list actions and client selection locked through the final refresh', async () => {
    const state = loadApp();
    state.openAddProviderModal();
    state.providerForm.name = 'new-provider';
    state.apiCall = async () => ({});
    state.showAlert = () => {};
    state.loadProviders = async () => {};
    let finishRefresh;
    state.refreshStatus = () => new Promise(resolve => { finishRefresh = resolve; });
    const pending = state.saveProvider();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(state.showAddProviderModal, false);
    assert.equal(state.canMutateProviders, false);
    state.openAddProviderModal();
    assert.equal(state.showAddProviderModal, false);
    await state.selectClient('google');
    assert.equal(state.selectedClient, 'claude');
    finishRefresh();
    await pending;
    assert.equal(state.canMutateProviders, true);
});

test('provider toggles serialize conflicting operations and release the lock after failure', async () => {
    const state = loadApp({ context: { console: { ...console, error() {} } } });
    const provider = { name: 'primary', enabled: true };
    let finish;
    let calls = 0;
    state.apiCall = () => { calls++; return new Promise((resolve, reject) => { finish = reject; }); };
    const pending = state.toggleProvider(provider, { target: { checked: false } });
    assert.equal(state.canMutateProviders, false);
    const duplicate = { target: { checked: true } };
    await state.toggleProvider(provider, duplicate);
    await state.pinProvider(provider.name);
    await state.selectClient('google');
    assert.equal(calls, 1);
    assert.equal(duplicate.target.checked, false);
    assert.equal(state.selectedClient, 'claude');
    finish(new Error('offline'));
    await pending;
    assert.equal(provider.enabled, true);
    assert.equal(state.canMutateProviders, true);
});

test('provider deletion prevents conflicting writes until the refreshed list is ready', async () => {
    const state = loadApp();
    state.askConfirm = async () => true;
    state.showAlert = () => {};
    state.apiCall = async () => ({});
    let finish;
    state.loadProviders = () => new Promise(resolve => { finish = resolve; });
    state.refreshStatus = async () => {};
    const pending = state.deleteProvider({ name: 'primary' });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(state.canMutateProviders, false);
    await state.selectClient('google');
    assert.equal(state.selectedClient, 'claude');
    finish();
    await pending;
    assert.equal(state.canMutateProviders, true);
});

test('data import locks file reads, preview and apply against duplicate or conflicting input', async () => {
    const state = loadApp();
    state.dataImportFiles = [{ name: 'backup.json', data: '{}' }];
    let finish;
    let calls = 0;
    state.apiCall = () => { calls++; return new Promise(resolve => { finish = resolve; }); };
    const preview = state.previewDataImport();
    await state.previewDataImport();
    await state.selectDataImportFiles({ target: { files: [] } });
    assert.equal(state.dataImportBusy, 'preview');
    assert.equal(state.dataImportFiles.length, 1);
    assert.equal(calls, 1);
    finish({ id: 'reviewed', mode: 'merge' });
    await preview;
    assert.equal(state.dataImportBusy, '');
    state.showAlert = () => {};
    state.loadGlobalConfig = state.loadProviders = state.refreshStatus = async () => {};
    const apply = state.applyDataImport();
    state.closeDataImportPreview();
    await state.applyDataImport();
    assert.equal(await state.setTab('settings'), false);
    assert.equal(state.activeTab, 'providers');
    assert.equal(state.dataImportPlan.id, 'reviewed');
    assert.equal(state.showDataImportPreviewModal, true);
    assert.equal(calls, 2);
    finish({});
    await apply;
    assert.equal(state.dataImportBusy, '');
    assert.equal(state.dataImportPlan, null);
});

test('API errors with missing or malformed server messages use the selected language', async () => {
    for (const json of [async () => ({}), async () => null, async () => { throw new SyntaxError('not JSON'); }]) {
        const state = loadApp({ context: { fetch: async () => ({ ok: false, json }) } });
        state.locale = 'zh-CN';
        state.showAlert = () => {};
        await assert.rejects(state.apiCall('/api/test'), { message: '请求失败' });
    }
});

test('global settings reads serialize reset and block saves until the authoritative snapshot arrives', async () => {
    const state = loadApp();
    let finish;
    const calls = [];
    state.apiCall = url => { calls.push(url); return new Promise(resolve => { finish = resolve; }); };
    const pending = state.loadGlobalConfig();
    assert.equal(state.globalLoading, true);
    await state.loadGlobalConfig();
    await state.saveGlobalConfig();
    assert.deepEqual(calls, ['/api/config/global']);
    finish({ listen_addr: '127.0.0.1', port: 3333, log_retention_days: 14 });
    await pending;
    assert.equal(state.globalLoading, false);
    assert.equal(state.globalDirty, false);
    state.globalConfig.log_retention_days = 21;
    assert.equal(state.globalDirty, true);
});

test('failed settings reads release controls and preserve the draft', async () => {
    const state = loadApp({ context: { console: { ...console, error() {} } } });
    state.globalConfig.log_retention_days = 21;
    state.apiCall = async () => { throw new Error('offline'); };
    await state.loadGlobalConfig();
    assert.equal(state.globalLoading, false);
    assert.equal(state.globalConfig.log_retention_days, 21);
});

test('initial settings load failure exposes retry and recovers editing and saving without a page reload', async () => {
    const state = loadApp({ context: { console: { ...console, error() {} } } });
    state.apiCall = async () => { throw new Error('offline'); };
    await state.loadGlobalConfig();
    assert.equal(state.globalLoadError, 'offline');
    assert.equal(state.savedGlobalConfig, null);
    assert.equal(state.globalLoading, false);

    let finish;
    state.apiCall = () => new Promise(resolve => { finish = resolve; });
    const retry = state.loadGlobalConfig();
    assert.equal(state.globalLoading, true);
    finish({ listen_addr: '127.0.0.1', port: 3333 });
    await retry;
    assert.equal(state.globalLoadError, '');
    assert.equal(state.globalDirty, false);
    state.globalConfig.port = 4444;
    assert.equal(state.globalDirty, true);
    const writes = [];
    state.apiCall = async (url, options) => { writes.push({ url, options }); };
    state.refreshStatus = async () => {};
    state.showAlert = () => {};
    await state.saveGlobalConfig();
    assert.equal(writes[0].url, '/api/config/global/update');
    assert.equal(JSON.parse(writes[0].options.body).port, 4444);
    assert.equal(state.globalDirty, false);

    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    assert.match(html, /role="alert" x-show="globalLoadError"/);
    assert.match(html, /@click="loadGlobalConfig\(\)"\s+:disabled="globalLoading \|\| globalSaving" x-text="t\('feedback.retry'\)"/);
    assert.match(html, /<fieldset class="settings-compact-grid" :disabled="globalLoading \|\| !savedGlobalConfig">/);
});

test('failed settings reset retains the saved baseline and draft while exposing retry', async () => {
    const state = loadApp({ context: { console: { ...console, error() {} } } });
    state.apiCall = async () => ({ port: 3333 });
    await state.loadGlobalConfig();
    state.globalConfig.port = 4444;
    state.apiCall = async () => { throw new Error('offline'); };
    await state.loadGlobalConfig();
    assert.equal(state.globalLoadError, 'offline');
    assert.equal(state.savedGlobalConfig.port, 3333);
    assert.equal(state.globalConfig.port, 4444);
    assert.equal(state.globalDirty, true);
    assert.equal(state.globalLoading, false);
});

test('literal translation references resolve in both UI languages', () => {
    const state = loadApp();
    const source = ['app.js', 'index.html', 'form-controls.js']
        .map(file => fs.readFileSync(path.join(__dirname, file), 'utf8')).join('\n');
    const keys = new Set([...source.matchAll(/\btf?\(\s*['"]([^'"]+)['"]\s*[,)]/g)].map(match => match[1]));
    for (const locale of state.supportedLocales) {
        for (const key of keys) {
            assert.equal(typeof state.lookupMessage(locale, key), 'string', `${locale}: ${key}`);
        }
    }
});

test('legacy Gemini page and pending OAuth session normalize to Google', async () => {
    const state = loadApp();
    state.parseTimestamp = () => new Date();
    const pending = state.normalizePendingOAuthSession({session_id: 'pending', provider: 'antigravity', client_type: 'gemini'});
    assert.equal(pending.client_type, 'google');
    state.requestCloseProvider = async () => true;
    state.closeEnvironment = async () => true;
    state.loadProviders = async () => {};
    state.loadOAuthProviders = async () => {};
    state.$nextTick = () => {};
    await state.selectClient('gemini');
    assert.equal(state.selectedClient, 'google');
    assert.equal(state.hashForState(), '#/providers/google');
});

test('takeover distinguishes written configuration from a routing conflict and allows recovery', () => {
    const state = loadApp();
    assert.equal(state.integrationStateLabel('configured'), 'Configuration written');
    assert.equal(state.integrationStateLabel('configured', 'blocked'), 'Needs attention');
    assert.equal(state.integrationStateTone('configured', 'blocked'), 'danger');
    assert.equal(state.integrationActionDisabledReason({product: 'claude', state: 'error', backup_available: true}, 'rollback'), '');
});

test('takeover saves explicit client location through the target endpoint', async () => {
    const state = loadApp();
    const options = {config_dir: '/tmp/client', config_file: '', model: '', profile: 'work', shell_dir: '', shell: ''};
    state.integrations = [state.normalizeIntegration({product: 'codex', target_options: options})];
    let request;
    state.apiCall = async (url, input) => {
        request = {url, input};
        return {message: 'Target saved', status: {product: 'codex', target_options: options, state: 'not_configured'}};
    };
    await state.runIntegrationAction('codex', 'target');
    assert.equal(request.url, '/api/integrations/codex/target');
    assert.deepEqual(JSON.parse(request.input.body), options);
    assert.equal(state.integrations[0].target_options.profile, 'work');
});


test('takeover loads distinct model IDs without a hardcoded fallback', async () => {
    const state = loadApp();
    state.apiCall = async url => {
        assert.equal(url, '/clipal/v1/models');
        return {data:[{id:'provider/model'},{id:'another'},{id:'provider/model'},{}]};
    };
    await state.loadIntegrationModels();
    assert.deepEqual(Array.from(state.integrationModels), ['another','provider/model']);
    assert.equal(state.integrationModelsBusy,false);
});

test('takeover preserves server capabilities and names across new-client actions', async () => {
    const state = loadApp();
    const capabilities = {config_dir: true, model: true, context_window: true};
    state.integrations = [state.normalizeIntegration({product: 'kimi', name: 'Kimi Code CLI', target_capabilities: capabilities, target_options: {model: 'arbitrary', context_window: '32768'}})];
    let request;
    state.apiCall = async (url, input) => {
        request = JSON.parse(input.body);
        return {message: 'Saved', status: {product: 'kimi', name: 'Kimi Code CLI', target_capabilities: capabilities, target_options: request, state: 'not_configured'}};
    };
    await state.runIntegrationAction('kimi', 'target');
    assert.equal(request.context_window, 32768);
    assert.equal(state.integrations[0].name, 'Kimi Code CLI');
    assert.equal(state.integrations[0].target_capabilities.context_window, true);
    state.integrations[0].target_options.context_window = '';
    assert.equal(Object.hasOwn(state.integrationTargetPayload('kimi'), 'context_window'), false);
    const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
    for (const key of ['config_dir', 'config_file', 'model', 'profile', 'shell', 'shell_dir', 'context_window']) {
        assert.ok(html.includes('integration.target_capabilities?.' + key));
    }
});
