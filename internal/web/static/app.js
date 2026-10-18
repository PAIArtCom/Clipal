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

// Keep floating tooltips inside the viewport and outside scroll clipping boundaries.
// Alpine owns each teleported element and removes its listeners on destroy.
function floatingTooltip() {
    let onViewportChange;
    return {
        open: false,
        position: { left: '0px', top: '0px' },
        init() {
            onViewportChange = () => { if (this.open) this.place(); };
            window.addEventListener('scroll', onViewportChange, true);
            window.addEventListener('resize', onViewportChange);
        },
        destroy() {
            window.removeEventListener('scroll', onViewportChange, true);
            window.removeEventListener('resize', onViewportChange);
        },
        show() {
            if (!this.$el.classList.contains('tooltip')) return;
            this.open = true;
            this.$nextTick(() => this.place());
        },
        leave() {
            if (!this.$el.contains(document.activeElement)) this.open = false;
        },
        blur() {
            this.$nextTick(() => {
                if (!this.$el.matches(':hover') && !this.$el.contains(document.activeElement)) this.open = false;
            });
        },
        place() {
            const tip = document.getElementById(this.$id('metric-tooltip'));
            if (!tip || !this.open) return;
            const anchor = this.$el.getBoundingClientRect();
            const body = this.$el.closest('.provider-inspector__body');
            const clip = body?.getBoundingClientRect();
            const toolbarBottom = document.querySelector('.toolbar')?.getBoundingClientRect().bottom || 0;
            if (anchor.bottom <= Math.max(toolbarBottom, clip?.top || 0) ||
                anchor.top >= Math.min(window.innerHeight, clip?.bottom ?? window.innerHeight)) {
                this.open = false;
                return;
            }
            const gap = 8;
            const bounds = tip.getBoundingClientRect();
            const left = this.$el.classList.contains('tooltip--end') ? anchor.right - bounds.width : anchor.left;
            const below = anchor.bottom + gap;
            const top = below + bounds.height <= window.innerHeight - gap ? below : anchor.top - bounds.height - gap;
            this.position = {
                left: `${Math.max(gap, Math.min(left, window.innerWidth - bounds.width - gap))}px`,
                top: `${Math.max(gap, top)}px`
            };
        }
    };
}

function app() {
    let oauthAuthorizationPopup = null;
    const countryOptionsByLocale = new Map();

    return {
        // State
        isLoading: false,
        theme: localStorage.getItem('theme') || 'system',
        locale: document.documentElement.lang === 'zh-CN' ? 'zh-CN' : 'en',
        supportedLocales: ['en', 'zh-CN'],
        activeTab: 'providers',
        tabNames: ['providers', 'integrations', 'settings', 'data', 'services', 'status'],
        savedGlobalConfig: null,
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
        integrationPreviewOpen: {},
        environmentVisible: false,
        environmentBusy: false,
        environmentSaving: false,
        environmentLoadError: '',
        environmentProvider: null,
        environmentCheckDoH: false,
        environmentPath: '',
        environmentAccount: '',
        environmentIdentity: '',
        environmentForm: { country: 'US', timezone: 'America/New_York' },
        environmentUpstream: {},
        environmentProfile: {},
        environmentDiagnostics: null,
        environmentGeneration: 0,
        serviceBusyAction: '',
        serviceActionRequestPending: false,
        messages: {
            en: {
                forms: {"unit": "Unit for {label}", "nonNegative": "Enter a non-negative number.", "wholeBytes": "Choose a size that is at least one whole byte.", "positiveDuration": "Enter a duration greater than zero.", "preserved": "Saved value: {value}. Enter a number to replace it.", "inherit": "Use provider default", "defaultValue": "Use default", "minimumSize": "The minimum request-body limit is 64 MiB.", "minimumTimeout": "Use zero to disable, or choose at least 5 minutes."},
                units: {"ns": "Nanoseconds", "us": "Microseconds", "µs": "Microseconds", "ms": "Milliseconds", "s": "Seconds", "m": "Minutes", "h": "Hours", "B": "Bytes", "KiB": "KiB", "MiB": "MiB", "GiB": "GiB"},
                feedback: {
                    unsavedTitle: 'Discard unsaved changes?',
                    unsaved: 'This form has unsaved changes. Discard them?',
                    failed: 'Cannot refresh status. The connection may be interrupted; displayed data may be out of date.',
                    stale: 'Status is out of date. Refresh to check the current state.',
                    never: 'No successful status update yet.',
                    updated: 'Last successful update: {time}',
                    runtime: 'Gateway status', service: 'Service status', retry: 'Retry'
                },
                requestEnv: {
                    "title": "OAuth request environment",
                    "open": "Account environment",
                    "country": "Request country / region",
                    "source": "Configuration source",
                    "pendingSource": "Unverified legacy values — confirmation required",
                    "sources": { "default": "Default request configuration", "user": "User configuration", "unknown": "Not configured" },
                    "confirmHint": "Legacy values may have been inferred from an exit. Review or change them, then save to confirm. Unconfirmed regions are not applied to requests.",
                    "save": "Save / confirm account preferences",
                    "saved": "Account preferences saved.",
                    "loadFailed": "Could not load account preferences.",
                    "upstream": "Credential region attributes (separate record)",
                    "reference": "Reference service only — target exit unverified",
                    "language": "Request language",
                    "locale": "Locale",
                    "timezone": "Request timezone (IANA)",
                    "dns": "DNS resolution",
                    "automatic": "Automatic: use proxy resolution when configured; otherwise try DoH, then the system hostname route on connection failure",
                    "resolverChoice": "No explicit proxy: {resolver} DoH; otherwise proxy resolution",
                    "checkDoH": "Include public DoH reference for proxy routes",
                    "checkDoHHint": "Optional when using a proxy. Failure does not block requests and results cannot verify the proxy's DNS or target exit.",
                    "waiting": "Not configured",
                    "direct": "No explicit proxy (system / TUN may apply)",
                    "systemDNS": "System / proxy resolver",
                    "device": "Stable account device ID",
                    "fallbackIdentity": "Stable account identity (account ID unavailable)",
                    "hint": "New accounts default to US / America/New_York; these request preferences do not prove registration or billing country. IANA timezones follow daylight saving automatically. Providers linked to the same account share saved settings. Imports preserve existing values. No system or Desktop changes.",
                    "diagnose": "Check consistency",
                    "checks": {
                        "account_identity": "Account / device identity",
                        "identity_source": "Identity source",
                        "region": "Stored request region",
                        "dns": "DNS route",
                        "doh_reference": "Optional public DoH reference",
                        "target_dns": "Final target DNS",
                        "network_exit": "Network exit",
                        "connection": "Target connection",
                        "reference_exit": "Reference observation",
                        "upstream_auth": "Upstream authentication"
                    },
                    "statuses": {
                        "ok": "Verified",
                        "pending": "Not verified",
                        "mismatch": "Mismatch"
                    }
                },
                meta: {
                    title: 'Clipal Management'
                },
                header: {
                    subtitle: 'Local ingress for Claude, OpenAI, and Google traffic',
                },
                nav: {
                    sectionsLabel: 'Sections',
                    providers: 'Providers',
                    integrations: 'CLI Takeover',
                    settings: 'Global Settings',
                    data: 'Backup & Import',
                    services: 'Services',
                    status: 'System Status'
                },
                setup: {
                    title: 'Server Setup Checklist',
                    service: 'Clipal service',
                    provider: 'First provider',
                    integrations: 'CLI takeover',
                    done: 'Ready',
                    todo: 'Needs attention',
                    serviceHint: 'Open Services',
                    providerHint: 'Add a provider',
                    integrationHint: 'Open CLI Takeover'
                },
                common: {
                    close: 'Close',
                    back: 'Back',
                    copy: 'Copy',
                    copied: 'Copied',
                    none: 'None',
                    load: 'Load',
                    save: 'Save',
                    reset: 'Reset',
                    cancel: 'Cancel',
                    confirmTitle: 'Please confirm',
                    refresh: 'Refresh',
                    working: 'Working...',
                    saving: 'Saving...',
                    enabled: 'Enabled',
                    disabled: 'Disabled',
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
                    add: 'Add provider',
                    loadFailed: 'Could not load providers. The displayed list may be out of date.',
                    candidate: 'Current candidate',
                    spend: 'Spend',
                    today: 'today',
                    week: '{amount} in 7 days',
                    tokensInOut: '{input} in · {output} out',
                    refreshed: 'Refreshed {time}',
                    keys: 'API keys',
                    providerId: 'ID',
                    search: 'Search by name or URL',
                    noMatch: 'No providers match this search.',
                    reorderLocked: 'Clear the search to reorder',
                    details: 'Details',
                    emptyNext: 'After adding one, connect your tools in',
                    runtimeStandby: 'Standby',
                    unknownCandidate: 'Not yet known',
                    runtimeCurrent: 'In use',
                    runtimeCoolingDown: 'Cooling down',
                    runtimeUnavailable: 'Unavailable',
                    runtimeRecoveryProbe: 'Probing recovery',
                    modeLabel: 'Mode',
                    modeAuto: 'Auto',
                    modeManual: 'Manual',
                    enableProviderFirst: 'Enable a provider first',
                    empty: 'No providers configured for {client}',
                    baseUrl: 'Base URL',
                    proxy: 'Proxy',
                    oauthAuth: 'Auth',
                    usageTotal: 'Usage',
                    usageReasoning: 'Reasoning',
                    usageThoughts: 'Thoughts',
                    usageIncluded: 'incl. {label} {tokens}',
                    usageTotalFormula: '{total} = {input} + {output} + {extra}',
                    usageTotalFormulaSimple: '{total} = {input} + {output}',
                    usageInOutFormula: '{input} / {output}',
                    usageBreakdownJoiner: ', ',
                    spendToday: 'Spend Today',
                    spendWeek: 'Spend 7d',
                    planAndLimits: 'Plan & Limits',
                    quotaUnavailable: 'The upstream returned no quota data for this account.',
                    rateLimit: 'Limit',
                    rateLimitUsed: '{percent}% used',
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
                    pinTitle: 'Pin (Manual)',
                    dragToReorder: 'Drag to reorder; Alt+↑/↓ to move',
                    edit: 'Edit',
                    delete: 'Delete',
                    disable: 'Disable',
                    enable: 'Enable',
                    pinnedProvider: 'Pinned provider',
                    pinnedProviderCannotDisable: 'Pinned provider cannot be disabled in manual mode',
                    enablePinnedProvider: 'Enable pinned provider',
                    modeHelpManual: 'Always uses the pinned provider. No failover.',
                    modeHelpAuto: 'Fails over between enabled providers by priority.',
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
                    deleteKeysNote: 'Its {count} configured API key(s) will be permanently removed.',
                    deletedTitle: 'Deleted provider {name}',
                    deletedMessage: 'It has been removed from {client}\'s provider list.',
                    clientTypeLabel: 'Client Type',
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
                    oauthUnavailable: 'OAuth is not available for this client yet.',
                    oauthImportCompletedTitle: 'Imported OAuth accounts',
                    oauthImportFailedTitle: 'OAuth import failed',
                    oauthImportSummary: 'Imported {imported} account(s), linked {linked} provider(s), skipped {skipped} entry(s), failed {failed} entry(s).',
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
                        oauthProviderAntigravity: 'Antigravity',
                        oauthDirectHint: 'After authorization, Clipal will automatically link the account to an existing provider or create one if needed.',
                        importFile: 'Import OAuth credential file',
                        importFileHint: 'Supported files: CLIProxyAPI single-account OAuth JSON, Codex CLI auth.json, or sub2api export JSON. Clipal imports only accounts matching the selected service.',
                        importFileHintCodex: 'Supported files: Codex CLI auth.json (~/.codex/auth.json), CLIProxyAPI single-account OAuth JSON, or sub2api export JSON. Clipal imports only Codex accounts from the selected files.',
                        importFileHintClaude: 'Supported files: CLIProxyAPI single-account Claude OAuth JSON or sub2api export JSON. Clipal imports only Claude accounts from the selected files.',

                        importFileHintAntigravity: 'Supported files: CLIProxyAPI single-account Antigravity OAuth JSON or sub2api export JSON. Clipal imports only Antigravity accounts from the selected files.',
                        name: 'Name *',
                        namePlaceholder: 'e.g. provider-primary',
                        nameHint: 'Start with a letter or number; use letters, numbers, dot (.), underscore (_) or hyphen (-), up to 64 characters.',
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
                        reasoningEffortHint: 'Choose a suggestion or enter a model-specific value. Leave empty to use the provider default.',
                        thinkingBudgetTokens: 'Thinking Budget',
                        thinkingBudgetTokensHint: 'Set 0 to clear the thinking budget override.',
                        apiKeys: 'API Keys',
                        apiKeysRequired: 'API Keys *',
                        onePerLine: 'One API key per line',
                        keepExistingKey: 'Leave empty to keep the current configured key.',
                        keepExistingKeys: 'Leave empty to keep the current {count} configured keys.',
                        overridesTitle: 'Overrides',
                        overridesHint: 'Leave empty if no override needed',
                        priority: 'Priority',
                        saveProvider: 'Save Provider',
                        authorizeProvider: 'Continue to Authorization'
                    }
                },
                settings: {
                    title: 'Global Settings',
                    loadFailed: 'Could not load global settings. Retry to load the saved configuration.',
                    subtitle: 'Runtime defaults, recovery policy, routing strategy, and local observability.',
                    runtimeTitle: 'Runtime',
                    runtimeCopy: 'Basic listener and request buffering defaults.',
                    listenAddress: 'Listen Address',
                    allowRemoteProxy: 'Allow Remote Proxy',
                    allowRemoteWebUI: 'Allow Remote Web UI',
                    remoteProxyHint: 'Lets other machines send model requests through this Clipal. Requests are not authenticated; protect the port with a firewall, VPN, or reverse proxy.',
                    remoteWebUIHint: 'Exposes this management UI, including provider API keys and OAuth tokens, to other machines. There is no built-in authentication.',
                    remoteWebUIWarning: 'Anyone who can reach this port can read and change your configuration. Only enable it behind a firewall, VPN, or authenticating reverse proxy.',
                    remoteWebUIConfirmTitle: 'Expose the Web UI remotely?',
                    remoteWebUIConfirm: 'The management UI has no authentication. Anyone who can reach this port can read your API keys and OAuth tokens.',
                    remoteWebUIConfirmAction: 'Enable and save',
                    port: 'Port',
                    logLevel: 'Log Level',
                    maxBodySize: 'Max Body Size',
                    maxBodySizeHint: 'Buffer limit for retryable requests. Minimum: 64 MiB.',
                    reliabilityTitle: 'Reliability',
                    reliabilityCopy: 'Timeouts, temporary deactivation, and circuit breaker behavior.',
                    reactivateAfter: 'Reactivate After',
                    reactivateAfterHint: 'How long a failed provider stays deactivated.',
                    upstreamIdleTimeout: 'Upstream Idle Timeout',
                    upstreamIdleTimeoutHint: 'Set 0 to disable; otherwise use at least 5 minutes.',
                    responseHeaderTimeout: 'Response Header Timeout',
                    responseHeaderTimeoutHint: 'Set 0 to disable; otherwise use at least 5 minutes.',
                    upstreamProxyMode: 'Default Upstream Proxy',
                    upstreamProxyUrl: 'Default Proxy URL',
                    upstreamProxyHint: 'Used by providers whose proxy mode is Default.',
                    upstreamProxyUrlHelp: 'Supports http://, https://, socks5://, and socks5h:// proxy URLs.',
                    proxyModeEnvironment: 'Environment / System',
                    proxyModeDirect: 'Direct',
                    proxyModeCustom: 'Custom Proxy',
                    upstreamProxyUrlHint: 'http://127.0.0.1:7890',
                    failureThreshold: 'Failure Threshold',
                    failureThresholdHint: '0 disables the circuit breaker.',
                    successThreshold: 'Success Threshold',
                    openTimeout: 'Circuit Breaker Cooldown',
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
                    stickySessionTtlHint: 'Idle lifetime for explicit session bindings. Leave empty to use the default.',
                    shortRetryAfterMax: 'Short Retry-After Max',
                    shortRetryAfterMaxHint: 'Upper bound for honoring short upstream retry-after hints. Leave empty to use the default.',
                    maxInlineWait: 'Max Inline Wait',
                    maxInlineWaitHint: 'How long Clipal may wait before overflowing to another provider. Leave empty to use the default.',
                    enableStickySessions: 'Enable Sticky Sessions',
                    enableBusyBackpressure: 'Enable Busy Backpressure',
                    dataTitle: 'Backup & Import',
                    dataSubtitle: 'Export a full backup or import data. These actions run immediately and are separate from Global Settings.',
                    unsaved: 'Unsaved changes',
                    restartRequired: 'Changing this requires a restart.',
                    restartNotice: 'Listen address or port changed. Restart Clipal for it to take effect.',
                    unsavedTitle: 'Discard unsaved settings?',
                    unsavedMessage: 'You have unsaved changes in Global Settings. Leaving this page will discard them.',
                    discardChanges: 'Discard changes',
                    footerHint: 'Saving updates `config.yaml`. Some runtime changes may require restart to take full effect.',
                    saveSuccess: 'Configuration saved. Some changes may require restart.',
                    exportSaved: 'Backup saved as {filename}.',
                    exportDownloadStarted: 'Download started for {filename}. Check your browser downloads for its location.',
                    exportFileType: 'Clipal backup (JSON)',
                    exportFailure: 'Failed to export backup',
                    exportFullBackup: 'Export Full Backup',
                    exportBackupCopy: 'Downloads global and client configuration, provider API keys, OAuth access and refresh tokens, and usage data.',
                    exportSaveHint: 'Choose the file name and save location before the backup is created.',
                    importTitle: 'Import Data',
                    importCopy: 'Restore a Clipal backup or import supported external credential exports.',
                    importFiles: 'Import JSON Files',
                    chooseImportFiles: 'Choose JSON Files',
                    importFilesHint: 'No files selected',
                    importFilePickerUnavailable: 'File selection is unavailable in this browser',
                    importMode: 'Import Mode',
                    modeAuto: 'Automatic',
                    modeReplace: 'Replace',
                    modeMerge: 'Merge',
                    previewImport: 'Preview Import',
                    applyImport: 'Apply Import',
                    importPreviewTitle: 'Import Preview',
                    importPreviewFiles: 'Files',
                    importPreviewSource: 'Detected Source',
                    importPreviewEffects: 'Planned Effects',
                    importPreviewConfiguration: 'Configuration',
                    importPreviewProviders: 'Providers',
                    importPreviewCredentials: 'Credentials',
                    importPreviewUsage: 'Usage Data',
                    importPreviewWarnings: 'Warnings',
                    importConfigReplace: 'Configuration will be replaced',
                    importConfigMerge: 'Configuration will be merged',
                    importConfigCredentialsOnly: 'No configuration changes',
                    importChangeSummary: 'Added {added} · Updated {updated} · Removed {removed}',
                    importPreviewGuard: 'If the selected data or current state changes, preview again before applying.',
                    importApplied: 'Imported {credentials} credentials, {providers} providers, and {usage} usage records',
                    importFailure: 'Failed to import data'
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
                    stateConfigured: 'Configuration written',
                    stateNeedsAttention: 'Needs attention',
                    stateNotConfigured: 'Not configured',
                    alreadyUsing: 'Configuration already written',
                    noBackupYet: 'No backup yet. Apply once before restore becomes available.',
                    dismissResult: 'Dismiss result',
                    fullFiles: 'Full files',
                    targetOptions: 'Configuration location',
                    configDir: 'Client configuration directory (optional)',
                    codexProfile: 'Codex profile (optional)',
                    shellDir: 'Shell startup directory (optional)',
                    shellType: 'Shell (automatic when available)',
                    saveTarget: 'Save location',
                    targetSaved: 'Configuration location saved',
                    runtimeUnverified: 'Runtime routing has not been verified.',
                    runtimeBlocked: 'Routing conflict detected; inspect the warning.',
                    previewChanges: 'Preview changes',
                    changesSummary: 'Changes: +{added} / −{removed} lines',
                    changesNone: 'No changes to apply',
                    changesLarge: 'Changes (file too large for a line diff)',
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
                    resultUsingClipalTitle: 'Configuration written',
                    resultRestoredTitle: 'Restored from backup',
                    resultRestartMessage: 'Restart the client or open a new session to apply changes.',
                    resultApplyErrorTitle: 'Couldn’t update this client',
                    resultRestoreErrorTitle: 'Couldn’t restore this client',
                },
                services: {
                    actionFailed: 'The service action failed. Retry or check the service output.',
                    actionUnconfirmed: 'Connection interrupted; the service action is unconfirmed. Checking status…',
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
                    pinned: 'Pinned',
                    configDir: 'Config Dir',
                    circuitBreaker: 'Circuit Breaker',
                    disabled: 'Disabled',
                    circuitBreakerSummary: '{failure} fail / {success} succ ({timeout})',
                    activitySwitch: 'Provider switch',
                    activityRequest: 'Request',
                    recentActivity: 'Latest activity',
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
                forms: {"unit": "{label}的单位", "nonNegative": "请输入非负数。", "wholeBytes": "请选择至少为一个整数字节的大小。", "positiveDuration": "请输入大于零的时长。", "preserved": "已保存：{value}。输入数值后可替换。", "inherit": "使用上游默认值", "defaultValue": "使用默认值", "minimumSize": "请求体大小至少为 64 MiB。", "minimumTimeout": "设为零可关闭，否则请至少设置 5 分钟。"},
                units: {"ns": "纳秒", "us": "微秒", "µs": "微秒", "ms": "毫秒", "s": "秒", "m": "分钟", "h": "小时", "B": "字节", "KiB": "KiB", "MiB": "MiB", "GiB": "GiB"},
                feedback: {
                    unsavedTitle: '丢弃未保存的修改？',
                    unsaved: '此表单有未保存的修改，是否丢弃？',
                    failed: '无法刷新状态，连接可能已中断；当前显示的数据可能已过期。',
                    stale: '状态已过期，请刷新以核实当前状态。',
                    never: '尚未成功获取状态。',
                    updated: '上次成功更新：{time}',
                    runtime: '网关状态', service: '服务状态', retry: '重试'
                },
                requestEnv: {
                    "title": "OAuth 请求环境",
                    "saved": "账号配置已保存。",
                    "loadFailed": "无法加载账号配置。",
                    "open": "账号环境",
                    "country": "请求国家 / 地区",
                    "source": "配置来源",
                    "pendingSource": "旧值来源未核验，需要确认",
                    "sources": { "default": "默认请求配置", "user": "用户配置", "unknown": "尚未配置" },
                    "confirmHint": "旧值可能来自出口推断，请检查或修改后保存确认。未确认的地区不应用到请求。",
                    "save": "保存 / 确认账号请求配置",
                    "upstream": "凭据地区属性（单独记录）",
                    "reference": "仅为参考服务观测，目标出口未核验",
                    "language": "请求语言",
                    "locale": "地区格式",
                    "timezone": "请求时区（IANA）",
                    "dns": "DNS 解析",
                    "automatic": "自动：有代理时由代理解析；否则优先 DoH，解析或连接失败后回退系统域名通路",
                    "resolverChoice": "无显式代理时使用 {resolver} DoH；有代理时由代理解析",
                    "checkDoH": "代理下附加公共 DoH 参考诊断",
                    "checkDoHHint": "使用代理时可选。失败不阻断请求，结果不能核验代理实际 DNS 或目标出口。",
                    "waiting": "尚未配置",
                    "direct": "未指定代理（系统 / TUN 可能生效）",
                    "systemDNS": "系统 / 代理解析器",
                    "device": "稳定账号设备标识",
                    "fallbackIdentity": "稳定账号身份（尚无账号 ID）",
                    "hint": "新账号默认 US / America/New_York，属于请求配置，不能证明注册地或账单国家。IANA 时区自动处理夏令时。同一账号关联的 provider 共享已保存设置；导入保留原值，不修改系统或 Desktop。",
                    "diagnose": "检查一致性",
                    "checks": {
                        "account_identity": "账号 / 设备身份",
                        "identity_source": "身份来源",
                        "region": "已保存请求地区",
                        "dns": "DNS 路径",
                        "doh_reference": "可选公共 DoH 参考",
                        "target_dns": "目标最终解析",
                        "network_exit": "网络出口",
                        "connection": "目标连接",
                        "reference_exit": "参考观测",
                        "upstream_auth": "上游认证"
                    },
                    "statuses": {
                        "ok": "已核验",
                        "pending": "尚未核验",
                        "mismatch": "存在差异"
                    }
                },
                meta: {
                    title: 'Clipal 管理界面'
                },
                header: {
                    subtitle: '面向 Claude、OpenAI 和 Google 流量的本地入口',
                },
                nav: {
                    sectionsLabel: '功能分区',
                    providers: 'Providers',
                    integrations: 'CLI 接管',
                    settings: '全局设置',
                    data: '备份与导入',
                    services: '服务',
                    status: '系统状态'
                },
                setup: {
                    title: '服务器初始化检查清单',
                    service: 'Clipal 服务',
                    provider: '首个 Provider',
                    integrations: 'CLI 接管',
                    done: '已就绪',
                    todo: '需要处理',
                    serviceHint: '打开服务管理',
                    providerHint: '添加 Provider',
                    integrationHint: '打开 CLI 接管'
                },
                common: {
                    close: '关闭',
                    back: '返回',
                    copy: '复制',
                    copied: '已复制',
                    none: '无',
                    load: '加载',
                    save: '保存',
                    reset: '重置',
                    cancel: '取消',
                    confirmTitle: '请确认',
                    refresh: '刷新',
                    working: '处理中...',
                    saving: '保存中...',
                    enabled: '已启用',
                    disabled: '已禁用',
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
                    add: '添加 Provider',
                    loadFailed: '无法加载 Provider，当前列表可能已过期。',
                    candidate: '当前候选',
                    spend: '花费',
                    today: '今日',
                    week: '近 7 天 {amount}',
                    tokensInOut: '输入 {input} · 输出 {output}',
                    refreshed: '{time}刷新',
                    keys: 'API Key',
                    providerId: 'ID',
                    search: '按名称或地址搜索',
                    noMatch: '没有匹配的 Provider。',
                    reorderLocked: '清空搜索后可调整顺序',
                    details: '详情',
                    emptyNext: '添加后，可在此接入你的工具：',
                    runtimeStandby: '待命',
                    unknownCandidate: '暂未确定',
                    runtimeCurrent: '使用中',
                    runtimeCoolingDown: '冷却中',
                    runtimeUnavailable: '不可用',
                    runtimeRecoveryProbe: '恢复探测中',
                    modeLabel: '模式',
                    modeAuto: '自动',
                    modeManual: '手动',
                    enableProviderFirst: '请先启用一个 Provider',
                    empty: '{client} 还没有配置任何 Provider',
                    baseUrl: 'Base URL',
                    proxy: '代理',
                    oauthAuth: '授权',
                    usageTotal: '用量',
                    usageReasoning: '推理',
                    usageThoughts: '思考',
                    usageIncluded: '含{label} {tokens}',
                    usageTotalFormula: '{total} = {input} + {output} + {extra}',
                    usageTotalFormulaSimple: '{total} = {input} + {output}',
                    usageInOutFormula: '{input} / {output}',
                    usageBreakdownJoiner: '，',
                    spendToday: '今日消费',
                    spendWeek: '近 7 天消费',
                    planAndLimits: '套餐与限额',
                    quotaUnavailable: '上游未返回此账号的额度数据。',
                    rateLimit: '限额',
                    rateLimitUsed: '已用 {percent}%',
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
                    pinTitle: '固定到手动模式',
                    edit: '编辑',
                    delete: '删除',
                    disable: '禁用',
                    enable: '启用',
                    pinnedProvider: '已固定 Provider',
                    pinnedProviderCannotDisable: '手动模式下不能禁用已固定的 Provider',
                    enablePinnedProvider: '先启用已固定的 Provider',
                    modeHelpManual: '始终使用固定的 Provider，不做故障切换。',
                    modeHelpAuto: '按优先级在已启用的 Provider 间故障切换。',
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
                    deleteKeysNote: '其已配置的 {count} 个 API Key 将被永久移除。',
                    deletedTitle: '已删除 Provider {name}',
                    deletedMessage: '它已从 {client} 的 Provider 列表中移除。',
                    clientTypeLabel: '客户端类型',
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
                    oauthUnavailable: '这个客户端暂时还不能使用 OAuth。',
                    oauthImportCompletedTitle: '已导入 OAuth 账号',
                    oauthImportFailedTitle: 'OAuth 导入失败',
                    oauthImportSummary: '已导入 {imported} 个账号，关联 {linked} 个 Provider，跳过 {skipped} 条记录，失败 {failed} 条记录。',
                    oauthImportBrowserUnsupported: '当前浏览器不支持 OAuth 凭据导入。',
                    dragToReorder: '拖拽排序；Alt+↑/↓ 移动',
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
                        oauthProviderAntigravity: 'Antigravity',
                        oauthDirectHint: '授权完成后，Clipal 会自动关联到已有 Provider；如果没有，再创建一个。',
                        importFile: '导入 OAuth 授权文件',
                        importFileHint: '支持：CLIProxyAPI 单账号 OAuth JSON、Codex CLI auth.json、sub2api 导出的 JSON。Clipal 只会导入与当前服务匹配的账号。',
                        importFileHintCodex: '支持：Codex CLI 的 auth.json（通常在 ~/.codex/auth.json）、CLIProxyAPI 单账号 OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Codex 账号。',
                        importFileHintClaude: '支持：CLIProxyAPI 单账号 Claude OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Claude 账号。',

                        importFileHintAntigravity: '支持：CLIProxyAPI 单账号 Antigravity OAuth JSON、sub2api 导出的 JSON。Clipal 只会导入文件中的 Antigravity 账号。',
                        name: '名称 *',
                        namePlaceholder: '例如 provider-primary',
                        nameHint: '以字母或数字开头，可含字母、数字、点号、下划线和连字符，最多 64 个字符。',
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
                        reasoningEffortHint: '可选常用值，也可填写模型专属值。留空时使用上游默认值。',
                        thinkingBudgetTokens: '思考预算',
                        thinkingBudgetTokensHint: '设为 0 可取消思考预算覆盖。',
                        apiKeys: 'API Keys',
                        apiKeysRequired: 'API Keys *',
                        onePerLine: '每行一个 API Key',
                        keepExistingKey: '留空则保留当前已配置的 API Key。',
                        keepExistingKeys: '留空则保留当前已配置的 {count} 个 API Key。',
                        overridesTitle: '覆盖',
                        overridesHint: '无需覆盖则留空',
                        priority: '优先级',
                        saveProvider: '保存 Provider',
                        authorizeProvider: '继续授权'
                    }
                },
                settings: {
                    title: '全局设置',
                    loadFailed: '无法加载全局设置，请重试以读取已保存的配置。',
                    subtitle: '运行时默认项、恢复策略、路由策略以及本地可观测性。',
                    runtimeTitle: '运行时',
                    runtimeCopy: '基础监听配置与请求缓冲默认项。',
                    listenAddress: '监听地址',
                    allowRemoteProxy: '允许远程代理访问',
                    allowRemoteWebUI: '允许远程 Web UI 访问',
                    remoteProxyHint: '允许其他机器通过本机 Clipal 发送模型请求。请求不做身份认证，请用防火墙、VPN 或反向代理保护该端口。',
                    remoteWebUIHint: '管理界面（含 Provider API Key 与 OAuth 令牌）将对其他机器开放，且没有内置认证。',
                    remoteWebUIWarning: '任何能访问该端口的人都可以读取并修改你的配置。请仅在防火墙、VPN 或带认证的反向代理之后开启。',
                    remoteWebUIConfirmTitle: '确认对外开放 Web UI？',
                    remoteWebUIConfirm: '管理界面没有认证。任何能访问该端口的人都能读取你的 API Key 和 OAuth 令牌。',
                    remoteWebUIConfirmAction: '开启并保存',
                    port: '端口',
                    logLevel: '日志级别',
                    maxBodySize: '最大请求体大小',
                    maxBodySizeHint: '可重试请求的缓冲大小，至少为 64 MiB。',
                    reliabilityTitle: '可靠性',
                    reliabilityCopy: '超时、临时停用和熔断器行为。',
                    reactivateAfter: '恢复激活时间',
                    reactivateAfterHint: '失败的 Provider 会停用多久后再恢复。',
                    upstreamIdleTimeout: '上游空闲超时',
                    upstreamIdleTimeoutHint: '设为 0 可关闭，否则请至少设置 5 分钟。',
                    responseHeaderTimeout: '响应头超时',
                    responseHeaderTimeoutHint: '设为 0 可关闭，否则请至少设置 5 分钟。',
                    upstreamProxyMode: '默认上游代理',
                    upstreamProxyUrl: '默认代理 URL',
                    upstreamProxyHint: '对代理模式为“使用默认值”的 Provider 生效。',
                    upstreamProxyUrlHelp: '支持 http://、https://、socks5:// 和 socks5h:// 代理 URL。',
                    proxyModeEnvironment: '环境变量／系统代理',
                    proxyModeDirect: '直连',
                    proxyModeCustom: '自定义代理',
                    upstreamProxyUrlHint: 'http://127.0.0.1:7890',
                    failureThreshold: '失败阈值',
                    failureThresholdHint: '设为 0 可关闭熔断器。',
                    successThreshold: '成功阈值',
                    openTimeout: '熔断冷却时间',
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
                    stickySessionTtlHint: '显式会话绑定的空闲生存时间，留空使用默认值。',
                    shortRetryAfterMax: '短 Retry-After 上限',
                    shortRetryAfterMaxHint: '对上游较短 retry-after 提示的最大遵从值，留空使用默认值。',
                    maxInlineWait: '最大内联等待',
                    maxInlineWaitHint: 'Clipal 在溢出到其他 Provider 前可等待的最长时间，留空使用默认值。',
                    enableStickySessions: '启用粘性会话',
                    enableBusyBackpressure: '启用 Busy Backpressure',
                    dataTitle: '备份与导入',
                    dataSubtitle: '导出完整备份或导入数据。这些操作会立即执行，与“全局设置”的保存无关。',
                    unsaved: '有未保存的修改',
                    restartRequired: '修改后需重启才会生效。',
                    restartNotice: '监听地址或端口已修改，需重启 Clipal 后生效。',
                    unsavedTitle: '放弃未保存的设置？',
                    unsavedMessage: '“全局设置”中有未保存的修改，离开此页将丢弃它们。',
                    discardChanges: '放弃修改',
                    footerHint: '保存会更新 `config.yaml`。部分运行时改动需要重启后才会完全生效。',
                    saveSuccess: '配置已保存。部分改动可能需要重启。',
                    exportSaved: '备份已保存为 {filename}。',
                    exportDownloadStarted: '已开始下载 {filename}，请在浏览器下载列表中查看保存位置。',
                    exportFileType: 'Clipal 备份（JSON）',
                    exportFailure: '备份导出失败',
                    exportFullBackup: '导出完整备份',
                    exportBackupCopy: '下载全局与客户端配置、Provider API Key、OAuth access 和 refresh token，以及用量历史。',
                    exportSaveHint: '创建备份前选择文件名和保存位置。',
                    importTitle: '导入数据',
                    importCopy: '恢复 Clipal 备份，或导入受支持的外部凭据导出。',
                    importFiles: '导入 JSON 文件',
                    chooseImportFiles: '选择 JSON 文件',
                    importFilesHint: '未选择文件',
                    importFilePickerUnavailable: '当前浏览器无法选择文件',
                    importMode: '导入模式',
                    modeAuto: '自动选择',
                    modeReplace: '替换',
                    modeMerge: '合并',
                    previewImport: '预览导入',
                    applyImport: '执行导入',
                    importPreviewTitle: '导入预览',
                    importPreviewFiles: '文件',
                    importPreviewSource: '识别结果',
                    importPreviewEffects: '计划影响',
                    importPreviewConfiguration: '配置',
                    importPreviewProviders: 'Provider',
                    importPreviewCredentials: '凭据',
                    importPreviewUsage: '用量数据',
                    importPreviewWarnings: '警告',
                    importConfigReplace: '配置将被替换',
                    importConfigMerge: '配置将被合并',
                    importConfigCredentialsOnly: '不会修改配置',
                    importChangeSummary: '新增 {added} · 更新 {updated} · 移除 {removed}',
                    importPreviewGuard: '若所选数据或当前状态发生变化，请重新预览后再执行。',
                    importApplied: '已导入 {credentials} 个凭据、{providers} 个 Provider 和 {usage} 条用量记录',
                    importFailure: '数据导入失败'
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
                    stateConfigured: '配置已写入',
                    stateNeedsAttention: '需要关注',
                    stateNotConfigured: '未配置',
                    alreadyUsing: '配置已写入',
                    noBackupYet: '目前还没有备份。至少先使用一次 Clipal 后才能恢复。',
                    dismissResult: '关闭结果',
                    fullFiles: '完整文件',
                    targetOptions: '配置位置',
                    configDir: '客户端配置目录（可选）',
                    codexProfile: 'Codex profile（可选）',
                    shellDir: 'Shell 启动文件目录（可选）',
                    shellType: 'Shell（可自动检测时留空）',
                    saveTarget: '保存位置',
                    targetSaved: '配置位置已保存',
                    runtimeUnverified: '运行时路由尚未验证。',
                    runtimeBlocked: '检测到路由冲突，请查看警告。',
                    previewChanges: '预览变更',
                    changesSummary: '变更：+{added} / −{removed} 行',
                    changesNone: '没有需要应用的变更',
                    changesLarge: '变更（文件过大，不显示逐行对比）',
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
                    resultUsingClipalTitle: '配置已写入',
                    resultRestoredTitle: '已从备份恢复',
                    resultRestartMessage: '请重启客户端或重新打开一个会话以应用改动。',
                    resultApplyErrorTitle: '更新此客户端失败',
                    resultRestoreErrorTitle: '恢复此客户端失败',
                },
                services: {
                    actionFailed: '服务操作失败，请重试或检查服务输出。',
                    actionUnconfirmed: '连接已中断，尚未确认服务操作结果，正在检查状态…',
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
                    pinned: '已固定',
                    configDir: '配置目录',
                    circuitBreaker: '熔断器',
                    disabled: '已禁用',
                    circuitBreakerSummary: '{failure} 次失败 / {success} 次成功（{timeout}）',
                    activitySwitch: '切换 Provider',
                    activityRequest: '请求',
                    recentActivity: '最新活动',
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
            { value: 'google', label: 'Google' }
        ],
        providers: [],
        providerLoadBusy: false,
        providerLoadError: '',
        providerLoadGeneration: 0,
        clientConfig: {
            mode: 'auto',
            pinned_provider: '',
            override_support: null
        },
        // Keep a stable object shape so Alpine x-model bindings don't explode
        // during the initial render (before loadGlobalConfig completes).
        globalConfig: {
            listen_addr: '',
            allow_remote_proxy: false,
            allow_remote_web_ui: false,
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
        dataImportBusy: '',
        dataImportFiles: [],
        dataImportMode: '',
        dataImportPlan: null,
        showDataImportPreviewModal: false,
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
        confirmDialog: { visible: false, title: '', message: '', detail: '', confirmLabel: '', danger: false },
        confirmResolver: null,
        confirmReturnFocus: null,
        dialogReturnFocus: null,
        providerSearch: '',
        selectedProviderName: '',
        providerDetailOpen: false,   // compact windows: the inspector replaces the list
        providerReordering: false,
        clientConfigSaving: false,
        providerUpdating: false,
        providerFormSnapshot: null,
        environmentFormSnapshot: null,
        providerSaving: false,
        statusClock: Date.now(),
        statusHealth: {
            runtime: { lastSuccess: 0, failed: false, busy: false },
            service: { lastSuccess: 0, failed: false, busy: false }
        },
        globalSaving: false,
        globalLoading: false,
        globalLoadError: '',
        savedAllowRemoteWebUI: false,
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
            request_country: 'US',
            request_timezone: 'America/New_York',
            request_environment_source: 'default',
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

        get selectedRuntime() {
            return (this.status.clients || {})[this.selectedClient] || {};
        },
        // Search only earns its space once the list no longer fits on one screen.
        get providerSearchAvailable() {
            return this.providers.length > 8 || !!this.providerSearch;
        },
        get visibleProviders() {
            const query = this.providerSearch.trim().toLowerCase();
            if (!query) return this.providers;
            return this.providers.filter(provider => [provider.name, provider.display_name, provider.base_url]
                .filter(Boolean).join(' ').toLowerCase().includes(query));
        },
        // The inspector shows the chosen provider, or the first visible one when none is chosen.
        get selectedProvider() {
            const list = this.visibleProviders;
            return list.find(provider => provider.name === this.selectedProviderName) || list[0] || null;
        },
        get providerDetailVisible() {
            // Keep the list's no-match feedback visible when filtering removes the inspector.
            return this.providerDetailOpen && !!this.selectedProvider;
        },
        get canReorderProviders() {
            return !this.providerSearch.trim() && this.canMutateProviders;
        },
        get providerMutationBusy() {
            return this.providerSaving || this.providerUpdating || this.providerReordering || this.clientConfigSaving;
        },
        get canMutateProviders() {
            return !this.providerMutationBusy && !this.providerLoadBusy && !this.providerLoadError;
        },
        providerPinned(provider) {
            return this.clientConfig.mode === 'manual' && !!provider && provider.name === this.clientConfig.pinned_provider;
        },
        providerNeedsAttention(provider) {
            const runtime = this.providerRuntime(provider);
            return !!provider.enabled && !!runtime && runtime.state !== 'current';
        },
        providerRowState(provider) {
            if (!provider.enabled) {
                const label = this.t('common.disabled');
                return { tone: 'off', label, title: label };
            }
            const runtime = this.providerRuntime(provider);
            if (runtime) return runtime;
            const label = this.t('providers.runtimeStandby');
            return { tone: 'idle', label, title: label };
        },
        // Secondary line under the provider name; the OAuth tag is rendered separately.
        providerRowMeta(provider) {
            const parts = [];
            if (!this.providerUsesOAuth(provider) && provider.base_url) {
                try {
                    parts.push(new URL(provider.base_url).host);
                } catch (_) {
                    parts.push(provider.base_url);
                }
            }
            if (provider.display_name && provider.display_name !== provider.name) parts.push(provider.name);
            return parts.join(' · ');
        },
        providerSelected(provider) {
            return !!this.selectedProvider && this.selectedProvider.name === provider.name;
        },
        selectProvider(provider) {
            this.selectedProviderName = provider.name;
            this.providerDetailOpen = true;
            // Compact windows push the inspector over the list: start at the top and focus Back.
            if (typeof window !== 'undefined' && window.matchMedia && window.matchMedia('(max-width: 760px), (max-height: 360px)').matches) {
                window.scrollTo({ top: 0 });
                this.$nextTick(() => document.querySelector('.provider-inspector__back')?.focus());
            }
        },
        closeProviderDetail() {
            this.providerDetailOpen = false;
        },
        providerRowClick(provider, event) {
            // Controls inside the row (drag handle, switch) handle their own clicks.
            if (event && event.target && event.target.closest && event.target.closest('.provider-drag-handle, input, label, a')) return;
            this.selectProvider(provider);
        },
        runtimeCandidatesLabel() {
            const client = this.selectedRuntime;
            const candidates = Object.entries(client.current_providers || {});
            const names = [...new Set(candidates.map(([, name]) => name).filter(Boolean))];
            if (names.length === 1) return names[0];
            return candidates.length ? candidates.map(([capability, name]) => `${capability}: ${name}`).join(' · ')
                : client.current_provider || this.t('providers.unknownCandidate');
        },

        setupServiceReady() { return !!this.serviceStatus.running; },
        setupProviderReady() {
            return Object.values(this.status.clients || {}).some(client => (client.enabled_providers || []).length > 0);
        },
        setupIntegrationReady() { return (this.integrations || []).some(item => item.state === 'configured'); },
        openSetupTab(tab) { return this.setTab(tab); },

        // Switches the visible tab; leaving Global Settings with unsaved edits asks first.
        async setTab(tab) {
            if (!this.tabNames.includes(tab) || tab === this.activeTab) return true;
            if (this.dataImportBusy === 'apply') {
                this.syncHash();
                return false;
            }
            if (!await this.requestCloseProvider() || !await this.closeEnvironment()) {
                this.syncHash();
                return false;
            }
            if (this.activeTab === 'settings' && this.globalDirty) {
                const ok = await this.askConfirm({
                    title: this.t('settings.unsavedTitle'),
                    message: this.t('settings.unsavedMessage'),
                    confirmLabel: this.t('settings.discardChanges'),
                    danger: true
                });
                if (!ok) {
                    this.syncHash();
                    return false;
                }
                this.globalConfig = JSON.parse(JSON.stringify(this.savedGlobalConfig));
            }
            this.activeTab = tab;
            // Refresh on arrival so data paused while the tab was hidden is not shown as stale.
            if (this.statusPolledOn('runtime')) this.refreshStatus();
            if (this.statusPolledOn('service')) this.loadServiceStatus(true);
            return true;
        },

        nextChoiceIndex(current, length, direction) {
            if (direction === 'first') return 0;
            if (direction === 'last') return length - 1;
            return (current + direction + length) % length;
        },

        moveTab(direction) {
            const names = this.tabNames;
            const current = names.indexOf(this.activeTab);
            const next = this.nextChoiceIndex(current, names.length, direction);
            this.setTab(names[next]).then(() => {
                this.$nextTick(() => {
                    const el = document.getElementById('tabbtn-' + this.activeTab);
                    if (el && typeof el.focus === 'function') el.focus();
                });
            });
        },

        // Hash routing: #/<tab>[/<client>] keeps tab and client across reloads and in history.
        parseHash() {
            const raw = String((window.location && window.location.hash) || '').replace(/^#\/?/, '');
            const [tab, client] = raw.split('/');
            return { tab: tab || '', client: this.canonicalClientType(client) };
        },

        hashForState() {
            const parts = [this.activeTab];
            if (this.activeTab === 'providers') parts.push(this.selectedClient);
            return '#/' + parts.join('/');
        },

        syncHash(replace = false) {
            if (!window.location) return;
            const hash = this.hashForState();
            if (window.location.hash === hash) return;
            if (replace && window.history && typeof window.history.replaceState === 'function') {
                window.history.replaceState(null, '', hash);
            } else {
                window.location.hash = hash;
            }
        },

        async applyHash() {
            const { tab, client } = this.parseHash();
            if (tab && this.tabNames.includes(tab)) {
                await this.setTab(tab);
            }
            if (client && this.activeTab === 'providers' && this.isSupportedClientType(client)) {
                await this.selectClient(client);
            }
        },

        get globalDirty() {
            if (!this.savedGlobalConfig) return false;
            return JSON.stringify(this.globalConfig) !== JSON.stringify(this.savedGlobalConfig);
        },

        get providerDirty() {
            return (this.showAddProviderModal || this.showEditProviderModal)
                && this.providerFormSnapshot !== null
                && JSON.stringify(this.providerForm) !== this.providerFormSnapshot;
        },
        get environmentDirty() {
            return this.environmentVisible && this.environmentFormSnapshot !== null
                && JSON.stringify(this.environmentForm) !== this.environmentFormSnapshot;
        },
        get hasUnsavedInput() {
            return this.globalDirty || this.providerDirty || this.environmentDirty;
        },
        async confirmDiscard(dirty) {
            if (!dirty) return true;
            return this.askConfirm({
                title: this.t('feedback.unsavedTitle'), message: this.t('feedback.unsaved'),
                confirmLabel: this.t('settings.discardChanges'), danger: true
            });
        },
        async requestCloseProvider(event) {
            if (!(this.showAddProviderModal || this.showEditProviderModal)) return true;
            if (this.confirmDialog.visible || this.providerSaving) return false;
            // Consume Escape before opening a confirmation so its window listener
            // cannot dismiss the new dialog with the same keypress.
            if (event) event.stopImmediatePropagation();
            if (!await this.confirmDiscard(this.providerDirty)) return false;
            this.closeModals();
            return true;
        },
        async closeEnvironment() {
            if (!this.environmentVisible) return true;
            if (this.environmentSaving || this.confirmDialog.visible) return false;
            if (await this.confirmDiscard(this.environmentDirty)) {
                this.environmentVisible = false;
                this.environmentBusy = false;
                this.environmentFormSnapshot = null;
                ++this.environmentGeneration;
                return true;
            }
            return false;
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
            const next = this.nextChoiceIndex(index, this.supportedLocales.length, direction);
            const locale = this.supportedLocales[next];
            this.setLocale(locale);
            this.focusLocaleButton(locale);
        },

        canonicalClientType(clientType) {
            const value = String(clientType || '').trim().toLowerCase();
            return value === 'gemini' ? 'google' : value;
        },

        isSupportedClientType(clientType) {
            clientType = this.canonicalClientType(clientType);
            return this.clientOptions.some(option => option && option.value === clientType);
        },

        normalizePendingOAuthSession(session) {
            if (!session || typeof session !== 'object') {
                return null;
            }

            const sessionId = String(session.session_id || '').trim();
            const provider = String(session.provider || '').trim().toLowerCase();
            const clientType = this.canonicalClientType(session.client_type);
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
            const client = this.selectedClient;
            try {
                const items = await this.apiCall(
                    `/api/oauth/providers?client_type=${encodeURIComponent(client)}`,
                    {},
                    background,
                    true
                );
                if (client !== this.selectedClient) return;
                this.oauthProviders = Array.isArray(items) ? items : [];
            } catch (error) {
                if (client !== this.selectedClient) return;
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
            const initialRoute = this.parseHash();
            if (this.tabNames.includes(initialRoute.tab)) {
                this.activeTab = initialRoute.tab;
            }
            if (this.isSupportedClientType(initialRoute.client)) {
                this.selectedClient = initialRoute.client;
            }
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

            this.syncHash(true);
            if (typeof this.$watch === 'function') {
                this.$watch('activeTab', () => this.syncHash());
                this.$watch('selectedClient', () => this.syncHash());
                this.$watch('visibleDialog', (current, previous) => this.handleDialogChange(current, previous));
            }
            if (typeof window.addEventListener === 'function') {
                window.addEventListener('hashchange', () => this.applyHash());
                window.addEventListener('beforeunload', event => {
                    if (this.hasUnsavedInput) {
                        event.preventDefault();
                        event.returnValue = '';
                    }
                });
            }

            // Poll only for the visible tab that shows the data; statusClock keeps staleness reactive.
            this.servicePoll = setInterval(() => {
                this.statusClock = Date.now();
                if (typeof document !== 'undefined' && document.hidden) return;
                if (this.statusPolledOn('service')) this.loadServiceStatus(true);
                if (this.statusPolledOn('runtime')) this.refreshStatus();
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
            if (state === 'current') return 'pill--primary';
            if (state === 'disabled') return 'pill--muted';
            if (state === 'cooling_down') return 'pill--warning';
            if (state === 'unavailable') return 'pill--danger';
            if (state === 'recovery_probe') return 'pill--primary';
            return 'pill--neutral';
        },

        environmentCountryOptions(selected) {
            if (!countryOptionsByLocale.has(this.locale)) {
                const names = typeof Intl.DisplayNames === 'function'
                    ? new Intl.DisplayNames([this.locale], { type: 'region' }) : null;
                const options = Object.entries(requestEnvironmentChoices.countries).map(([value, fallback]) => ({
                    value, label: `${names ? names.of(value) : fallback} (${value})`
                }));
                options.sort((a, b) => a.label.localeCompare(b.label, this.locale));
                countryOptionsByLocale.set(this.locale, options);
            }
            const options = countryOptionsByLocale.get(this.locale);
            return selected && !options.some(option => option.value === selected)
                ? [{ value: selected, label: selected }, ...options] : options;
        },
        environmentTimezoneOptions(selected) {
            const options = requestEnvironmentChoices.timezones;
            // Keep saved aliases (for example US/Eastern) exactly as stored.
            return selected && !options.includes(selected) ? [selected, ...options] : options;
        },
        async openEnvironment(provider) {
            if (this.providerMutationBusy || !this.providerUsesOAuth(provider)) return;
            const generation = ++this.environmentGeneration;
            this.environmentProvider = provider;
            this.environmentLoadError = '';
            this.environmentPath = `/api/providers/${this.selectedClient}/${encodeURIComponent(provider.name)}`;
            this.environmentVisible = true;
            this.environmentBusy = true;
            this.environmentDiagnostics = null;
            this.environmentCheckDoH = false;
            this.environmentProfile = {};
            this.environmentIdentity = '';
            this.environmentUpstream = {};
            this.environmentForm = { country: 'US', timezone: 'America/New_York' };
            this.environmentFormSnapshot = JSON.stringify(this.environmentForm);
            this.environmentAccount = '';
            try {
                const data = await this.apiCall(this.environmentPath + '/oauth-environment', {}, true, true);
                if (generation !== this.environmentGeneration) return;
                this.environmentProfile = { ...data.environment };
                this.environmentAccount = data.account_id || this.t('requestEnv.fallbackIdentity');
                this.environmentIdentity = data.account_identity || '';
                this.environmentUpstream = data.upstream_region || {};
                this.environmentForm = { country: data.environment.country || 'US', timezone: data.environment.timezone || 'America/New_York' };
                this.environmentFormSnapshot = JSON.stringify(this.environmentForm);
            } catch (error) {
                if (generation === this.environmentGeneration && this.environmentVisible) {
                    this.environmentLoadError = error.message || this.t('requestEnv.loadFailed');
                }
            } finally {
                if (generation === this.environmentGeneration) this.environmentBusy = false;
            }
        },
        async saveEnvironment() {
            if (this.environmentBusy || !this.environmentIdentity) return;
            const generation = this.environmentGeneration;
            this.environmentBusy = true;
            this.environmentSaving = true;
            try {
                const data = await this.apiCall(this.environmentPath + '/oauth-environment', {
                    method: 'PUT', body: JSON.stringify({ country: this.environmentForm.country, timezone: this.environmentForm.timezone, account_identity: this.environmentIdentity })
                }, true);
                if (generation !== this.environmentGeneration) return;
                this.environmentProfile = { ...data.environment };
                this.environmentDiagnostics = null;
                this.environmentForm = { country: data.environment.country, timezone: data.environment.timezone };
                this.environmentFormSnapshot = JSON.stringify(this.environmentForm);
                this.showAlert('success', this.t('requestEnv.saved'));
            } catch (_) {
                // apiCall reports the error; preserve user input for correction.
            } finally {
                if (generation === this.environmentGeneration) {
                    this.environmentBusy = false;
                    this.environmentSaving = false;
                }
            }
        },
        environmentDNSLabel() {
            const dns = this.environmentProfile.dns || 'auto';
            return dns === 'auto' ? this.t('requestEnv.automatic') : dns === 'system' ? this.t('requestEnv.systemDNS') : this.tf('requestEnv.resolverChoice', { resolver: dns === 'google' ? 'Google' : 'Cloudflare' });
        },
        environmentSourceLabel() {
            if (this.environmentProfile.needs_confirmation) return this.t('requestEnv.pendingSource');
            return this.t('requestEnv.sources.' + (this.environmentProfile.source || 'unknown'));
        },
        environmentCheckDetail(check) {
            if (this.locale !== 'zh-CN') return check.detail;
            const details = {
                'Persistent device ID matches this OAuth account.': '持久设备标识与当前 OAuth 账号一致。',
                'Account ID is unavailable; device ID uses the stable account identity fallback.': '尚无账号 ID，设备标识使用稳定账号身份生成。',
                'Request region requires user confirmation; it is not inferred from network exits.': '请求地区需要用户确认，不根据网络出口推断。',
                'Request preferences are stored on the account; they do not prove registration or billing country.': '请求配置已按账号保存，不能证明注册地或账单国家。',
                'Target proxy policy failed.': '目标代理策略无法解析。',
                'Account DNS transport could not be created.': '无法建立账号 DNS 连接。',
                'DNS follows the system or proxy resolver; Google/Cloudflare is not enforced.': '此凭据沿用系统或代理解析器，尚未核验 Cloudflare / Google DoH。',
                'Account DoH lookup failed; no system DNS fallback was used.': '账号 DoH 查询失败，未回退系统 DNS。',
                'Target hostnames are delegated to the standard proxy; public DoH is not required.': '目标域名交给标准代理解析，不依赖公共 DoH。',
                "The proxy's actual DNS backend and target addresses are unverified.": '尚未核验代理实际使用的 DNS 后端及目标地址。',
                'Optional DoH reference lookup failed; proxy requests do not depend on it.': '可选 DoH 参考查询失败；代理请求不依赖此查询。',
                'Target resolution follows the system resolver; system/TUN routing is not observed.': '目标域名由系统解析器处理；尚未观测系统或 TUN 路由。',
                'Automatic DNS tries DoH first, then the system hostname route if connection establishment fails.': '自动 DNS 优先使用 DoH，解析或连接建立失败后回退系统域名通路。',
                'System/TUN routing and target addresses are unverified; no network settings are changed.': '尚未核验系统／TUN 路由及目标地址；不会修改网络设置。',
                'Target connection did not complete on the configured routing policy.': '目标连接未能按当前路由策略完成。',
                'Target connections use DoH returned addresses with original Host and TLS SNI.': '目标连接使用 DoH 返回的地址，保留原始 Host 与 TLS SNI。',
                'Target HTTP connection succeeded without account credentials; authentication is unverified.': '目标 HTTP 连接成功，未发送账号凭据；认证尚未核验。',
                'Reference service observation only; it does not prove the target exit or account country.': '仅为参考服务的观测，不能证明目标出口或账号国家。',
                'No reference service observation was available on the selected entry.': '当前入口未获得参考服务观测。',
                'The target exit is unverified; matching proxy entries do not prove matching egress.': '目标出口尚未核验；相同代理入口不能证明相同出口。',
                'No token refresh or paid model request was sent by this diagnostic.': '诊断未发送 token 刷新或付费模型请求，未验证上游认证。'
            };
            const resolver = /^(cloudflare|google) DoH succeeded/.exec(check.detail || '');
            if (resolver) return `${resolver[1] === 'cloudflare' ? 'Cloudflare' : 'Google'} DoH 已通过当前入口核验。`;
            const reference = /^(cloudflare|google) DoH reference lookup succeeded/.exec(check.detail || '');
            if (reference) return `${reference[1] === 'cloudflare' ? 'Cloudflare' : 'Google'} DoH 参考查询成功，不能证明代理实际 DNS 或目标出口。`;
            return details[check.detail] || check.detail;
        },
        environmentBootstrapDetail() {
            const value = this.environmentDiagnostics?.bootstrap_dns || '';
            if (this.locale !== 'zh-CN') return value;
            if (!value) return '';
            let detail = value.includes('for proxy requests') ? '代理业务请求不使用 DoH，由代理解析目标域名' : value.includes('DoH is not used') ? '未使用 DoH' : value.includes('Automatic DoH route') ? '自动优先使用 DoH，连接建立失败后回退系统域名通路' : 'DoH 服务域名引导（固定 IP 后备），未指定显式代理';
            if (value.includes('system DNS resolves')) detail += '；代理域名第一跳由系统 DNS 解析';
            return detail;
        },

        async diagnoseEnvironment() {
            if (this.environmentBusy) return;
            const generation = this.environmentGeneration;
            this.environmentBusy = true;
            try {
                const report = await this.apiCall(this.environmentPath + '/oauth-diagnostics', { method: 'POST', body: JSON.stringify({ check_doh: !!this.environmentCheckDoH }) }, true, true);
                if (generation !== this.environmentGeneration) return;
                this.environmentDiagnostics = report;
                this.environmentProfile = { ...report.environment };
            } catch (error) {
                if (generation === this.environmentGeneration && this.environmentVisible) this.showAlert('error', error.message);
            } finally {
                if (generation === this.environmentGeneration) this.environmentBusy = false;
            }
        },

        // In-app confirmation dialog; resolves true on confirm, false on cancel/Escape/backdrop.
        askConfirm({ title = '', message = '', detail = '', confirmLabel = '', danger = false } = {}) {
            if (this.confirmResolver) {
                this.resolveConfirm(false);
            }
            this.confirmReturnFocus = (typeof document !== 'undefined' && document.activeElement) || null;
            this.setBackgroundInert(true);
            this.confirmDialog = {
                visible: true,
                title: title || this.t('common.confirmTitle'),
                message,
                detail,
                confirmLabel: confirmLabel || this.t('common.confirmTitle'),
                danger: !!danger
            };
            const pending = new Promise(resolve => {
                this.confirmResolver = resolve;
            });
            // x-show reveals the dialog a frame or two later; focus only works once it is rendered.
            let attempts = 0;
            const focusCancel = () => {
                const cancel = typeof document.querySelector === 'function'
                    ? document.querySelector('.modal--confirm [data-confirm-cancel]')
                    : null;
                if (!cancel || typeof cancel.focus !== 'function') return;
                const rendered = typeof cancel.getClientRects !== 'function' || cancel.getClientRects().length > 0;
                if (!rendered && attempts++ < 100) {
                    // x-show reveals the dialog on the next animation frame, which can be delayed
                    // (for example in a throttled or occluded tab), so keep polling for ~1.6s.
                    setTimeout(focusCancel, 16);
                    return;
                }
                cancel.focus();
            };
            this.$nextTick(focusCancel);
            return pending;
        },

        resolveConfirm(result) {
            const resolve = this.confirmResolver;
            this.confirmResolver = null;
            this.confirmDialog = { ...this.confirmDialog, visible: false };
            this.setBackgroundInert(!!this.visibleDialog);
            const target = this.confirmReturnFocus;
            this.confirmReturnFocus = null;
            if (target && typeof target.focus === 'function') {
                this.$nextTick(() => target.focus());
            }
            if (resolve) resolve(!!result);
        },

        // Isolate page controls behind a dialog; the separate toast region stays live.
        setBackgroundInert(inert) {
            if (typeof document === 'undefined' || typeof document.querySelector !== 'function') return;
            for (const selector of ['.toolbar', '.container']) {
                const element = document.querySelector(selector);
                if (element) element.inert = !!inert;
            }
        },

        get visibleDialog() {
            if (this.confirmDialog.visible) return 'confirm';
            if (this.showAddProviderModal || this.showEditProviderModal) return 'provider';
            if (this.environmentVisible) return 'environment';
            return this.showDataImportPreviewModal ? 'import' : '';
        },
        handleDialogChange(current, previous) {
            this.setBackgroundInert(!!current);
            if (!previous && current && current !== 'confirm') this.dialogReturnFocus = document.activeElement;
            if (!current) {
                const target = this.dialogReturnFocus;
                this.dialogReturnFocus = null;
                if (target && target.isConnected !== false) this.$nextTick(() => target.focus());
                return;
            }
            // Confirm dialogs handle their own initial and return focus.
            if (current === 'confirm' || previous === 'confirm') return;
            let attempts = 0;
            const focusFirst = () => {
                if (this.visibleDialog !== current) return;
                const dialog = document.querySelector(`[data-dialog="${current}"]`);
                const controls = dialog ? this.dialogFocusables(dialog) : [];
                const target = controls.find(element => element.matches('input, select, textarea')) || controls[0];
                if (!target) {
                    if (attempts++ < 100) setTimeout(focusFirst, 16);
                    return;
                }
                target.focus();
            };
            this.$nextTick(focusFirst);
        },
        dialogFocusables(dialog) {
            return Array.from(dialog.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])'))
                .filter(element => !element.closest?.('[inert]') && (typeof element.getClientRects !== 'function' || element.getClientRects().length > 0));
        },
        // Every modal uses the same keyboard boundary, including nested confirmations.
        trapDialogFocus(event) {
            const dialog = event.currentTarget;
            const focusable = this.dialogFocusables(dialog);
            if (focusable.length === 0) return;
            const first = focusable[0];
            const last = focusable[focusable.length - 1];
            const active = document.activeElement;
            if (event.shiftKey && (active === first || !dialog.contains(active))) {
                event.preventDefault();
                last.focus();
            } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
                event.preventDefault();
                first.focus();
            }
        },

        // API Calls
        async apiCall(url, options = {}, background = false, suppressAlert = false) {
            if (!background) this.isLoading = true;
            try {
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
                    const error = await response.json().catch(() => ({}));
                    throw new Error(error?.error || this.t('toast.requestFailed'));
                }

                const data = await response.json();

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
        statusPolledOn(key) {
            return key === 'service' ? this.activeTab === 'services' || this.activeTab === 'status'
                : this.activeTab === 'providers' || this.activeTab === 'status';
        },
        statusNeedsAttention(key) {
            const health = this.statusHealth[key];
            return health.failed || (!!health.lastSuccess && this.statusClock - health.lastSuccess >= 15000);
        },
        statusUpdatedLabel(key) {
            const timestamp = this.statusHealth[key].lastSuccess;
            return timestamp ? this.tf('feedback.updated', {
                time: new Date(timestamp).toLocaleTimeString(this.locale)
            }) : this.t('feedback.never');
        },
        async fetchTrackedStatus(key, url) {
            const health = this.statusHealth[key];
            if (health.busy) return null;
            health.busy = true;
            const controller = new AbortController();
            const timeout = setTimeout(() => controller.abort(), 10000);
            try {
                const data = await this.apiCall(url, { signal: controller.signal }, true, true);
                health.lastSuccess = Date.now();
                this.statusClock = health.lastSuccess;
                health.failed = false;
                return data;
            } catch (_) {
                health.failed = true;
                return null;
            } finally {
                clearTimeout(timeout);
                health.busy = false;
            }
        },
        async refreshStatus() {
            const data = await this.fetchTrackedStatus('runtime', '/api/status');
            if (data) this.status = data;
        },

        // Services
        async loadServiceStatus(background = false) {
            const status = await this.fetchTrackedStatus('service', '/api/service/status');
            if (status) {
                this.serviceStatus = this.withDefaultServiceStatus(status);
                if (!this.serviceActionRequestPending) this.serviceBusyAction = '';
            }
        },

        async serviceAction(action) {
            if (this.serviceBusyAction) return;
            const disabledReason = this.serviceActionDisabledReason(action);
            if (disabledReason) {
                return;
            }
            const confirmKeys = {
                uninstall: 'services.confirmUninstall',
                stop: 'services.confirmStop',
                restart: 'services.confirmRestart'
            };
            if (confirmKeys[action]) {
                const ok = await this.askConfirm({
                    message: this.t(confirmKeys[action]),
                    confirmLabel: this.t('services.' + action),
                    danger: action !== 'restart'
                });
                if (!ok) return;
            }

            // Best-effort request: the service might stop/restart mid-flight.
            this.serviceBusyAction = action;
            this.serviceActionRequestPending = true;
            let response;
            try {
                response = await fetch(`/api/service/${action}`, {
                    method: 'POST',
                    headers: { 'X-Clipal-UI': '1', 'Content-Type': 'application/json' },
                    body: JSON.stringify(this.serviceForm),
                    keepalive: true
                });
            } catch (e) {
                // Ignore network errors (common when the service restarts).
            } finally {
                this.serviceActionRequestPending = false;
            }

            if (response && !response.ok) {
                const error = await response.json().catch(() => ({}));
                this.serviceBusyAction = '';
                this.showAlert('error', error.error || this.t('services.actionFailed'));
                return;
            }

            this.showAlert('info', response
                ? this.tf('services.requestedAction', { action: this.serviceActionLabel(action) })
                : this.t('services.actionUnconfirmed'));
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

        async copyValue(text) {
            if (await this.copyToClipboard(text)) this.showAlert('success', '', this.t('common.copied'));
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
                // Use the document copy command when clipboard access is unavailable.
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
            if (!this.serviceStatus.supported) return 'pill--danger';
            if (!this.serviceStatus.installed) return 'pill--warning';
            if (this.serviceStatus.running) return 'pill--success';
            if (this.serviceStatus.loaded) return 'pill--warning';
            return 'pill--danger';
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

        integrationStateLabel(state, verification) {
            if (verification === 'blocked') return this.t('integrations.stateNeedsAttention');
            switch (String(state || '').trim()) {
                case 'configured':
                    return this.t('integrations.stateConfigured');
                case 'error':
                    return this.t('integrations.stateNeedsAttention');
                default:
                    return this.t('integrations.stateNotConfigured');
            }
        },

        integrationStateTone(state, verification) {
            if (verification === 'blocked') return 'danger';
            switch (String(state || '').trim()) {
                case 'configured':
                    return 'success';
                case 'error':
                    return 'danger';
                default:
                    return 'idle';
            }
        },

        toggleIntegrationPreview(product) {
            this.integrationPreviewOpen = { ...this.integrationPreviewOpen, [product]: !this.integrationPreviewOpen[product] };
        },

        // Show paths under the user's home as ~/...; the full path stays in the title attribute.
        displayHomePath(path) {
            return String(path || '').replace(/^(\/Users\/[^/]+|\/home\/[^/]+|[A-Za-z]:\\Users\\[^\\]+)(?=[\/\\])/, '~');
        },

        integrationProductName(product) {
            switch (String(product || '').trim()) {
                case 'claude':
                    return 'Claude Code';
                case 'codex':
                    return 'Codex CLI';
                case 'opencode':
                    return 'OpenCode';
                case 'antigravity':
                    return 'Antigravity CLI';
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
            return '';
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

        integrationChangesSummary(integration) {
            const changes = integration && integration.changes;
            if (!changes || changes.limited) return this.t('integrations.changesLarge');
            if (!changes.rows.length) return this.t('integrations.changesNone');
            return this.tf('integrations.changesSummary', changes);
        },

        integrationChanges(before, after) {
            if (before === after) return { rows: [], added: 0, removed: 0, limited: false };
            const a = before ? String(before).split('\n') : [];
            const b = after ? String(after).split('\n') : [];
            if (String(before).length + String(after).length > 100000 || a.length * b.length > 160000 || a.length + b.length > 2000) {
                return { rows: [], added: 0, removed: 0, limited: true };
            }
            const matrix = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
            for (let i = a.length - 1; i >= 0; i--) {
                for (let j = b.length - 1; j >= 0; j--) {
                    matrix[i][j] = a[i] === b[j] ? matrix[i + 1][j + 1] + 1
                        : Math.max(matrix[i + 1][j], matrix[i][j + 1]);
                }
            }
            const rows = [];
            let i = 0, j = 0, added = 0, removed = 0;
            while (i < a.length || j < b.length) {
                if (i < a.length && j < b.length && a[i] === b[j]) { i++; j++; continue; }
                if (j < b.length && (i === a.length || matrix[i][j + 1] > matrix[i + 1][j])) {
                    rows.push({ kind: 'added', text: '+ ' + b[j], line: ++j }); added++;
                } else {
                    rows.push({ kind: 'removed', text: '− ' + a[i], line: ++i }); removed++;
                }
            }
            return { rows, added, removed, limited: false };
        },

        normalizeIntegration(item) {
            return {
                ...item,
                target_options: {config_dir: '', profile: '', shell_dir: '', shell: '', ...((item && item.target_options) || {})},
                changes: this.integrationChanges(String(item.current_content || ''), this.integrationSecondaryPreviewContent(item)),
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
                    method: 'POST',
                    ...(op === 'target' ? { body: JSON.stringify((this.integrations.find(item => item.product === name) || {}).target_options || {}) } : {})
                }, false, true);
                this.integrations = (this.integrations || []).map(item =>
                    item && item.product === name
                        ? this.normalizeIntegration({ ...response.status, name: this.integrationProductName(name) || response.status.name })
                        : item
                );
                if (op === 'target') {
                    this.setIntegrationResult(name, 'success', this.t('integrations.targetSaved'), response.message);
                } else if (op === 'apply') {
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

        modeToggleTitle() {
            return String(this.clientConfig.mode || '').trim() === 'auto' && !this.hasEnabledProviders
                ? this.t('providers.enableProviderFirst')
                : '';
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

        providerUsageTotalMain(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            return this.formatCompactTokenCount(usage.total_tokens || 0);
        },

        providerUsageBreakdowns(provider, parent) {
            const usage = provider && provider.usage;
            const entries = Array.isArray(usage && usage.usage_breakdowns)
                ? provider.usage.usage_breakdowns
                : [];
            const normalized = entries.filter((entry) => {
                if (!entry || String(entry.parent || '') !== String(parent || '')) {
                    return false;
                }
                return Number(entry.tokens || 0) > 0;
            });
            if (normalized.length > 0) {
                return normalized;
            }

            const fallback = [];
            if (parent === 'output' && Number(usage && usage.reasoning_tokens || 0) > 0) {
                fallback.push({
                    kind: 'reasoning',
                    parent: 'output',
                    tokens: Number(usage.reasoning_tokens || 0)
                });
            }
            if (parent === 'total' && Number(usage && usage.thoughts_tokens || 0) > 0) {
                fallback.push({
                    kind: 'thoughts',
                    parent: 'total',
                    tokens: Number(usage.thoughts_tokens || 0)
                });
            }
            return fallback;
        },

        providerUsageBreakdownLabel(kind) {
            switch (String(kind || '')) {
                case 'reasoning':
                    return this.t('providers.usageReasoning');
                case 'thoughts':
                    return this.t('providers.usageThoughts');
                default:
                    return String(kind || '').trim();
            }
        },

        providerUsageBreakdownSummary(entry) {
            if (!entry) {
                return '';
            }
            const label = this.providerUsageBreakdownLabel(entry.kind);
            const tokens = this.formatTokenCount(entry.tokens || 0);
            return this.tf('providers.usageIncluded', { label, tokens });
        },

        providerUsageTotalTitle(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            const entries = this.providerUsageBreakdowns(provider, 'total');
            const total = this.formatTokenCount(usage.total_tokens || 0);
            const input = this.formatTokenCount(usage.input_tokens || 0);
            const output = this.formatTokenCount(usage.output_tokens || 0);
            if (!entries.length) {
                return this.tf('providers.usageTotalFormulaSimple', { total, input, output });
            }
            const extra = entries
                .map((entry) => this.providerUsageBreakdownSummary(entry))
                .join(this.t('providers.usageBreakdownJoiner'));
            return this.tf('providers.usageTotalFormula', { total, input, output, extra });
        },

        providerUsageInOutTitle(provider) {
            const usage = provider && provider.usage;
            if (!usage || !usage.has_usage) {
                return this.t('common.none');
            }
            const input = this.formatTokenCount(usage.input_tokens || 0);
            const output = this.formatTokenCount(usage.output_tokens || 0);
            const formula = this.tf('providers.usageInOutFormula', { input, output });
            const entries = this.providerUsageBreakdowns(provider, 'output');
            if (!entries.length) {
                return formula;
            }
            const suffix = entries
                .map((entry) => this.providerUsageBreakdownSummary(entry))
                .join(this.t('providers.usageBreakdownJoiner'));
            return `${formula} · ${suffix}`;
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
            return String((provider && provider.oauth_plan_type) || '').trim();
        },

        providerOAuthPlanLabel(provider) {
            const raw = this.providerOAuthPlanRaw(provider);
            if (!raw) {
                return '';
            }
            switch (raw.toLowerCase()) {
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
            const normalized = String((limit && (limit.limit_id || limit.limit_name)) || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
            const base = this.humanizeRateLimitName(
                this.providerOAuthUsesLocalizedLimitID(normalized)
                    ? normalized
                    : ((limit && (limit.limit_name || limit.limit_id)) || '')
            );
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

        providerOAuthUsesLocalizedLimitID(normalized) {
            switch (String(normalized || '').trim().toLowerCase()) {
                case 'claude_oauth_apps':
                case 'claude_opus':
                case 'claude_sonnet':
                case 'claude_extra_usage':
                    return true;
                default:
                    return false;
            }
        },

        humanizeRateLimitName(value) {
            // All windows carry used_percent, even when an upstream title says
            // "Remaining". Keep the title neutral so it cannot invert the metric.
            const raw = String(value || '').trim().replace(/\s+remaining$/i, '');
            if (!raw) {
                return '';
            }
            const normalized = raw.toLowerCase().replace(/[\s-]+/g, '_');
            if (normalized === 'code_review') {
                return this.locale === 'zh-CN' ? '代码审查' : 'Code review';
            }
            if (normalized === 'claude_oauth_apps') {
                return this.locale === 'zh-CN' ? 'OAuth 应用' : 'OAuth apps';
            }
            if (normalized === 'claude_opus') {
                return 'Opus';
            }
            if (normalized === 'claude_sonnet') {
                return 'Sonnet';
            }
            if (normalized === 'claude_extra_usage') {
                return this.locale === 'zh-CN' ? '额外用量' : 'Extra usage';
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
            return this.tf('providers.rateLimitUsed', {
                percent: Math.round(this.providerOAuthRateLimitPercent(window))
            });
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
            const oauthProvider = String((provider && provider.oauth_provider) || '').trim().toLowerCase();
            return this.providerUsesOAuth(provider)
                && ['codex', 'claude', 'antigravity'].includes(oauthProvider)
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

        providerOAuthAuthStatusTone(provider) {
            switch (this.providerOAuthAuthStatus(provider)) {
                case 'ready':
                    return 'success';
                case 'refresh_due':
                    return 'warn';
                case 'reauth_needed':
                default:
                    return 'danger';
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
                case 'antigravity':
                    return this.t('modal.provider.oauthProviderAntigravity');
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
                case 'antigravity':
                    return this.t('modal.provider.importFileHintAntigravity');
                default:
                    return this.t('modal.provider.importFileHint');
            }
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

        // Go durations such as "26h3m4.51s" read as "1d 2h 3m"; seconds only show in the first minute.
        statusUptimeLabel() {
            const raw = String((this.status && this.status.uptime) || '').trim();
            const units = { h: 3600, m: 60, s: 1, ms: 0.001, 'µs': 0.000001, us: 0.000001, ns: 0.000000001 };
            let seconds = 0;
            let matched = false;
            for (const [, value, unit] of raw.matchAll(/([\d.]+)(h|ms|µs|us|ns|m|s)/g)) {
                seconds += Number(value) * units[unit];
                matched = true;
            }
            if (!matched) return raw || this.t('common.none');
            const d = Math.floor(seconds / 86400);
            const h = Math.floor((seconds % 86400) / 3600);
            const m = Math.floor((seconds % 3600) / 60);
            if (d) return `${d}d ${h}h ${m}m`;
            if (h) return `${h}h ${m}m`;
            if (m) return `${m}m`;
            return `${Math.floor(seconds)}s`;
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

        modeHelpSummary() {
            if ((this.clientConfig.mode || '') === 'manual') {
                return this.t('providers.modeHelpManual');
            }
            return this.t('providers.modeHelpAuto');
        },

        // Runtime badge for a provider card; null when nothing noteworthy (healthy standby or disabled).
        providerRuntime(provider) {
            const name = String((provider && provider.name) || '').trim();
            const client = this.status && this.status.clients ? this.status.clients[this.selectedClient] : null;
            if (!name || !client) return null;

            const entry = (Array.isArray(client.providers) ? client.providers : [])
                .find(p => p && String(p.name || '').trim() === name) || null;
            const isCurrent = String(client.current_provider || '').trim() === name;
            let state = '';
            if (entry && ['cooling_down', 'unavailable', 'recovery_probe'].includes(entry.state)) {
                // A degraded health state is more useful than "in use" when troubleshooting.
                state = entry.state;
            } else if (isCurrent) {
                state = 'current';
            }
            if (!state) return null;

            const keys = {
                current: 'providers.runtimeCurrent',
                cooling_down: 'providers.runtimeCoolingDown',
                unavailable: 'providers.runtimeUnavailable',
                recovery_probe: 'providers.runtimeRecoveryProbe'
            };
            const tones = { current: 'primary', cooling_down: 'warn', unavailable: 'danger', recovery_probe: 'info' };
            const remaining = entry ? String(entry.deactivated_in || entry.circuit_open_in || '').trim() : '';
            const base = this.t(keys[state]);
            const reason = state === 'current' ? null : this.providerRuntimeReason(entry);
            return {
                state,
                tone: tones[state],
                label: remaining ? `${base} · ${remaining}` : base,
                current: isCurrent,
                reason,
                title: [
                    isCurrent && state !== 'current' ? this.t('providers.runtimeCurrent') : '',
                    reason ? reason.summary : base,
                    reason ? reason.message : ''
                ].filter(Boolean).join('\n')
            };
        },
        // Why a provider is degraded: the backend's own description and the upstream error message,
        // shown as returned. The provider's name is not part of it; the notice sits under that name.
        providerRuntimeReason(entry) {
            if (!entry) return null;
            const summary = String(entry.detail || '').trim();
            const raw = String(entry.skip_reason || '').trim() === 'deactivated' ? String(entry.deactivated_message || '').trim() : '';
            const message = raw.length > 300 ? `${raw.slice(0, 300)}...` : raw;
            if (!summary && !message) return null;
            return { summary, message };
        },

        // Latest-activity rows on the Status page.
        activityTone(kind, item) {
            if (kind === 'switch') return 'warn';
            const status = Number(item && item.status);
            if (status >= 200 && status < 300) return 'success';
            if (status >= 400 || status === 0) return 'danger';
            return 'info';
        },

        activityTime(item) {
            return this.formatRelativeTime(item && item.at, '');
        },

        activityRequestMeta(item) {
            if (!item) return '';
            const parts = [];
            if (item.provider) parts.push(item.provider);
            if (item.status) parts.push(String(item.status));
            if (item.protocol) parts.push(item.protocol);
            if (item.bytes) parts.push(`${item.bytes} B`);
            return parts.join(' · ');
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
                handle: '.provider-drag-handle',
                draggable: '.provider-row',
                onMove: () => this.canReorderProviders,
                ghostClass: 'sortable-ghost',
                chosenClass: 'sortable-chosen',
                dragClass: 'sortable-drag',
                forceFallback: false,
                onEnd: async (evt) => {
                    // Restore the DOM before Alpine applies the new keyed order.
                    this.syncSortableDomOrder(this.providers.map(p => p.name));
                    await this.reorderProviders(evt.oldDraggableIndex ?? evt.oldIndex, evt.newDraggableIndex ?? evt.newIndex);
                }
            });
        },

        async moveProvider(provider, delta) {
            const index = this.providers.findIndex(p => p.name === provider.name);
            await this.reorderProviders(index, index + delta);
        },
        async reorderProviders(from, to) {
            if (!this.canReorderProviders || !Number.isInteger(from) || !Number.isInteger(to)
                || from < 0 || to < 0 || from >= this.providers.length || to >= this.providers.length || from === to) return;
            this.providerReordering = true;
            const previousProviders = this.providers.map(provider => ({ ...provider }));
            const client = this.selectedClient;
            this.applyLocalProviderReorder(from, to);
            await this.afterProviderRender();
            try {
                await this.apiCall(`/api/providers/${client}/_reorder`, {
                    method: 'PUT', body: JSON.stringify({ providers: this.providers.map(p => p.name) })
                }, true);
            } catch (error) {
                this.providers = previousProviders;
                await this.afterProviderRender();
            } finally {
                this.providerReordering = false;
            }
        },

        syncSortableDomOrder(order) {
            const list = document.getElementById('providers-list');
            if (!list || !Array.isArray(order)) return;
            // Sortable.sort() indexes all children against its filtered toArray(),
            // so Alpine's non-draggable <template> shifts its node lookup by one.
            // Move the existing keyed rows directly, keeping Alpine's nodes intact.
            const rows = new Map(Array.from(list.children)
                .filter(row => row.matches('.provider-row'))
                .map(row => [row.getAttribute('data-id'), row]));
            for (const name of order) {
                const row = rows.get(name);
                if (row) list.appendChild(row);
            }
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
            const client = this.selectedClient;
            const generation = ++this.providerLoadGeneration;
            this.providerLoadBusy = true;
            this.providerLoadError = '';
            try {
                const [providers, clientCfg] = await Promise.all([
                    this.apiCall(`/api/providers/${client}`, {}, background),
                    this.apiCall(`/api/client-config/${client}`, {}, true)
                ]);
                if (generation !== this.providerLoadGeneration || client !== this.selectedClient) return;
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
                if (generation !== this.providerLoadGeneration || client !== this.selectedClient) return;
                console.error('Failed to load providers:', error);
                this.providerLoadError = error.message || this.t('providers.loadFailed');
            } finally {
                if (generation === this.providerLoadGeneration) this.providerLoadBusy = false;
            }
        },

        async moveClient(direction) {
            const index = this.clientOptions.findIndex(client => client.value === this.selectedClient);
            const next = this.nextChoiceIndex(index, this.clientOptions.length, direction);
            await this.selectClient(this.clientOptions[next].value);
            this.$nextTick(() => document.querySelector(`[data-client="${this.selectedClient}"]`)?.focus());
        },

        async selectClient(clientType) {
            clientType = this.canonicalClientType(clientType);
            if (this.providerMutationBusy || this.selectedClient === clientType) {
                this.syncHash();
                return;
            }
            if (!await this.requestCloseProvider() || !await this.closeEnvironment()) {
                this.syncHash();
                return;
            }
            this.providerSearch = '';
            this.selectedProviderName = '';
            this.providerDetailOpen = false;
            this.providers = [];
            this.selectedClient = clientType;
            await Promise.all([
                this.loadProviders(),
                this.loadOAuthProviders(true)
            ]);
            this.$nextTick(() => {
                this.initSortable();
            });
        },

        async saveClientConfig(successToast = null, nextConfig = this.clientConfig) {
            if (!this.canMutateProviders) return;
            this.clientConfigSaving = true;
            const client = this.selectedClient;
            const previousConfig = this.clientConfig;
            try {
                await this.apiCall(`/api/client-config/${client}`, {
                    method: 'PUT',
                    body: JSON.stringify(nextConfig)
                }, true);
                // A deletion or another load may have completed while this response
                // was in flight. Read the current server state instead of restoring
                // a stale pin and announcing a mode that is no longer active.
                if (this.clientConfig !== previousConfig) {
                    await this.loadProviders(true);
                    await this.refreshStatus();
                    return;
                }
                this.clientConfig = nextConfig;
                if (successToast && (successToast.title || successToast.message)) {
                    this.showAlert('success', successToast.message || '', successToast.title || '');
                }
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to save client config:', error);
            } finally {
                this.clientConfigSaving = false;
            }
        },

        async setClientMode(mode) {
            if (!this.canMutateProviders) return;
            const m = String(mode || '').toLowerCase();
            if (m !== 'auto' && m !== 'manual') return;

            if (m === 'manual' && !this.hasEnabledProviders) {
                this.showAlert('error', this.t('providers.enableBeforeManual'));
                return;
            }

            const nextConfig = { ...this.clientConfig, mode: m };
            if (m === 'auto') {
                nextConfig.pinned_provider = '';
            } else {
                // Default pin: prefer current provider, else highest priority enabled provider.
                const pinned = String(nextConfig.pinned_provider || '').trim();
                const pinnedProvider = pinned ? (this.providers || []).find(p => p && p.name === pinned) : null;
                const pinnedOk = pinnedProvider && !!pinnedProvider.enabled;

                if (!pinnedOk) {
                    const st = (this.status && this.status.clients) ? this.status.clients[this.selectedClient] : null;
                    const cur = st ? String(st.current_provider || '').trim() : '';
                    const curProvider = cur ? (this.providers || []).find(p => p && p.name === cur) : null;

                    if (curProvider && curProvider.name && !!curProvider.enabled) {
                        nextConfig.pinned_provider = curProvider.name;
                    } else {
                        const enabled = (this.providers || []).filter(p => p && p.name && !!p.enabled);
                        enabled.sort((a, b) => Number(a.priority || 0) - Number(b.priority || 0));
                        nextConfig.pinned_provider = (enabled[0] && enabled[0].name) ? enabled[0].name : '';
                    }
                }
            }
            const client = this.providerToastClientLabel();
            const pinned = String(nextConfig.pinned_provider || '').trim();
            if (m === 'auto') {
                await this.saveClientConfig({
                    title: this.tf('providers.switchedToAutoTitle', { client }),
                    message: this.t('providers.switchedToAutoMessage')
                }, nextConfig);
                return;
            }
            await this.saveClientConfig({
                title: this.tf('providers.switchedToManualTitle', { client }),
                message: pinned
                    ? this.tf('providers.switchedToManualMessagePinned', { provider: pinned })
                    : this.t('providers.switchedToManualMessage')
            }, nextConfig);
        },

        async pinProvider(name) {
            if (!this.canMutateProviders) return;
            const v = String(name || '').trim();
            if (!v) return;

            const p = (this.providers || []).find(x => x && x.name === v);
            if (p && p.enabled === false) {
                this.showAlert('error', this.t('providers.enableBeforePinning'));
                return;
            }

            const nextConfig = { ...this.clientConfig, mode: 'manual', pinned_provider: v };
            const client = this.providerToastClientLabel();
            await this.saveClientConfig({
                title: this.tf('providers.pinnedTitle', { client, provider: v }),
                message: this.t('providers.pinnedMessage')
            }, nextConfig);
        },

        async toggleProvider(provider, event) {
            if (!this.canMutateProviders) {
                event.target.checked = provider.enabled;
                return;
            }
            const oldEnabled = provider.enabled;
            const newEnabled = !!event.target.checked;
            provider.enabled = newEnabled;
            this.providerUpdating = true;

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
            } finally {
                this.providerUpdating = false;
            }
        },

        async saveProvider() {
            if (!this.canMutateProviders) return;
            try {
                if (!this.showEditProviderModal && this.providerFormUsesOAuth()) {
                    await this.startOAuthProviderAuthorization();
                    return;
                }
                this.providerSaving = true;

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
            } finally {
                this.providerSaving = false;
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
                request_environment: { country: this.providerForm.request_country ?? 'US', timezone: this.providerForm.request_timezone ?? 'America/New_York', source: this.providerForm.request_environment_source || 'default' },
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
            if (!this.canMutateProviders) return;
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
            this.providerFormSnapshot = JSON.stringify(this.providerForm);
            this.showEditProviderModal = true;
        },

        async deleteProvider(provider) {
            if (!this.canMutateProviders) return;
            const item = provider || {};
            const name = String(item.name || '').trim();
            if (!name) {
                return;
            }
            const displayName = this.providerDisplayName(item) || name;
            const keyCount = this.providerUsesOAuth(item) ? 0 : Number(item.key_count) || 0;
            const confirmed = await this.askConfirm({
                title: this.t('providers.delete'),
                message: this.tf('providers.deleteConfirm', { name: displayName }),
                detail: keyCount > 0 ? this.tf('providers.deleteKeysNote', { count: keyCount }) : '',
                confirmLabel: this.t('providers.delete'),
                danger: true
            });
            if (!confirmed || !this.canMutateProviders) {
                return;
            }

            this.providerUpdating = true;
            try {
                await this.apiCall(
                    `/api/providers/${this.selectedClient}/${encodeURIComponent(name)}`,
                    { method: 'DELETE' },
                    true
                );
                this.showAlert(
                    'success',
                    this.tf('providers.deletedMessage', { client: this.providerToastClientLabel() }),
                    this.tf('providers.deletedTitle', { name: displayName })
                );
                // The inspector was showing this provider: return to the list rather than
                // silently showing whichever provider is now first.
                if (this.selectedProviderName === name) {
                    this.selectedProviderName = '';
                    this.providerDetailOpen = false;
                }
                await this.loadProviders();
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to delete provider:', error);
            } finally {
                this.providerUpdating = false;
            }
        },

        openAddProviderModal() {
            if (!this.canMutateProviders) return;
            const maxPriority = this.providers.reduce((max, p) => {
                const pr = typeof p.priority === 'number' ? p.priority : 0;
                return pr > max ? pr : max;
            }, 0);
            this.cancelOAuthAuthorization(true);
            this.providerForm = {
                auth_type: 'api_key',
                oauth_provider: this.defaultOAuthProviderValue(),
                oauth_ref: '',
                request_country: 'US',
                request_timezone: 'America/New_York',
                request_environment_source: 'default',
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
            this.providerFormSnapshot = JSON.stringify(this.providerForm);
            this.showAddProviderModal = true;
        },

        // Global Config
        async loadGlobalConfig() {
            if (this.globalLoading || this.globalSaving) return;
            this.globalLoading = true;
            try {
                const cfg = await this.apiCall('/api/config/global');
                this.globalConfig = this.withDefaultGlobalConfig(cfg);
                this.savedAllowRemoteWebUI = !!this.globalConfig.allow_remote_web_ui;
                this.savedGlobalConfig = JSON.parse(JSON.stringify(this.globalConfig));
                this.globalLoadError = '';
            } catch (error) {
                this.globalLoadError = error.message || this.t('settings.loadFailed');
                console.error('Failed to load global config:', error);
            } finally {
                this.globalLoading = false;
            }
        },

        async saveGlobalConfig() {
            if (this.globalSaving || this.globalLoading) return;
            if (this.globalConfig.allow_remote_web_ui && !this.savedAllowRemoteWebUI) {
                const ok = await this.askConfirm({
                    title: this.t('settings.remoteWebUIConfirmTitle'),
                    message: this.t('settings.remoteWebUIConfirm'),
                    confirmLabel: this.t('settings.remoteWebUIConfirmAction'),
                    danger: true
                });
                if (!ok) return;
            }
            this.globalSaving = true;
            try {
                // Snapshot at submit time: edits made while the request is in flight stay unsaved.
                const submitted = JSON.parse(JSON.stringify(this.globalConfig));
                const payload = {
                    ...submitted,
                    upstream_proxy_mode: this.normalizeGlobalProxyMode(submitted.upstream_proxy_mode)
                };
                if (payload.upstream_proxy_mode !== 'custom') {
                    payload.upstream_proxy_url = '';
                }
                await this.apiCall('/api/config/global/update', {
                    method: 'PUT',
                    body: JSON.stringify(payload)
                }, true);
                const restartNeeded = !!this.savedGlobalConfig && (
                    this.savedGlobalConfig.listen_addr !== submitted.listen_addr
                    || this.savedGlobalConfig.port !== submitted.port
                );
                this.savedAllowRemoteWebUI = !!payload.allow_remote_web_ui;
                this.savedGlobalConfig = submitted;
                this.showAlert('success', this.t('settings.saveSuccess'));
                if (restartNeeded) {
                    this.showAlert('info', this.t('settings.restartNotice'));
                }
                await this.refreshStatus();
            } catch (error) {
                console.error('Failed to save global config:', error);
            } finally {
                this.globalSaving = false;
            }
        },

        async exportConfig() {
            const filename = 'clipal-data.json';
            try {
                const saveFile = typeof window.showSaveFilePicker === 'function'
                    ? await window.showSaveFilePicker({
                        suggestedName: filename,
                        types: [{
                            description: this.t('settings.exportFileType'),
                            accept: { 'application/json': ['.json'] }
                        }]
                    })
                    : null;
                const response = await fetch('/api/data/export');
                if (!response.ok) {
                    throw new Error(`HTTP ${response.status}`);
                }
                const blob = await response.blob();
                if (saveFile) {
                    const writable = await saveFile.createWritable();
                    try {
                        await writable.write(blob);
                        await writable.close();
                    } catch (error) {
                        if (typeof writable.abort === 'function') {
                            await writable.abort();
                        }
                        throw error;
                    }
                    this.showAlert('success', this.tf('settings.exportSaved', { filename }));
                    return;
                }
                const url = window.URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = filename;
                document.body.appendChild(a);
                a.click();
                window.URL.revokeObjectURL(url);
                document.body.removeChild(a);
                this.showAlert('success', this.tf('settings.exportDownloadStarted', { filename }));
            } catch (error) {
                if (error && error.name === 'AbortError') {
                    return;
                }
                this.showAlert('error', this.t('settings.exportFailure'));
                console.error('Failed to export config:', error);
            }
        },

        async selectDataImportFiles(event) {
            if (this.dataImportBusy) return;
            this.dataImportBusy = 'reading';
            const files = Array.from((event && event.target && event.target.files) || []);
            try {
                this.dataImportFiles = await Promise.all(files.map(async file => ({
                    name: file.name,
                    data: await file.text()
                })));
                this.dataImportPlan = null;
                this.showDataImportPreviewModal = false;
                this.dataImportMode = '';
            } catch (error) {
                this.dataImportFiles = [];
                this.dataImportPlan = null;
                this.showAlert('error', this.t('settings.importFailure'));
            } finally {
                this.dataImportBusy = '';
            }
        },

        triggerDataImportPicker() {
            if (this.dataImportBusy) return;
            const input = this.$refs && this.$refs.dataImportInput;
            if (!input || typeof input.click !== 'function') {
                this.showAlert('error', this.t('settings.importFilePickerUnavailable'));
                return;
            }
            input.value = '';
            input.click();
        },

        dataImportFilesLabel() {
            const names = (this.dataImportFiles || []).map(file => String((file && file.name) || '').trim())
                .filter(Boolean);
            return names.length ? names.join(', ') : this.t('settings.importFilesHint');
        },

        dataImportChangeSummary(change) {
            const value = change || {};
            return this.tf('settings.importChangeSummary', {
                added: Number(value.added || 0),
                updated: Number(value.updated || 0),
                removed: Number(value.removed || 0)
            });
        },

        dataImportConfigurationSummary() {
            const configuration = String((this.dataImportPlan && this.dataImportPlan.changes && this.dataImportPlan.changes.configuration) || 'credentials_only');
            if (configuration === 'replace') return this.t('settings.importConfigReplace');
            if (configuration === 'merge') return this.t('settings.importConfigMerge');
            return this.t('settings.importConfigCredentialsOnly');
        },

        closeDataImportPreview() {
            if (this.dataImportBusy) return;
            this.showDataImportPreviewModal = false;
            this.dataImportPlan = null;
        },

        dataImportPayload() {
            const payload = {
                files: this.dataImportFiles,
                format: 'auto',
                mode: this.dataImportMode
            };
            if (this.dataImportPlan && this.dataImportPlan.id) {
                payload.plan_id = this.dataImportPlan.id;
            }
            return payload;
        },

        async previewDataImport() {
            if (this.dataImportBusy || !this.dataImportFiles.length) return;
            this.dataImportBusy = 'preview';
            try {
                this.dataImportPlan = await this.apiCall('/api/data/import/preview', {
                    method: 'POST',
                    body: JSON.stringify(this.dataImportPayload())
                }, false, true);
                this.dataImportMode = this.dataImportPlan.mode || this.dataImportMode;
                this.showDataImportPreviewModal = true;
            } catch (error) {
                this.dataImportPlan = null;
                this.showDataImportPreviewModal = false;
                const message = String((error && error.message) || '').trim() || this.t('settings.importFailure');
                this.showAlert('error', message);
            } finally {
                this.dataImportBusy = '';
            }
        },

        async applyDataImport() {
            if (this.dataImportBusy || !this.dataImportPlan) return;
            this.dataImportBusy = 'apply';
            try {
                const result = await this.apiCall('/api/data/import/apply', {
                    method: 'POST',
                    body: JSON.stringify(this.dataImportPayload())
                }, false, true);
                this.showAlert('success', this.tf('settings.importApplied', {
                    credentials: result.credentials || 0,
                    providers: result.providers || 0,
                    usage: result.usage_providers || 0
                }));
                this.dataImportPlan = null;
                this.showDataImportPreviewModal = false;
                await this.loadGlobalConfig();
                await this.loadProviders();
                await this.refreshStatus();
            } catch (error) {
                const message = String((error && error.message) || '').trim() || this.t('settings.importFailure');
                if (message.includes('preview again')) {
                    this.dataImportPlan = null;
                    this.showDataImportPreviewModal = false;
                }
                this.showAlert('error', message);
            } finally {
                this.dataImportBusy = '';
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
            this.providerFormSnapshot = null;
            this.cancelOAuthAuthorization(false);
            this.showAddProviderModal = false;
            this.showEditProviderModal = false;
            this.providerForm = {
                auth_type: 'api_key',
                oauth_provider: this.defaultOAuthProviderValue(),
                oauth_ref: '',
                request_country: 'US',
                request_timezone: 'America/New_York',
                request_environment_source: 'default',
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
