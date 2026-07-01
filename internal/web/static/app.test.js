const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

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
    const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');
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

test('syncSortableDomOrder delegates to Sortable instance with provider names', () => {
    const state = loadApp();
    const calls = [];
    state.sortableInstance = {
        sort(order, useAnimation) {
            calls.push({ order, useAnimation });
        }
    };

    state.syncSortableDomOrder(['p3', 'p1', 'p2']);

    assert.deepEqual(calls, [
        { order: ['p3', 'p1', 'p2'], useAnimation: false }
    ]);
});

test('afterProviderRender aligns Sortable DOM order to current providers on nextTick', async () => {
    const state = loadApp();
    const calls = [];
    state.providers = [
        { name: 'p2', priority: 1, enabled: true },
        { name: 'p1', priority: 2, enabled: true }
    ];
    state.sortableInstance = {
        sort(order, useAnimation) {
            calls.push({ order, useAnimation });
        }
    };
    state.$nextTick = callback => callback();

    await state.afterProviderRender();

    assert.deepEqual(calls, [
        { order: ['p2', 'p1'], useAnimation: false }
    ]);
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
    state.selectedClient = 'gemini';
    state.providerForm.auth_type = 'oauth';
    state.providerForm.oauth_provider = 'codex';
    state.apiCall = async url => {
        calls.push(url);
        return [{ provider: 'antigravity' }];
    };

    await state.loadOAuthProviders(true);

    assert.deepEqual(calls, ['/api/oauth/providers?client_type=gemini']);
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

    state.providerForm.oauth_provider = 'gemini';
    assert.match(state.oauthImportFileHint(), /Gemini OAuth JSON/);
    assert.match(state.oauthImportFileHint(), /Gemini accounts/);
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

test('providerCardTitle truncates oauth display name on card', () => {
    const state = loadApp();

    const title = state.providerCardTitle({
        auth_type: 'oauth',
        display_name: 'gamebabies@gmail.com',
        name: 'codex-gamebabies-gmail-com'
    });

    assert.equal(title, 'gamebabies@g...');
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

    assert.equal(state.providerUsageTotal(provider), '724M · incl. Thoughts 9.88K');
    assert.equal(state.providerUsageTotalMain(provider), '724M');
    assert.equal(state.providerUsageTotalBreakdown(provider), '· incl. Thoughts 9.88K');
    assert.equal(state.providerUsageTotalTitle(provider), '724,117,614 = 720,473,100 + 3,644,514 + incl. Thoughts 9,876');
    assert.equal(state.providerUsageInOut(provider), '720M / 3.64M · incl. Reasoning 120K');
    assert.equal(state.providerUsageInOutMain(provider), '720M / 3.64M');
    assert.equal(state.providerUsageInOutBreakdown(provider), '· incl. Reasoning 120K');
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
    assert.equal(state.providerUsageTotal(provider), '25 · incl. Thoughts 17');
    assert.equal(state.providerUsageTotalTitle(provider), '25 = 7 + 1 + incl. Thoughts 17');
    assert.equal(state.providerUsageInOut(provider), '7 / 1');
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

    assert.equal(state.providerUsageTotal(provider), '92');
    assert.equal(state.providerUsageTotalTitle(provider), '92 = 23 + 69');
    assert.equal(state.providerUsageInOut(provider), '23 / 69');
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

test('providerHasVisibleDetails keeps oauth plan summary visible without token usage', () => {
    const state = loadApp();

    const visible = state.providerHasVisibleDetails({
        auth_type: 'oauth',
        base_url: '',
        proxy_mode: 'default',
        usage: null,
        oauth_plan_type: 'free'
    });

    assert.equal(visible, true);
});

test('providerHasVisibleDetails keeps spend-only providers visible', () => {
    const state = loadApp();

    const visible = state.providerHasVisibleDetails({
        auth_type: 'api_key',
        base_url: '',
        proxy_mode: 'default',
        usage: {
            has_cost: true,
            spend_today_micros: 1
        }
    });

    assert.equal(visible, true);
});

test('providerHasVisibleDetails shows oauth metadata loader before metadata fetch', () => {
    const state = loadApp();

    const visible = state.providerHasVisibleDetails({
        name: 'codex-sean-example-com',
        auth_type: 'oauth',
        oauth_provider: 'codex',
        oauth_ref: 'codex-sean-example-com',
        base_url: '',
        proxy_mode: 'default',
        usage: null
    });

    assert.equal(visible, true);
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
    assert.equal(state.providerOAuthRateLimitPercentLabel(provider.oauth_rate_limits.primary), '100%');

    const sections = state.providerOAuthRateLimitSections(provider);

    const summaries = sections.map(section => `${section.key}:${section.label}:${state.providerOAuthRateLimitPercentLabel(section.window)}`);

    assert.deepEqual(
        JSON.parse(JSON.stringify(summaries)),
        [
            'primary:Weekly limit:100%',
            'additional-0-primary:Code review weekly limit:75%'
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

test('loadProviderOAuthMetadata supports Claude OAuth providers', async () => {
    const state = loadApp();
    const calls = [];
    const provider = {
        name: 'claude-sean-example-com',
        auth_type: 'oauth',
        oauth_provider: 'claude',
        oauth_ref: 'claude-sean-example-com'
    };
    state.selectedClient = 'claude';
    state.apiCall = async (url, options, background, suppressAlert) => {
        calls.push({ url, options, background, suppressAlert });
        return {
            oauth_rate_limits: {
                primary: {
                    used_percent: 88,
                    window_minutes: 300,
                    resets_at: '2026-05-08T13:30:00Z'
                }
            }
        };
    };

    assert.equal(state.providerSupportsOAuthMetadata(provider), true);

    await state.loadProviderOAuthMetadata(provider);

    assert.deepEqual(
        JSON.parse(JSON.stringify(calls)),
        [
            {
                url: '/api/providers/claude/claude-sean-example-com/oauth-metadata',
                options: {},
                background: true,
                suppressAlert: true
            }
        ]
    );
    assert.equal(provider.oauth_rate_limits.primary.used_percent, 88);
    assert.equal(state.providerHasLoadedOAuthMetadata(provider), true);
});

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
    state.loadProviders = async () => {};
    state.refreshStatus = async () => {};

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
    state.selectedClient = 'gemini';
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
    assert.equal(calls[0].url, '/api/providers/gemini');
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

test('startOAuthProviderAuthorization sends custom proxy settings', async () => {
    const state = loadApp();
    state.selectedClient = 'openai';
    state.providerForm = {
        ...state.providerForm,
        auth_type: 'oauth',
        oauth_provider: 'codex',
        proxy_mode: 'custom',
        proxy_url: ' http://127.0.0.1:7890 '
    };
    state.apiCall = async (url, options) => {
        assert.equal(url, '/api/oauth/providers/start');
        assert.deepEqual(JSON.parse(options.body), {
            client_type: 'openai',
            provider: 'codex',
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
                client_type: 'gemini',
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
                client_type: 'gemini'
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
                client_type: 'gemini',
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
                client_type: 'gemini',
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
        auth_url: 'https://platform.claude.com/oauth/authorize?state=sess-claude'
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
