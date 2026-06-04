package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/logger"
	"github.com/lansespirit/Clipal/internal/notify"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"golang.org/x/net/http/httpproxy"
)

var (
	loggerSetLevelFunc      = logger.SetLevel
	notifyConfigureFunc     = notify.Configure
	detectAuthCarrierFuncMu sync.RWMutex
	detectAuthCarrierFunc   = detectAuthCarrier
)

func registerExactAndSubtree(mux *http.ServeMux, path string, h http.HandlerFunc) {
	if mux == nil || h == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	mux.HandleFunc(path, h)
	if !strings.HasSuffix(path, "/") {
		mux.HandleFunc(path+"/", h)
	}
}

// ClientType represents the type of CLI client
type ClientType string

const (
	ClientClaude ClientType = "claude"
	ClientOpenAI ClientType = "openai"
	ClientGemini ClientType = "gemini"
)

const defaultOAuthRefreshMaintainerTimeout = 90 * time.Second

type ProviderSwitchEvent struct {
	At     time.Time
	From   string
	To     string
	Reason string
	Status int
}

type authCarrier string

const (
	authCarrierNone          authCarrier = "none"
	authCarrierAuthorization authCarrier = "authorization"
	authCarrierClaudeHeader  authCarrier = "x-api-key"
	authCarrierGeminiHeader  authCarrier = "x-goog-api-key"
	authCarrierQueryKey      authCarrier = "query:key"
	authCarrierQueryAPIKey   authCarrier = "query:api_key"
)

type RequestOutcomeEvent struct {
	At         time.Time
	Provider   string
	Status     int
	Delivery   string
	Protocol   string
	Capability string
	Cause      string
	Bytes      int
	Result     string
	Detail     string
}

type routingRuntimeSettings struct {
	explicitTTL            time.Duration
	cacheHintTTL           time.Duration
	dynamicFeatureTTL      time.Duration
	responseLookupTTL      time.Duration
	dynamicFeatureCapacity int
	busyRetryDelays        []time.Duration
	busyProbeMaxInFlight   int
	shortRetryAfterMax     time.Duration
	maxInlineWait          time.Duration
}

type upstreamProxyPolicyMode string

const (
	upstreamProxyPolicyEnvironment upstreamProxyPolicyMode = "environment"
	upstreamProxyPolicyDirect      upstreamProxyPolicyMode = "direct"
	upstreamProxyPolicyCustom      upstreamProxyPolicyMode = "custom"
)

type upstreamProxyPolicyKey struct {
	mode upstreamProxyPolicyMode
	url  string
}

// Router manages multiple client proxies
type Router struct {
	cfg        *config.Config
	configDir  string
	telemetry  *telemetry.Store
	oauth      *oauthpkg.Service
	proxies    map[ClientType]*ClientProxy
	server     *http.Server
	mu         sync.RWMutex
	reloadMu   sync.Mutex
	watchMu    sync.Mutex
	watchStop  chan struct{}
	watchDone  chan struct{}
	lastMod    map[string]time.Time
	watchEvery time.Duration

	oauthRefreshMu     sync.Mutex
	oauthRefreshStop   chan struct{}
	oauthRefreshDone   chan struct{}
	oauthRefreshCancel context.CancelFunc
	oauthRefreshEvery  time.Duration
}

// ConfigSnapshot returns the current in-memory config pointer.
// The returned config should be treated as immutable by callers.
func (r *Router) ConfigSnapshot() *config.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

func (r *Router) TelemetryStore() *telemetry.Store {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.telemetry
}

// ClientProxy handles requests for a specific client type
type ClientProxy struct {
	clientType            ClientType
	mode                  config.ClientMode
	pinnedProvider        string
	pinnedIndex           int
	providers             []config.Provider
	providerKeys          [][]string
	currentIndex          int
	countTokensIndex      int
	responsesIndex        int
	geminiStreamIndex     int
	currentKeyIndex       []int
	countTokensKeyIndex   []int
	responsesKeyIndex     []int
	geminiStreamKeyIndex  []int
	mu                    sync.RWMutex
	httpClient            *http.Client
	providerHTTPClients   []*http.Client
	providerProxyPolicies []upstreamProxyPolicyKey
	deactivated           []providerDeactivation
	keyDeactivated        [][]providerDeactivation
	providerBusy          []providerBusyState
	reactivateAfter       time.Duration
	upstreamIdle          time.Duration

	stickyBindings         map[string]stickyBinding
	responseLookup         map[string]stickyLookupEntry
	dynamicFeatureBindings map[string]stickyLookupEntry
	routing                routingRuntimeSettings
	breakers               []*circuitBreaker
	lastSwitch             ProviderSwitchEvent
	lastRequest            RequestOutcomeEvent
	telemetry              *telemetry.Store
	oauth                  *oauthpkg.Service
}

// Close releases resources held by the ClientProxy.
func (cp *ClientProxy) Close() {
	seen := make(map[*http.Client]struct{}, len(cp.providerHTTPClients)+1)
	closeClient := func(client *http.Client) {
		if client == nil {
			return
		}
		if _, ok := seen[client]; ok {
			return
		}
		seen[client] = struct{}{}
		client.CloseIdleConnections()
	}
	closeClient(cp.httpClient)
	for _, client := range cp.providerHTTPClients {
		closeClient(client)
	}
}

// NewRouter creates a new Router instance
func NewRouter(cfg *config.Config) *Router {
	durations, err := cfg.Global.RuntimeDurations()
	if err != nil {
		durations = config.DefaultRuntimeDurations()
		logger.Warn("invalid runtime durations; defaulting to reactivate_after=1h upstream_idle_timeout=3m response_header_timeout=2m: %v", err)
	}
	cbCfg := normalizeCircuitBreakerConfig(cfg.Global.CircuitBreaker)
	routingCfg := routingRuntimeSettingsFromConfig(cfg.Global.Routing)
	telemetryStore, err := telemetry.NewStore(cfg.ConfigDir())
	if err != nil {
		logger.Warn("failed to load usage telemetry from %s: %v", cfg.ConfigDir(), err)
	}
	r := &Router{
		cfg:        cfg,
		configDir:  cfg.ConfigDir(),
		telemetry:  telemetryStore,
		oauth:      oauthpkg.NewService(cfg.ConfigDir()),
		proxies:    make(map[ClientType]*ClientProxy),
		lastMod:    make(map[string]time.Time),
		watchEvery: 5 * time.Second,

		oauthRefreshEvery: 5 * time.Minute,
	}

	// Initialize client proxies
	claudeProviders := config.GetEnabledProviders(cfg.Claude)
	if len(claudeProviders) > 0 {
		r.proxies[ClientClaude] = newClientProxyWithGlobalProxy(ClientClaude, cfg.Claude.Mode, cfg.Claude.PinnedProvider, claudeProviders, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, cfg.Global.NormalizedUpstreamProxyMode(), cfg.Global.EffectiveUpstreamProxyIdentity(), telemetryStore)
		r.proxies[ClientClaude].oauth = r.oauth
		r.proxies[ClientClaude].applyRoutingRuntimeSettings(routingCfg)
	}

	codexProviders := config.GetEnabledProviders(cfg.OpenAI)
	if len(codexProviders) > 0 {
		r.proxies[ClientOpenAI] = newClientProxyWithGlobalProxy(ClientOpenAI, cfg.OpenAI.Mode, cfg.OpenAI.PinnedProvider, codexProviders, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, cfg.Global.NormalizedUpstreamProxyMode(), cfg.Global.EffectiveUpstreamProxyIdentity(), telemetryStore)
		r.proxies[ClientOpenAI].oauth = r.oauth
		r.proxies[ClientOpenAI].applyRoutingRuntimeSettings(routingCfg)
	}

	geminiProviders := config.GetEnabledProviders(cfg.Gemini)
	if len(geminiProviders) > 0 {
		r.proxies[ClientGemini] = newClientProxyWithGlobalProxy(ClientGemini, cfg.Gemini.Mode, cfg.Gemini.PinnedProvider, geminiProviders, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, cfg.Global.NormalizedUpstreamProxyMode(), cfg.Global.EffectiveUpstreamProxyIdentity(), telemetryStore)
		r.proxies[ClientGemini].oauth = r.oauth
		r.proxies[ClientGemini].applyRoutingRuntimeSettings(routingCfg)
	}

	return r
}

func newClientProxy(clientType ClientType, mode config.ClientMode, pinnedProvider string, providers []config.Provider, reactivateAfter time.Duration, upstreamIdle time.Duration, responseHeaderTimeout time.Duration, cbCfg circuitBreakerConfig, telemetryStore ...*telemetry.Store) *ClientProxy {
	return newClientProxyWithGlobalProxy(clientType, mode, pinnedProvider, providers, reactivateAfter, upstreamIdle, responseHeaderTimeout, cbCfg, config.GlobalUpstreamProxyModeEnvironment, "", telemetryStore...)
}

func newClientProxyWithGlobalProxy(clientType ClientType, mode config.ClientMode, pinnedProvider string, providers []config.Provider, reactivateAfter time.Duration, upstreamIdle time.Duration, responseHeaderTimeout time.Duration, cbCfg circuitBreakerConfig, globalProxyMode config.GlobalUpstreamProxyMode, globalProxyURL string, telemetryStore ...*telemetry.Store) *ClientProxy {
	var store *telemetry.Store
	if len(telemetryStore) > 0 {
		store = telemetryStore[0]
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	sharedClient := newUpstreamHTTPClient(dialer, responseHeaderTimeout, http.ProxyFromEnvironment)
	pinnedIndex := -1
	pinnedProvider = strings.TrimSpace(pinnedProvider)
	if mode == config.ClientModeManual && pinnedProvider != "" {
		for i := range providers {
			if providers[i].Name == pinnedProvider {
				pinnedIndex = i
				break
			}
		}
	}

	breakers := make([]*circuitBreaker, len(providers))
	providerKeys := make([][]string, len(providers))
	currentKeyIndex := make([]int, len(providers))
	countTokensKeyIndex := make([]int, len(providers))
	responsesKeyIndex := make([]int, len(providers))
	geminiStreamKeyIndex := make([]int, len(providers))
	providerHTTPClients := make([]*http.Client, len(providers))
	providerProxyPolicies := make([]upstreamProxyPolicyKey, len(providers))
	policyClients := map[upstreamProxyPolicyKey]*http.Client{
		{mode: upstreamProxyPolicyEnvironment}: sharedClient,
	}
	keyDeactivated := make([][]providerDeactivation, len(providers))
	for i := range providers {
		breakers[i] = newCircuitBreaker(cbCfg)
		providerKeys[i] = providers[i].NormalizedAPIKeys()
		providerProxyPolicies[i] = effectiveProviderProxyPolicy(providers[i], globalProxyMode, globalProxyURL)
		if client, ok := policyClients[providerProxyPolicies[i]]; ok {
			providerHTTPClients[i] = client
		} else {
			client = newProviderHTTPClient(providerProxyPolicies[i], providers[i].Name, sharedClient, dialer, responseHeaderTimeout)
			policyClients[providerProxyPolicies[i]] = client
			providerHTTPClients[i] = client
		}
		if len(providerKeys[i]) == 0 {
			providerKeys[i] = []string{""}
		}
		keyDeactivated[i] = make([]providerDeactivation, len(providerKeys[i]))
	}
	return &ClientProxy{
		clientType:     clientType,
		mode:           mode,
		pinnedProvider: pinnedProvider,
		pinnedIndex:    pinnedIndex,
		providers:      providers,
		providerKeys:   providerKeys,
		currentIndex: func() int {
			if pinnedIndex >= 0 {
				return pinnedIndex
			}
			return 0
		}(),
		countTokensIndex: func() int {
			if pinnedIndex >= 0 {
				return pinnedIndex
			}
			return 0
		}(),
		responsesIndex: func() int {
			if pinnedIndex >= 0 {
				return pinnedIndex
			}
			return 0
		}(),
		geminiStreamIndex: func() int {
			if pinnedIndex >= 0 {
				return pinnedIndex
			}
			return 0
		}(),
		currentKeyIndex:        currentKeyIndex,
		countTokensKeyIndex:    countTokensKeyIndex,
		responsesKeyIndex:      responsesKeyIndex,
		geminiStreamKeyIndex:   geminiStreamKeyIndex,
		telemetry:              store,
		providerHTTPClients:    providerHTTPClients,
		providerProxyPolicies:  providerProxyPolicies,
		deactivated:            make([]providerDeactivation, len(providers)),
		keyDeactivated:         keyDeactivated,
		providerBusy:           make([]providerBusyState, len(providers)),
		reactivateAfter:        reactivateAfter,
		upstreamIdle:           upstreamIdle,
		stickyBindings:         make(map[string]stickyBinding),
		responseLookup:         make(map[string]stickyLookupEntry),
		dynamicFeatureBindings: make(map[string]stickyLookupEntry),
		routing:                defaultRoutingRuntimeSettings(),
		breakers:               breakers,
		httpClient:             sharedClient,
	}
}

func newUpstreamHTTPClient(dialer *net.Dialer, responseHeaderTimeout time.Duration, proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 proxy,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: responseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			// Keep response bytes unchanged unless the client explicitly asks for compression.
			DisableCompression: true,
		},
	}
}

func NewOAuthHTTPClientForProvider(provider config.Provider, global config.GlobalConfig) *http.Client {
	durations, err := global.RuntimeDurations()
	if err != nil {
		durations = config.DefaultRuntimeDurations()
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	sharedClient := newUpstreamHTTPClient(dialer, durations.ResponseHeaderTimeout, http.ProxyFromEnvironment)
	policy := effectiveProviderProxyPolicy(provider, global.NormalizedUpstreamProxyMode(), global.EffectiveUpstreamProxyIdentity())
	return newProviderHTTPClient(policy, provider.Name, sharedClient, dialer, durations.ResponseHeaderTimeout)
}

func OAuthProviderUsesEnvironmentProxy(provider config.OAuthProvider) bool {
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	for _, rawURL := range oauthProviderEnvironmentProxyProbeURLs(provider) {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			continue
		}
		proxyURL, err := proxyFunc(req.URL)
		if err == nil && proxyURL != nil {
			return true
		}
	}
	return false
}

func oauthProviderEnvironmentProxyProbeURLs(provider config.OAuthProvider) []string {
	switch config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case config.OAuthProviderCodex:
		return []string{
			"https://auth.openai.com/oauth/token",
			"https://chatgpt.com/backend-api/wham/usage",
		}
	case config.OAuthProviderClaude:
		return []string{"https://platform.claude.com/v1/oauth/token"}
	case config.OAuthProviderGemini:
		return []string{
			"https://oauth2.googleapis.com/token",
			"https://www.googleapis.com/oauth2/v1/userinfo",
			"https://cloudcode-pa.googleapis.com",
		}
	default:
		return nil
	}
}

func effectiveProviderProxyPolicy(provider config.Provider, globalMode config.GlobalUpstreamProxyMode, globalURL string) upstreamProxyPolicyKey {
	switch provider.NormalizedProxyMode() {
	case config.ProviderProxyModeDirect:
		return upstreamProxyPolicyKey{mode: upstreamProxyPolicyDirect}
	case config.ProviderProxyModeCustom:
		return upstreamProxyPolicyKey{mode: upstreamProxyPolicyCustom, url: provider.EffectiveProxyIdentity()}
	default:
		switch globalMode {
		case config.GlobalUpstreamProxyModeDirect:
			return upstreamProxyPolicyKey{mode: upstreamProxyPolicyDirect}
		case config.GlobalUpstreamProxyModeCustom:
			return upstreamProxyPolicyKey{mode: upstreamProxyPolicyCustom, url: globalURL}
		default:
			return upstreamProxyPolicyKey{mode: upstreamProxyPolicyEnvironment}
		}
	}
}

func newProviderHTTPClient(policy upstreamProxyPolicyKey, providerName string, sharedClient *http.Client, dialer *net.Dialer, responseHeaderTimeout time.Duration) *http.Client {
	switch policy.mode {
	case upstreamProxyPolicyDirect:
		return newUpstreamHTTPClient(dialer, responseHeaderTimeout, nil)
	case upstreamProxyPolicyCustom:
		proxyURL, err := config.ParseProxyURL(policy.url)
		if err != nil {
			logger.Warn("invalid custom proxy for provider %s; falling back to environment proxy settings", providerName)
			return sharedClient
		}
		return newUpstreamHTTPClient(dialer, responseHeaderTimeout, http.ProxyURL(proxyURL))
	default:
		return sharedClient
	}
}

func (cp *ClientProxy) upstreamHTTPClient(providerIndex int) *http.Client {
	if cp == nil {
		return nil
	}
	if providerIndex >= 0 && providerIndex < len(cp.providerHTTPClients) && cp.providerHTTPClients[providerIndex] != nil {
		return cp.providerHTTPClients[providerIndex]
	}
	return cp.httpClient
}

func (cp *ClientProxy) applyRoutingRuntimeSettings(settings routingRuntimeSettings) {
	if cp == nil {
		return
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.routing = settings
}

func defaultRoutingRuntimeSettings() routingRuntimeSettings {
	return routingRuntimeSettings{
		explicitTTL:            30 * time.Minute,
		cacheHintTTL:           10 * time.Minute,
		dynamicFeatureTTL:      10 * time.Minute,
		responseLookupTTL:      15 * time.Minute,
		dynamicFeatureCapacity: 1024,
		busyRetryDelays:        []time.Duration{5 * time.Second, 10 * time.Second},
		busyProbeMaxInFlight:   1,
		shortRetryAfterMax:     shortBusyRetryAfterMax,
		maxInlineWait:          8 * time.Second,
	}
}

func routingRuntimeSettingsFromConfig(cfg config.RoutingConfig) routingRuntimeSettings {
	out := defaultRoutingRuntimeSettings()

	if d, err := time.ParseDuration(strings.TrimSpace(cfg.StickySessions.ExplicitTTL)); err == nil && d > 0 {
		out.explicitTTL = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.StickySessions.CacheHintTTL)); err == nil && d > 0 {
		out.cacheHintTTL = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.StickySessions.DynamicFeatureTTL)); err == nil && d > 0 {
		out.dynamicFeatureTTL = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.StickySessions.ResponseLookupTTL)); err == nil && d > 0 {
		out.responseLookupTTL = d
	}
	if cfg.StickySessions.DynamicFeatureCapacity > 0 {
		out.dynamicFeatureCapacity = cfg.StickySessions.DynamicFeatureCapacity
	}
	if cfg.BusyBackpressure.ProbeMaxInFlight > 0 {
		out.busyProbeMaxInFlight = cfg.BusyBackpressure.ProbeMaxInFlight
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.BusyBackpressure.ShortRetryAfterMax)); err == nil && d > 0 {
		out.shortRetryAfterMax = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.BusyBackpressure.MaxInlineWait)); err == nil && d > 0 {
		out.maxInlineWait = d
	}
	if len(cfg.BusyBackpressure.RetryDelays) > 0 {
		delays := make([]time.Duration, 0, len(cfg.BusyBackpressure.RetryDelays))
		for _, raw := range cfg.BusyBackpressure.RetryDelays {
			if d, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil && d > 0 {
				delays = append(delays, d)
			}
		}
		if len(delays) > 0 {
			out.busyRetryDelays = delays
		}
	}

	return out
}

// Start starts the proxy server
func (r *Router) Start(version string, webHandler interface{}) error {
	port := r.cfg.Global.Port

	mux := http.NewServeMux()

	// Register web management interface routes if provided
	hasWebUI := false
	if webHandler != nil {
		if wh, ok := webHandler.(interface{ RegisterRoutes(*http.ServeMux) }); ok {
			wh.RegisterRoutes(mux)
			hasWebUI = true
		}
	}

	// Proxy endpoints
	registerExactAndSubtree(mux, "/clipal", r.handleRequest)
	registerExactAndSubtree(mux, "/claude", r.handleRequest)
	registerExactAndSubtree(mux, "/openai", r.handleRequest)
	registerExactAndSubtree(mux, "/gemini", r.handleRequest)
	registerExactAndSubtree(mux, "/claudecode", r.handleRequest)
	registerExactAndSubtree(mux, "/codex", r.handleRequest)
	mux.HandleFunc("/health", r.handleHealth)

	addr := net.JoinHostPort(r.cfg.Global.ListenAddr, strconv.Itoa(port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	r.mu.Lock()
	r.server = srv
	r.mu.Unlock()

	logger.Info("clipal starting on %s", addr)
	if hasWebUI {
		// The web UI is localhost-only (enforced by the web handler).
		logger.Info("web management interface available at http://localhost:%d/ (localhost only)", port)
	}

	// Log loaded providers
	for clientType, proxy := range r.proxies {
		logger.Info("loaded %d providers for %s", len(proxy.providers), clientType)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	r.startProviderConfigWatcher()
	r.startOAuthRefreshMaintainer()

	err = srv.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		r.stopOAuthRefreshMaintainer()
		r.stopProviderConfigWatcher()
	}
	return err
}

// Stop gracefully stops the proxy server
func (r *Router) Stop() error {
	r.stopOAuthRefreshMaintainer()
	r.stopProviderConfigWatcher()
	r.mu.RLock()
	srv := r.server
	r.mu.RUnlock()
	var shutdownErr error
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr = srv.Shutdown(ctx)
	}
	var flushErr error
	if r.telemetry != nil {
		flushErr = r.telemetry.Close()
		if flushErr != nil {
			logger.Warn("failed to flush usage telemetry: %v", flushErr)
		}
	}
	return errors.Join(shutdownErr, flushErr)
}

func (r *Router) startProviderConfigWatcher() {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()

	if r.configDir == "" || r.watchEvery <= 0 {
		return
	}
	if r.watchStop != nil {
		return
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	r.watchStop = stopCh
	r.watchDone = doneCh

	// Initialize snapshot so we don't reload immediately on start.
	r.snapshotProviderConfigModTimes()

	go func(stopCh <-chan struct{}, doneCh chan struct{}) {
		defer close(doneCh)
		ticker := time.NewTicker(r.watchEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.reloadIfProviderConfigsChanged()
				r.sweepReactivations()
			case <-stopCh:
				return
			}
		}
	}(stopCh, doneCh)
}

func (r *Router) stopProviderConfigWatcher() {
	r.watchMu.Lock()
	stopCh := r.watchStop
	doneCh := r.watchDone
	r.watchMu.Unlock()

	if stopCh == nil {
		return
	}

	select {
	case <-stopCh:
		// already closed
	default:
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}

	r.watchMu.Lock()
	if r.watchStop == stopCh {
		r.watchStop = nil
		r.watchDone = nil
	}
	r.watchMu.Unlock()
}

func (r *Router) startOAuthRefreshMaintainer() {
	if r == nil || r.oauth == nil || r.oauthRefreshEvery <= 0 {
		return
	}

	r.oauthRefreshMu.Lock()
	defer r.oauthRefreshMu.Unlock()
	if r.oauthRefreshStop != nil {
		return
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	r.oauthRefreshStop = stopCh
	r.oauthRefreshDone = doneCh
	r.oauthRefreshCancel = cancel

	go func(ctx context.Context, stopCh <-chan struct{}, doneCh chan struct{}) {
		defer close(doneCh)
		r.refreshDueOAuthProvidersWithContext(ctx)
		ticker := time.NewTicker(r.oauthRefreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.refreshDueOAuthProvidersWithContext(ctx)
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}(ctx, stopCh, doneCh)
}

func (r *Router) stopOAuthRefreshMaintainer() {
	if r == nil {
		return
	}
	r.oauthRefreshMu.Lock()
	stopCh := r.oauthRefreshStop
	doneCh := r.oauthRefreshDone
	cancel := r.oauthRefreshCancel
	r.oauthRefreshMu.Unlock()

	if stopCh == nil {
		return
	}

	if cancel != nil {
		cancel()
	}
	select {
	case <-stopCh:
	default:
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}

	r.oauthRefreshMu.Lock()
	if r.oauthRefreshStop == stopCh {
		r.oauthRefreshStop = nil
		r.oauthRefreshDone = nil
		r.oauthRefreshCancel = nil
	}
	r.oauthRefreshMu.Unlock()
}

func (r *Router) refreshDueOAuthProviders() {
	r.refreshDueOAuthProvidersWithContext(context.Background())
}

func (r *Router) refreshDueOAuthProvidersWithContext(ctx context.Context) {
	if r == nil || r.oauth == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.RLock()
	snapshot := make(map[ClientType]*ClientProxy, len(r.proxies))
	for clientType, proxy := range r.proxies {
		snapshot[clientType] = proxy
	}
	r.mu.RUnlock()

	for clientType, proxy := range snapshot {
		if ctx.Err() != nil {
			return
		}
		if proxy == nil {
			continue
		}
		for index, provider := range proxy.providers {
			if ctx.Err() != nil {
				return
			}
			if !provider.UsesOAuth() {
				continue
			}
			oauthProvider := provider.NormalizedOAuthProvider()
			ref := provider.NormalizedOAuthRef()
			if oauthProvider == "" || ref == "" {
				continue
			}
			refreshCtx, cancel := context.WithTimeout(ctx, defaultOAuthRefreshMaintainerTimeout)
			_, err := r.oauth.RefreshIfNeededWithHTTPClient(refreshCtx, oauthProvider, ref, proxy.oauthHTTPClientForProvider(provider, index))
			cancel()
			if err != nil {
				logger.Warn("[%s] oauth background refresh failed for %s: %v", clientType, provider.Name, err)
			}
		}
	}
}

func (r *Router) providerConfigFiles() []string {
	// config.yaml carries global runtime knobs (log level, failover policy, body limit, etc.)
	// and should be hot-reloaded together with provider configs.
	return config.WatchedConfigFilenames()
}

func (r *Router) snapshotProviderConfigModTimes() {
	r.lastMod = make(map[string]time.Time, len(r.providerConfigFiles()))
	for _, name := range r.providerConfigFiles() {
		path := filepath.Join(r.configDir, name)
		if fi, err := os.Stat(path); err == nil {
			r.lastMod[path] = fi.ModTime()
		} else {
			delete(r.lastMod, path)
		}
	}
}

func (r *Router) providerConfigModTimesSnapshot() map[string]time.Time {
	out := make(map[string]time.Time, len(r.providerConfigFiles()))
	for _, name := range r.providerConfigFiles() {
		path := filepath.Join(r.configDir, name)
		if fi, err := os.Stat(path); err == nil {
			out[path] = fi.ModTime()
		}
	}
	return out
}

func configModTimesChanged(prev map[string]time.Time, next map[string]time.Time) bool {
	if len(prev) != len(next) {
		return true
	}
	for path, prevTime := range prev {
		nextTime, ok := next[path]
		if !ok || !nextTime.Equal(prevTime) {
			return true
		}
	}
	return false
}

// ReloadProviderConfigs forces a best-effort reload from disk.
// Intended for the web UI, so changes apply immediately.
func (r *Router) ReloadProviderConfigs() error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	if err := r.reloadProviderConfigsLocked(); err != nil {
		return err
	}
	r.snapshotProviderConfigModTimes()
	return nil
}

func (r *Router) reloadIfProviderConfigsChanged() {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	nextMod := r.providerConfigModTimesSnapshot()
	if !configModTimesChanged(r.lastMod, nextMod) {
		return
	}

	if err := r.reloadProviderConfigsLocked(); err != nil {
		logger.Warn("provider config reload failed: %v", err)
		return
	}
	r.lastMod = nextMod
}

func (r *Router) reloadProviderConfigsLocked() error {
	newCfg, err := config.Load(r.configDir)
	if err != nil {
		return err
	}

	// Keep listen settings stable at runtime, but allow log level and failover policy changes.
	r.mu.RLock()
	oldCfg := r.cfg
	currentGlobal := r.cfg.Global
	oldProxies := make(map[ClientType]*ClientProxy, len(r.proxies))
	for ct, p := range r.proxies {
		oldProxies[ct] = p
	}
	r.mu.RUnlock()

	newCfg.Global.ListenAddr = currentGlobal.ListenAddr
	newCfg.Global.Port = currentGlobal.Port

	if err := newCfg.Validate(); err != nil {
		return err
	}

	loggerSetLevelFunc(newCfg.Global.LogLevel)
	notifyConfigureFunc(newCfg.Global.Notifications)
	durations, err := newCfg.Global.RuntimeDurations()
	if err != nil {
		durations = config.DefaultRuntimeDurations()
		logger.Warn("invalid runtime durations; defaulting to reactivate_after=1h upstream_idle_timeout=3m response_header_timeout=2m: %v", err)
	}
	cbCfg := normalizeCircuitBreakerConfig(newCfg.Global.CircuitBreaker)
	globalProxyMode := newCfg.Global.NormalizedUpstreamProxyMode()
	globalProxyURL := newCfg.Global.EffectiveUpstreamProxyIdentity()

	newProxies := make(map[ClientType]*ClientProxy)
	if ps := config.GetEnabledProviders(newCfg.Claude); len(ps) > 0 {
		newProxies[ClientClaude] = newReloadedClientProxy(ClientClaude, newCfg.Claude.Mode, newCfg.Claude.PinnedProvider, ps, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, routingRuntimeSettingsFromConfig(newCfg.Global.Routing), globalProxyMode, globalProxyURL, oldProxies[ClientClaude], r.telemetry)
		newProxies[ClientClaude].oauth = r.oauth
	}
	if ps := config.GetEnabledProviders(newCfg.OpenAI); len(ps) > 0 {
		newProxies[ClientOpenAI] = newReloadedClientProxy(ClientOpenAI, newCfg.OpenAI.Mode, newCfg.OpenAI.PinnedProvider, ps, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, routingRuntimeSettingsFromConfig(newCfg.Global.Routing), globalProxyMode, globalProxyURL, oldProxies[ClientOpenAI], r.telemetry)
		newProxies[ClientOpenAI].oauth = r.oauth
	}
	if ps := config.GetEnabledProviders(newCfg.Gemini); len(ps) > 0 {
		newProxies[ClientGemini] = newReloadedClientProxy(ClientGemini, newCfg.Gemini.Mode, newCfg.Gemini.PinnedProvider, ps, durations.ReactivateAfter, durations.UpstreamIdleTimeout, durations.ResponseHeaderTimeout, cbCfg, routingRuntimeSettingsFromConfig(newCfg.Global.Routing), globalProxyMode, globalProxyURL, oldProxies[ClientGemini], r.telemetry)
		newProxies[ClientGemini].oauth = r.oauth
	}
	r.reconcileTelemetryUsage(oldCfg, newCfg)

	r.mu.Lock()
	r.cfg = newCfg
	r.proxies = newProxies
	r.mu.Unlock()

	// Close old proxies to release idle connections.
	for _, p := range oldProxies {
		p.Close()
	}

	logger.Info("configs reloaded from %s", r.configDir)
	logger.Info("runtime provider state preserved where possible across config reload")
	for ct, p := range newProxies {
		logger.Info("loaded %d providers for %s", len(p.providers), ct)
	}
	return nil
}

func newReloadedClientProxy(clientType ClientType, mode config.ClientMode, pinnedProvider string, providers []config.Provider, reactivateAfter time.Duration, upstreamIdle time.Duration, responseHeaderTimeout time.Duration, cbCfg circuitBreakerConfig, routing routingRuntimeSettings, globalProxyMode config.GlobalUpstreamProxyMode, globalProxyURL string, old *ClientProxy, telemetryStore *telemetry.Store) *ClientProxy {
	cp := newClientProxyWithGlobalProxy(clientType, mode, pinnedProvider, providers, reactivateAfter, upstreamIdle, responseHeaderTimeout, cbCfg, globalProxyMode, globalProxyURL, telemetryStore)
	cp.applyRoutingRuntimeSettings(routing)
	if old != nil {
		cp.inheritRuntimeState(old)
	}
	return cp
}

func (cp *ClientProxy) inheritRuntimeState(old *ClientProxy) {
	if cp == nil || old == nil {
		return
	}

	old.mu.RLock()
	defer old.mu.RUnlock()

	oldCurrentProvider := providerNameAtIndex(old.providers, old.currentIndex)
	oldCountTokensProvider := providerNameAtIndex(old.providers, old.countTokensIndex)
	oldResponsesProvider := providerNameAtIndex(old.providers, old.responsesIndex)
	oldGeminiStreamProvider := providerNameAtIndex(old.providers, old.geminiStreamIndex)

	oldByName := make(map[string]int, len(old.providers))
	for i := range old.providers {
		oldByName[old.providers[i].Name] = i
	}

	for newIdx := range cp.providers {
		oldIdx, ok := oldByName[cp.providers[newIdx].Name]
		if !ok {
			continue
		}
		if !sameProviderRuntimeIdentity(cp.providers[newIdx], cp.providerProxyPolicies[newIdx], old.providers[oldIdx], old.providerProxyPolicies[oldIdx]) {
			continue
		}
		cp.deactivated[newIdx] = old.deactivated[oldIdx]
		cp.providerBusy[newIdx] = old.providerBusy[oldIdx]
		inheritKeyState(cp, newIdx, old, oldIdx)
		inheritBreakerState(cp.breakers[newIdx], old.breakers[oldIdx])
	}

	newByOldIndex := make(map[int]int, len(cp.providers))
	for newIdx := range cp.providers {
		oldIdx, ok := oldByName[cp.providers[newIdx].Name]
		if !ok {
			continue
		}
		if !sameProviderRuntimeIdentity(cp.providers[newIdx], cp.providerProxyPolicies[newIdx], old.providers[oldIdx], old.providerProxyPolicies[oldIdx]) {
			continue
		}
		newByOldIndex[oldIdx] = newIdx
	}

	if oldCurrentProvider != "" && cp.mode != config.ClientModeManual {
		if idx := providerIndexByName(cp.providers, oldCurrentProvider); idx >= 0 {
			cp.currentIndex = idx
		}
	}
	if oldCountTokensProvider != "" && cp.mode != config.ClientModeManual {
		if idx := providerIndexByName(cp.providers, oldCountTokensProvider); idx >= 0 {
			cp.countTokensIndex = idx
		}
	}
	if oldResponsesProvider != "" && cp.mode != config.ClientModeManual {
		if idx := providerIndexByName(cp.providers, oldResponsesProvider); idx >= 0 {
			cp.responsesIndex = idx
		}
	}
	if oldGeminiStreamProvider != "" && cp.mode != config.ClientModeManual {
		if idx := providerIndexByName(cp.providers, oldGeminiStreamProvider); idx >= 0 {
			cp.geminiStreamIndex = idx
		}
	}

	cp.lastSwitch = old.lastSwitch
	cp.lastRequest = old.lastRequest
	inheritStickyRuntimeState(cp, old, newByOldIndex)
}

func inheritKeyState(dst *ClientProxy, dstProviderIndex int, src *ClientProxy, srcProviderIndex int) {
	if dst == nil || src == nil {
		return
	}
	if dstProviderIndex < 0 || dstProviderIndex >= len(dst.providerKeys) {
		return
	}
	if srcProviderIndex < 0 || srcProviderIndex >= len(src.providerKeys) {
		return
	}

	srcKeyIndexByValue := make(map[string]int, len(src.providerKeys[srcProviderIndex]))
	for i, key := range src.providerKeys[srcProviderIndex] {
		if _, ok := srcKeyIndexByValue[key]; !ok {
			srcKeyIndexByValue[key] = i
		}
	}

	for dstKeyIndex, key := range dst.providerKeys[dstProviderIndex] {
		srcKeyIndex, ok := srcKeyIndexByValue[key]
		if !ok {
			continue
		}
		if srcProviderIndex < len(src.keyDeactivated) && srcKeyIndex < len(src.keyDeactivated[srcProviderIndex]) {
			dst.keyDeactivated[dstProviderIndex][dstKeyIndex] = src.keyDeactivated[srcProviderIndex][srcKeyIndex]
		}
		if srcProviderIndex < len(src.currentKeyIndex) &&
			src.currentKeyIndex[srcProviderIndex] == srcKeyIndex {
			dst.currentKeyIndex[dstProviderIndex] = dstKeyIndex
		}
		if srcProviderIndex < len(src.countTokensKeyIndex) &&
			src.countTokensKeyIndex[srcProviderIndex] == srcKeyIndex {
			dst.countTokensKeyIndex[dstProviderIndex] = dstKeyIndex
		}
		if srcProviderIndex < len(src.responsesKeyIndex) &&
			src.responsesKeyIndex[srcProviderIndex] == srcKeyIndex {
			dst.responsesKeyIndex[dstProviderIndex] = dstKeyIndex
		}
		if srcProviderIndex < len(src.geminiStreamKeyIndex) &&
			src.geminiStreamKeyIndex[srcProviderIndex] == srcKeyIndex {
			dst.geminiStreamKeyIndex[dstProviderIndex] = dstKeyIndex
		}
	}
}

func inheritBreakerState(dst *circuitBreaker, src *circuitBreaker) {
	if dst == nil || src == nil {
		return
	}

	src.mu.Lock()
	defer src.mu.Unlock()

	if dst.cfg != src.cfg {
		return
	}

	dst.mu.Lock()
	defer dst.mu.Unlock()
	dst.state = src.state
	dst.consecutiveFailures = src.consecutiveFailures
	dst.consecutiveSuccesses = src.consecutiveSuccesses
	dst.openedAt = src.openedAt
	dst.halfOpenInFlight = src.halfOpenInFlight
}

func inheritStickyRuntimeState(dst *ClientProxy, src *ClientProxy, indexMap map[int]int) {
	if dst == nil || src == nil {
		return
	}
	now := time.Now()

	for key, binding := range src.stickyBindings {
		newIndex, ok := indexMap[binding.ProviderIndex]
		if !ok {
			continue
		}
		binding.ProviderIndex = newIndex
		if binding.LastSeenAt.IsZero() {
			binding.LastSeenAt = now
		}
		dst.stickyBindings[key] = binding
	}
	for key, entry := range src.responseLookup {
		newIndex, ok := indexMap[entry.ProviderIndex]
		if !ok {
			continue
		}
		entry.ProviderIndex = newIndex
		if entry.LastSeenAt.IsZero() {
			entry.LastSeenAt = now
		}
		dst.responseLookup[key] = entry
	}
	for key, entry := range src.dynamicFeatureBindings {
		newIndex, ok := indexMap[entry.ProviderIndex]
		if !ok {
			continue
		}
		entry.ProviderIndex = newIndex
		if entry.LastSeenAt.IsZero() {
			entry.LastSeenAt = now
		}
		dst.dynamicFeatureBindings[key] = entry
	}
}

func sameProviderRuntimeIdentity(a config.Provider, aPolicy upstreamProxyPolicyKey, b config.Provider, bPolicy upstreamProxyPolicyKey) bool {
	return a.Name == b.Name &&
		a.NormalizedAuthType() == b.NormalizedAuthType() &&
		strings.TrimSpace(a.BaseURL) == strings.TrimSpace(b.BaseURL) &&
		a.NormalizedOAuthProvider() == b.NormalizedOAuthProvider() &&
		a.NormalizedOAuthRef() == b.NormalizedOAuthRef() &&
		aPolicy == bPolicy
}

func providerIndexByName(providers []config.Provider, name string) int {
	for i := range providers {
		if providers[i].Name == name {
			return i
		}
	}
	return -1
}

func providerNameAtIndex(providers []config.Provider, index int) string {
	if index < 0 || index >= len(providers) {
		return ""
	}
	return providers[index].Name
}

func (r *Router) sweepReactivations() {
	r.mu.RLock()
	proxies := make([]*ClientProxy, 0, len(r.proxies))
	for _, p := range r.proxies {
		proxies = append(proxies, p)
	}
	r.mu.RUnlock()

	for _, p := range proxies {
		p.reactivateExpired()
	}
}

// handleHealth handles health check requests
func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"healthy"}`))
}

// handleRequest routes requests to the appropriate client proxy
func (r *Router) handleRequest(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path

	// Determine client type from path prefix
	var requestCtx RequestContext
	var clientType ClientType
	var stripPrefix string
	var clipalPath bool

	switch {
	case pathMatchesPrefix(path, "/clipal"):
		clipalPath = true
		stripPrefix = "/clipal"
	case pathMatchesPrefix(path, "/claude"):
		clientType = ClientClaude
		stripPrefix = "/claude"
	case pathMatchesPrefix(path, "/openai"):
		clientType = ClientOpenAI
		stripPrefix = "/openai"
	case pathMatchesPrefix(path, "/claudecode"):
		clientType = ClientClaude
		stripPrefix = "/claudecode"
	case pathMatchesPrefix(path, "/codex"):
		clientType = ClientOpenAI
		stripPrefix = "/codex"
	case pathMatchesPrefix(path, "/gemini"):
		clientType = ClientGemini
		stripPrefix = "/gemini"
	default:
		logger.Warn("unknown path prefix: %s", path)
		writeProxyError(w, "Unknown endpoint. Use /clipal (preferred), canonical aliases /claude, /openai, /gemini, or legacy aliases /claudecode, /codex", http.StatusNotFound)
		return
	}

	newPath := stripClientPrefix(path, stripPrefix)
	if clipalPath {
		newPath = canonicalizeClipalPath(newPath)
		var ok bool
		requestCtx, ok = detectClipalRequestContext(newPath)
		if !ok {
			logger.Warn("unknown /clipal protocol path: %s", newPath)
			writeProxyError(w, "Unknown /clipal protocol endpoint", http.StatusNotFound)
			return
		}
		clientType = requestCtx.ClientType
	} else {
		requestCtx = requestContextForClientPath(clientType, newPath, false)
	}
	req = withRequestContext(req, requestCtx)

	r.mu.RLock()
	proxy, exists := r.proxies[clientType]
	maxBody := r.cfg.Global.MaxRequestBody
	r.mu.RUnlock()

	if !exists || len(proxy.providers) == 0 {
		logger.Warn("[%s] no providers configured", clientType)
		writeProxyError(w, fmt.Sprintf("No providers configured for %s", clientType), http.StatusServiceUnavailable)
		return
	}

	if maxBody > 0 {
		req.Body = http.MaxBytesReader(w, req.Body, maxBody)
	}

	logger.Debug("[%s] request received: %s %s", clientType, req.Method, newPath)

	// Count token endpoints are lightweight advisory requests, so handle them as
	// single-shot passthroughs that never mutate provider health state.
	if requestCtx.Capability == CapabilityClaudeCountTokens || requestCtx.Capability == CapabilityGeminiCountTokens {
		proxy.forwardCountTokensSingleShot(w, req, newPath)
		return
	}

	// Forward request with failover.
	proxy.forwardWithFailover(w, req, newPath)
}

func isClaudeCountTokensPath(path string) bool {
	return path == "/v1/messages/count_tokens" || path == "/v1/messages/count_tokens/"
}

// createProxyRequest creates a new request to forward to the provider
func (cp *ClientProxy) createProxyRequest(original *http.Request, provider config.Provider, apiKey string, path string, body []byte) (*http.Request, error) {
	return cp.createProxyRequestWithPayload(original, provider, apiKey, path, newRequestPayload(body))
}

func (cp *ClientProxy) createProxyRequestWithPayload(original *http.Request, provider config.Provider, apiKey string, path string, payload *requestPayload) (*http.Request, error) {
	return cp.createProxyRequestWithPayloadForProvider(original, provider, -1, apiKey, path, payload)
}

func (cp *ClientProxy) createProxyRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, apiKey string, path string, payload *requestPayload) (*http.Request, error) {
	if payload == nil {
		payload = newRequestPayload(nil)
	}
	if provider.UsesOAuth() {
		return cp.createOAuthProxyRequestWithPayloadForProvider(original, provider, providerIndex, path, payload)
	}

	targetURL, err := buildTargetURL(provider.BaseURL, path, original.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		requestCtx = requestContextForClientPath(cp.clientType, path, false)
	}
	body := payload.providerBody(original, requestCtx, provider)

	// Create the request
	proxyReq, err := http.NewRequestWithContext(original.Context(), original.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Copy headers from original request
	for key, values := range original.Header {
		// Skip hop-by-hop headers
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Add standard proxy headers
	addForwardedHeaders(proxyReq, original)

	if apiKey != "" {
		applyProviderAPIKey(proxyReq, original, apiKey)
	}

	// Set content length
	proxyReq.ContentLength = int64(len(body))
	proxyReq.Header.Del("Content-Length")

	return proxyReq, nil
}

func applyProviderAPIKey(proxyReq *http.Request, original *http.Request, apiKey string) {
	if proxyReq == nil || original == nil || strings.TrimSpace(apiKey) == "" {
		return
	}

	clearAuthCarriers(proxyReq)

	switch detectAuthCarrierForRequest(original) {
	case authCarrierNone:
		applyDefaultProviderAPIKey(proxyReq, original, apiKey)
	case authCarrierClaudeHeader:
		proxyReq.Header.Set("x-api-key", apiKey)
	case authCarrierGeminiHeader:
		proxyReq.Header.Set("x-goog-api-key", apiKey)
	case authCarrierAuthorization:
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	case authCarrierQueryKey:
		setAuthQueryValue(proxyReq, "key", apiKey)
	case authCarrierQueryAPIKey:
		setAuthQueryValue(proxyReq, "api_key", apiKey)
	default:
		// Defensive fallback: if a future or unknown auth carrier appears,
		// fall back to the protocol family's default auth style.
		applyDefaultProviderAPIKey(proxyReq, original, apiKey)
	}
}

func detectAuthCarrierForRequest(original *http.Request) authCarrier {
	detectAuthCarrierFuncMu.RLock()
	fn := detectAuthCarrierFunc
	detectAuthCarrierFuncMu.RUnlock()
	return fn(original)
}

func applyDefaultProviderAPIKey(proxyReq *http.Request, original *http.Request, apiKey string) {
	switch requestFamilyForAuth(original) {
	case ProtocolFamilyClaude:
		proxyReq.Header.Set("x-api-key", apiKey)
	case ProtocolFamilyGemini:
		proxyReq.Header.Set("x-goog-api-key", apiKey)
	default:
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func clearAuthCarriers(proxyReq *http.Request) {
	if proxyReq == nil {
		return
	}
	proxyReq.Header.Del("Authorization")
	proxyReq.Header.Del("x-api-key")
	proxyReq.Header.Del("x-goog-api-key")
	q := proxyReq.URL.Query()
	q.Del("key")
	q.Del("api_key")
	proxyReq.URL.RawQuery = q.Encode()
}

func setAuthQueryValue(proxyReq *http.Request, key string, value string) {
	if proxyReq == nil || proxyReq.URL == nil {
		return
	}
	q := proxyReq.URL.Query()
	q.Set(key, value)
	proxyReq.URL.RawQuery = q.Encode()
}

func detectAuthCarrier(original *http.Request) authCarrier {
	if original == nil {
		return authCarrierAuthorization
	}
	switch {
	case strings.TrimSpace(original.Header.Get("x-api-key")) != "":
		return authCarrierClaudeHeader
	case strings.TrimSpace(original.Header.Get("x-goog-api-key")) != "":
		return authCarrierGeminiHeader
	case strings.TrimSpace(original.Header.Get("Authorization")) != "":
		return authCarrierAuthorization
	case strings.TrimSpace(original.URL.Query().Get("key")) != "":
		return authCarrierQueryKey
	case strings.TrimSpace(original.URL.Query().Get("api_key")) != "":
		return authCarrierQueryAPIKey
	default:
		return authCarrierNone
	}
}

func requestFamilyForAuth(original *http.Request) ProtocolFamily {
	if requestCtx, ok := requestContextFromRequest(original); ok {
		return requestCtx.Family
	}
	return ""
}

// getCurrentIndex returns the current provider index
func (cp *ClientProxy) getCurrentIndex() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.currentIndex
}

// hopByHopHeaders is a set of hop-by-hop headers that should not be forwarded.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// isHopByHopHeader checks if a header is hop-by-hop
func isHopByHopHeader(header string) bool {
	return hopByHopHeaders[http.CanonicalHeaderKey(header)]
}

func pathMatchesPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func stripClientPrefix(path, prefix string) string {
	if path == prefix || path == prefix+"/" {
		return "/"
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == "" {
		return "/"
	}
	return trimmed
}

func buildTargetURL(baseURL string, path string, rawQuery string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("base_url is empty")
	}

	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url %q: %w", baseURL, err)
	}
	if parsedBase.Scheme == "" {
		parsedBase, err = url.Parse("https://" + baseURL)
		if err != nil {
			return "", fmt.Errorf("invalid base_url %q: %w", baseURL, err)
		}
	}
	if parsedBase.Host == "" {
		return "", fmt.Errorf("invalid base_url %q: host is empty", baseURL)
	}

	path = stripDuplicateVersionedPrefix(parsedBase.Path, path)

	// Join base path and request path, keeping exactly one slash.
	parsedBase.Path = singleJoiningSlash(parsedBase.Path, path)
	parsedBase.RawQuery = rawQuery
	parsedBase.Fragment = ""

	return parsedBase.String(), nil
}

func stripDuplicateVersionedPrefix(basePath string, requestPath string) string {
	requestPath = normalizeUpstreamPath(requestPath)
	basePath = normalizeUpstreamPath(basePath)

	for _, root := range versionedPathRoots {
		if !strings.HasSuffix(basePath, root) || !pathMatchesPrefix(requestPath, root) {
			continue
		}

		trimmed := strings.TrimPrefix(requestPath, root)
		if strings.TrimSpace(trimmed) == "" {
			return "/"
		}
		return normalizeUpstreamPath(trimmed)
	}

	return requestPath
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if a == "" {
			return "/" + b
		}
		return a + "/" + b
	default:
		return a + b
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for k := range dst {
		dst.Del(k)
	}
	for k, vv := range src {
		if isHopByHopHeader(k) {
			continue
		}
		dst[k] = append([]string(nil), vv...)
	}
}

func addForwardedHeaders(proxyReq *http.Request, original *http.Request) {
	// X-Forwarded-For
	if ip, _, err := net.SplitHostPort(original.RemoteAddr); err == nil && ip != "" {
		prior := proxyReq.Header.Get("X-Forwarded-For")
		if prior != "" {
			proxyReq.Header.Set("X-Forwarded-For", prior+", "+ip)
		} else {
			proxyReq.Header.Set("X-Forwarded-For", ip)
		}
	}

	// X-Forwarded-Proto
	proto := "http"
	if original.TLS != nil {
		proto = "https"
	}
	proxyReq.Header.Set("X-Forwarded-Proto", proto)

	// X-Forwarded-Host
	if original.Host != "" {
		proxyReq.Header.Set("X-Forwarded-Host", original.Host)
	}

	// Forward an explicit port hint (some upstreams log it).
	if host := original.Host; host != "" {
		if _, port, err := netSplitHostPortMaybe(host); err == nil && port != "" {
			proxyReq.Header.Set("X-Forwarded-Port", port)
		}
	}
}

func netSplitHostPortMaybe(hostport string) (host string, port string, err error) {
	// Treat bare host as no port.
	if h, p, e := net.SplitHostPort(hostport); e == nil {
		return h, p, nil
	}
	return hostport, "", nil
}
