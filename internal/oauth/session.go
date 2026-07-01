package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

type Option func(*Service)

var ErrSessionNotFound = errors.New("oauth session not found")
var ErrInvalidAuthorizationResponse = errors.New("invalid authorization response")

const defaultLoginExchangeTimeout = 90 * time.Second
const defaultTerminalSessionRetention = 10 * time.Minute
const defaultGeminiUsageTTL = 30 * time.Second

type Service struct {
	store                *Store
	codex                *CodexClient
	claude               *ClaudeClient
	gemini               *GeminiClient
	antigravity          *AntigravityClient
	clients              map[config.OAuthProvider]ProviderClient
	now                  func() time.Time
	sessionTTL           time.Duration
	loginExchangeTimeout time.Duration
	terminalRetention    time.Duration
	refreshSkew          time.Duration
	geminiUsageTTL       time.Duration

	mu               sync.Mutex
	sessions         map[string]*LoginSession
	refreshes        map[string]*refreshCall
	geminiUsageCache map[string]geminiUsageCacheEntry
	geminiUsageCalls map[string]*geminiUsageCall
}

type refreshCall struct {
	done chan struct{}
	cred *Credential
	err  error
}

type geminiUsageCall struct {
	done    chan struct{}
	details *GeminiUsageDetails
	err     error
}

type geminiUsageCacheEntry struct {
	fetchedAt time.Time
	details   *GeminiUsageDetails
}

func NewService(configDir string, opts ...Option) *Service {
	svc := &Service{
		store:                NewStore(configDir),
		codex:                NewCodexClient(),
		claude:               NewClaudeClient(),
		gemini:               NewGeminiClient(),
		antigravity:          NewAntigravityClient(),
		clients:              make(map[config.OAuthProvider]ProviderClient),
		now:                  time.Now,
		sessionTTL:           5 * time.Minute,
		loginExchangeTimeout: defaultLoginExchangeTimeout,
		terminalRetention:    defaultTerminalSessionRetention,
		refreshSkew:          30 * time.Second,
		geminiUsageTTL:       defaultGeminiUsageTTL,
		sessions:             make(map[string]*LoginSession),
		refreshes:            make(map[string]*refreshCall),
		geminiUsageCache:     make(map[string]geminiUsageCacheEntry),
		geminiUsageCalls:     make(map[string]*geminiUsageCall),
	}
	svc.registerProviderClient(svc.codex)
	svc.registerProviderClient(svc.claude)
	svc.registerProviderClient(svc.gemini)
	svc.registerProviderClient(svc.antigravity)
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

func WithNowFunc(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

func WithCodexClient(client *CodexClient) Option {
	return func(s *Service) {
		if client != nil {
			s.codex = client
			s.registerProviderClient(client)
		}
	}
}

func WithClaudeClient(client *ClaudeClient) Option {
	return func(s *Service) {
		if client != nil {
			s.claude = client
			s.registerProviderClient(client)
		}
	}
}

func WithGeminiClient(client *GeminiClient) Option {
	return func(s *Service) {
		if client != nil {
			s.gemini = client
			s.registerProviderClient(client)
		}
	}
}

func WithAntigravityClient(client *AntigravityClient) Option {
	return func(s *Service) {
		if client != nil {
			s.antigravity = client
			s.registerProviderClient(client)
		}
	}
}

func WithProviderClient(client ProviderClient) Option {
	return func(s *Service) {
		s.registerProviderClient(client)
	}
}

func WithSessionTTL(ttl time.Duration) Option {
	return func(s *Service) {
		if ttl > 0 {
			s.sessionTTL = ttl
		}
	}
}

func WithLoginExchangeTimeout(timeout time.Duration) Option {
	return func(s *Service) {
		if timeout > 0 {
			s.loginExchangeTimeout = timeout
		}
	}
}

func WithTerminalSessionRetention(retention time.Duration) Option {
	return func(s *Service) {
		if retention >= 0 {
			s.terminalRetention = retention
		}
	}
}

func WithRefreshSkew(skew time.Duration) Option {
	return func(s *Service) {
		if skew >= 0 {
			s.refreshSkew = skew
		}
	}
}

func WithGeminiUsageTTL(ttl time.Duration) Option {
	return func(s *Service) {
		if ttl >= 0 {
			s.geminiUsageTTL = ttl
		}
	}
}

func (s *Service) StartLogin(provider config.OAuthProvider) (*LoginSession, error) {
	return s.StartLoginWithHTTPClient(provider, nil)
}

func (s *Service) StartLoginWithHTTPClient(provider config.OAuthProvider, httpClient *http.Client) (*LoginSession, error) {
	provider = normalizeProvider(provider)
	client, ok := s.providerClient(provider)
	if !ok {
		return nil, fmt.Errorf("unsupported oauth provider %q", provider)
	}

	s.mu.Lock()
	s.sweepExpiredSessionsLocked()
	callbacks := s.supersedePendingSessionsLocked(provider)
	s.mu.Unlock()
	for _, callback := range callbacks {
		if callback != nil {
			_ = callback.Close()
		}
	}
	session, err := client.StartLogin(s.now(), s.sessionTTL)
	if err != nil {
		return nil, err
	}
	session.httpClient = httpClient

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepExpiredSessionsLocked()
	s.sessions[session.ID] = session
	if session.callback != nil {
		session.callback.SetHandler(func(result callbackResult) callbackPage {
			return s.handleCallbackResult(session.ID, result)
		})
	}
	return session.Clone(), nil
}

func (s *Service) supersedePendingSessionsLocked(provider config.OAuthProvider) []*callbackServer {
	var callbacks []*callbackServer
	for _, session := range s.sessions {
		if session == nil || session.Provider != provider || session.Status != LoginStatusPending || session.callback == nil {
			continue
		}
		callbacks = append(callbacks, session.callback)
		session.callback = nil
		session.Status = LoginStatusError
		session.Error = "oauth session superseded by a new authorization attempt"
		session.terminalAt = s.now()
	}
	return callbacks
}

func (s *Service) PollLogin(sessionID string) (*LoginSession, error) {
	return s.pollLogin(sessionID, nil)
}

func (s *Service) CompleteLoginWithCode(ctx context.Context, sessionID string, code string) (*LoginSession, error) {
	return s.CompleteLoginWithCodeWithHTTPClient(ctx, sessionID, code, nil)
}

func (s *Service) CompleteLoginWithCodeWithHTTPClient(ctx context.Context, sessionID string, code string, httpClient *http.Client) (*LoginSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	code = strings.TrimSpace(code)
	if sessionID == "" {
		return nil, ErrSessionNotFound
	}
	if code == "" {
		return nil, fmt.Errorf("%w: authorization code is required", ErrInvalidAuthorizationResponse)
	}
	parsed, err := parseManualAuthorizationInput(code)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	if session.completionDone != nil {
		done := session.completionDone
		s.mu.Unlock()
		return s.waitForSessionCompletion(ctx, sessionID, done), nil
	}
	if session.Status == LoginStatusPending && !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(s.now()) {
		callback := session.callback
		session.callback = nil
		session.Status = LoginStatusExpired
		session.Error = "oauth session expired"
		session.terminalAt = s.now()
		out := session.Clone()
		s.mu.Unlock()
		if callback != nil {
			_ = callback.Close()
		}
		return out, nil
	}
	if session.Status != LoginStatusPending {
		out := session.Clone()
		s.mu.Unlock()
		return out, nil
	}
	if parsed.Error == "" && manualAuthorizationStateRequired(session.Provider) && parsed.State == "" {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s authorization requires the full callback URL or an authorization code that still includes its state", ErrInvalidAuthorizationResponse, providerDisplayName(session.Provider))
	}
	s.mu.Unlock()

	sessionSnapshot, callback, done, terminal, err := s.beginSessionCompletion(sessionID)
	if err != nil {
		return nil, err
	}
	if terminal != nil {
		return terminal, nil
	}
	if done != nil {
		return s.waitForSessionCompletion(ctx, sessionID, done), nil
	}

	if parsed.Error != "" {
		return s.finishSessionWithError(sessionID, parsed.errorMessage(), callback), nil
	}
	if parsed.State != "" && parsed.State != sessionID {
		return s.finishSessionWithError(sessionID, "oauth state mismatch", callback), nil
	}

	return s.completeSessionWithCode(ctx, sessionSnapshot, parsed.Code, callback, httpClient), nil
}

func (s *Service) CancelLogin(sessionID string) (*LoginSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrSessionNotFound
	}

	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	if session.completionDone != nil {
		done := session.completionDone
		s.mu.Unlock()
		return s.waitForSessionCompletion(context.Background(), sessionID, done), nil
	}
	if session.Status != LoginStatusPending {
		out := session.Clone()
		s.mu.Unlock()
		return out, nil
	}

	callback := session.callback
	session.callback = nil
	session.Status = LoginStatusError
	session.Error = "oauth session cancelled"
	session.terminalAt = s.now()
	out := session.Clone()
	s.mu.Unlock()

	if callback != nil {
		_ = callback.Close()
	}
	return out, nil
}

func (s *Service) Load(provider config.OAuthProvider, ref string) (*Credential, error) {
	return s.store.Load(provider, ref)
}

func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) List(provider config.OAuthProvider) ([]Credential, error) {
	return s.store.List(provider)
}

func (s *Service) Delete(provider config.OAuthProvider, ref string) error {
	return s.store.Delete(provider, ref)
}

func (s *Service) RefreshIfNeeded(ctx context.Context, provider config.OAuthProvider, ref string) (*Credential, error) {
	return s.RefreshIfNeededWithHTTPClient(ctx, provider, ref, nil)
}

func (s *Service) RefreshIfNeededWithHTTPClient(ctx context.Context, provider config.OAuthProvider, ref string, httpClient *http.Client) (*Credential, error) {
	return s.refresh(ctx, provider, ref, false, httpClient)
}

func (s *Service) Refresh(ctx context.Context, provider config.OAuthProvider, ref string) (*Credential, error) {
	return s.RefreshWithHTTPClient(ctx, provider, ref, nil)
}

func (s *Service) RefreshWithHTTPClient(ctx context.Context, provider config.OAuthProvider, ref string, httpClient *http.Client) (*Credential, error) {
	return s.refresh(ctx, provider, ref, true, httpClient)
}

func (s *Service) refresh(ctx context.Context, provider config.OAuthProvider, ref string, force bool, httpClient *http.Client) (*Credential, error) {
	cred, err := s.store.Load(provider, ref)
	if err != nil {
		return nil, err
	}
	if !force && (!cred.NeedsRefresh(s.now(), s.refreshSkew) || strings.TrimSpace(cred.RefreshToken) == "") {
		return cred, nil
	}
	if strings.TrimSpace(cred.RefreshToken) == "" {
		return nil, fmt.Errorf("oauth credential %q has no refresh token", strings.TrimSpace(ref))
	}

	key := string(cred.Provider) + ":" + cred.Ref
	s.mu.Lock()
	if call, ok := s.refreshes[key]; ok {
		s.mu.Unlock()
		<-call.done
		if call.cred == nil {
			return nil, call.err
		}
		return call.cred.Clone(), call.err
	}
	call := &refreshCall{done: make(chan struct{})}
	s.refreshes[key] = call
	s.mu.Unlock()

	refreshed, err := s.refreshCredential(ctx, cred, httpClient)

	s.mu.Lock()
	delete(s.refreshes, key)
	call.cred = refreshed
	call.err = err
	close(call.done)
	s.mu.Unlock()

	if refreshed == nil {
		return nil, err
	}
	return refreshed.Clone(), err
}

func (s *Service) refreshCredential(ctx context.Context, cred *Credential, httpClient *http.Client) (*Credential, error) {
	client, ok := s.providerClient(cred.Provider)
	if !ok {
		return nil, fmt.Errorf("unsupported oauth provider %q", cred.Provider)
	}
	client = providerClientWithHTTPClient(client, httpClient)
	refreshed, err := client.Refresh(ctx, cred)
	if err != nil {
		return nil, err
	}
	if err := s.store.Save(refreshed); err != nil {
		return nil, err
	}
	if refreshed.Provider == config.OAuthProviderGemini {
		s.invalidateGeminiUsageCache(refreshed.Ref)
	}
	return refreshed, nil
}

func (s *Service) registerProviderClient(client ProviderClient) {
	if s == nil || client == nil {
		return
	}
	if s.clients == nil {
		s.clients = make(map[config.OAuthProvider]ProviderClient)
	}
	provider := normalizeProvider(client.Provider())
	if provider == "" {
		return
	}
	s.clients[provider] = client
}

func (s *Service) providerClient(provider config.OAuthProvider) (ProviderClient, bool) {
	if s == nil {
		return nil, false
	}
	client, ok := s.clients[normalizeProvider(provider)]
	return client, ok
}

func newLoginSessionSnapshot(id string, provider config.OAuthProvider, authURL string, status LoginStatus, expiresAt time.Time, pkce PKCECodes, redirectURI string, httpClient *http.Client) *LoginSession {
	return &LoginSession{
		ID:          strings.TrimSpace(id),
		Provider:    normalizeProvider(provider),
		AuthURL:     strings.TrimSpace(authURL),
		Status:      status,
		ExpiresAt:   expiresAt,
		pkce:        pkce,
		redirectURI: strings.TrimSpace(redirectURI),
		httpClient:  httpClient,
	}
}

func (s *Service) beginSessionCompletion(sessionID string) (*LoginSession, *callbackServer, <-chan struct{}, *LoginSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, nil, nil, ErrSessionNotFound
	}
	if session.completionDone != nil {
		done := session.completionDone
		s.mu.Unlock()
		return nil, nil, done, nil, nil
	}
	if session.Status == LoginStatusPending && !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(s.now()) {
		callback := session.callback
		done := session.completionDone
		session.callback = nil
		session.completionDone = nil
		session.Status = LoginStatusExpired
		session.Error = "oauth session expired"
		session.terminalAt = s.now()
		out := session.Clone()
		s.mu.Unlock()
		if callback != nil {
			_ = callback.Close()
		}
		if done != nil {
			close(done)
		}
		return nil, nil, nil, out, nil
	}
	if session.Status != LoginStatusPending {
		out := session.Clone()
		s.mu.Unlock()
		return nil, nil, nil, out, nil
	}
	session.completionDone = make(chan struct{})
	callback := session.callback
	session.callback = nil
	snapshot := newLoginSessionSnapshot(
		session.ID,
		session.Provider,
		session.AuthURL,
		session.Status,
		session.ExpiresAt,
		session.pkce,
		session.redirectURI,
		session.httpClient,
	)
	s.mu.Unlock()
	return snapshot, callback, nil, nil, nil
}

func (s *Service) waitForSessionCompletion(ctx context.Context, sessionID string, done <-chan struct{}) *LoginSession {
	if done == nil {
		return s.loadSessionClone(sessionID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
	return s.loadSessionClone(sessionID)
}

func (s *Service) loadSessionClone(sessionID string) *LoginSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[strings.TrimSpace(sessionID)]
	if session == nil {
		return nil
	}
	return session.Clone()
}

func (s *Service) completionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.loginExchangeTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.loginExchangeTimeout)
}

func (s *Service) finalizeSession(sessionID string, callback *callbackServer, apply func(*LoginSession)) *LoginSession {
	if callback != nil {
		_ = callback.Close()
	}

	var (
		done chan struct{}
		out  *LoginSession
	)
	s.mu.Lock()
	session := s.sessions[strings.TrimSpace(sessionID)]
	if session != nil {
		apply(session)
		session.callback = nil
		if isLoginStatusTerminal(session.Status) && session.terminalAt.IsZero() {
			session.terminalAt = s.now()
		}
		done = session.completionDone
		session.completionDone = nil
		out = session.Clone()
	}
	s.mu.Unlock()
	if done != nil {
		close(done)
	}
	return out
}

func (s *Service) PollLoginWithHTTPClient(sessionID string, httpClient *http.Client) (*LoginSession, error) {
	return s.pollLogin(sessionID, httpClient)
}

func (s *Service) pollLogin(sessionID string, httpClient *http.Client) (*LoginSession, error) {
	s.mu.Lock()
	session, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		s.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	if session.completionDone == nil && session.Status == LoginStatusPending && !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(s.now()) {
		callback := session.callback
		session.callback = nil
		session.Status = LoginStatusExpired
		session.Error = "oauth session expired"
		session.terminalAt = s.now()
		s.mu.Unlock()
		if callback != nil {
			_ = callback.Close()
		}
		return session.Clone(), nil
	}
	if session.Status != LoginStatusPending || session.callback == nil {
		out := session.Clone()
		s.mu.Unlock()
		return out, nil
	}
	callback := session.callback
	expectedState := session.ID
	s.mu.Unlock()

	result, ok := callback.Poll()
	if !ok {
		s.mu.Lock()
		out := s.sessions[sessionID].Clone()
		s.mu.Unlock()
		return out, nil
	}
	if result.Error != "" {
		sessionSnapshot, callback, done, terminal, err := s.beginSessionCompletion(sessionID)
		if err != nil {
			return nil, err
		}
		if terminal != nil {
			return terminal, nil
		}
		if done != nil {
			return s.waitForSessionCompletion(context.Background(), sessionID, done), nil
		}
		return s.finishSessionWithError(sessionSnapshot.ID, result.errorMessage(), callback), nil
	}
	if result.State != expectedState {
		sessionSnapshot, callback, done, terminal, err := s.beginSessionCompletion(sessionID)
		if err != nil {
			return nil, err
		}
		if terminal != nil {
			return terminal, nil
		}
		if done != nil {
			return s.waitForSessionCompletion(context.Background(), sessionID, done), nil
		}
		return s.finishSessionWithError(sessionSnapshot.ID, "oauth state mismatch", callback), nil
	}

	sessionSnapshot, callback, done, terminal, err := s.beginSessionCompletion(sessionID)
	if err != nil {
		return nil, err
	}
	if terminal != nil {
		return terminal, nil
	}
	if done != nil {
		return s.waitForSessionCompletion(context.Background(), sessionID, done), nil
	}

	return s.completeSessionWithCode(
		context.Background(),
		sessionSnapshot,
		result.Code,
		callback,
		httpClient,
	), nil
}

func (s *Service) completeSessionWithCode(ctx context.Context, sessionSnapshot *LoginSession, code string, callback *callbackServer, httpClient *http.Client) *LoginSession {
	if sessionSnapshot == nil {
		return nil
	}

	client, ok := s.providerClient(sessionSnapshot.Provider)
	if !ok {
		return s.finishSessionWithError(sessionSnapshot.ID, fmt.Sprintf("unsupported oauth provider %q", sessionSnapshot.Provider), callback)
	}
	if httpClient == nil {
		httpClient = sessionSnapshot.httpClient
	}
	client = providerClientWithHTTPClient(client, httpClient)
	exchangeCtx, cancel := s.completionContext(ctx)
	defer cancel()

	cred, err := client.ExchangeSessionCode(exchangeCtx, sessionSnapshot, code)
	if err != nil {
		return s.finishSessionWithError(sessionSnapshot.ID, err.Error(), callback)
	}
	if err := s.store.Save(cred); err != nil {
		return s.finishSessionWithError(sessionSnapshot.ID, err.Error(), callback)
	}

	return s.finalizeSession(sessionSnapshot.ID, callback, func(session *LoginSession) {
		session.Status = LoginStatusCompleted
		session.CredentialRef = cred.Ref
		session.Email = cred.Email
		session.Error = ""
	})
}

func (s *Service) finishSessionWithError(sessionID string, msg string, callback *callbackServer) *LoginSession {
	return s.finalizeSession(sessionID, callback, func(session *LoginSession) {
		session.Status = LoginStatusError
		session.Error = strings.TrimSpace(msg)
	})
}

func (s *Service) handleCallbackResult(sessionID string, result callbackResult) callbackPage {
	sessionSnapshot, callback, done, terminal, err := s.beginSessionCompletion(sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		return callbackPage{
			Tone:    "error",
			Title:   "Authorization session not found",
			Message: "Clipal could not find this OAuth session. Return to Clipal and start the authorization again.",
		}
	}
	if err != nil {
		return callbackPageForSession(s.finishSessionWithError(sessionID, err.Error(), callback))
	}
	if terminal != nil {
		return callbackPageForSession(terminal)
	}
	if done != nil {
		return callbackPageForSession(s.waitForSessionCompletion(context.Background(), sessionID, done))
	}

	if result.Error != "" {
		return callbackPageForSession(s.finishSessionWithError(sessionID, result.errorMessage(), callback))
	}
	if result.State != sessionID {
		return callbackPageForSession(s.finishSessionWithError(sessionID, "oauth state mismatch", callback))
	}
	return callbackPageForSession(s.completeSessionWithCode(context.Background(), sessionSnapshot, result.Code, callback, nil))
}

type manualAuthorizationInput struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

func (i *manualAuthorizationInput) errorMessage() string {
	if i == nil {
		return ""
	}
	if msg := strings.TrimSpace(i.ErrorDescription); msg != "" {
		return msg
	}
	return strings.TrimSpace(i.Error)
}

func parseManualAuthorizationInput(input string) (*manualAuthorizationInput, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: authorization code is required", ErrInvalidAuthorizationResponse)
	}
	if !looksLikeOAuthCallbackInput(trimmed) {
		code, state := splitAuthorizationCodeAndState(trimmed)
		return &manualAuthorizationInput{
			Code:  code,
			State: state,
		}, nil
	}

	parsed, err := parseOAuthCallbackInput(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAuthorizationResponse, err)
	}
	return parsed, nil
}

func looksLikeOAuthCallbackInput(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(trimmed, "://") ||
		strings.HasPrefix(trimmed, "?") ||
		strings.HasPrefix(trimmed, "/") ||
		strings.Contains(lower, "code=") ||
		strings.Contains(lower, "state=") ||
		strings.Contains(lower, "error=")
}

func splitAuthorizationCodeAndState(code string) (string, string) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" || !strings.Contains(trimmed, "#") {
		return trimmed, ""
	}
	parts := strings.SplitN(trimmed, "#", 2)
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func manualAuthorizationStateRequired(provider config.OAuthProvider) bool {
	switch normalizeProvider(provider) {
	case config.OAuthProviderClaude:
		return true
	default:
		return false
	}
}

func parseOAuthCallbackInput(input string) (*manualAuthorizationInput, error) {
	candidate := strings.TrimSpace(input)
	switch {
	case candidate == "":
		return nil, fmt.Errorf("authorization code is required")
	case strings.Contains(candidate, "://"):
	case strings.HasPrefix(candidate, "?"):
		candidate = "http://127.0.0.1/" + candidate
	case strings.HasPrefix(candidate, "/"):
		candidate = "http://127.0.0.1" + candidate
	default:
		candidate = "http://127.0.0.1/?" + strings.TrimPrefix(candidate, "?")
	}

	parsedURL, err := url.Parse(candidate)
	if err != nil {
		return nil, err
	}

	query := parsedURL.Query()
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	errCode := strings.TrimSpace(query.Get("error"))
	errDesc := strings.TrimSpace(query.Get("error_description"))

	if parsedURL.Fragment != "" {
		if fragQuery, err := url.ParseQuery(parsedURL.Fragment); err == nil {
			if code == "" {
				code = strings.TrimSpace(fragQuery.Get("code"))
			}
			if state == "" {
				state = strings.TrimSpace(fragQuery.Get("state"))
			}
			if errCode == "" {
				errCode = strings.TrimSpace(fragQuery.Get("error"))
			}
			if errDesc == "" {
				errDesc = strings.TrimSpace(fragQuery.Get("error_description"))
			}
		}
		if state == "" {
			fragment := strings.TrimSpace(parsedURL.Fragment)
			if fragment != "" && !strings.Contains(fragment, "=") {
				state = fragment
			}
		}
	}

	if code != "" && state == "" {
		code, state = splitAuthorizationCodeAndState(code)
	}
	if code == "" && errCode == "" {
		return nil, fmt.Errorf("callback URL missing code")
	}

	return &manualAuthorizationInput{
		Code:             code,
		State:            state,
		Error:            errCode,
		ErrorDescription: errDesc,
	}, nil
}

func (s *Service) sweepExpiredSessionsLocked() {
	now := s.now()
	for id, session := range s.sessions {
		if session == nil {
			delete(s.sessions, id)
			continue
		}
		if isLoginStatusTerminal(session.Status) && session.callback == nil {
			if session.terminalAt.IsZero() {
				session.terminalAt = now
				s.sessions[id] = session
			}
			if s.terminalRetention == 0 || !session.terminalAt.Add(s.terminalRetention).After(now) {
				delete(s.sessions, id)
			}
			continue
		}
		if session.callback != nil && !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now) {
			_ = session.callback.Close()
			session.callback = nil
			session.Status = LoginStatusExpired
			session.Error = "oauth session expired"
			session.terminalAt = now
			s.sessions[id] = session
		}
	}
}

func isLoginStatusTerminal(status LoginStatus) bool {
	switch status {
	case LoginStatusCompleted, LoginStatusExpired, LoginStatusError:
		return true
	default:
		return false
	}
}

type callbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

func (r callbackResult) errorMessage() string {
	if msg := strings.TrimSpace(r.ErrorDescription); msg != "" {
		return msg
	}
	return strings.TrimSpace(r.Error)
}

type callbackPage struct {
	Tone      string
	Title     string
	Message   string
	AutoClose bool
}

type callbackServer struct {
	listener net.Listener
	server   *http.Server
	results  chan callbackResult

	mu      sync.RWMutex
	handler func(callbackResult) callbackPage
}

func startCallbackServer(host string, port int, path string) (*callbackServer, string, error) {
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + strings.TrimSpace(path)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, "", err
	}

	server := &callbackServer{
		listener: listener,
		results:  make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		result := callbackResult{
			Code:             strings.TrimSpace(r.URL.Query().Get("code")),
			State:            strings.TrimSpace(r.URL.Query().Get("state")),
			Error:            strings.TrimSpace(r.URL.Query().Get("error")),
			ErrorDescription: strings.TrimSpace(r.URL.Query().Get("error_description")),
		}
		if result.Error == "" && result.Code == "" {
			result.Error = "authorization code not found"
		}
		page, handled := server.handle(result)
		if !handled {
			select {
			case server.results <- result:
			default:
			}
			page = defaultCallbackPage(result)
		}
		writeCallbackPage(w, page)
		if handled {
			go func() {
				time.Sleep(150 * time.Millisecond)
				_ = server.Close()
			}()
		}
	})
	server.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		_ = server.server.Serve(listener)
	}()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, "", fmt.Errorf("listener address is not TCP")
	}
	redirectHost := host
	if redirectHost == "" || redirectHost == "0.0.0.0" || redirectHost == "::" {
		redirectHost = "127.0.0.1"
	}
	redirectURI := "http://" + net.JoinHostPort(redirectHost, strconv.Itoa(tcpAddr.Port)) + path
	return server, redirectURI, nil
}

func (s *callbackServer) Poll() (callbackResult, bool) {
	if s == nil {
		return callbackResult{}, false
	}
	select {
	case result := <-s.results:
		return result, true
	default:
		return callbackResult{}, false
	}
}

func (s *callbackServer) SetHandler(handler func(callbackResult) callbackPage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

func (s *callbackServer) handle(result callbackResult) (callbackPage, bool) {
	if s == nil {
		return callbackPage{}, false
	}
	s.mu.RLock()
	handler := s.handler
	s.mu.RUnlock()
	if handler == nil {
		return callbackPage{}, false
	}
	return handler(result), true
}

func (s *callbackServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.server.Shutdown(ctx)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	return err
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func defaultCallbackPage(result callbackResult) callbackPage {
	if result.Error != "" {
		return callbackPage{
			Tone:    "error",
			Title:   "Authorization failed",
			Message: result.errorMessage(),
		}
	}
	return callbackPage{
		Tone:      "success",
		Title:     "Authentication received",
		Message:   "Return to Clipal to finish setup. You can close this window if it does not close automatically.",
		AutoClose: true,
	}
}

func callbackPageForSession(session *LoginSession) callbackPage {
	if session == nil {
		return callbackPage{
			Tone:    "error",
			Title:   "Authorization failed",
			Message: "Clipal could not complete this OAuth session. Return to Clipal and try again.",
		}
	}

	switch session.Status {
	case LoginStatusCompleted:
		message := fmt.Sprintf("%s is now available in Clipal.", providerDisplayName(session.Provider))
		if email := strings.TrimSpace(session.Email); email != "" {
			message = fmt.Sprintf("%s is now available in Clipal as %s.", providerDisplayName(session.Provider), email)
		}
		return callbackPage{
			Tone:      "success",
			Title:     fmt.Sprintf("%s authorized", providerDisplayName(session.Provider)),
			Message:   message,
			AutoClose: true,
		}
	case LoginStatusExpired:
		return callbackPage{
			Tone:    "warning",
			Title:   "Authorization expired",
			Message: "This OAuth session expired before Clipal could finish it. Return to Clipal and start a new authorization session.",
		}
	case LoginStatusError:
		msg := strings.TrimSpace(session.Error)
		if msg == "" {
			msg = "Clipal could not complete this OAuth session."
		}
		return callbackPage{
			Tone:    "error",
			Title:   "Authorization failed",
			Message: msg,
		}
	default:
		return callbackPage{
			Tone:      "success",
			Title:     "Authentication received",
			Message:   "Return to Clipal to finish setup.",
			AutoClose: true,
		}
	}
}

func providerDisplayName(provider config.OAuthProvider) string {
	switch normalizeProvider(provider) {
	case config.OAuthProviderCodex:
		return "Codex"
	case config.OAuthProviderClaude:
		return "Claude Code"
	case config.OAuthProviderGemini:
		return "Gemini"
	case config.OAuthProviderAntigravity:
		return "Antigravity"
	default:
		value := strings.TrimSpace(string(provider))
		if value == "" {
			return "OAuth"
		}
		return value
	}
}

func writeCallbackPage(w http.ResponseWriter, page callbackPage) {
	if w == nil {
		return
	}
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = "Authentication received"
	}
	message := strings.TrimSpace(page.Message)
	if message == "" {
		message = "Return to Clipal to finish setup."
	}
	tone := strings.TrimSpace(page.Tone)
	if tone == "" {
		tone = "success"
	}

	autoCloseScript := ""
	if page.AutoClose {
		autoCloseScript = "setTimeout(closeWindow, 1200);"
	}

	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body{margin:0;font-family:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f5f7fb;color:#0f172a}
.shell{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{width:min(520px,100%%);background:#fff;border:1px solid #dbe4f0;border-radius:18px;box-shadow:0 18px 40px rgba(15,23,42,.08);padding:28px}
.badge{display:inline-flex;align-items:center;border-radius:999px;padding:6px 10px;font-size:12px;font-weight:600;letter-spacing:.02em}
.badge.success{background:#dcfce7;color:#166534}
.badge.warning{background:#fef3c7;color:#92400e}
.badge.error{background:#fee2e2;color:#b91c1c}
h1{margin:16px 0 12px;font-size:24px;line-height:1.2}
p{margin:0;font-size:14px;line-height:1.65;color:#475569}
.actions{margin-top:20px;display:flex;gap:10px;flex-wrap:wrap}
button{border:0;border-radius:10px;padding:10px 14px;font-size:14px;font-weight:600;background:#0f172a;color:#fff;cursor:pointer}
button.secondary{background:#e2e8f0;color:#0f172a}
</style>
</head>
<body>
<div class="shell">
  <div class="card">
    <span class="badge %s">%s</span>
    <h1>%s</h1>
    <p>%s</p>
    <div class="actions">
      <button type="button" onclick="closeWindow()">Close Window</button>
      <button type="button" class="secondary" onclick="window.location.reload()">Refresh</button>
    </div>
  </div>
</div>
<script>
(function(){
  window.closeWindow = function(){
    window.close();
    setTimeout(function(){ window.close(); }, 120);
  };
  %s
})();
</script>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(tone),
		html.EscapeString(strings.ToUpper(tone[:1])+tone[1:]),
		html.EscapeString(title),
		html.EscapeString(message),
		autoCloseScript,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
