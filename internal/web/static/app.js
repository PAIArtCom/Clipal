const pendingOAuthSessionStorageKey = 'clipal.pendingOAuthSession'
const oauthAuthorizationCancelledError = 'clipal.oauth.cancelled'

function defaultOAuthAuthorizationState() {
    return {
        phase: 'idle',
        provider: '',
        client_type: '',
        session_id: '',
        started_at: 0,
        expires_at: '',
        auth_url: '',
        manual_code: '',
        manual_submit_busy: false,
        manual_submit_error: '',
        popup_blocked: false,
        error: ''
    };
}

function app() {
    let oauthAuthorizationPopup = null;

    return {
        // State
        isLoading: false,
        theme: localStorage.getItem('theme') || 'system',
        locale: document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en',
        supportedLocales: ['en', 'zh-CN'],
        activeTab: 'providers',
        servicePoll: null,
        selectedClient: 'claude',
        oauthProviders: [],
        pendingOAuthSession: null,
        oauthPollingSessionId: '',
        oauthPollingPromise: null,
        oauthPollingToken: 0,
        oauthLinkingSessionId: '',
        oauthLinkingPromise: null,
        oauthAuthorization: defaultOAuthAuthorizationState(),
        integrations: [],
        integrationBusyProduct: '',
        serviceBusyAction: '',
        messages: {
            en: {
                meta: {
                    title: 'Clipal Management'
                },
                header: {
                    subtitle: 'Local ingress for Claude, OpenAI, and Gemini traffic',
                    uptime: 'Uptime: {uptime}'
                },
                nav: {
                    sectionsLabel: 'Sections',
                    providers: 'Providers',
                    integrations: 'CLI Takeover',
                    settings: 'Global Settings',
                    services: 'Services',
                    status: 'System Status'
                },
                common: {
                    none: 'None',
                    load: 'Load',
                    save: 'Save',
                    reset: 'Reset',
                    export: 'Export',
                    cancel: 'Cancel',
                    show: 'Show',
                    hide: 'Hide',
                    refresh: 'Refresh',
                    working: 'Working...',
                    enabled: 'Enabled',
                    disabled: 'Disabled',
                    active: 'Active'
                },
                locale: {
                    label: 'Language',
                    english: 'English',
                    chinese: 'Chinese'
                },
                theme: {
                    current: 'Current theme: {theme}',
                    system: 'System',
                    light: 'Light',
                    dark: 'Dark'
                },
                providers: {
                    addTo: 'Add Provider to {client}',
                    modeLabel: 'Mode',
                    modeAuto: 'Auto',
                    modeManual: 'Manual',
                    pinned: 'Pinned:',
                    switchToManual: 'Switch to Manual',
                    backToAuto: 'Back to Auto',
                    enableProviderFirst: 'Enable a provider first',
                    pinHint: 'Pin a provider by clicking 📌 on a provider card.',
                    empty: 'No providers configured for {client}',
                    pinBadge: 'Pinned',
                    baseUrl: 'Base URL',
                    proxy: 'Proxy',
                    service: 'Service',
                    oauthAuth: 'Auth',
                    refresh: 'Refresh',
                    apiKeys: 'API Keys',
                    usageTotal: 'Usage',
                    usageInOut: 'Input / Output',
                    usageReasoning: 'Reasoning (in Output)',
                    usageThoughts: 'Thoughts',
                    spendToday: 'Spend Today',
                    spendWeek: 'Spend 7d',
                    planAndLimits: 'Plan & Limits',
                    plan: 'Plan',
                    rateLimit: 'Limit',
                    rateLimitCodeReviewWeekly: 'Code review weekly limit',
                    rateLimitWeekly: 'Weekly limit',
                    rateLimitDaily: 'Daily limit',
                    rateLimitHourly: 'Hourly limit',
                    rateLimitMinutes: '{minutes}m limit',
                    rateLimitHours: '{hours}h limit',
                    rateLimitDays: '{days}d limit',
                    oauthStatusReady: 'Ready',
                    oauthStatusRefreshDue: 'Refresh due',
                    oauthStatusReauthNeeded: 'Reauth needed',
                    justNow: 'Just now',
                    never: 'Never',
                    model: 'Model Override',
                    reasoningEffort: 'Reasoning Effort',
                    thinkingBudgetTokens: 'Thinking Budget Tokens',
                    configuredCount: '{count} configured',
                    pinTitle: 'Pin (Manual)',
                    dragToReorder: 'Drag to reorder providers',
                    edit: 'Edit',
                    delete: 'Delete',
                    disable: 'Disable',
                    enable: 'Enable',
                    pinnedProvider: 'Pinned provider',
                    pinnedProviderCannotDisable: 'Pinned provider cannot be disabled in manual mode',
                    enablePinnedProvider: 'Enable pinned provider',
                    modeHelpManual: 'Manual (Pinned)\nAlways use the pinned provider.\nNo failover; failures return errors.',
                    modeHelpAuto: 'Auto (Failover)\nTries enabled providers by priority.\nSwitches on failures.',
                    enableBeforeManual: 'Enable a provider before switching to manual mode',
                    enableBeforePinning: 'Enable the provider before pinning it',
                    switchedToAutoTitle: '{client} switched to Auto',
                    switchedToAutoMessage: 'Failover now follows enabled providers by priority.',
                    switchedToManualTitle: '{client} switched to Manual',
                    switchedToManualMessagePinned: 'Pinned to {provider}. Requests stay on this provider until you switch back to Auto.',
                    switchedToManualMessage: 'Requests now stay on the pinned provider until you switch back to Auto.',
                    pinnedTitle: 'Pinned {client} to {provider}',
                    pinnedMessage: 'New requests will stay on this provider until you switch back to Auto.',
                    enabledTitle: 'Enabled {provider} for {client}',
                    enabledMessage: 'It is available again for failover selection.',
                    disabledTitle: 'Disabled {provider} for {client}',
                    disabledMessage: 'It has been removed from failover selection.',
                    updatedTitle: 'Updated provider {provider}',
                    updatedMessage: 'Changes are now active for {client}.',
                    addedTitle: 'Added provider {provider}',
                    addedMessage: 'The provider is now available for {client}.',
                    deleteConfirm: 'Are you sure you want to delete provider "{name}"?',
                    deletedTitle: 'Deleted provider {name}',
                    deletedMessage: 'It has been removed from {client}\'s provider list.',
                    clientTypeLabel: 'Client Type',
                    authType: 'Auth',
                    oauthAccount: 'OAuth Account',
                    oauthAuthorizingTitle: 'Authorize {provider}',
                    oauthAuthorizingMessage: 'Finish the OAuth flow in the opened window. Clipal will link the authorized account automatically.',
                    oauthOpeningMessage: 'Preparing the authorization window...',
                    oauthWaitingMessage: 'Finish the OAuth flow in the opened window. Clipal will keep listening here and link the account when authorization completes.',
                    oauthPopupBlockedMessage: 'Clipal could not open the authorization window automatically. Use the button below to continue in a new window.',
                    oauthTimedOutMessage: 'Authorization timed out before completion. Start a new authorization session and try again.',
                    oauthFailedMessage: 'Authorization did not complete. Start a new authorization session and try again.',
                    oauthOpenWindow: 'Open Authorization Window',
                    oauthManualUrlLabel: 'Authorization URL',
                    oauthManualCodeHint: 'If Clipal cannot receive the callback, open the URL above in any browser, then paste the callback URL or the authorization code here.',
                    oauthManualCodeHintClaude: 'Claude requires the full callback URL so Clipal can keep the original state. Paste the full callback URL, or a value that still includes both code and state.',
                    oauthManualCodePlaceholder: 'Paste the callback URL or authorization code',
                    oauthManualCodePlaceholderClaude: 'Paste the full callback URL with code and state',
                    oauthManualSubmit: 'Submit Authorization Code',
                    oauthManualEntryLabelClaude: 'Callback URL',
                    oauthManualSubmitClaude: 'Submit Callback URL',
                    oauthRetry: 'Retry Authorization',
                    oauthExpiresHint: 'Session expires {time}',
                    oauthAddedTitle: '{provider} authorization complete',
                    oauthAddedMessage: '{email} is available for {client}.',
                    oauthAddedMessageCreated: '{email} is available for {client} as provider {providerName}.',
                    oauthAddedMessageReused: '{email} refreshed existing provider {providerName} for {client}.',
                    oauthAddedMessageRelinked: '{email} relinked provider {providerName} for {client}.',
                    oauthEditLocked: 'OAuth accounts are managed by authorization. Reauthorize or delete the account instead.',
                    oauthToggleLocked: 'Use the provider toggle to enable or disable OAuth accounts.',
                    oauthUnavailable: 'OAuth is not available for this client yet.',
                    oauthImportCompletedTitle: 'Imported OAuth accounts',
                    oauthImportFailedTitle: 'OAuth import failed',
                    oauthImportSummary: 'Imported {imported} account(s), linked {linked} provider(s), skipped {skipped} entry(s), failed {failed} entry(s).',
                    oauthImportDetailsMore: '+{count} more entries',
                    oauthImportBrowserUnsupported: 'OAuth credential import is not supported in this browser.',
                    proxyDirect: 'Direct',
                    proxyCustom: 'Custom'
                },
                modal: {
                    provider: {
                        addTitle: 'Add Provider',
                        editTitle: 'Edit Provider',
                        close: 'Close modal',
                        credentialSource: 'Credential Source',
                        credentialSourceApiKey: 'API Key',
                        credentialSourceOAuth: 'OAuth',
                        oauthProvider: 'Service',
                        oauthProviderCodex: 'Codex',
                        oauthDirectHint: 'After authorization, Clipal will automatically link the account to an existing provider or create one if needed.',
                        importFile: 'Import OAuth credential file',
                        importFileHint: 'Supported files: CLIProxyAPI single-account OAuth JSON, Codex CLI auth.json, or sub2api export JSON. Clipal imports only accounts matching the selected service.',
                        importFileHintCodex: 'Supported files: Codex CLI auth.json (~/.codex/auth.json), CLIProxyAPI single-account OAuth JSON, or sub2api export JSON. Clipal imports only Codex accounts from the selected files.',
                        importFileHintClaude: 'Supported files: CLIProxyAPI single-account Claude OAuth JSON or sub2api export JSON. Clipal imports only Claude accounts from the selected files.',
                        importFileHintGemini: 'Supported files: CLIProxyAPI single-account Gemini OAuth JSON or sub2api export JSON. Clipal imports only Gemini accounts from the selected files.',
                        name: 'Name *',
                        nameHint: 'Letters, numbers, dot (.), underscore (_), and hyphen (-).',
                        baseUrl: 'Base URL *',
                        proxyMode: 'Proxy Mode',
                        proxyModeDefault: 'Use Default',
                        proxyModeDirect: 'Direct',
                        proxyModeCustom: 'Custom Proxy',
                        proxyUrl: 'Proxy URL',
                        proxyUrlHint: 'http://127.0.0.1:7890',
                        proxyUrlHelp: 'Supports http://, https://, socks5://, and socks5h:// proxy URLs.',
                        keepExistingProxy: 'Leave empty to keep the current proxy ({proxy}).',
                        model: 'Model',
                        modelHint: 'model-id',
                        reasoningEffort: 'Reasoning Effort',
                        reasoningEffortHint: 'Used as OpenAI Responses reasoning.effort.',
                        thinkingBudgetTokens: 'Thinking Budget',
                        thinkingBudgetTokensHint: 'Use 0 to clear Claude thinking.budget_tokens.',
                        apiKeys: 'API Keys',
                        apiKeysRequired: 'API Keys *',
                        onePerLine: 'One API key per line',
                        savedAs: 'Saved as',
                        savedAsSingle: '1 line -> api_key',
                        savedAsMultiple: '2+ lines -> api_keys',
                        keepExistingKey: 'Leave empty to keep the current configured key.',
                        keepExistingKeys: 'Leave empty to keep the current {count} configured keys.',
                        overridesTitle: 'Overrides',
                        overridesHint: 'Leave empty if no override needed',
                        overridesOptional: 'Optional',
                        priority: 'Priority',
                        priorityHint: 'Smaller numbers are tried first.',
                        saveProvider: 'Save Provider',
                        authorizeProvider: 'Continue to Authorization'
                    }
                },
                settings: {
                    title: 'Global Settings',
                    subtitle: 'Runtime defaults, recovery policy, routing strategy, and local observability.',
                    runtimeTitle: 'Runtime',
                    runtimeCopy: 'Basic listener and request buffering defaults.',
                    listenAddress: 'Listen Address',
                    port: 'Port',
                    logLevel: 'Log Level',
                    maxBodySize: 'Max Body Size',
                    maxBodySizeHint: 'Bytes buffered for retryable requests.',
                    reliabilityTitle: 'Reliability',
                    reliabilityCopy: 'Timeouts, temporary deactivation, and circuit breaker behavior.',
                    reactivateAfter: 'Reactivate After',
                    reactivateAfterHint: 'How long a failed provider stays deactivated.',
                    upstreamIdleTimeout: 'Upstream Idle Timeout',
                    upstreamIdleTimeoutHint: 'Set to 0 to disable stalled-stream protection.',
                    responseHeaderTimeout: 'Response Header Timeout',
                    responseHeaderTimeoutHint: 'Set to 0 to wait indefinitely for headers.',
                    upstreamProxyMode: 'Default Upstream Proxy',
                    upstreamProxyUrl: 'Default Proxy URL',
                    upstreamProxyHint: 'Used by providers whose proxy mode is Default.',
                    upstreamProxyUrlHelp: 'Supports http://, https://, socks5://, and socks5h:// proxy URLs.',
                    proxyModeEnvironment: 'Use Environment',
                    proxyModeDirect: 'Direct',
                    proxyModeCustom: 'Custom Proxy',
                    upstreamProxyUrlHint: 'http://127.0.0.1:7890',
                    failureThreshold: 'Failure Threshold',
                    failureThresholdHint: '0 disables the circuit breaker.',
                    successThreshold: 'Success Threshold',
                    openTimeout: 'Open Timeout',
                    halfOpenMaxInflight: 'Half-Open Max In-Flight',
                    logsAlertsTitle: 'Logs & Alerts',
                    logsAlertsCopy: 'Local logging, retention, and desktop notification behavior.',
                    logDirectory: 'Log Directory',
                    logDirectoryHint: 'Leave empty to use the default config-dir logs folder.',
                    retentionDays: 'Retention (Days)',
                    retentionDaysHint: '0 keeps logs forever.',
                    notificationLevel: 'Notification Level',
                    logToStdout: 'Log to Stdout',
                    desktopNotifications: 'Desktop Notifications',
                    notifyOnProviderSwitch: 'Notify on Provider Switch',
                    routingTitle: 'Routing Strategy',
                    routingCopy: 'Expose only the routing controls that change user-visible behavior.',
                    stickySessionTtl: 'Sticky Session TTL',
                    stickySessionTtlHint: 'Idle lifetime for explicit session bindings.',
                    shortRetryAfterMax: 'Short Retry-After Max',
                    shortRetryAfterMaxHint: 'Upper bound for honoring short upstream retry-after hints.',
                    maxInlineWait: 'Max Inline Wait',
                    maxInlineWaitHint: 'How long Clipal may wait before overflowing to another provider.',
                    enableStickySessions: 'Enable Sticky Sessions',
                    enableBusyBackpressure: 'Enable Busy Backpressure',
                    footerHint: 'Saving updates `config.yaml`. Some runtime changes may require restart to take full effect.',
                    saveSettings: 'Save Settings',
                    saveSuccess: 'Configuration saved. Some changes may require restart.',
                    exportSuccess: 'Configuration exported successfully',
                    exportFailure: 'Failed to export configuration'
                },
                integrations: {
                    title: 'CLI Takeover',
                    subtitle: 'Let Clipal take over supported CLI clients by modifying their user-level config files.',
                    refresh: 'Refresh',
                    userConfigHint: 'This only edits your user-level config file. Project-local or managed settings may still override the effective behavior.',
                    restartHint: 'Restart the client or open a new session after applying changes.',
                    empty: 'No supported integrations detected.',
                    apply: 'Use Clipal',
                    rollback: 'Restore',
                    stateConfigured: 'Configured',
                    stateNeedsAttention: 'Needs attention',
                    stateNotConfigured: 'Not configured',
                    alreadyUsing: 'Already using Clipal',
                    noBackupYet: 'No backup yet. Apply once before restore becomes available.',
                    restoreOnlyActive: 'Restore is only available while Clipal is active.',
                    dismissResult: 'Dismiss result',
                    detailsPreview: 'View Configuration Details & Preview',
                    backupAvailable: 'Backup Available',
                    noBackupMeta: 'No backup yet',
                    currentFile: 'Current file',
                    fileDoesNotExistYet: 'File does not exist yet.',
                    latestBackup: 'Latest backup',
                    afterApply: 'After apply',
                    backupEmpty: 'Backup is empty.',
                    originalFileMissing: 'Original file did not exist before Clipal takeover.',
                    noPlannedChanges: 'No planned changes.',
                    resultUpdated: 'Updated',
                    resultError: 'Error',
                    resultNotice: 'Notice',
                    resultUsingClipalTitle: 'Now using Clipal',
                    resultRestoredTitle: 'Restored from backup',
                    resultRestartMessage: 'Restart the client or open a new session to apply changes.',
                    resultApplyErrorTitle: 'Couldn’t update this client',
                    resultRestoreErrorTitle: 'Couldn’t restore this client',
                    noteClaude: 'Clipal updates ANTHROPIC_BASE_URL in ~/.claude/settings.json, may also mark ~/.claude.json onboarding complete, and leaves ANTHROPIC_AUTH_TOKEN untouched.',
                    noteCodex: 'Clipal updates model_provider to clipal and writes [model_providers.clipal] with the local URL and wire_api = "responses".',
                    noteOpencode: 'Clipal adds or updates provider.clipal, points it at the local Clipal OpenAI-compatible URL, and switches the active model to clipal/<current-model>.',
                    noteGemini: 'Clipal only updates GEMINI_API_BASE in ~/.gemini/.env. Other Gemini environment overrides may still take precedence.',
                    noteContinue: 'Clipal adds or updates a user-level Continue model entry that points at the local Clipal OpenAI-compatible URL. You may still need to select that model inside Continue.',
                    noteAider: 'Clipal updates the home-level .aider.conf.yml openai-api-base and a minimal OpenAI-compatible model value. Existing openai-api-key values stay untouched; if missing, Clipal inserts a placeholder clipal key. Repo-local config, .env, or CLI flags can still override it.',
                    noteGoose: 'Clipal creates or updates a managed Goose custom provider file. You may still need to select the Clipal provider or model inside Goose.',
                    noteDefault: 'Clipal only edits the user-level config file shown on this card.'
                },
                services: {
                    title: 'Clipal Service',
                    subtitle: 'Manage the OS background service (same as clipal service *)',
                    supported: 'Supported',
                    unsupported: 'Unsupported',
                    installed: 'Installed',
                    notInstalled: 'Not installed',
                    running: 'Running',
                    stopped: 'Stopped',
                    needsAttention: 'Needs attention',
                    unsupportedBuild: 'Service manager is not supported on this OS build.',
                    autostartNotInstalled: 'Autostart service is not installed yet.',
                    installWindows: 'Install requires an elevated console on Windows. Use the command below.',
                    installOther: 'Click Install to register Clipal as a background service for this user.',
                    copyInstallCommand: 'Copy Install Command',
                    installService: 'Install Service',
                    start: 'Start',
                    restart: 'Restart',
                    stop: 'Stop',
                    uninstall: 'Uninstall',
                    check: 'Check',
                    force: 'Force',
                    reinstall: 'Reinstall',
                    forceHint: 'Reinstall or refresh the existing service definition when needed',
                    restartDisconnectHint: 'Restart or stop may temporarily disconnect this page. Status auto-refreshes every 3s while this tab is open.',
                    confirmUninstall: 'Uninstall the system service?',
                    confirmStop: 'Stop the system service?',
                    confirmRestart: 'Restart the system service?',
                    requestedAction: 'Requested service {action}. Refreshing...',
                    installCommandCopied: 'Install command copied',
                    copyCommandFailed: 'Failed to copy command',
                    notSupportedReason: 'Service manager is not supported on this OS',
                    alreadyInstalledReason: 'Already installed. Enable Force to reinstall or refresh the service definition.',
                    installFirst: 'Install the service first',
                    alreadyRunning: 'Service is already running',
                    notRunning: 'Service is not running',
                    serviceNotInstalled: 'Service is not installed'
                },
                statusPage: {
                    systemInfo: 'System Info',
                    uptime: 'Uptime',
                    configDir: 'Config Dir',
                    circuitBreaker: 'Circuit Breaker',
                    disabled: 'Disabled',
                    circuitBreakerSummary: '{failure} fail / {success} succ ({timeout})',
                    pinned: 'Pinned',
                    lastSwitch: 'Last switch:',
                    lastRequest: 'Last request:',
                    recentActivity: 'Recent activity',
                    providersCount: 'Providers: {count}',
                    enabledCount: 'Enabled: {count}',
                    groupCurrent: 'Current',
                    groupActive: 'Active',
                    groupDisabled: 'Disabled',
                    groupCoolingDown: 'Cooling down',
                    groupUnavailable: 'Unavailable',
                    groupRecoveryProbe: 'Recovery probe',
                    keysAvailable: 'Keys available: {available}/{total}'
                },
                toast: {
                    success: 'Success',
                    requestFailed: 'Request failed',
                    notice: 'Notice',
                    error: 'Error',
                    info: 'Info',
                    dismissNotification: 'Dismiss notification'
                },
                level: {
                    debug: 'Debug',
                    info: 'Info',
                    warn: 'Warn',
                    error: 'Error'
                }
            },
            'zh-CN': {
                meta: {
                    title: 'Clipal 管理界面'
                },
                header: {
                    subtitle: '面向 Claude、OpenAI 和 Gemini 流量的本地入口',
                    uptime: '运行时长：{uptime}'
                },
                nav: {
                    sectionsLabel: '功能分区',
                    providers: 'Providers',
                    integrations: 'CLI 接管',
                    settings: '全局设置',
                    services: '服务',
                    status: '系统状态'
                },
                common: {
                    none: '无',
                    load: '加载',
                    save: '保存',
                    reset: '重置',
                    export: '导出',
                    cancel: '取消',
                    show: '展开',
                    hide: '收起',
                    refresh: '刷新',
                    working: '处理中...',
                    enabled: '已启用',
                    disabled: '已禁用',
                    active: '可用'
                },
                locale: {
                    label: '语言',
                    english: '英文',
                    chinese: '中文'
                },
                theme: {
                    current: '当前主题：{theme}',
                    system: '跟随系统',
                    light: '浅色',
                    dark: '深色'
                },
                providers: {
                    addTo: '为 {client} 添加 Provider',
                    modeLabel: '模式',
                    modeAuto: '自动',
                    modeManual: '手动',
                    pinned: '固定：',
                    switchToManual: '切到手动',
                    backToAuto: '返回自动',
                    enableProviderFirst: '请先启用一个 Provider',
                    pinHint: '点击 Provider 卡片上的 📌 可固定 Provider。',
                    empty: '{client} 还没有配置任何 Provider',
                    pinBadge: '已固定',
                    baseUrl: 'Base URL',
                    proxy: '代理',
                    service: '服务',
                    oauthAuth: '授权',
                    refresh: '刷新',
                    apiKeys: 'API Keys',
                    usageTotal: '用量',
                    usageInOut: '输入 / 输出',
                    usageReasoning: '推理（含在输出中）',
                    usageThoughts: '思考',
                    spendToday: '今日消费',
                    spendWeek: '近 7 天消费',
                    planAndLimits: '套餐与限额',
                    plan: '套餐',
                    rateLimit: '限额',
                    rateLimitCodeReviewWeekly: '代码审查周限额',
                    rateLimitWeekly: '周限额',
                    rateLimitDaily: '日限额',
                    rateLimitHourly: '小时限额',
                    rateLimitMinutes: '{minutes} 分钟限额',
                    rateLimitHours: '{hours} 小时限额',
                    rateLimitDays: '{days} 天限额',
                    oauthStatusReady: '可用',
                    oauthStatusRefreshDue: '待刷新',
                    oauthStatusReauthNeeded: '需重新授权',
                    justNow: '刚刚',
                    never: '从未',
                    model: '模型覆盖',
                    reasoningEffort: '思考强度',
                    thinkingBudgetTokens: '思考预算 Tokens',
                    configuredCount: '已配置 {count} 个',
                    pinTitle: '固定到手动模式',
                    edit: '编辑',
                    delete: '删除',
                    disable: '禁用',
                    enable: '启用',
                    pinnedProvider: '已固定 Provider',
                    pinnedProviderCannotDisable: '手动模式下不能禁用已固定的 Provider',
                    enablePinnedProvider: '先启用已固定的 Provider',
                    modeHelpManual: '手动（固定）\n始终使用固定的 Provider。\n不进行故障切换，失败会直接报错。',
                    modeHelpAuto: '自动（故障切换）\n按优先级尝试已启用的 Provider。\n失败时自动切换。',
                    enableBeforeManual: '切到手动模式前请先启用一个 Provider',
                    enableBeforePinning: '固定前请先启用该 Provider',
                    switchedToAutoTitle: '{client} 已切到自动模式',
                    switchedToAutoMessage: '故障切换将按已启用 Provider 的优先级进行。',
                    switchedToManualTitle: '{client} 已切到手动模式',
                    switchedToManualMessagePinned: '已固定到 {provider}。在切回自动模式前，请求都会停留在这个 Provider 上。',
                    switchedToManualMessage: '在切回自动模式前，请求都会停留在固定的 Provider 上。',
                    pinnedTitle: '已将 {client} 固定到 {provider}',
                    pinnedMessage: '后续请求会停留在这个 Provider 上，直到你切回自动模式。',
                    enabledTitle: '已为 {client} 启用 {provider}',
                    enabledMessage: '它现在会重新参与故障切换选择。',
                    disabledTitle: '已为 {client} 禁用 {provider}',
                    disabledMessage: '它已从故障切换选择中移除。',
                    updatedTitle: '已更新 Provider {provider}',
                    updatedMessage: '{client} 的改动已经生效。',
                    addedTitle: '已添加 Provider {provider}',
                    addedMessage: '这个 Provider 现在可供 {client} 使用。',
                    deleteConfirm: '确认删除 Provider “{name}” 吗？',
                    deletedTitle: '已删除 Provider {name}',
                    deletedMessage: '它已从 {client} 的 Provider 列表中移除。',
                    clientTypeLabel: '客户端类型',
                    authType: '认证方式',
                    oauthAccount: 'OAuth 账号',
                    oauthAuthorizingTitle: '授权 {provider}',
                    oauthAuthorizingMessage: '请在新打开的窗口完成 OAuth 授权。授权完成后，Clipal 会自动关联这个账号。',
                    oauthOpeningMessage: '正在准备授权窗口...',
                    oauthWaitingMessage: '请在新打开的窗口完成 OAuth 授权。Clipal 会在这里持续等待，并在授权完成后自动关联账号。',
                    oauthPopupBlockedMessage: 'Clipal 无法自动打开授权窗口。请使用下面的按钮在新窗口中继续。',
                    oauthTimedOutMessage: '授权超时了。请重新发起一次授权。',
                    oauthFailedMessage: '授权没有完成。请重新发起一次授权。',
                    oauthOpenWindow: '打开授权窗口',
                    oauthManualUrlLabel: '授权链接',
                    oauthManualCodeHint: '如果 Clipal 无法收到回调，请在任意浏览器打开上面的链接，然后把回调 URL 或授权码粘贴到这里。',
                    oauthManualCodeHintClaude: 'Claude 必须保留原始 state，因此需要完整的回调 URL。请粘贴完整回调 URL，或仍然同时包含 code 和 state 的值。',
                    oauthManualCodePlaceholder: '粘贴回调 URL 或授权码',
                    oauthManualCodePlaceholderClaude: '粘贴包含 code 和 state 的完整回调 URL',
                    oauthManualSubmit: '提交授权码',
                    oauthManualEntryLabelClaude: '回调 URL',
                    oauthManualSubmitClaude: '提交回调 URL',
                    oauthRetry: '重新授权',
                    oauthExpiresHint: '会话将在 {time} 过期',
                    oauthAddedTitle: '{provider} 授权已完成',
                    oauthAddedMessage: '{email} 现在可供 {client} 使用。',
                    oauthAddedMessageCreated: '{email} 已作为 Provider {providerName} 接入 {client}。',
                    oauthAddedMessageReused: '{email} 已刷新并继续复用现有 Provider {providerName} 供 {client} 使用。',
                    oauthAddedMessageRelinked: '{email} 已重新关联到 Provider {providerName}，现在可供 {client} 使用。',
                    oauthEditLocked: 'OAuth 账号由授权流程管理。请重新授权或删除账号。',
                    oauthToggleLocked: '可通过 Provider 开关启用或禁用 OAuth 账号。',
                    oauthUnavailable: '这个客户端暂时还不能使用 OAuth。',
                    oauthImportCompletedTitle: '已导入 OAuth 账号',
                    oauthImportFailedTitle: 'OAuth 导入失败',
                    oauthImportSummary: '已导入 {imported} 个账号，关联 {linked} 个 Provider，跳过 {skipped} 条记录，失败 {failed} 条记录。',
                    oauthImportDetailsMore: '另有 {count} 条记录',
                    oauthImportBrowserUnsupported: '当前浏览器不支持 OAuth 凭据导入。',
                    dragToReorder: '拖拽调整优先级',
                    proxyDirect: '直连',
                    proxyCustom: '自定义'
                },
                modal: {
                    provider: {
                        addTitle: '添加 Provider',
                        editTitle: '编辑 Provider',
                        close: '关闭弹窗',
                        credentialSource: '凭证来源',
                        credentialSourceApiKey: 'API Key',
                        credentialSourceOAuth: 'OAuth',
                        oauthProvider: '服务',
                        oauthProviderCodex: 'Codex',
                        oauthDirectHint: '授权完成后，Clipal 会自动关联到已有 Provider；如果没有，再创建一个。',
                        importFile: '导入 OAuth 授权文件',
                        importFileHint: '支持：CLIProxyAPI 单账号 OAuth JSON、Codex CLI auth.json、sub2api 导出的 JSON。Clipal 只会导入与当前服务匹配的账号。',
                        importFileHintCodex: '支持：Codex CLI 的 auth.json（通常在 ~/.codex/auth.json）、CLIProxyAPI 单账号 OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Codex 账号。',
                        importFileHintClaude: '支持：CLIProxyAPI 单账号 Claude OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Claude 账号。',
                        importFileHintGemini: '支持：CLIProxyAPI 单账号 Gemini OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Gemini 账号。',
                        name: '名称 *',
                        nameHint: '允许字母、数字、点号 (.)、下划线 (_) 和连字符 (-)。',
                        baseUrl: 'Base URL *',
                        proxyMode: '代理模式',
                        proxyModeDefault: '使用默认值',
                        proxyModeDirect: '直连',
                        proxyModeCustom: '自定义代理',
                        proxyUrl: '代理 URL',
                        proxyUrlHint: 'http://127.0.0.1:7890',
                        proxyUrlHelp: '支持 http://、https://、socks5:// 和 socks5h:// 代理 URL。',
                        keepExistingProxy: '留空则保留当前代理（{proxy}）。',
                        model: '模型',
                        modelHint: 'model-id',
                        reasoningEffort: '思考强度',
                        reasoningEffortHint: '写入 OpenAI Responses 的 reasoning.effort。',
                        thinkingBudgetTokens: '思考预算',
                        thinkingBudgetTokensHint: '填 0 清空 Claude 的 thinking.budget_tokens。',
                        apiKeys: 'API Keys',
                        apiKeysRequired: 'API Keys *',
                        onePerLine: '每行一个 API Key',
                        savedAs: '保存方式',
                        savedAsSingle: '1 行 -> api_key',
                        savedAsMultiple: '2 行及以上 -> api_keys',
                        keepExistingKeys: '留空则保留当前已配置的 {count} 个 key。',
                        overridesTitle: '覆盖',
                        overridesHint: '无需覆盖则留空',
                        overridesOptional: '可选',
                        priority: '优先级',
                        priorityHint: '数字越小越先尝试。',
                        saveProvider: '保存 Provider',
                        authorizeProvider: '继续授权'
                    }
                },
                settings: {
                    title: '全局设置',
                    subtitle: '运行时默认项、恢复策略、路由策略以及本地可观测性。',
                    runtimeTitle: '运行时',
                    runtimeCopy: '基础监听配置与请求缓冲默认项。',
                    listenAddress: '监听地址',
                    port: '端口',
                    logLevel: '日志级别',
                    maxBodySize: '最大请求体大小',
                    maxBodySizeHint: '用于可重试请求的缓冲字节数。',
                    reliabilityTitle: '可靠性',
                    reliabilityCopy: '超时、临时停用和熔断器行为。',
                    reactivateAfter: '恢复激活时间',
                    reactivateAfterHint: '失败的 Provider 会停用多久后再恢复。',
                    upstreamIdleTimeout: '上游空闲超时',
                    upstreamIdleTimeoutHint: '设为 0 可关闭流式响应停滞保护。',
                    responseHeaderTimeout: '响应头超时',
                    responseHeaderTimeoutHint: '设为 0 表示无限等待响应头。',
                    upstreamProxyMode: '默认上游代理',
                    upstreamProxyUrl: '默认代理 URL',
                    upstreamProxyHint: '对代理模式为“使用默认值”的 Provider 生效。',
                    upstreamProxyUrlHelp: '支持 http://、https://、socks5:// 和 socks5h:// 代理 URL。',
                    proxyModeEnvironment: '使用环境变量',
                    proxyModeDirect: '直连',
                    proxyModeCustom: '自定义代理',
                    upstreamProxyUrlHint: 'http://127.0.0.1:7890',
                    failureThreshold: '失败阈值',
                    failureThresholdHint: '设为 0 可关闭熔断器。',
                    successThreshold: '成功阈值',
                    openTimeout: '打开超时',
                    halfOpenMaxInflight: '半开最大并发',
                    logsAlertsTitle: '日志与提醒',
                    logsAlertsCopy: '本地日志、保留策略和桌面通知行为。',
                    logDirectory: '日志目录',
                    logDirectoryHint: '留空则使用默认的配置目录 logs 文件夹。',
                    retentionDays: '保留天数',
                    retentionDaysHint: '设为 0 表示永久保留日志。',
                    notificationLevel: '通知级别',
                    logToStdout: '输出到 Stdout',
                    desktopNotifications: '桌面通知',
                    notifyOnProviderSwitch: 'Provider 切换时通知',
                    routingTitle: '路由策略',
                    routingCopy: '只暴露会影响用户可见行为的路由控制项。',
                    stickySessionTtl: '粘性会话 TTL',
                    stickySessionTtlHint: '显式会话绑定的空闲生存时间。',
                    shortRetryAfterMax: '短 Retry-After 上限',
                    shortRetryAfterMaxHint: '对上游较短 retry-after 提示的最大遵从值。',
                    maxInlineWait: '最大内联等待',
                    maxInlineWaitHint: 'Clipal 在溢出到其他 Provider 前可等待的最长时间。',
                    enableStickySessions: '启用粘性会话',
                    enableBusyBackpressure: '启用 Busy Backpressure',
                    footerHint: '保存会更新 `config.yaml`。部分运行时改动需要重启后才会完全生效。',
                    saveSettings: '保存设置',
                    saveSuccess: '配置已保存。部分改动可能需要重启。',
                    exportSuccess: '配置导出成功',
                    exportFailure: '配置导出失败'
                },
                integrations: {
                    title: 'CLI 接管',
                    subtitle: '让 Clipal 通过修改用户级配置文件接管受支持的 CLI 客户端。',
                    refresh: '刷新',
                    userConfigHint: '这里只会修改你的用户级配置文件。项目级或受管控配置仍可能覆盖最终生效结果。',
                    restartHint: '应用修改后，请重启客户端或重新打开一个会话。',
                    empty: '未检测到受支持的集成。',
                    apply: '使用 Clipal',
                    rollback: '恢复',
                    stateConfigured: '已配置',
                    stateNeedsAttention: '需要关注',
                    stateNotConfigured: '未配置',
                    alreadyUsing: '已经在使用 Clipal',
                    noBackupYet: '目前还没有备份。至少先使用一次 Clipal 后才能恢复。',
                    restoreOnlyActive: '只有在当前由 Clipal 接管时才能恢复。',
                    dismissResult: '关闭结果',
                    detailsPreview: '查看配置详情与预览',
                    backupAvailable: '已有备份',
                    noBackupMeta: '暂无备份',
                    currentFile: '当前文件',
                    fileDoesNotExistYet: '文件尚不存在。',
                    latestBackup: '最新备份',
                    afterApply: '启用后结果',
                    backupEmpty: '备份内容为空。',
                    originalFileMissing: 'Clipal 接管前原始文件不存在。',
                    noPlannedChanges: '没有计划中的变更。',
                    resultUpdated: '已更新',
                    resultError: '错误',
                    resultNotice: '提示',
                    resultUsingClipalTitle: '已开始使用 Clipal',
                    resultRestoredTitle: '已从备份恢复',
                    resultRestartMessage: '请重启客户端或重新打开一个会话以应用改动。',
                    resultApplyErrorTitle: '更新此客户端失败',
                    resultRestoreErrorTitle: '恢复此客户端失败',
                    noteClaude: 'Clipal 会更新 ~/.claude/settings.json 中的 ANTHROPIC_BASE_URL，必要时也会把 ~/.claude.json 的 onboarding 标记补成完成，不会改动 ANTHROPIC_AUTH_TOKEN。',
                    noteCodex: 'Clipal 会把 model_provider 更新为 clipal，并写入 [model_providers.clipal]，使用本地 URL 和 wire_api = "responses"。',
                    noteOpencode: 'Clipal 会新增或更新 provider.clipal，指向本地 Clipal OpenAI 兼容地址，并把当前 model 切到 clipal/<当前模型>。',
                    noteGemini: 'Clipal 只会更新 ~/.gemini/.env 中的 GEMINI_API_BASE。其他 Gemini 环境覆盖项仍可能优先生效。',
                    noteContinue: 'Clipal 会新增或更新用户级 Continue 模型项，指向本地 Clipal OpenAI 兼容地址。你可能仍需在 Continue 中手动选择该模型。',
                    noteAider: 'Clipal 会更新 home 级 .aider.conf.yml 中的 openai-api-base 和一个最小 OpenAI 兼容 model。已有 openai-api-key 会保留，只有缺失时才会自动补占位值 clipal。仓库级配置、.env 或 CLI 参数仍可能覆盖它。',
                    noteGoose: 'Clipal 会创建或更新受管控的 Goose 自定义 provider 文件。你可能仍需在 Goose 中选择 Clipal provider 或 model。',
                    noteDefault: 'Clipal 只会修改此卡片展示的用户级配置文件。'
                },
                services: {
                    title: 'Clipal 服务',
                    subtitle: '管理操作系统后台服务（等同于 clipal service *）',
                    supported: '支持',
                    unsupported: '不支持',
                    installed: '已安装',
                    notInstalled: '未安装',
                    running: '运行中',
                    stopped: '已停止',
                    needsAttention: '需要关注',
                    unsupportedBuild: '当前操作系统构建不支持服务管理器。',
                    autostartNotInstalled: '自动启动服务尚未安装。',
                    installWindows: 'Windows 上安装需要提升权限的控制台。请使用下方命令。',
                    installOther: '点击“安装服务”即可为当前用户注册 Clipal 后台服务。',
                    copyInstallCommand: '复制安装命令',
                    installService: '安装服务',
                    start: '启动',
                    restart: '重启',
                    stop: '停止',
                    uninstall: '卸载',
                    check: '检查',
                    force: '强制',
                    reinstall: '重新安装',
                    forceHint: '在需要时重新安装或刷新现有服务定义',
                    restartDisconnectHint: '重启或停止服务时此页面可能会暂时断开。当前标签页打开时状态每 3 秒自动刷新一次。',
                    confirmUninstall: '确认卸载系统服务吗？',
                    confirmStop: '确认停止系统服务吗？',
                    confirmRestart: '确认重启系统服务吗？',
                    requestedAction: '已请求执行服务操作：{action}。正在刷新……',
                    installCommandCopied: '安装命令已复制',
                    copyCommandFailed: '复制命令失败',
                    notSupportedReason: '当前操作系统不支持服务管理器',
                    alreadyInstalledReason: '服务已安装。启用“强制”即可重新安装或刷新服务定义。',
                    installFirst: '请先安装服务',
                    alreadyRunning: '服务已在运行中',
                    notRunning: '服务当前未运行',
                    serviceNotInstalled: '服务尚未安装'
                },
                statusPage: {
                    systemInfo: '系统信息',
                    uptime: '运行时长',
                    configDir: '配置目录',
                    circuitBreaker: '熔断器',
                    disabled: '已禁用',
                    circuitBreakerSummary: '{failure} 次失败 / {success} 次成功（{timeout}）',
                    pinned: '已固定',
                    lastSwitch: '最近切换：',
                    lastRequest: '最近请求：',
                    recentActivity: '最近活动',
                    providersCount: '提供方：{count}',
                    enabledCount: '已启用：{count}',
                    groupCurrent: '当前',
                    groupActive: '可用',
                    groupDisabled: '已禁用',
                    groupCoolingDown: '冷却中',
                    groupUnavailable: '不可用',
                    groupRecoveryProbe: '恢复探测',
                    keysAvailable: '可用密钥：{available}/{total}'
                },
                toast: {
                    success: '成功',
                    requestFailed: '请求失败',
                    notice: '提示',
                    error: '错误',
                    info: '信息',
                    dismissNotification: '关闭通知'
                },
                level: {
                    debug: '调试',
                    info: '信息',
                    warn: '警告',
                    error: '错误'
                }
            }
        },
        clientOptions: [
            { value: 'claude', label: 'Claude' },
            { value: 'openai', label: 'OpenAI' },
            { value: 'gemini', label: 'Gemini' }
        ],
        providers: [],
        clientConfig: {
            mode: 'auto',
            pinned_provider: '',
            override_support: null
        },
        // Keep a stable object shape so Alpine x-model bindings don't explode
        // during the initial render (before loadGlobalConfig completes).
        globalConfig: {
            listen_addr: '',
            port: 0,
            log_level: 'info',
            reactivate_after: '',
            upstream_idle_timeout: '',
            response_header_timeout: '',
            upstream_proxy_mode: 'environment',
            upstream_proxy_url: '',
            max_request_body_bytes: 0,
            log_dir: '',
            log_retention_days: 7,
            log_stdout: true,
            notifications: {
                enabled: false,
                min_level: 'error',
                provider_switch: true
            },
            circuit_breaker: {
                failure_threshold: 4,
                success_threshold: 2,
                open_timeout: '60s',
                half_open_max_inflight: 1
            },
            routing: {
                sticky_sessions: {
                    enabled: true,
                    explicit_ttl: '30m'
                },
                busy_backpressure: {
                    enabled: true,
                    short_retry_after_max: '3s',
                    max_inline_wait: '8s'
                }
            }
        },
        status: {
            version: '',
            uptime: '',
            config_dir: '',
            clients: {}
        },
        serviceStatus: {
            os: '',
            install_command: '',
            install_hint: '',
            supported: true,
            installed: false,
            loaded: false,
            running: false,
            ok: false,
            detail: '',
            output: '',
            error: ''
        },
        serviceForm: {
            force: false,
            stdout_path: '',
            stderr_path: ''
        },
        toasts: [],
        toastCounter: 0,
        toastTimeouts: {},
        integrationResults: {},
        showAddProviderModal: false,
        showEditProviderModal: false,
        providerForm: {
            auth_type: 'api_key',
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
        },
        editingProviderName: '',
        editingProviderKeyCount: 0,

        // Helpers
        withDefaultGlobalConfig(cfg) {
            // Merge (shallow) and also ensure nested notifications exists.
            const def = this.globalConfig;
            const out = { ...def, ...(cfg || {}) };
            out.notifications = { ...def.notifications, ...((cfg && cfg.notifications) ? cfg.notifications : {}) };
            out.circuit_breaker = { ...def.circuit_breaker, ...((cfg && cfg.circuit_breaker) ? cfg.circuit_breaker : {}) };
            out.routing = { ...def.routing, ...((cfg && cfg.routing) ? cfg.routing : {}) };
            out.routing.sticky_sessions = {
                ...def.routing.sticky_sessions,
                ...((cfg && cfg.routing && cfg.routing.sticky_sessions) ? cfg.routing.sticky_sessions : {})
            };
            out.routing.busy_backpressure = {
                ...def.routing.busy_backpressure,
                ...((cfg && cfg.routing && cfg.routing.busy_backpressure) ? cfg.routing.busy_backpressure : {})
            };
            return out;
        },

        withDefaultServiceStatus(status) {
            const def = this.serviceStatus;
            return {
                ...def,
                ...(status || {}),
                supported: status && typeof status.supported === 'boolean' ? status.supported : def.supported,
                installed: status && typeof status.installed === 'boolean' ? status.installed : def.installed,
                loaded: status && typeof status.loaded === 'boolean' ? status.loaded : def.loaded,
                running: status && typeof status.running === 'boolean' ? status.running : def.running,
                ok: status && typeof status.ok === 'boolean' ? status.ok : def.ok,
                install_command: String((status && status.install_command) || ''),
                install_hint: String((status && status.install_hint) || ''),
                detail: String((status && status.detail) || ''),
                output: String((status && status.output) || ''),
                error: String((status && status.error) || '')
            };
        },

        normalizeLocale(locale) {
            const value = String(locale || '').trim();
            return this.supportedLocales.includes(value) ? value : 'en';
        },

        lookupMessage(locale, key) {
            const root = this.messages[this.normalizeLocale(locale)];
            return String(key || '')
                .split('.')
                .filter(Boolean)
                .reduce((value, part) => {
                    if (!value || typeof value !== 'object' || !(part in value)) {
                        return null;
                    }
                    return value[part];
                }, root);
        },

        t(key) {
            return this.lookupMessage(this.locale, key)
                ?? this.lookupMessage('en', key)
                ?? key;
        },

        tf(key, params = {}) {
            return String(this.t(key)).replace(/\{(\w+)\}/g, (_, name) => {
                return Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : `{${name}}`;
            });
        },

        applyLocale() {
            document.documentElement.lang = this.locale === 'zh-CN' ? 'zh-CN' : 'en';
            document.title = this.t('meta.title');
        },

        initLocale() {
            const stored = localStorage.getItem('clipal.locale');
            this.locale = this.normalizeLocale(stored);
            this.applyLocale();
        },

        setLocale(locale) {
            this.locale = this.normalizeLocale(locale);
            localStorage.setItem('clipal.locale', this.locale);
            this.applyLocale();
        },

        focusLocaleButton(locale) {
            this.$nextTick(() => {
                const ref = locale === 'zh-CN' ? this.$refs.localeZh : this.$refs.localeEn;
                if (ref && typeof ref.focus === 'function') {
                    ref.focus();
                }
            });
        },

        moveLocale(direction) {
            const index = this.supportedLocales.indexOf(this.locale);
            if (index === -1) {
                return;
            }
            const next = (index + direction + this.supportedLocales.length) % this.supportedLocales.length;
            const locale = this.supportedLocales[next];
            this.setLocale(locale);
            this.focusLocaleButton(locale);
        },

        isSupportedClientType(clientType) {
            return this.clientOptions.some(option => option && option.value === clientType);
        },

        normalizePendingOAuthSession(session) {
            if (!session || typeof session !== 'object') {
                return null;
            }

            const sessionId = String(session.session_id || '').trim();
            const provider = String(session.provider || '').trim().toLowerCase();
            const clientType = String(session.client_type || '').trim().toLowerCase();
            const startedAtValue = Number(session.started_at || 0);
            const expiresAt = String(session.expires_at || '').trim();
            const authURL = String(session.auth_url || '').trim();

            if (!sessionId || !provider || !this.isSupportedClientType(clientType)) {
                return null;
            }

            return {
                session_id: sessionId,
                provider,
                client_type: clientType,
                started_at: Number.isFinite(startedAtValue) && startedAtValue > 0 ? startedAtValue : Date.now(),
                expires_at: this.parseTimestamp(expiresAt) ? expiresAt : '',
                auth_url: authURL
            };
        },

        stopOAuthPolling() {
            this.oauthPollingToken += 1;
            this.oauthPollingSessionId = '';
            this.oauthPollingPromise = null;
        },

        closeOAuthAuthorizationPopup() {
            const popup = oauthAuthorizationPopup;
            oauthAuthorizationPopup = null;
            if (!popup || typeof popup.close !== 'function') {
                return;
            }
            try {
                popup.close();
            } catch (error) {
                console.error('Failed to close OAuth popup:', error);
            }
        },

        updateOAuthAuthorization(patch = {}) {
            const current = this.oauthAuthorization && typeof this.oauthAuthorization === 'object'
                ? this.oauthAuthorization
                : defaultOAuthAuthorizationState();
            this.oauthAuthorization = {
                ...defaultOAuthAuthorizationState(),
                ...current,
                ...(patch || {})
            };
            return this.oauthAuthorization;
        },

        resetOAuthAuthorization() {
            this.oauthAuthorization = defaultOAuthAuthorizationState();
            this.closeOAuthAuthorizationPopup();
        },

        showOAuthAuthorizationModal(session = null) {
            const provider = String((session && session.provider) || this.providerForm.oauth_provider || '').trim().toLowerCase();
            this.showEditProviderModal = false;
            this.showAddProviderModal = true;
            this.providerForm.auth_type = 'oauth';
            if (provider) {
                this.providerForm.oauth_provider = provider;
            }
        },

        applyOAuthAuthorizationState(session = null, patch = {}) {
            const base = session && typeof session === 'object'
                ? this.normalizePendingOAuthSession(session) || session
                : null;
            if (base) {
                this.showOAuthAuthorizationModal(base);
            }
            return this.updateOAuthAuthorization({
                provider: String((base && base.provider) || this.oauthAuthorization.provider || this.providerForm.oauth_provider || '').trim().toLowerCase(),
                client_type: String((base && base.client_type) || this.oauthAuthorization.client_type || this.selectedClient || '').trim().toLowerCase(),
                session_id: String((base && base.session_id) || this.oauthAuthorization.session_id || '').trim(),
                started_at: Number(base && base.started_at) || Number(this.oauthAuthorization.started_at) || 0,
                expires_at: String((base && base.expires_at) || this.oauthAuthorization.expires_at || '').trim(),
                auth_url: String((base && base.auth_url) || this.oauthAuthorization.auth_url || '').trim(),
                popup_blocked: false,
                error: '',
                ...(patch || {})
            });
        },

        oauthAuthorizationActive() {
            return this.showAddProviderModal
                && !this.showEditProviderModal
                && String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim() !== 'idle';
        },

        oauthAuthorizationShowsSpinner() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            return phase === 'opening' || phase === 'waiting';
        },

        oauthAuthorizationProviderLabel() {
            return this.oauthProviderLabel((this.oauthAuthorization && this.oauthAuthorization.provider) || this.providerForm.oauth_provider || '');
        },

        oauthAuthorizationProviderValue() {
            return String((this.oauthAuthorization && this.oauthAuthorization.provider) || this.providerForm.oauth_provider || '').trim().toLowerCase();
        },

        oauthAuthorizationTitle() {
            return this.tf('providers.oauthAuthorizingTitle', {
                provider: this.oauthAuthorizationProviderLabel()
            });
        },

        oauthAuthorizationMessage() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            switch (phase) {
                case 'opening':
                    return this.t('providers.oauthOpeningMessage');
                case 'blocked':
                    return this.t('providers.oauthPopupBlockedMessage');
                case 'timed_out':
                    return String((this.oauthAuthorization && this.oauthAuthorization.error) || '').trim() || this.t('providers.oauthTimedOutMessage');
                case 'error':
                    return String((this.oauthAuthorization && this.oauthAuthorization.error) || '').trim() || this.t('providers.oauthFailedMessage');
                default:
                    return this.t('providers.oauthWaitingMessage');
            }
        },

        oauthAuthorizationExpiresHint() {
            const expiresAt = this.parseTimestamp(this.oauthAuthorization && this.oauthAuthorization.expires_at);
            if (!expiresAt) {
                return '';
            }
            return this.tf('providers.oauthExpiresHint', {
                time: this.formatRelativeTime(expiresAt, '')
            });
        },

        oauthAuthorizationToneClass() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            if (phase === 'blocked' || phase === 'timed_out') {
                return 'provider-oauth-flow provider-oauth-flow--warning';
            }
            if (phase === 'error') {
                return 'provider-oauth-flow provider-oauth-flow--error';
            }
            return 'provider-oauth-flow provider-oauth-flow--active';
        },

        oauthAuthorizationCanOpenWindow() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            const authURL = String((this.oauthAuthorization && this.oauthAuthorization.auth_url) || '').trim();
            return !!authURL && (phase === 'blocked' || phase === 'waiting');
        },

        oauthAuthorizationCanRetry() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            return phase === 'timed_out' || phase === 'error';
        },

        oauthAuthorizationCanEnterCode() {
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            const sessionID = String((this.oauthAuthorization && this.oauthAuthorization.session_id) || '').trim();
            return !!sessionID && (phase === 'waiting' || phase === 'blocked');
        },

        oauthAuthorizationCanSubmitCode() {
            if (!this.oauthAuthorizationCanEnterCode()) {
                return false;
            }
            if (this.oauthAuthorization && this.oauthAuthorization.manual_submit_busy) {
                return false;
            }
            return String((this.oauthAuthorization && this.oauthAuthorization.manual_code) || '').trim() !== '';
        },

        oauthAuthorizationManualEntryLabel() {
            if (this.oauthAuthorizationProviderValue() === 'claude') {
                return this.t('providers.oauthManualEntryLabelClaude');
            }
            return this.t('providers.oauthManualSubmit');
        },

        oauthAuthorizationManualCodeHint() {
            if (this.oauthAuthorizationProviderValue() === 'claude') {
                return this.t('providers.oauthManualCodeHintClaude');
            }
            return this.t('providers.oauthManualCodeHint');
        },

        oauthAuthorizationManualCodePlaceholder() {
            if (this.oauthAuthorizationProviderValue() === 'claude') {
                return this.t('providers.oauthManualCodePlaceholderClaude');
            }
            return this.t('providers.oauthManualCodePlaceholder');
        },

        oauthAuthorizationSubmitLabel() {
            if (this.oauthAuthorization && this.oauthAuthorization.manual_submit_busy) {
                return this.t('common.working');
            }
            if (this.oauthAuthorizationProviderValue() === 'claude') {
                return this.t('providers.oauthManualSubmitClaude');
            }
            return this.t('providers.oauthManualSubmit');
        },

        openOAuthAuthorizationPopup(url = '') {
            const nextURL = String(url || '').trim();
            if (!nextURL || typeof window === 'undefined' || typeof window.open !== 'function') {
                oauthAuthorizationPopup = null;
                return null;
            }
            this.closeOAuthAuthorizationPopup();
            try {
                const popup = window.open(nextURL, '_blank');
                oauthAuthorizationPopup = popup || null;
                return popup || null;
            } catch (error) {
                console.error('Failed to open OAuth popup:', error);
                oauthAuthorizationPopup = null;
                return null;
            }
        },

        openPendingOAuthAuthorizationWindow() {
            const authURL = String((this.oauthAuthorization && this.oauthAuthorization.auth_url) || '').trim();
            if (!authURL) {
                return false;
            }
            const popup = this.openOAuthAuthorizationPopup(authURL);
            if (!popup) {
                this.updateOAuthAuthorization({
                    phase: 'blocked',
                    popup_blocked: true
                });
                return false;
            }
            this.updateOAuthAuthorization({
                phase: 'waiting',
                popup_blocked: false
            });
            return true;
        },

        cancelOAuthSessionOnServer(session = null) {
            const pending = this.normalizePendingOAuthSession(session)
                || this.loadPendingOAuthSession()
                || this.pendingOAuthSession;
            const sessionID = String((pending && pending.session_id) || '').trim();
            if (!sessionID) {
                return Promise.resolve(null);
            }
            return this.apiCall(`/api/oauth/sessions/${encodeURIComponent(sessionID)}/cancel`, {
                method: 'POST'
            }, true, true).catch(error => {
                console.error('Failed to cancel OAuth session:', error);
                return null;
            });
        },

        cancelOAuthAuthorization(keepModal = true) {
            const pending = this.normalizePendingOAuthSession(this.oauthAuthorization)
                || this.loadPendingOAuthSession()
                || this.pendingOAuthSession;
            const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim();
            this.stopOAuthPolling();
            if (pending && phase !== 'completed' && phase !== 'timed_out' && phase !== 'error') {
                this.cancelOAuthSessionOnServer(pending);
            }
            this.clearPendingOAuthSession();
            this.resetOAuthAuthorization();
            if (!keepModal) {
                this.showAddProviderModal = false;
                this.showEditProviderModal = false;
            }
        },

        retryOAuthAuthorization() {
            const provider = String((this.oauthAuthorization && this.oauthAuthorization.provider) || this.providerForm.oauth_provider || '').trim().toLowerCase();
            this.cancelOAuthAuthorization(true);
            this.providerForm.auth_type = 'oauth';
            if (provider) {
                this.providerForm.oauth_provider = provider;
            }
            return this.startOAuthProviderAuthorization();
        },

        async submitPendingOAuthAuthorizationCode() {
            const pending = this.normalizePendingOAuthSession(this.oauthAuthorization) || this.loadPendingOAuthSession();
            const sessionID = String((pending && pending.session_id) || (this.oauthAuthorization && this.oauthAuthorization.session_id) || '').trim();
            const input = String((this.oauthAuthorization && this.oauthAuthorization.manual_code) || '').trim();
            if (!sessionID || !input) {
                this.updateOAuthAuthorization({
                    manual_submit_error: this.oauthAuthorizationManualCodeHint()
                });
                return null;
            }

            this.stopOAuthPolling();
            this.updateOAuthAuthorization({
                manual_submit_busy: true,
                manual_submit_error: '',
                error: ''
            });

            try {
                const session = await this.apiCall(`/api/oauth/sessions/${encodeURIComponent(sessionID)}/code`, {
                    method: 'POST',
                    body: JSON.stringify({
                        code: input,
                        client_type: String((pending && pending.client_type) || this.selectedClient || '').trim().toLowerCase()
                    })
                }, false, true);
                const status = String((session && session.status) || '').trim().toLowerCase();
                if (status === 'completed') {
                    const linkedSession = await this.linkCompletedOAuthSession(pending, session);
                    this.clearPendingOAuthSession();
                    await this.handleCompletedOAuthSession(pending || {
                        session_id: sessionID,
                        provider: String((this.oauthAuthorization && this.oauthAuthorization.provider) || '').trim().toLowerCase(),
                        client_type: String((this.oauthAuthorization && this.oauthAuthorization.client_type) || this.selectedClient || '').trim().toLowerCase(),
                        expires_at: String((this.oauthAuthorization && this.oauthAuthorization.expires_at) || '').trim(),
                        auth_url: String((this.oauthAuthorization && this.oauthAuthorization.auth_url) || '').trim()
                    }, linkedSession);
                    return linkedSession;
                }

                if (status === 'expired' || status === 'error') {
                    const message = String((session && session.error) || '').trim()
                        || (status === 'expired'
                            ? this.t('providers.oauthTimedOutMessage')
                            : this.t('providers.oauthFailedMessage'));
                    this.clearPendingOAuthSession();
                    this.applyOAuthAuthorizationState(pending, {
                        phase: status === 'expired' ? 'timed_out' : 'error',
                        popup_blocked: false,
                        manual_submit_busy: false,
                        manual_submit_error: message,
                        error: message
                    });
                    return session;
                }

                this.applyOAuthAuthorizationState(pending, {
                    phase: 'waiting',
                    popup_blocked: false,
                    manual_submit_busy: false,
                    manual_submit_error: ''
                });
                this.resumePendingOAuthSession(pending, {
                    showError: false,
                    phase: 'waiting'
                }).catch(error => {
                    if (error && error.message !== oauthAuthorizationCancelledError) {
                        console.error('Failed to resume pending OAuth session after manual submission:', error);
                    }
                });
                return session;
            } catch (error) {
                const message = String((error && error.message) || '').trim() || this.t('providers.oauthFailedMessage');
                this.updateOAuthAuthorization({
                    manual_submit_busy: false,
                    manual_submit_error: message
                });
                if (pending) {
                    const phase = String((this.oauthAuthorization && this.oauthAuthorization.phase) || '').trim() === 'blocked'
                        ? 'blocked'
                        : 'waiting';
                    this.resumePendingOAuthSession(pending, {
                        showError: false,
                        phase
                    }).catch(resumeError => {
                        if (resumeError && resumeError.message !== oauthAuthorizationCancelledError) {
                            console.error('Failed to resume pending OAuth session after manual submission error:', resumeError);
                        }
                    });
                }
                return null;
            }
        },

        loadPendingOAuthSession() {
            if (typeof localStorage === 'undefined' || !localStorage || typeof localStorage.getItem !== 'function') {
                this.pendingOAuthSession = null;
                return null;
            }

            let raw = null;
            try {
                raw = localStorage.getItem(pendingOAuthSessionStorageKey);
            } catch (error) {
                this.pendingOAuthSession = null;
                return null;
            }

            if (!raw) {
                this.pendingOAuthSession = null;
                return null;
            }

            try {
                const pending = this.normalizePendingOAuthSession(JSON.parse(raw));
                if (!pending) {
                    this.clearPendingOAuthSession();
                    return null;
                }
                this.pendingOAuthSession = pending;
                return pending;
            } catch (error) {
                this.clearPendingOAuthSession();
                return null;
            }
        },

        savePendingOAuthSession(session) {
            const pending = this.normalizePendingOAuthSession(session);
            if (!pending) {
                this.clearPendingOAuthSession();
                return null;
            }

            this.pendingOAuthSession = pending;
            if (typeof localStorage !== 'undefined' && localStorage && typeof localStorage.setItem === 'function') {
                try {
                    localStorage.setItem(pendingOAuthSessionStorageKey, JSON.stringify(pending));
                } catch (error) {
                    console.error('Failed to persist pending OAuth session:', error);
                }
            }
            return pending;
        },

        clearPendingOAuthSession() {
            this.pendingOAuthSession = null;
            if (typeof localStorage !== 'undefined' && localStorage && typeof localStorage.removeItem === 'function') {
                try {
                    localStorage.removeItem(pendingOAuthSessionStorageKey);
                } catch (error) {
                    console.error('Failed to clear pending OAuth session:', error);
                }
            }
        },

        async handleCompletedOAuthSession(pending, session) {
            const targetClient = String((pending && pending.client_type) || this.selectedClient || '').trim().toLowerCase();
            if (this.isSupportedClientType(targetClient)) {
                this.selectedClient = targetClient;
            }

            this.applyOAuthAuthorizationState(pending, { phase: 'completed', popup_blocked: false, error: '' });
            this.showAlert(
                'success',
                this.oauthSessionSuccessMessage(session),
                this.tf('providers.oauthAddedTitle', {
                    provider: this.oauthProviderLabel(session.provider || (pending && pending.provider))
                })
            );

            try {
                this.closeModals();
            } catch (error) {
                console.error('Failed to close OAuth authorization modal after success:', error);
                this.showAddProviderModal = false;
                this.showEditProviderModal = false;
                this.oauthAuthorization = defaultOAuthAuthorizationState();
            }

            try {
                await this.loadProviders();
            } catch (error) {
                console.error('Failed to reload providers after successful OAuth authorization:', error);
            }

            try {
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to refresh status after successful OAuth authorization:', error);
            }
        },

        async linkCompletedOAuthSession(pending, session) {
            const completed = session && typeof session === 'object' ? session : {};
            if (String(completed.status || '').trim().toLowerCase() !== 'completed') {
                return completed;
            }
            if (String(completed.provider_name || '').trim()) {
                return completed;
            }

            const sessionID = String(completed.session_id || (pending && pending.session_id) || '').trim();
            const clientType = String((pending && pending.client_type) || this.selectedClient || '').trim().toLowerCase();
            if (!sessionID || !clientType) {
                return completed;
            }
            if (this.oauthLinkingPromise && this.oauthLinkingSessionId === sessionID) {
                return await this.oauthLinkingPromise;
            }

            const promise = this.apiCall(`/api/oauth/sessions/${encodeURIComponent(sessionID)}/link`, {
                method: 'POST',
                body: JSON.stringify({
                    client_type: clientType
                })
            }, false, true);
            this.oauthLinkingSessionId = sessionID;
            this.oauthLinkingPromise = promise;
            try {
                return await promise;
            } finally {
                if (this.oauthLinkingPromise === promise) {
                    this.oauthLinkingSessionId = '';
                    this.oauthLinkingPromise = null;
                }
            }
        },

        oauthSessionSuccessMessage(session) {
            const action = String((session && session.provider_action) || '').trim().toLowerCase();
            const providerName = String((session && session.provider_name) || '').trim()
                || this.oauthProviderLabel(session && session.provider);
            const params = {
                client: this.providerToastClientLabel(),
                email: String((session && (session.display_name || session.email)) || providerName || '').trim(),
                providerName
            };
            const key = ({
                reused: 'providers.oauthAddedMessageReused',
                relinked: 'providers.oauthAddedMessageRelinked',
                created: 'providers.oauthAddedMessageCreated'
            })[action] || 'providers.oauthAddedMessageCreated';
            return this.tf(key, params) || this.tf('providers.oauthAddedMessage', params);
        },

        oauthImportAlertMessage(result) {
            const imported = Number((result && result.imported_count) || 0);
            const linked = Number((result && result.linked_count) || 0);
            const skipped = Number((result && result.skipped_count) || 0);
            const failed = Number((result && result.failed_count) || 0);
            const summary = this.tf('providers.oauthImportSummary', { imported, linked, skipped, failed })
                || String((result && result.message) || '').trim();
            const details = this.oauthImportResultDetails(result && result.results);
            if (!details) {
                return summary;
            }
            return `${summary}\n${details}`;
        },

        oauthImportResultDetails(results) {
            if (!Array.isArray(results) || results.length === 0) {
                return '';
            }
            const details = [];
            for (const item of results) {
                const file = String((item && item.file) || 'credential.json').trim() || 'credential.json';
                const message = String((item && item.message) || '').trim();
                const status = String((item && item.status) || '').trim();
                if (message) {
                    details.push(`${file}: ${message}`);
                    continue;
                }
                if (status) {
                    details.push(`${file}: ${status}`);
                }
            }
            return details.join('\n');
        },

        defaultOAuthProviderValue() {
            return (this.oauthProviders[0] && this.oauthProviders[0].provider) || '';
        },

        hasOAuthProviders() {
            return Array.isArray(this.oauthProviders) && this.oauthProviders.length > 0;
        },

        syncProviderFormOAuthAvailability() {
            if (this.showEditProviderModal) {
                return;
            }
            if (!this.hasOAuthProviders()) {
                if (this.providerFormUsesOAuth()) {
                    this.providerForm.auth_type = 'api_key';
                }
                this.providerForm.oauth_provider = '';
                return;
            }

            const current = String(this.providerForm.oauth_provider || '').trim().toLowerCase();
            if (!this.oauthProviders.some(item => String((item && item.provider) || '').trim().toLowerCase() === current)) {
                this.providerForm.oauth_provider = this.defaultOAuthProviderValue();
            }
        },

        async loadOAuthProviders(background = false) {
            try {
                const items = await this.apiCall(
                    `/api/oauth/providers?client_type=${encodeURIComponent(this.selectedClient)}`,
                    {},
                    background,
                    true
                );
                this.oauthProviders = Array.isArray(items) ? items : [];
            } catch (error) {
                console.error('Failed to load oauth providers:', error);
                this.oauthProviders = [];
            }
            this.syncProviderFormOAuthAvailability();
        },

        async resumePendingOAuthSession(pendingSession = null, options = {}) {
            const pending = pendingSession ? this.savePendingOAuthSession(pendingSession) : this.loadPendingOAuthSession();
            if (!pending) {
                return null;
            }

            if (this.oauthPollingPromise && this.oauthPollingSessionId === pending.session_id) {
                return this.oauthPollingPromise;
            }

            const showError = options.showError !== false;
            const phase = String(options.phase || '').trim() || 'waiting';
            const token = this.oauthPollingToken + 1;
            this.oauthPollingToken = token;
            this.oauthPollingSessionId = pending.session_id;
            this.applyOAuthAuthorizationState(pending, {
                phase,
                popup_blocked: phase === 'blocked',
                error: ''
            });
            const promise = (async () => {
                try {
                    const session = await this.pollOAuthSession(pending.session_id, { pending, token });
                    if (token && token !== this.oauthPollingToken) {
                        throw new Error(oauthAuthorizationCancelledError);
                    }
                    const status = String((session && session.status) || '').trim().toLowerCase();
                    if (status !== 'completed') {
                        const message = String((session && session.error) || '').trim()
                            || (status === 'expired'
                                ? this.t('providers.oauthTimedOutMessage')
                                : this.t('providers.oauthFailedMessage'));
                        this.clearPendingOAuthSession();
                        this.applyOAuthAuthorizationState(pending, {
                            phase: status === 'expired' ? 'timed_out' : 'error',
                            popup_blocked: false,
                            error: message
                        });
                        if (showError) {
                            this.showAlert('error', message, this.oauthAuthorizationTitle());
                        }
                        return session;
                    }

                    const linkedSession = await this.linkCompletedOAuthSession(pending, session);
                    if (token && token !== this.oauthPollingToken) {
                        throw new Error(oauthAuthorizationCancelledError);
                    }
                    this.clearPendingOAuthSession();
                    await this.handleCompletedOAuthSession(pending, linkedSession);
                    return linkedSession;
                } catch (error) {
                    if (error && error.message === oauthAuthorizationCancelledError) {
                        return null;
                    }
                    const message = String((error && error.message) || '').trim() || this.t('providers.oauthFailedMessage');
                    this.clearPendingOAuthSession();
                    this.applyOAuthAuthorizationState(pending, {
                        phase: message === this.t('providers.oauthTimedOutMessage') ? 'timed_out' : 'error',
                        popup_blocked: false,
                        error: message
                    });
                    if (showError && message) {
                        this.showAlert('error', message, this.oauthAuthorizationTitle());
                    }
                    throw error;
                } finally {
                    if (this.oauthPollingPromise === promise) {
                        this.oauthPollingSessionId = '';
                        this.oauthPollingPromise = null;
                    }
                }
            })();
            this.oauthPollingPromise = promise;
            return promise;
        },

        // Initialization
        async init() {
            this.initLocale();
            this.initTheme();
            const pendingOAuthSession = this.loadPendingOAuthSession();
            if (pendingOAuthSession && this.isSupportedClientType(pendingOAuthSession.client_type)) {
                this.selectedClient = pendingOAuthSession.client_type;
            }

            // Initial data load
            this.isLoading = true;
            try {
                await Promise.all([
                    this.refreshStatus(),
                    this.loadServiceStatus(),
                    this.loadProviders(),
                    this.loadOAuthProviders(true),
                    this.loadGlobalConfig(),
                    this.loadIntegrations(true)
                ]);
                this.$nextTick(() => {
                    this.initSortable();
                });
            } finally {
                this.isLoading = false;
            }

            if (pendingOAuthSession) {
                this.resumePendingOAuthSession(pendingOAuthSession, { showError: false }).catch(error => {
                    console.error('Failed to resume pending OAuth session:', error);
                });
            }

            // Simple poller: 3s refresh while on Services/Status tabs.
            this.servicePoll = setInterval(() => {
                if (this.activeTab === 'services') {
                    this.loadServiceStatus(true);
                }
                if (this.activeTab === 'status') {
                    this.refreshStatus();
                }
            }, 3000);
        },

        // Theme Management
        initTheme() {
            this.applyTheme(this.theme);

            // Listen for system preference changes if in system mode
            window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
                if (this.theme === 'system') {
                    this.applyTheme('system');
                }
            });
        },

        toggleTheme() {
            const modes = ['system', 'light', 'dark'];
            const nextIndex = (modes.indexOf(this.theme) + 1) % modes.length;
            this.theme = modes[nextIndex];
            localStorage.setItem('theme', this.theme);
            this.applyTheme(this.theme);
        },

        applyTheme(theme) {
            const isDark = theme === 'dark' ||
                (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);

            if (isDark) {
                document.documentElement.classList.add('dark');
            } else {
                document.documentElement.classList.remove('dark');
            }
        },

        get isDarkTheme() {
            return this.theme === 'dark' ||
                (this.theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);
        },

        get brandIconSrc() {
            return this.isDarkTheme ? '/static/clipal-icon-dark.svg' : '/static/clipal-icon.svg';
        },

        get themeIcon() {
            if (this.theme === 'system') return '💻';
            return this.theme === 'dark' ? '🌙' : '☀️';
        },

        get themeLabel() {
            switch (this.theme) {
                case 'light':
                    return this.t('theme.light');
                case 'dark':
                    return this.t('theme.dark');
                default:
                    return this.t('theme.system');
            }
        },

        providerStatusEntries(client, state) {
            const current = String((client && client.current_provider) || '').trim();
            const providers = Array.isArray(client && client.providers) ? client.providers : [];
            const entries = providers
                .filter(p => p && String(p.name || '').trim())
                .map(p => ({ ...p }));

            if (current && !entries.some(p => String(p.name || '').trim() === current)) {
                entries.unshift({
                    name: current,
                    enabled: true,
                    priority: 0,
                    key_count: 0,
                    available_key_count: 0,
                    state: 'available',
                    detail: 'Current provider is active but has not appeared in the latest provider snapshot yet.'
                });
            }

            const sorted = entries.sort((a, b) => {
                const aPriority = Number(a && a.priority);
                const bPriority = Number(b && b.priority);
                const aValue = Number.isFinite(aPriority) && aPriority > 0 ? aPriority : Number.MAX_SAFE_INTEGER;
                const bValue = Number.isFinite(bPriority) && bPriority > 0 ? bPriority : Number.MAX_SAFE_INTEGER;
                if (aValue !== bValue) return aValue - bValue;
                return String(a.name || '').localeCompare(String(b.name || ''));
            });

            return sorted.filter(p => {
                const name = String(p.name || '').trim();
                const skip = String(p.skip_reason || '').trim();
                const isCurrent = !!current && name === current;
                const isDisabled = p.enabled === false || !!skip;

                switch (state) {
                    case 'current':
                        return isCurrent;
                    case 'active':
                        return !isCurrent && !isDisabled && p.state === 'available';
                    case 'disabled':
                        return !isCurrent && p.state === 'disabled';
                    case 'cooling_down':
                        return !isCurrent && p.state === 'cooling_down';
                    case 'unavailable':
                        return !isCurrent && p.state === 'unavailable';
                    case 'recovery_probe':
                        return !isCurrent && p.state === 'recovery_probe';
                    default:
                        return false;
                }
            });
        },

        providerStatusGroups(client) {
            const groups = [
                { key: 'current', label: this.t('statusPage.groupCurrent') },
                { key: 'active', label: this.t('statusPage.groupActive') },
                { key: 'disabled', label: this.t('statusPage.groupDisabled') },
                { key: 'cooling_down', label: this.t('statusPage.groupCoolingDown') },
                { key: 'unavailable', label: this.t('statusPage.groupUnavailable') },
                { key: 'recovery_probe', label: this.t('statusPage.groupRecoveryProbe') }
            ];
            return groups.filter(group => this.providerStatusEntries(client, group.key).length > 0);
        },

        providerStatusChipClass(state, p) {
            if (state === 'current') return 'chip-primary';
            if (state === 'disabled') return 'chip-danger';
            if (state === 'cooling_down' || state === 'unavailable') return 'chip-warn';
            if (state === 'recovery_probe') return 'chip-info';
            return '';
        },

        // API Calls
        async apiCall(url, options = {}, background = false, suppressAlert = false) {
            if (!background) this.isLoading = true;
            try {
                // Minimum loading time to prevent flickering for fast requests
                const start = Date.now();
                const headers = {
                    'X-Clipal-UI': '1',
                    ...options.headers
                };
                const bodyIsFormData = typeof FormData !== 'undefined'
                    && options
                    && options.body instanceof FormData;
                const hasContentTypeHeader = Object.keys(headers).some(key => String(key).toLowerCase() === 'content-type');
                if (!bodyIsFormData && !hasContentTypeHeader) {
                    headers['Content-Type'] = 'application/json';
                }

                const response = await fetch(url, {
                    ...options,
                    headers
                });

                if (!response.ok) {
                    const error = await response.json();
                    throw new Error(error.error || 'Request failed');
                }

                const data = await response.json();

                // Artificial delay for smoother UX if request was too fast
                if (!background) {
                    const elapsed = Date.now() - start;
                    if (elapsed < 300) await new Promise(r => setTimeout(r, 300 - elapsed));
                }

                return data;
            } catch (error) {
                if (!suppressAlert) {
                    this.showAlert('error', error.message);
                }
                throw error;
            } finally {
                if (!background) this.isLoading = false;
            }
        },

        // Status
        async refreshStatus() {
            try {
                this.status = await this.apiCall('/api/status', {}, true); // Background update
            } catch (error) {
                console.error('Failed to refresh status:', error);
            }
        },

        // Services
        async loadServiceStatus(background = false) {
            try {
                const status = await this.apiCall('/api/service/status', {}, !!background);
                this.serviceStatus = this.withDefaultServiceStatus(status);
                this.serviceBusyAction = '';
            } catch (error) {
                console.error('Failed to load service status:', error);
            }
        },

        async serviceAction(action) {
            const disabledReason = this.serviceActionDisabledReason(action);
            if (disabledReason) {
                return;
            }
            if (action === 'uninstall' && !confirm(this.t('services.confirmUninstall'))) return;
            if (action === 'stop' && !confirm(this.t('services.confirmStop'))) return;
            if (action === 'restart' && !confirm(this.t('services.confirmRestart'))) return;

            // Best-effort request: the service might stop/restart mid-flight.
            this.serviceBusyAction = action;
            try {
                await fetch(`/api/service/${action}`, {
                    method: 'POST',
                    headers: { 'X-Clipal-UI': '1', 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.serviceForm),
                    keepalive: true
                });
            } catch (e) {
                // Ignore network errors (common when the service restarts).
            }

            this.showAlert('info', this.tf('services.requestedAction', { action: this.serviceActionLabel(action) }));
            // Staggered refresh to cover fast/slow restart paths.
            setTimeout(() => this.loadServiceStatus(true), 1500);
            setTimeout(() => this.loadServiceStatus(true), 3500);
            setTimeout(() => this.loadServiceStatus(true), 7000);
            setTimeout(() => {
                if (this.serviceBusyAction === action) {
                    this.serviceBusyAction = '';
                }
            }, 9000);
        },

        async copyToClipboard(text) {
            const value = String(text || '').trim();
            if (!value) return false;

            try {
                if (navigator.clipboard && navigator.clipboard.writeText) {
                    await navigator.clipboard.writeText(value);
                    return true;
                }
            } catch (e) {
                // fall through to legacy approach
            }

            try {
                const ta = document.createElement('textarea');
                ta.value = value;
                ta.setAttribute('readonly', '');
                ta.style.position = 'absolute';
                ta.style.left = '-9999px';
                document.body.appendChild(ta);
                ta.select();
                const ok = document.execCommand('copy');
                document.body.removeChild(ta);
                return ok;
            } catch (e) {
                return false;
            }
        },

        async copyServiceInstallCommand() {
            const ok = await this.copyToClipboard(this.serviceStatus.install_command);
            if (ok) this.showAlert('success', this.t('services.installCommandCopied'));
            else this.showAlert('error', this.t('services.copyCommandFailed'));
        },

        serviceRuntimeLabel() {
            if (!this.serviceStatus.supported) return this.t('services.unsupported');
            if (!this.serviceStatus.installed) return this.t('services.notInstalled');
            if (this.serviceStatus.running) return this.t('services.running');
            if (this.serviceStatus.loaded) return this.t('services.stopped');
            return this.t('services.needsAttention');
        },

        serviceRuntimeClass() {
            if (!this.serviceStatus.supported) return 'pill-danger';
            if (!this.serviceStatus.installed) return 'pill-warning';
            if (this.serviceStatus.running) return 'pill-success';
            if (this.serviceStatus.loaded) return 'pill-warning';
            return 'pill-danger';
        },

        serviceActionIsBusy(action) {
            return this.serviceBusyAction === action;
        },

        serviceActionDisabledReason(action) {
            if (this.serviceActionIsBusy(action) || action === 'check') {
                return '';
            }
            if (!this.serviceStatus.supported) {
                return this.t('services.notSupportedReason');
            }

            switch (String(action || '').trim()) {
                case 'install':
                    if (!this.serviceStatus.installed) return '';
                    return this.serviceForm.force
                        ? ''
                        : this.t('services.alreadyInstalledReason');
                case 'start':
                    if (!this.serviceStatus.installed) return this.t('services.installFirst');
                    if (this.serviceStatus.running) return this.t('services.alreadyRunning');
                    return '';
                case 'stop':
                    if (!this.serviceStatus.installed) return this.t('services.installFirst');
                    if (!this.serviceStatus.running) return this.t('services.notRunning');
                    return '';
                case 'restart':
                    if (!this.serviceStatus.installed) return this.t('services.installFirst');
                    if (!this.serviceStatus.running) return this.t('services.notRunning');
                    return '';
                case 'uninstall':
                    if (!this.serviceStatus.installed) return this.t('services.serviceNotInstalled');
                    return '';
                default:
                    return '';
            }
        },

        integrationStateLabel(state) {
            switch (String(state || '').trim()) {
                case 'configured':
                    return this.t('integrations.stateConfigured');
                case 'error':
                    return this.t('integrations.stateNeedsAttention');
                default:
                    return this.t('integrations.stateNotConfigured');
            }
        },

        integrationStateClass(state) {
            switch (String(state || '').trim()) {
                case 'configured':
                    return 'pill-success';
                case 'error':
                    return 'pill-danger';
                default:
                    return 'pill-warning';
            }
        },

        integrationProductName(product) {
            switch (String(product || '').trim()) {
                case 'claude':
                    return 'Claude Code';
                case 'codex':
                    return 'Codex CLI';
                case 'opencode':
                    return 'OpenCode';
                case 'gemini':
                    return 'Gemini CLI';
                case 'continue':
                    return 'Continue';
                case 'aider':
                    return 'Aider';
                case 'goose':
                    return 'Goose';
                default:
                    return product;
            }
        },


        integrationApplyLabel() {
            return this.t('integrations.apply');
        },

        integrationRollbackLabel() {
            return this.t('integrations.rollback');
        },

        integrationActionIsBusy(integration, action) {
            if (!integration || this.integrationBusyProduct !== integration.product) {
                return false;
            }
            return action === 'apply'
                ? integration.state !== 'configured'
                : integration.state === 'configured';
        },

        integrationActionDisabledReason(integration, action) {
            if (!integration || this.integrationActionIsBusy(integration, action)) {
                return '';
            }
            if (action === 'apply') {
                return integration.state === 'configured' ? this.t('integrations.alreadyUsing') : '';
            }
            if (!integration.backup_available) {
                return this.t('integrations.noBackupYet');
            }
            return integration.state !== 'configured'
                ? this.t('integrations.restoreOnlyActive')
                : '';
        },


        integrationProductNote(product) {
            switch (String(product || '').trim()) {
                case 'claude':
                    return this.t('integrations.noteClaude');
                case 'codex':
                    return this.t('integrations.noteCodex');
                case 'opencode':
                    return this.t('integrations.noteOpencode');
                case 'gemini':
                    return this.t('integrations.noteGemini');
                case 'continue':
                    return this.t('integrations.noteContinue');
                case 'aider':
                    return this.t('integrations.noteAider');
                case 'goose':
                    return this.t('integrations.noteGoose');
                default:
                    return this.t('integrations.noteDefault');
            }
        },


        integrationPreviewValue(content, emptyLabel) {
            const value = String(content || '');
            return value.trim() ? value : emptyLabel;
        },

        integrationSecondaryPreviewLabel(integration) {
            return integration && integration.state === 'configured' && integration.backup_available
                ? this.t('integrations.latestBackup')
                : this.t('integrations.afterApply');
        },

        integrationSecondaryPreviewContent(integration) {
            if (integration && integration.state === 'configured' && integration.backup_available) {
                return String((integration && integration.backup_content) || '');
            }
            return String((integration && integration.planned_content) || '');
        },

        integrationSecondaryPreviewEmptyLabel(integration) {
            if (integration && integration.state === 'configured' && integration.backup_available) {
                return integration.backup_target_existed
                    ? this.t('integrations.backupEmpty')
                    : this.t('integrations.originalFileMissing');
            }
            return this.t('integrations.noPlannedChanges');
        },

        integrationResultFor(integration) {
            const product = String((integration && integration.product) || '').trim();
            if (!product) {
                return null;
            }
            return this.integrationResults[product] || null;
        },

        integrationResultClass(result) {
            const type = String((result && result.type) || 'info').trim() || 'info';
            return `integration-result--${type}`;
        },

        integrationResultBadgeClass(result) {
            const type = String((result && result.type) || 'info').trim() || 'info';
            return `integration-result__badge--${type}`;
        },

        integrationResultBadgeLabel(type) {
            switch (String(type || '').trim()) {
                case 'success':
                    return this.t('integrations.resultUpdated');
                case 'error':
                    return this.t('integrations.resultError');
                default:
                    return this.t('integrations.resultNotice');
            }
        },

        setIntegrationResult(product, type, title, message) {
            const name = String(product || '').trim();
            if (!name) {
                return;
            }
            this.integrationResults = {
                ...this.integrationResults,
                [name]: {
                    type: String(type || 'info').trim() || 'info',
                    title: String(title || '').trim(),
                    message: String(message || '').trim()
                }
            };
        },

        clearIntegrationResult(product) {
            const name = String(product || '').trim();
            if (!name || !this.integrationResults[name]) {
                return;
            }
            const next = { ...this.integrationResults };
            delete next[name];
            this.integrationResults = next;
        },

        normalizeIntegration(item) {
            return {
                ...item,
                name: this.integrationProductName(item.product) || item.name,
                current_content: String((item && item.current_content) || ''),
                planned_content: String((item && item.planned_content) || ''),
                backup_content: String((item && item.backup_content) || ''),
                backup_target_existed: !!(item && item.backup_target_existed)
            };
        },


        async loadIntegrations(background = false) {
            try {
                const items = await this.apiCall('/api/integrations', {}, !!background);
                const integrations = (items || []).map(item => this.normalizeIntegration(item));
                this.integrations = integrations;
                const knownProducts = new Set(integrations.map(item => String((item && item.product) || '').trim()).filter(Boolean));
                this.integrationResults = Object.fromEntries(
                    Object.entries(this.integrationResults).filter(([product]) => knownProducts.has(product))
                );
            } catch (error) {
                console.error('Failed to load integrations:', error);
                this.integrations = [];
            }
        },

        async runIntegrationAction(product, action) {
            const name = String(product || '').trim();
            const op = String(action || '').trim();
            if (!name || !op) return;

            this.clearIntegrationResult(name);
            this.integrationBusyProduct = name;
            try {
                const response = await this.apiCall(`/api/integrations/${encodeURIComponent(name)}/${op}`, {
                    method: 'POST'
                }, false, true);
                this.integrations = (this.integrations || []).map(item =>
                    item && item.product === name
                        ? this.normalizeIntegration({ ...response.status, name: this.integrationProductName(name) || response.status.name })
                        : item
                );
                if (op === 'apply') {
                    this.setIntegrationResult(
                        name,
                        'success',
                        this.t('integrations.resultUsingClipalTitle'),
                        this.t('integrations.resultRestartMessage')
                    );
                } else {
                    this.setIntegrationResult(
                        name,
                        'success',
                        this.t('integrations.resultRestoredTitle'),
                        this.t('integrations.resultRestartMessage')
                    );
                }
            } catch (error) {
                const title = op === 'apply'
                    ? this.t('integrations.resultApplyErrorTitle')
                    : this.t('integrations.resultRestoreErrorTitle');
                this.setIntegrationResult(name, 'error', title, error.message);
                this.showAlert('error', error.message, title);
                console.error(`Failed to ${op} integration:`, error);
            } finally {
                this.integrationBusyProduct = '';
            }
        },

        // Providers
        clientLabel(clientType) {
            const match = this.clientOptions.find(c => c.value === clientType);
            return match ? match.label : clientType;
        },

        providerToastClientLabel() {
            return this.clientLabel(this.selectedClient);
        },

        modeLabel(mode) {
            return String(mode || '').trim() === 'manual'
                ? this.t('providers.modeManual')
                : this.t('providers.modeAuto');
        },

        modeToggleLabel() {
            return String(this.clientConfig.mode || '').trim() === 'auto'
                ? this.t('providers.switchToManual')
                : this.t('providers.backToAuto');
        },

        modeToggleTitle() {
            return String(this.clientConfig.mode || '').trim() === 'auto' && !this.hasEnabledProviders
                ? this.t('providers.enableProviderFirst')
                : '';
        },

        configuredKeyCountLabel(count) {
            return this.tf('providers.configuredCount', { count: Number(count || 0) });
        },

        formatTokenCount(value) {
            const count = Number(value || 0);
            return Number.isFinite(count) ? count.toLocaleString(this.locale === 'zh-CN' ? 'zh-CN' : 'en-US') : '0';
        },

        formatCompactTokenCount(value) {
            const count = Number(value || 0);
            if (!Number.isFinite(count)) {
                return '0';
            }

            const locale = this.locale === 'zh-CN' ? 'zh-CN' : 'en-US';
            const sign = count < 0 ? '-' : '';
            const units = ['', 'K', 'M', 'B', 'T', 'P', 'E', 'Z', 'Y'];
            let scaled = Math.abs(count);
            let unitIndex = 0;

            while (scaled >= 1000 && unitIndex < units.length - 1) {
                scaled /= 1000;
                unitIndex += 1;
            }

            let decimals = scaled < 10 ? 2 : scaled < 100 ? 1 : 0;
            let rounded = Number(scaled.toFixed(decimals));
            while (rounded >= 1000 && unitIndex < units.length - 1) {
                scaled = rounded / 1000;
                unitIndex += 1;
                decimals = scaled < 10 ? 2 : scaled < 100 ? 1 : 0;
                rounded = Number(scaled.toFixed(decimals));
            }

            const numberText = rounded.toLocaleString(locale, {
                maximumFractionDigits: decimals
            });
            return `${sign}${numberText}${units[unitIndex]}`;
        },

        formatUSDMicros(value, exact = false) {
            const micros = Number(value || 0);
            if (!Number.isFinite(micros)) {
                return '$0';
            }

            const dollars = micros / 1_000_000;
            const absDollars = Math.abs(dollars);
            const locale = this.locale === 'zh-CN' ? 'zh-CN' : 'en-US';

            if (exact) {
                return new Intl.NumberFormat(locale, {
                    style: 'currency',
                    currency: 'USD',
                    minimumFractionDigits: 6,
                    maximumFractionDigits: 6
                }).format(dollars);
            }

            if (absDollars === 0) {
                return '$0';
            }

            const displayRoundedValue = (amount, fractionDigits, threshold) => {
                const scale = fractionDigits > 0 ? 10 ** fractionDigits : 1;
                const rounded = fractionDigits > 0
                    ? Math.round(amount * scale) / scale
                    : Math.round(amount);
                const truncated = fractionDigits > 0
                    ? Math.floor((amount + Number.EPSILON) * scale) / scale
                    : Math.floor(amount);
                const chosen = threshold > 0 && amount < threshold && rounded >= threshold
                    ? truncated
                    : rounded;
                return fractionDigits > 0 ? Number(chosen.toFixed(fractionDigits)) : chosen;
            };

            const sign = dollars < 0 ? '-' : '';
            if (absDollars >= 1000) {
                const units = ['', 'K', 'M', 'B', 'T', 'P', 'E', 'Z', 'Y'];
                let scaled = absDollars;
                let unitIndex = 0;

                while (scaled >= 1000 && unitIndex < units.length - 1) {
                    scaled /= 1000;
                    unitIndex += 1;
                }

                let decimals = scaled < 10 ? 2 : scaled < 100 ? 1 : 0;
                const rounded = displayRoundedValue(scaled, decimals, 1000);

                const numberText = rounded.toLocaleString(locale, {
                    maximumFractionDigits: decimals
                });
                return `${sign}$${numberText}${units[unitIndex]}`;
            }

            let digits = 0;
            if (absDollars < 100) {
                digits = 1;
            }
            if (absDollars < 10) {
                digits = 2;
            }
            if (absDollars < 1) {
                digits = Math.min(6, Math.max(2, Math.ceil(-Math.log10(absDollars)) + 2));
            }

            const threshold = absDollars < 1 ? 1 : absDollars < 10 ? 10 : absDollars < 100 ? 100 : 1000;
            const rounded = displayRoundedValue(absDollars, digits, threshold);
            const numberText = rounded.toLocaleString(locale, {
                minimumFractionDigits: 0,
                maximumFractionDigits: digits
            });
            return `${sign}$${numberText}`;
        },

        parseTimestamp(value) {
            const text = String(value || '').trim();
            if (!text) {
                return null;
            }
            const parsed = new Date(text);
            return Number.isNaN(parsed.getTime()) ? null : parsed;
        },

        formatRelativeTime(value, emptyLabel) {
            const parsed = value instanceof Date ? value : this.parseTimestamp(value);
            if (!parsed) {
                return emptyLabel || this.t('providers.never');
            }

            const diffMs = parsed.getTime() - Date.now();
            const absMs = Math.abs(diffMs);
            if (absMs < 60 * 1000) {
                return this.t('providers.justNow');
            }

            const units = [
                ['year', 365 * 24 * 60 * 60 * 1000],
                ['month', 30 * 24 * 60 * 60 * 1000],
                ['day', 24 * 60 * 60 * 1000],
                ['hour', 60 * 60 * 1000],
                ['minute', 60 * 1000]
            ];
            for (const [unit, unitMs] of units) {
                if (absMs >= unitMs || unit === 'minute') {
                    const delta = Math.round(diffMs / unitMs);
                    if (typeof Intl !== 'undefined' && typeof Intl.RelativeTimeFormat === 'function') {
                        const locale = this.locale === 'zh-CN' ? 'zh-CN' : 'en';
                        return new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(delta, unit);
                    }

                    const absDelta = Math.abs(delta);
                    const suffix = delta < 0 ? 'ago' : 'from now';
                    return `${absDelta} ${unit}${absDelta === 1 ? '' : 's'} ${suffix}`;
                }
            }

            return this.t('providers.justNow');
        },

        providerUsageTotal(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            return this.formatCompactTokenCount(usage.total_tokens || 0);
        },

        providerUsageTotalTitle(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            return this.formatTokenCount(usage.total_tokens || 0);
        },

        providerUsageInOut(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            return `${this.formatCompactTokenCount(usage.input_tokens || 0)} / ${this.formatCompactTokenCount(usage.output_tokens || 0)}`;
        },

        providerUsageInOutTitle(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            return `${this.formatTokenCount(usage.input_tokens || 0)} / ${this.formatTokenCount(usage.output_tokens || 0)}`;
        },

        providerUsageReasoningVisible(provider) {
            return !!(provider && provider.usage && Number(provider.usage.reasoning_tokens || 0) > 0);
        },

        providerUsageReasoning(provider) {
            if (!this.providerUsageReasoningVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatCompactTokenCount(provider.usage.reasoning_tokens || 0);
        },

        providerUsageReasoningTitle(provider) {
            if (!this.providerUsageReasoningVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatTokenCount(provider.usage.reasoning_tokens || 0);
        },

        providerUsageThoughtsVisible(provider) {
            return !!(provider && provider.usage && Number(provider.usage.thoughts_tokens || 0) > 0);
        },

        providerUsageThoughts(provider) {
            if (!this.providerUsageThoughtsVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatCompactTokenCount(provider.usage.thoughts_tokens || 0);
        },

        providerUsageThoughtsTitle(provider) {
            if (!this.providerUsageThoughtsVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatTokenCount(provider.usage.thoughts_tokens || 0);
        },

        providerSpendVisible(provider) {
            return !!(provider && provider.usage && provider.usage.has_cost);
        },

        providerSpendToday(provider) {
            if (!this.providerSpendVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatUSDMicros(provider.usage.spend_today_micros || 0);
        },

        providerSpendTodayTitle(provider) {
            if (!this.providerSpendVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatUSDMicros(provider.usage.spend_today_micros || 0, true);
        },

        providerSpendWeek(provider) {
            if (!this.providerSpendVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatUSDMicros(provider.usage.spend_week_micros || 0);
        },

        providerSpendWeekTitle(provider) {
            if (!this.providerSpendVisible(provider)) {
                return this.t('common.none');
            }
            return this.formatUSDMicros(provider.usage.spend_week_micros || 0, true);
        },

        providerOAuthPlanRaw(provider) {
            return String((provider && provider.oauth_plan_type) || '').trim().toLowerCase();
        },

        providerOAuthPlanLabel(provider) {
            const raw = this.providerOAuthPlanRaw(provider);
            if (!raw) {
                return '';
            }
            switch (raw) {
                case 'free':
                    return 'Free';
                case 'plus':
                    return 'Plus';
                case 'pro':
                    return 'Pro';
                case 'prolite':
                    return 'Pro Lite';
                case 'team':
                    return 'Team';
                case 'business':
                    return 'Business';
                case 'enterprise':
                    return 'Enterprise';
                case 'education':
                case 'edu':
                    return 'Education';
                case 'go':
                    return 'Go';
                default:
                    return String(raw)
                        .split(/[_\s-]+/)
                        .filter(Boolean)
                        .map(part => part.charAt(0).toUpperCase() + part.slice(1))
                        .join(' ');
            }
        },

        providerOAuthRateLimitData(provider) {
            return provider && provider.oauth_rate_limits && typeof provider.oauth_rate_limits === 'object'
                ? provider.oauth_rate_limits
                : null;
        },

        providerOAuthHasSummary(provider) {
            return !!this.providerOAuthPlanLabel(provider) || this.providerOAuthRateLimitSections(provider).length > 0;
        },

        providerOAuthRateLimitSections(provider) {
            const limits = this.providerOAuthRateLimitData(provider);
            if (!limits) {
                return [];
            }

            const sections = [];
            if (limits.primary) {
                sections.push({
                    key: 'primary',
                    label: this.providerOAuthRateLimitWindowLabel(limits.primary),
                    window: limits.primary
                });
            }
            if (limits.secondary) {
                sections.push({
                    key: 'secondary',
                    label: this.providerOAuthRateLimitWindowLabel(limits.secondary),
                    window: limits.secondary
                });
            }

            const additional = Array.isArray(limits.additional) ? limits.additional : [];
            additional.forEach((limit, index) => {
                if (limit && limit.primary) {
                    sections.push({
                        key: `additional-${index}-primary`,
                        label: this.providerOAuthAdditionalLimitLabel(limit, limit.primary),
                        window: limit.primary
                    });
                }
                if (limit && limit.secondary) {
                    sections.push({
                        key: `additional-${index}-secondary`,
                        label: this.providerOAuthAdditionalLimitLabel(limit, limit.secondary),
                        window: limit.secondary
                    });
                }
            });

            return sections.filter(section => section && section.window && section.label);
        },

        providerOAuthAdditionalLimitLabel(limit, window) {
            const base = this.humanizeRateLimitName((limit && (limit.limit_name || limit.limit_id)) || '');
            const normalized = String((limit && (limit.limit_id || limit.limit_name)) || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
            const windowLabel = this.providerOAuthRateLimitWindowLabel(window);
            const windowMinutes = Math.round(Number(window && window.window_minutes) || 0);
            if (normalized === 'code_review' && windowMinutes === 10080) {
                return this.t('providers.rateLimitCodeReviewWeekly');
            }
            if (!base) {
                return windowLabel;
            }
            if (!windowLabel) {
                return base;
            }
            if (base.toLowerCase().includes(windowLabel.toLowerCase())) {
                return base;
            }
            if (this.locale === 'zh-CN') {
                return `${base}${windowLabel}`;
            }
            return `${base} ${windowLabel.charAt(0).toLowerCase()}${windowLabel.slice(1)}`;
        },

        humanizeRateLimitName(value) {
            const raw = String(value || '').trim();
            if (!raw) {
                return '';
            }
            const normalized = raw.toLowerCase().replace(/[\s-]+/g, '_');
            if (normalized === 'code_review') {
                return this.locale === 'zh-CN' ? '代码审查' : 'Code review';
            }
            return raw
                .replace(/[_-]+/g, ' ')
                .trim()
                .replace(/\s+/g, ' ')
                .replace(/\b\w/g, letter => letter.toUpperCase());
        },

        providerOAuthRateLimitWindowLabel(window) {
            const minutes = Math.round(Number(window && window.window_minutes) || 0);
            if (minutes <= 0) {
                return this.t('providers.rateLimit');
            }
            if (minutes === 10080) {
                return this.t('providers.rateLimitWeekly');
            }
            if (minutes === 1440) {
                return this.t('providers.rateLimitDaily');
            }
            if (minutes === 60) {
                return this.t('providers.rateLimitHourly');
            }
            if (minutes % 1440 === 0) {
                return this.tf('providers.rateLimitDays', { days: minutes / 1440 });
            }
            if (minutes % 60 === 0) {
                return this.tf('providers.rateLimitHours', { hours: minutes / 60 });
            }
            return this.tf('providers.rateLimitMinutes', { minutes });
        },

        providerOAuthRateLimitPercent(window) {
            const value = Number(window && window.used_percent);
            if (!Number.isFinite(value)) {
                return 0;
            }
            return Math.max(0, Math.min(100, value));
        },

        providerOAuthRateLimitPercentLabel(window) {
            return `${Math.round(this.providerOAuthRateLimitPercent(window))}%`;
        },

        providerOAuthRateLimitResetLabel(window) {
            return this.formatCompactDateTime(window && window.resets_at);
        },

        providerOAuthRateLimitBarStyle(window) {
            return `width: ${this.providerOAuthRateLimitPercent(window)}%;`;
        },

        formatCompactDateTime(value) {
            const parsed = this.parseTimestamp(value);
            if (!parsed) {
                return '';
            }
            const month = String(parsed.getMonth() + 1).padStart(2, '0');
            const day = String(parsed.getDate()).padStart(2, '0');
            const hours = String(parsed.getHours()).padStart(2, '0');
            const minutes = String(parsed.getMinutes()).padStart(2, '0');
            return `${month}/${day}, ${hours}:${minutes}`;
        },

        providerPinBadgeTitle() {
            return this.t('providers.pinnedProvider');
        },

        providerStatusText(enabled) {
            return enabled ? this.t('common.active') : this.t('common.disabled');
        },

        providerToggleTitle(provider) {
            const isPinned = String(this.clientConfig.mode || '').trim() === 'manual'
                && provider
                && provider.name === this.clientConfig.pinned_provider;
            if (isPinned) {
                return provider.enabled
                    ? this.t('providers.pinnedProviderCannotDisable')
                    : this.t('providers.enablePinnedProvider');
            }
            return provider && provider.enabled ? this.t('providers.disable') : this.t('providers.enable');
        },

        providerPinTitle(provider) {
            const isPinned = String(this.clientConfig.mode || '').trim() === 'manual'
                && provider
                && provider.name === this.clientConfig.pinned_provider;
            return isPinned ? this.t('providers.pinnedProvider') : this.t('providers.pinTitle');
        },

        providerEditTitle(provider) {
            if (this.providerUsesOAuth(provider)) {
                return this.t('providers.oauthEditLocked');
            }
            return this.t('providers.edit');
        },

        providerEditKeyHint() {
            const count = Number(this.editingProviderKeyCount || 0);
            if (this.locale === 'zh-CN') {
                return this.tf('modal.provider.keepExistingKeys', { count });
            }
            if (count === 1) {
                return this.t('modal.provider.keepExistingKey');
            }
            return this.tf('modal.provider.keepExistingKeys', { count });
        },

        normalizeGlobalProxyMode(mode) {
            const value = String(mode || '').trim().toLowerCase();
            return value || 'environment';
        },

        normalizeProviderProxyMode(mode) {
            const value = String(mode || '').trim().toLowerCase();
            return value || 'default';
        },

        providerUsesOAuth(provider) {
            return String((provider && provider.auth_type) || '').trim().toLowerCase() === 'oauth';
        },

        providerSupportsOAuthMetadata(provider) {
            return this.providerUsesOAuth(provider)
                && String((provider && provider.oauth_provider) || '').trim().toLowerCase() === 'codex'
                && String((provider && provider.oauth_ref) || '').trim() !== '';
        },

        ensureProviderOAuthMetadataState(provider) {
            if (!provider || typeof provider !== 'object') {
                return {
                    oauth_metadata_loading: false,
                    oauth_metadata_loaded: false,
                    oauth_metadata_error: ''
                };
            }
            if (typeof provider.oauth_metadata_loading === 'undefined') {
                provider.oauth_metadata_loading = false;
            }
            if (typeof provider.oauth_metadata_loaded === 'undefined') {
                provider.oauth_metadata_loaded = false;
            }
            if (typeof provider.oauth_metadata_error === 'undefined') {
                provider.oauth_metadata_error = '';
            }
            return provider;
        },

        providerOAuthMetadataBusy(provider) {
            return !!this.ensureProviderOAuthMetadataState(provider).oauth_metadata_loading;
        },

        providerHasLoadedOAuthMetadata(provider) {
            return !!this.ensureProviderOAuthMetadataState(provider).oauth_metadata_loaded;
        },

        providerOAuthMetadataError(provider) {
            return String(this.ensureProviderOAuthMetadataState(provider).oauth_metadata_error || '').trim();
        },

        providerOAuthMetadataButtonLabel(provider) {
            if (this.providerOAuthMetadataBusy(provider)) {
                return this.t('common.working');
            }
            if (this.providerHasLoadedOAuthMetadata(provider)) {
                return this.t('common.refresh');
            }
            return this.t('common.load');
        },

        providerFormUsesOAuth() {
            return String(this.providerForm.auth_type || '').trim().toLowerCase() === 'oauth';
        },

        setProviderAuthType(authType) {
            const next = String(authType || '').trim().toLowerCase();
            if (next === 'oauth' && !this.hasOAuthProviders()) {
                this.providerForm.auth_type = 'api_key';
                return;
            }
            this.providerForm.auth_type = next === 'oauth' ? 'oauth' : 'api_key';
        },

        providerDisplayName(provider) {
            const displayName = String((provider && provider.display_name) || '').trim();
            if (displayName) {
                return displayName;
            }
            return String((provider && provider.name) || '').trim();
        },

        providerCardTitle(provider) {
            const displayName = this.providerDisplayName(provider);
            if (!this.providerUsesOAuth(provider)) {
                return displayName;
            }
            return this.truncateLabel(displayName, 15);
        },

        truncateLabel(value, maxChars = 15) {
            const text = String(value || '').trim();
            const limit = Number(maxChars);
            if (!text || !Number.isFinite(limit) || limit < 1) {
                return text;
            }
            const chars = Array.from(text);
            if (chars.length <= limit) {
                return text;
            }
            if (limit <= 3) {
                return chars.slice(0, limit).join('');
            }
            return chars.slice(0, limit - 3).join('') + '...';
        },

        normalizeOAuthAuthStatus(status) {
            switch (String(status || '').trim().toLowerCase()) {
                case 'ready':
                case 'refresh_due':
                case 'reauth_needed':
                    return String(status || '').trim().toLowerCase();
                default:
                    return '';
            }
        },

        providerOAuthAuthStatus(provider) {
            if (!this.providerUsesOAuth(provider)) {
                return '';
            }

            const explicit = this.normalizeOAuthAuthStatus(provider && provider.oauth_auth_status);
            if (explicit) {
                return explicit;
            }

            const expiresAt = this.parseTimestamp(provider && provider.oauth_expires_at);
            if (expiresAt && expiresAt.getTime() <= Date.now()) {
                return 'refresh_due';
            }

            if (this.parseTimestamp(provider && provider.oauth_last_refresh)) {
                return 'ready';
            }

            return 'reauth_needed';
        },

        providerOAuthAuthStatusLabel(provider) {
            switch (this.providerOAuthAuthStatus(provider)) {
                case 'ready':
                    return this.t('providers.oauthStatusReady');
                case 'refresh_due':
                    return this.t('providers.oauthStatusRefreshDue');
                case 'reauth_needed':
                default:
                    return this.t('providers.oauthStatusReauthNeeded');
            }
        },

        providerOAuthAuthStatusClass(provider) {
            switch (this.providerOAuthAuthStatus(provider)) {
                case 'ready':
                    return 'pill--success';
                case 'refresh_due':
                    return 'pill--warning';
                case 'reauth_needed':
                default:
                    return 'pill--danger';
            }
        },

        providerOAuthRefreshSummary(provider) {
            if (!this.providerUsesOAuth(provider)) {
                return '';
            }
            return this.formatRelativeTime(provider && provider.oauth_last_refresh, this.t('providers.never'));
        },

        oauthProviderLabel(provider) {
            switch (String(provider || '').trim().toLowerCase()) {
                case 'codex':
                    return this.t('modal.provider.oauthProviderCodex');
                case 'gemini':
                    return 'Gemini';
                case 'claude':
                    return 'Claude Code';
                default:
                    return String(provider || '').trim();
            }
        },

        oauthImportFileHint() {
            switch (String(this.providerForm.oauth_provider || '').trim().toLowerCase()) {
                case 'codex':
                    return this.t('modal.provider.importFileHintCodex');
                case 'claude':
                    return this.t('modal.provider.importFileHintClaude');
                case 'gemini':
                    return this.t('modal.provider.importFileHintGemini');
                default:
                    return this.t('modal.provider.importFileHint');
            }
        },

        providerAuthSummary(provider) {
            if (this.providerUsesOAuth(provider)) {
                return `${this.t('modal.provider.credentialSourceOAuth')} / ${this.oauthProviderLabel(provider && provider.oauth_provider)}`;
            }
            return this.t('modal.provider.credentialSourceApiKey');
        },

        providerModalSaveLabel() {
            if (!this.showEditProviderModal && this.providerFormUsesOAuth()) {
                return this.t('modal.provider.authorizeProvider');
            }
            return this.t('modal.provider.saveProvider');
        },

        providerModalTitle() {
            if (this.oauthAuthorizationActive()) {
                return this.oauthAuthorizationTitle();
            }
            return this.showEditProviderModal
                ? this.t('modal.provider.editTitle')
                : this.t('modal.provider.addTitle');
        },

        providerProxySummary(provider) {
            const mode = this.normalizeProviderProxyMode(provider && provider.proxy_mode);
            if (mode === 'direct') {
                return this.t('providers.proxyDirect');
            }
            if (mode === 'custom') {
                return String((provider && provider.proxy_url_hint) || '').trim() || this.t('providers.proxyCustom');
            }
            if (mode !== 'default') {
                return mode;
            }
            return '';
        },

        providerHasVisibleDetails(provider) {
            if (this.providerUsesOAuth(provider)) {
                return this.providerSupportsOAuthMetadata(provider)
                    || this.providerOAuthHasSummary(provider)
                    || !!(provider && provider.base_url)
                    || this.normalizeProviderProxyMode(provider && provider.proxy_mode) !== 'default'
                    || !!(provider && provider.usage && (provider.usage.has_usage || provider.usage.has_cost));
            }
            if (provider && provider.base_url) {
                return true;
            }
            if (this.normalizeProviderProxyMode(provider && provider.proxy_mode) !== 'default') {
                return true;
            }
            return !!(provider && provider.usage && (provider.usage.has_usage || provider.usage.has_cost));
        },

        async loadProviderOAuthMetadata(provider) {
            if (!this.providerSupportsOAuthMetadata(provider) || this.providerOAuthMetadataBusy(provider)) {
                return null;
            }

            const state = this.ensureProviderOAuthMetadataState(provider);
            state.oauth_metadata_loading = true;
            state.oauth_metadata_error = '';

            try {
                const payload = await this.apiCall(
                    `/api/providers/${this.selectedClient}/${encodeURIComponent(provider.name)}/oauth-metadata`,
                    {},
                    true,
                    true
                );
                provider.oauth_plan_type = String((payload && payload.oauth_plan_type) || '').trim();
                provider.oauth_rate_limits = payload && payload.oauth_rate_limits && typeof payload.oauth_rate_limits === 'object'
                    ? payload.oauth_rate_limits
                    : null;
                state.oauth_metadata_loaded = true;
                return payload;
            } catch (error) {
                state.oauth_metadata_error = error && error.message ? error.message : 'Failed to load metadata';
                console.error('Failed to load provider oauth metadata:', error);
                return null;
            } finally {
                state.oauth_metadata_loading = false;
            }
        },

        providerFormUsesCustomProxy() {
            return this.normalizeProviderProxyMode(this.providerForm.proxy_mode) === 'custom';
        },

        providerEditProxyHint() {
            const proxy = String(this.providerForm.proxy_url_hint || '').trim();
            if (!proxy) {
                return '';
            }
            return this.tf('modal.provider.keepExistingProxy', { proxy });
        },

        providerOverrideSupport() {
            const support = this.clientConfig && this.clientConfig.override_support;
            if (!support || typeof support !== 'object') {
                return this.defaultProviderOverrideSupport();
            }
            return {
                model: !!support.model,
                openai: {
                    reasoning_effort: !!(support.openai && support.openai.reasoning_effort)
                },
                claude: {
                    thinking_budget_tokens: !!(support.claude && support.claude.thinking_budget_tokens)
                }
            };
        },

        defaultProviderOverrideSupport() {
            return {
                model: false,
                openai: {
                    reasoning_effort: false
                },
                claude: {
                    thinking_budget_tokens: false
                }
            };
        },

        providerSupportsModelOverride() {
            return this.providerOverrideSupport().model;
        },

        providerSupportsReasoningEffort() {
            return this.providerOverrideSupport().openai.reasoning_effort;
        },

        providerSupportsThinkingBudget() {
            return this.providerOverrideSupport().claude.thinking_budget_tokens;
        },

        providerHasAnyOverrideSupport() {
            return this.providerSupportsModelOverride()
                || this.providerSupportsReasoningEffort()
                || this.providerSupportsThinkingBudget();
        },

        normalizeThinkingBudgetTokens(value) {
            const parsed = Number.parseInt(String(value ?? '').trim(), 10);
            if (Number.isNaN(parsed) || parsed < 0) {
                return 0;
            }
            return parsed;
        },

        levelLabel(level) {
            const value = String(level || '').trim().toLowerCase();
            return this.t(`level.${value}`);
        },

        serviceActionLabel(action) {
            switch (String(action || '').trim()) {
                case 'install':
                    return this.t('services.installService');
                case 'start':
                    return this.t('services.start');
                case 'restart':
                    return this.t('services.restart');
                case 'stop':
                    return this.t('services.stop');
                case 'uninstall':
                    return this.t('services.uninstall');
                case 'check':
                    return this.t('services.check');
                default:
                    return action;
            }
        },

        integrationBackupMeta(integration) {
            return integration && integration.backup_available
                ? this.t('integrations.backupAvailable')
                : this.t('integrations.noBackupMeta');
        },

        integrationCurrentPreviewEmptyLabel() {
            return this.t('integrations.fileDoesNotExistYet');
        },

        statusSystemInfoLabel() {
            return this.t('statusPage.systemInfo');
        },

        statusModeLabel(mode) {
            return this.modeLabel(mode || 'auto');
        },

        statusMetricProviders(count) {
            return this.tf('statusPage.providersCount', { count: Number(count || 0) });
        },

        statusMetricEnabled(count) {
            return this.tf('statusPage.enabledCount', { count: Number(count || 0) });
        },

        statusCircuitBreakerSummary() {
            return this.tf('statusPage.circuitBreakerSummary', {
                failure: this.globalConfig.circuit_breaker.failure_threshold,
                success: this.globalConfig.circuit_breaker.success_threshold,
                timeout: this.globalConfig.circuit_breaker.open_timeout
            });
        },

        get hasEnabledProviders() {
            return (this.providers || []).some(p => !!p.enabled);
        },

        modeHelpText() {
            if ((this.clientConfig.mode || '') === 'manual') {
                return this.t('providers.modeHelpManual');
            }
            return this.t('providers.modeHelpAuto');
        },

        providerStatusLabel(p) {
            if (!p) return '';
            const name = String(p.name || '').trim();
            if (!name) return '';
            return name;
        },

        providerStatusTitle(p) {
            const name = String((p && p.name) || '').trim();
            if (!name) return '';

            const label = String((p && p.label) || '').trim();
            const base = label || name;
            if (!base) return '';

            const detail = String((p && p.detail) || '').trim();
            let title = detail ? `${base}\n${detail}` : base;

            const available = Number((p && p.available_key_count) || 0);
            const total = Number((p && p.key_count) || 0);
            if (total > 0) {
                title = `${title}\n${this.tf('statusPage.keysAvailable', { available, total })}`;
            }

            const skip = String((p && p.skip_reason) || '').trim();
            if (skip !== 'deactivated') return title;

            const msg = String((p && p.deactivated_message) || '').trim();
            if (!msg) return title;

            const max = 300;
            const clipped = msg.length > max ? (msg.slice(0, max) + '...') : msg;
            title = `${title}\n${clipped}`;
            return title;
        },

        initSortable() {
            const el = document.getElementById('providers-list');
            if (!el || typeof Sortable === 'undefined') return;

            if (this.sortableInstance) {
                this.sortableInstance.destroy();
            }

            this.sortableInstance = Sortable.create(el, {
                animation: 250,
                // Make the entire card draggable
                handle: '.provider-card',
                // Filter out buttons and inputs so they remain clickable
                filter: '.btn, button, input, .pill, .provider-card__actions',
                preventOnFilter: false,
                ghostClass: 'sortable-ghost',
                chosenClass: 'sortable-chosen',
                dragClass: 'sortable-drag',
                forceFallback: false,
                onEnd: async (evt) => {
                    if (evt.oldIndex === evt.newIndex) return;

                    const previousProviders = (this.providers || []).map(provider => ({ ...provider }));
                    const previousOrder = previousProviders.map(provider => provider.name);

                    // Sortable has already mutated the DOM, but Alpine still thinks the
                    // previous keyed order is intact. Restore the old DOM order first so
                    // Alpine can apply the reordered state without scrambling cards.
                    this.syncSortableDomOrder(previousOrder);
                    this.applyLocalProviderReorder(evt.oldIndex, evt.newIndex);
                    await this.afterProviderRender();
                    const names = (this.providers || []).map(provider => provider.name);

                    try {
                        await this.apiCall(`/api/providers/${this.selectedClient}/_reorder`, {
                            method: 'PUT',
                            body: JSON.stringify({ providers: names })
                        }, true);
                    } catch (error) {
                        console.error('Failed to reorder providers:', error);
                        this.providers = previousProviders;
                        await this.afterProviderRender();
                    }
                }
            });
        },

        syncSortableDomOrder(order) {
            if (!this.sortableInstance || typeof this.sortableInstance.sort !== 'function') {
                return;
            }
            this.sortableInstance.sort(Array.isArray(order) ? order : [], false);
        },

        afterProviderRender() {
            return new Promise(resolve => {
                this.$nextTick(() => {
                    this.syncSortableDomOrder((this.providers || []).map(provider => provider.name));
                    resolve();
                });
            });
        },

        applyLocalProviderReorder(oldIndex, newIndex) {
            const providers = Array.isArray(this.providers)
                ? this.providers.map(provider => ({ ...provider }))
                : [];
            if (oldIndex < 0 || newIndex < 0 || oldIndex >= providers.length || newIndex >= providers.length) {
                return;
            }

            const [moved] = providers.splice(oldIndex, 1);
            if (!moved) {
                return;
            }
            providers.splice(newIndex, 0, moved);

            this.providers = providers.map((provider, index) => ({
                ...provider,
                priority: index + 1
            }));
        },

        async loadProviders(background = false) {
            try {
                const [providers, clientCfg] = await Promise.all([
                    this.apiCall(`/api/providers/${this.selectedClient}`, {}, background),
                    this.apiCall(`/api/client-config/${this.selectedClient}`, {}, true)
                ]);
                this.providers = providers || [];
                this.clientConfig = {
                    ...this.clientConfig,
                    ...(clientCfg || {}),
                    override_support: {
                        ...this.defaultProviderOverrideSupport(),
                        ...((clientCfg && clientCfg.override_support) ? clientCfg.override_support : {})
                    }
                };
            } catch (error) {
                console.error('Failed to load providers:', error);
                this.providers = [];
            }
        },

        async selectClient(clientType) {
            if (this.selectedClient === clientType) {
                return;
            }
            this.selectedClient = clientType;
            await Promise.all([
                this.loadProviders(),
                this.loadOAuthProviders(true)
            ]);
            this.$nextTick(() => {
                this.initSortable();
            });
        },

        async saveClientConfig(successToast = null) {
            try {
                await this.apiCall(`/api/client-config/${this.selectedClient}`, {
                    method: 'PUT',
                    body: JSON.stringify(this.clientConfig)
                });
                if (successToast && (successToast.title || successToast.message)) {
                    this.showAlert('success', successToast.message || '', successToast.title || '');
                }
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to save client config:', error);
            }
        },

        async setClientMode(mode) {
            const m = String(mode || '').toLowerCase();
            if (m !== 'auto' && m !== 'manual') return;

            if (m === 'manual' && !this.hasEnabledProviders) {
                this.showAlert('error', this.t('providers.enableBeforeManual'));
                return;
            }

            this.clientConfig.mode = m;
            if (m === 'auto') {
                this.clientConfig.pinned_provider = '';
            } else {
                // Default pin: prefer current provider, else highest priority enabled provider.
                const pinned = String(this.clientConfig.pinned_provider || '').trim();
                const pinnedProvider = pinned ? (this.providers || []).find(p => p && p.name === pinned) : null;
                const pinnedOk = pinnedProvider && !!pinnedProvider.enabled;

                if (!pinnedOk) {
                    // Best-effort refresh so "current provider" is as accurate as possible.
                    try {
                        await this.refreshStatus();
                    } catch (e) {
                        // Ignore refresh failures; we fall back to local provider list.
                    }

                    const st = (this.status && this.status.clients) ? this.status.clients[this.selectedClient] : null;
                    const cur = st ? String(st.current_provider || '').trim() : '';
                    const curProvider = cur ? (this.providers || []).find(p => p && p.name === cur) : null;

                    if (curProvider && curProvider.name && !!curProvider.enabled) {
                        this.clientConfig.pinned_provider = curProvider.name;
                    } else {
                        const enabled = (this.providers || []).filter(p => p && p.name && !!p.enabled);
                        enabled.sort((a, b) => Number(a.priority || 0) - Number(b.priority || 0));
                        this.clientConfig.pinned_provider = (enabled[0] && enabled[0].name) ? enabled[0].name : '';
                    }
                }
            }
            const client = this.providerToastClientLabel();
            const pinned = String(this.clientConfig.pinned_provider || '').trim();
            if (m === 'auto') {
                await this.saveClientConfig({
                    title: this.tf('providers.switchedToAutoTitle', { client }),
                    message: this.t('providers.switchedToAutoMessage')
                });
                return;
            }
            await this.saveClientConfig({
                title: this.tf('providers.switchedToManualTitle', { client }),
                message: pinned
                    ? this.tf('providers.switchedToManualMessagePinned', { provider: pinned })
                    : this.t('providers.switchedToManualMessage')
            });
        },

        async pinProvider(name) {
            const v = String(name || '').trim();
            if (!v) return;

            const p = (this.providers || []).find(x => x && x.name === v);
            if (p && p.enabled === false) {
                this.showAlert('error', this.t('providers.enableBeforePinning'));
                return;
            }

            this.clientConfig.mode = 'manual';
            this.clientConfig.pinned_provider = v;
            const client = this.providerToastClientLabel();
            await this.saveClientConfig({
                title: this.tf('providers.pinnedTitle', { client, provider: v }),
                message: this.t('providers.pinnedMessage')
            });
        },

        async toggleProvider(provider, event) {
            const oldEnabled = provider.enabled;
            const newEnabled = !!event.target.checked;
            provider.enabled = newEnabled;

            try {
                await this.apiCall(
                    `/api/providers/${this.selectedClient}/${encodeURIComponent(provider.name)}`,
                    {
                        method: 'PUT',
                        body: JSON.stringify({ enabled: newEnabled })
                    },
                    true // Background op for toggles to feel instant
                );
                const client = this.providerToastClientLabel();
                if (newEnabled) {
                    this.showAlert(
                        'success',
                        this.t('providers.enabledMessage'),
                        this.tf('providers.enabledTitle', { provider: provider.name, client })
                    );
                } else {
                    this.showAlert(
                        'success',
                        this.t('providers.disabledMessage'),
                        this.tf('providers.disabledTitle', { provider: provider.name, client })
                    );
                }
                await this.refreshStatus();
            } catch (error) {
                provider.enabled = oldEnabled;
                event.target.checked = oldEnabled;
                console.error('Failed to toggle provider:', error);
            }
        },

        async saveProvider() {
            try {
                if (!this.showEditProviderModal && this.providerFormUsesOAuth()) {
                    await this.startOAuthProviderAuthorization();
                    return;
                }

                const payload = {
                    proxy_mode: this.normalizeProviderProxyMode(this.providerForm.proxy_mode),
                    priority: this.providerForm.priority,
                    enabled: this.providerForm.enabled
                };
                if (this.providerFormUsesOAuth()) {
                    payload.auth_type = 'oauth';
                    payload.oauth_provider = this.providerForm.oauth_provider;
                    payload.oauth_ref = this.providerForm.oauth_ref;
                    if (this.providerForm.name) {
                        payload.name = this.providerForm.name;
                    }
                } else {
                    payload.name = this.providerForm.name;
                    payload.base_url = this.providerForm.base_url;
                }
                if (payload.proxy_mode === 'custom') {
                    const proxyURL = String(this.providerForm.proxy_url || '').trim();
                    if (proxyURL) {
                        payload.proxy_url = proxyURL;
                    }
                }
                const overrides = {};
                if (this.providerSupportsModelOverride()) {
                    overrides.model = String(this.providerForm.model || '');
                }
                if (this.providerSupportsReasoningEffort()) {
                    overrides.openai = {
                        reasoning_effort: String(this.providerForm.reasoning_effort || '')
                    };
                }
                if (this.providerSupportsThinkingBudget()) {
                    overrides.claude = {
                        thinking_budget_tokens: this.normalizeThinkingBudgetTokens(this.providerForm.thinking_budget_tokens)
                    };
                }
                if (Object.keys(overrides).length > 0) {
                    payload.overrides = overrides;
                }
                if (!this.providerFormUsesOAuth()) {
                    const keys = String(this.providerForm.api_keys_text || '')
                        .split('\n')
                        .map(v => v.trim())
                        .filter(Boolean);
                    if (keys.length === 1) {
                        payload.api_key = keys[0];
                    } else if (keys.length > 1) {
                        payload.api_keys = keys;
                    }
                }
                if (this.showEditProviderModal) {
                    // Update existing provider
                    await this.apiCall(
                        `/api/providers/${this.selectedClient}/${encodeURIComponent(this.editingProviderName)}`,
                        {
                            method: 'PUT',
                            body: JSON.stringify(payload)
                        }
                    );
                    this.showAlert(
                        'success',
                        this.tf('providers.updatedMessage', { client: this.providerToastClientLabel() }),
                        this.tf('providers.updatedTitle', { provider: payload.name })
                    );
                } else {
                    // Add new provider
                    await this.apiCall(
                        `/api/providers/${this.selectedClient}`,
                        {
                            method: 'POST',
                            body: JSON.stringify(payload)
                        }
                    );
                    this.showAlert(
                        'success',
                        this.tf('providers.addedMessage', { client: this.providerToastClientLabel() }),
                        this.tf('providers.addedTitle', { provider: payload.name })
                    );
                }

                this.closeModals();
                await this.loadProviders();
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to save provider:', error);
            }
        },

        async startOAuthProviderAuthorization() {
            const provider = String(this.providerForm.oauth_provider || '').trim().toLowerCase();
            if (!provider) {
                throw new Error(this.t('providers.oauthUnavailable'));
            }
            const proxyMode = this.normalizeProviderProxyMode(this.providerForm.proxy_mode);
            const payload = {
                client_type: this.selectedClient,
                provider,
                proxy_mode: proxyMode
            };
            if (proxyMode === 'custom') {
                const proxyURL = String(this.providerForm.proxy_url || '').trim();
                if (proxyURL) {
                    payload.proxy_url = proxyURL;
                }
            }
            this.cancelOAuthAuthorization(true);
            this.showOAuthAuthorizationModal({
                provider,
                client_type: this.selectedClient
            });
            this.applyOAuthAuthorizationState({
                provider,
                client_type: this.selectedClient,
                started_at: Date.now()
            }, {
                phase: 'opening',
                popup_blocked: false,
                error: ''
            });

            let started;
            try {
                started = await this.apiCall('/api/oauth/providers/start', {
                    method: 'POST',
                    body: JSON.stringify(payload)
                });
            } catch (error) {
                this.closeOAuthAuthorizationPopup();
                this.applyOAuthAuthorizationState({
                    provider,
                    client_type: this.selectedClient,
                    started_at: Date.now()
                }, {
                    phase: 'error',
                    popup_blocked: false,
                    error: String((error && error.message) || '').trim() || this.t('providers.oauthFailedMessage')
                });
                throw error;
            }

            const pending = this.savePendingOAuthSession({
                session_id: started.session_id,
                provider,
                client_type: this.selectedClient,
                started_at: Date.now(),
                expires_at: started.expires_at,
                auth_url: started.auth_url
            });

            const popupOpened = !!this.openOAuthAuthorizationPopup(started.auth_url);
            this.showAlert(
                'info',
                this.t('providers.oauthAuthorizingMessage'),
                this.tf('providers.oauthAuthorizingTitle', { provider: this.oauthProviderLabel(provider) })
            );

            const nextPhase = popupOpened ? 'waiting' : 'blocked';
            this.applyOAuthAuthorizationState(pending || {
                session_id: started.session_id,
                provider,
                client_type: this.selectedClient,
                expires_at: started.expires_at,
                auth_url: started.auth_url
            }, {
                phase: nextPhase,
                popup_blocked: !popupOpened,
                error: ''
            });

            this.resumePendingOAuthSession(pending || {
                session_id: started.session_id,
                provider,
                client_type: this.selectedClient,
                expires_at: started.expires_at,
                auth_url: started.auth_url
            }, {
                showError: false,
                phase: nextPhase
            }).catch(error => {
                if (error && error.message !== oauthAuthorizationCancelledError) {
                    console.error('Failed to resume pending OAuth session:', error);
                }
            });

            return pending;
        },

        triggerOAuthImportPicker() {
            const input = this.$refs && this.$refs.oauthImportInput;
            if (!input || typeof input.click !== 'function') {
                this.showAlert('error', this.t('providers.oauthImportBrowserUnsupported'));
                return;
            }
            input.value = '';
            input.click();
        },

        async handleOAuthImportSelection(event) {
            const input = event && event.target ? event.target : null;
            const files = Array.from((input && input.files) || []);
            try {
                await this.importCLIProxyAPIFiles(files);
            } finally {
                if (input) {
                    input.value = '';
                }
            }
        },

        async importCLIProxyAPIFiles(files) {
            const provider = String(this.providerForm.oauth_provider || '').trim().toLowerCase();
            if (!provider) {
                throw new Error(this.t('providers.oauthUnavailable'));
            }
            if (!Array.isArray(files) || files.length === 0) {
                return null;
            }
            if (typeof FormData === 'undefined') {
                throw new Error(this.t('providers.oauthImportBrowserUnsupported'));
            }

            const formData = new FormData();
            formData.append('client_type', this.selectedClient);
            formData.append('provider', provider);
            for (const file of files) {
                if (!file) {
                    continue;
                }
                const filename = String(file.name || 'credential.json').trim() || 'credential.json';
                formData.append('files', file, filename);
            }

            const result = await this.apiCall('/api/oauth/import/cli-proxy-api', {
                method: 'POST',
                body: formData
            });
            const imported = Number(result.imported_count || 0);
            const linked = Number(result.linked_count || 0);
            const skipped = Number(result.skipped_count || 0);
            const failed = Number(result.failed_count || 0);
            let alertType = 'success';
            let titleKey = 'providers.oauthImportCompletedTitle';
            if (imported === 0 && failed > 0) {
                alertType = 'error';
                titleKey = 'providers.oauthImportFailedTitle';
            } else if (failed > 0 || skipped > 0) {
                alertType = 'info';
            }
            const message = this.oauthImportAlertMessage(result);
            this.showAlert(alertType, message, this.t(titleKey));

            if (imported > 0) {
                this.closeModals();
                await this.loadProviders();
                await this.refreshStatus();
            }
            return result;
        },

        async pollOAuthSession(sessionId, options = {}) {
            const id = String(sessionId || '').trim();
            if (!id) {
                throw new Error('Missing OAuth session ID');
            }
            const token = Number(options.token || 0);
            const pending = options.pending && typeof options.pending === 'object' ? options.pending : null;
            const expiresAt = this.parseTimestamp(pending && pending.expires_at);
            const deadline = expiresAt
                ? expiresAt.getTime()
                : Date.now() + 5 * 60 * 1000;
            while (Date.now() < deadline) {
                if (token && token !== this.oauthPollingToken) {
                    throw new Error(oauthAuthorizationCancelledError);
                }
                const session = await this.apiCall(`/api/oauth/sessions/${encodeURIComponent(id)}`, {}, true, true);
                if (token && token !== this.oauthPollingToken) {
                    throw new Error(oauthAuthorizationCancelledError);
                }
                const status = String((session && session.status) || '').trim().toLowerCase();
                if (status === 'completed' || status === 'error' || status === 'expired') {
                    return session;
                }
                if (token && token !== this.oauthPollingToken) {
                    throw new Error(oauthAuthorizationCancelledError);
                }
                await new Promise(resolve => setTimeout(resolve, 1000));
            }
            throw new Error(this.t('providers.oauthTimedOutMessage'));
        },

        editProvider(provider) {
            if (this.providerUsesOAuth(provider)) {
                this.showAlert('error', this.t('providers.oauthEditLocked'));
                return;
            }
            this.providerForm = {
                auth_type: String(provider.auth_type || 'api_key').trim().toLowerCase() || 'api_key',
                oauth_provider: String(provider.oauth_provider || 'codex').trim().toLowerCase() || 'codex',
                oauth_ref: String(provider.oauth_ref || ''),
                name: provider.name,
                base_url: provider.base_url,
                proxy_mode: this.normalizeProviderProxyMode(provider.proxy_mode),
                proxy_url: '',
                proxy_url_hint: String(provider.proxy_url_hint || ''),
                model: String((provider.overrides && provider.overrides.model) || ''),
                reasoning_effort: String((provider.overrides && provider.overrides.openai && provider.overrides.openai.reasoning_effort) || ''),
                thinking_budget_tokens: Number((provider.overrides && provider.overrides.claude && provider.overrides.claude.thinking_budget_tokens) || 0),
                api_keys_text: '',
                priority: provider.priority,
                enabled: !!provider.enabled
            };
            this.editingProviderName = provider.name;
            this.editingProviderKeyCount = Number(provider.key_count || 0);
            this.showEditProviderModal = true;
        },

        async deleteProvider(provider) {
            const item = provider || {};
            const name = String(item.name || '').trim();
            if (!name) {
                return;
            }
            const displayName = this.providerDisplayName(item) || name;
            if (!confirm(this.tf('providers.deleteConfirm', { name: displayName }))) {
                return;
            }

            try {
                await this.apiCall(
                    `/api/providers/${this.selectedClient}/${encodeURIComponent(name)}`,
                    { method: 'DELETE' }
                );
                this.showAlert(
                    'success',
                    this.tf('providers.deletedMessage', { client: this.providerToastClientLabel() }),
                    this.tf('providers.deletedTitle', { name: displayName })
                );
                await this.loadProviders();
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to delete provider:', error);
            }
        },

        openAddProviderModal() {
            const maxPriority = this.providers.reduce((max, p) => {
                const pr = typeof p.priority === 'number' ? p.priority : 0;
                return pr > max ? pr : max;
            }, 0);
            this.cancelOAuthAuthorization(true);
            this.providerForm = {
                auth_type: 'api_key',
                oauth_provider: this.defaultOAuthProviderValue(),
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
                priority: maxPriority + 1,
                enabled: true
            };
            this.editingProviderName = '';
            this.editingProviderKeyCount = 0;
            this.showAddProviderModal = true;
        },

        // Global Config
        async loadGlobalConfig() {
            try {
                const cfg = await this.apiCall('/api/config/global');
                this.globalConfig = this.withDefaultGlobalConfig(cfg);
            } catch (error) {
                console.error('Failed to load global config:', error);
            }
        },

        async saveGlobalConfig() {
            try {
                const payload = {
                    ...this.globalConfig,
                    upstream_proxy_mode: this.normalizeGlobalProxyMode(this.globalConfig.upstream_proxy_mode)
                };
                if (payload.upstream_proxy_mode !== 'custom') {
                    payload.upstream_proxy_url = '';
                }
                await this.apiCall('/api/config/global/update', {
                    method: 'PUT',
                    body: JSON.stringify(payload)
                });
                this.showAlert('success', this.t('settings.saveSuccess'));
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to save global config:', error);
            }
        },

        async exportConfig() {
            try {
                const response = await fetch('/api/config/export');
                const blob = await response.blob();
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = 'clipal-config.json';
                document.body.appendChild(a);
                a.click();
                window.URL.revokeObjectURL(url);
                document.body.removeChild(a);
                this.showAlert('success', this.t('settings.exportSuccess'));
            } catch (error) {
                this.showAlert('error', this.t('settings.exportFailure'));
                console.error('Failed to export config:', error);
            }
        },

        defaultToastTitle(type) {
            switch (String(type || '').trim()) {
                case 'success':
                    return this.t('toast.success');
                case 'error':
                    return this.t('toast.requestFailed');
                default:
                    return this.t('toast.notice');
            }
        },

        toastTypeLabel(type) {
            switch (String(type || '').trim()) {
                case 'success':
                    return this.t('toast.success');
                case 'error':
                    return this.t('toast.error');
                default:
                    return this.t('toast.info');
            }
        },

        clearToastTimer(id) {
            const key = String(id || '').trim();
            if (!key || !this.toastTimeouts[key]) {
                return;
            }
            clearTimeout(this.toastTimeouts[key]);
            delete this.toastTimeouts[key];
        },

        dismissToast(id) {
            const key = String(id || '').trim();
            if (!key) {
                return;
            }
            this.clearToastTimer(key);
            this.toasts = this.toasts.filter(toast => toast.id !== key);
        },

        pauseToast(id) {
            const key = String(id || '').trim();
            const toast = this.toasts.find(item => item.id === key);
            if (!toast) {
                return;
            }
            if (typeof toast.dismissAt === 'number' && toast.dismissAt > 0) {
                toast.remaining = Math.max(0, toast.dismissAt - Date.now());
            }
            this.clearToastTimer(key);
        },

        resumeToast(id) {
            const key = String(id || '').trim();
            const toast = this.toasts.find(item => item.id === key);
            if (!toast) {
                return;
            }
            const delay = Math.max(1, Number(toast.remaining) || 0);
            toast.dismissAt = Date.now() + delay;
            this.clearToastTimer(key);
            this.toastTimeouts[key] = setTimeout(() => {
                this.dismissToast(key);
            }, delay);
        },

        showAlert(type, message, title = '') {
            const body = String(message || '').trim();
            if (!body) {
                return;
            }

            const kind = ['success', 'error', 'info'].includes(type) ? type : 'info';
            const id = `toast-${Date.now()}-${++this.toastCounter}`;
            const toast = {
                id,
                type: kind,
                title: String(title || '').trim() || this.defaultToastTitle(kind),
                message: body,
                remaining: kind === 'error' ? 7000 : 5200,
                dismissAt: 0
            };

            const nextToasts = [...this.toasts, toast];
            while (nextToasts.length > 3) {
                const removed = nextToasts.shift();
                if (removed && removed.id) {
                    this.clearToastTimer(removed.id);
                }
            }

            this.toasts = nextToasts;
            this.resumeToast(id);
        },

        closeModals() {
            this.cancelOAuthAuthorization(false);
            this.showAddProviderModal = false;
            this.showEditProviderModal = false;
            this.providerForm = {
                auth_type: 'api_key',
                oauth_provider: this.defaultOAuthProviderValue(),
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
            this.editingProviderName = '';
            this.editingProviderKeyCount = 0;
        }
    };
}
