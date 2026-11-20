package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/diagnostic"
)

const (
	providerTestTimeout = 30 * time.Second
	providerTestTTL     = 5 * time.Minute
	providerTestLimit   = 2 * 1024 * 1024
)

var (
	ErrProviderTestUnavailable = errors.New("enable the provider before testing it")
	ErrProviderTestBusy        = errors.New("a test is already running for this provider")
	ErrProviderTestExpired     = errors.New("the test expired or configuration changed; fetch models again")
	ErrProviderTestModel       = errors.New("select a model from the fetched list")
	ErrProviderTestProtocol    = errors.New("the selected test protocol is unavailable for this provider")
	errProviderTestTimeout     = &ProviderTestFailure{Reason: "timeout", message: "the upstream request timed out"}
)

type ProviderTestFailure struct {
	Reason         string
	UpstreamStatus int
	message        string
	Detail         string
}

func (failure *ProviderTestFailure) Error() string { return failure.message }

type providerTestContextKey struct{}

func isProviderTest(ctx context.Context) bool {
	return ctx.Value(providerTestContextKey{}) != nil
}

type providerTestTarget struct {
	client ClientType
	name   string
}

type providerTestSession struct {
	id       string
	proxy    *ClientProxy
	provider int
	key      int
	models   []string
	expires  time.Time
	running  bool
}

type ProviderTestModels struct {
	ID     string   `json:"id"`
	Models []string `json:"models"`
	Key    int      `json:"key,omitempty"`
}

type ProviderTestResult struct {
	Recovered bool `json:"recovered"`
	Restored  bool `json:"restored"`
}

func (r *Router) FetchProviderTestModels(ctx context.Context, client ClientType, name string, keyNumber int) (ProviderTestModels, error) {
	target := providerTestTarget{client, name}
	session, err := r.startProviderTest(target, keyNumber)
	if err != nil {
		return ProviderTestModels{}, err
	}
	models, err := session.fetchModels(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil || r.proxies[client] != session.proxy {
		delete(r.providerTests, target)
		if err != nil {
			return ProviderTestModels{}, err
		}
		return ProviderTestModels{}, ErrProviderTestExpired
	}
	session.models = models
	session.running = false
	session.expires = time.Now().Add(providerTestTTL)
	result := ProviderTestModels{ID: session.id, Models: models}
	if !session.proxy.providers[session.provider].UsesOAuth() {
		result.Key = session.key + 1
	}
	return result, nil
}

func (r *Router) startProviderTest(target providerTestTarget, keyNumber int) (*providerTestSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for target, session := range r.providerTests {
		if !session.running && (!now.Before(session.expires) || r.proxies[target.client] != session.proxy) {
			delete(r.providerTests, target)
		}
	}
	if session := r.providerTests[target]; session != nil && session.running {
		return nil, ErrProviderTestBusy
	}
	cp := r.proxies[target.client]
	if cp == nil {
		return nil, ErrProviderTestUnavailable
	}
	index := providerIndexByName(cp.providers, target.name)
	if index < 0 || len(cp.providerKeys[index]) == 0 {
		return nil, ErrProviderTestUnavailable
	}
	if keyNumber < 0 || keyNumber > len(cp.providerKeys[index]) {
		return nil, errors.New("the selected API key is no longer configured")
	}
	key := keyNumber - 1
	if key < 0 {
		cp.mu.RLock()
		key = cp.nextActiveKeyIndexLocked(index, -1, now)
		cp.mu.RUnlock()
	}
	session := &providerTestSession{id: rand.Text(), proxy: cp, provider: index, key: key, running: true}
	if r.providerTests == nil {
		r.providerTests = make(map[providerTestTarget]*providerTestSession)
	}
	r.providerTests[target] = session
	return session, nil
}

func (r *Router) RunProviderTest(ctx context.Context, client ClientType, name, id, model, protocol string) (ProviderTestResult, error) {
	target := providerTestTarget{client, name}
	session, err := r.claimProviderTest(target, id, model)
	if err != nil {
		return ProviderTestResult{}, err
	}
	before := session.healthSnapshot()
	err = session.generate(ctx, model, protocol)
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	session.running = false
	if err != nil {
		return ProviderTestResult{}, err
	}
	delete(r.providerTests, target)
	if ctx.Err() != nil || r.proxies[client] != session.proxy {
		return ProviderTestResult{}, nil
	}
	return session.recover(before), nil
}

func (r *Router) claimProviderTest(target providerTestTarget, id, model string) (*providerTestSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.providerTests[target]
	if session == nil || session.id != id || r.proxies[target.client] != session.proxy {
		return nil, ErrProviderTestExpired
	}
	if session.running {
		return nil, ErrProviderTestBusy
	}
	if !time.Now().Before(session.expires) {
		delete(r.providerTests, target)
		return nil, ErrProviderTestExpired
	}
	if !slices.Contains(session.models, model) {
		return nil, ErrProviderTestModel
	}
	session.running = true
	return session, nil
}

type providerTestHealth struct {
	cooldowns uint64
	failures  uint64
}

func (s *providerTestSession) healthSnapshot() providerTestHealth {
	cp := s.proxy
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	breaker := cp.breakers[s.provider]
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	return providerTestHealth{cooldowns: cp.healthVersion[s.provider], failures: breaker.failureVersion}
}

func (s *providerTestSession) recover(before providerTestHealth) ProviderTestResult {
	cp := s.proxy
	cp.mu.Lock()
	defer cp.mu.Unlock()
	breaker := cp.breakers[s.provider]
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if breaker.failureVersion != before.failures || cp.healthVersion[s.provider] != before.cooldowns {
		return ProviderTestResult{}
	}
	now := time.Now()
	busy := &cp.providerBusy[s.provider]
	restored := now.Before(cp.deactivated[s.provider].until) || now.Before(busy.Until) ||
		(breaker.cfg.enabled && breaker.state != circuitClosed) || cp.availableKeyCountLocked(s.provider, now) == 0
	cp.deactivated[s.provider] = providerDeactivation{}
	cp.keyDeactivated[s.provider][s.key] = providerDeactivation{}
	*busy = providerBusyState{ProbeInFlight: busy.ProbeInFlight}
	breaker.state = circuitClosed
	breaker.openedAt = time.Time{}
	breaker.consecutiveFailures = 0
	breaker.consecutiveSuccesses = 0
	breaker.halfOpenInFlight = 0
	return ProviderTestResult{Recovered: true, Restored: restored}
}

func (s *providerTestSession) request(ctx context.Context, method, path, query string, payload []byte) (*http.Request, *http.Response, error) {
	ctx = context.WithValue(ctx, providerTestContextKey{}, true)
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, errors.New("could not construct the test request")
	}
	req.URL.RawQuery = query
	req = withRequestContext(req, requestContextForClientPath(s.proxy.clientType, path, false))
	req.Header.Set("Content-Type", "application/json")
	if s.proxy.clientType == ClientClaude {
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("x-api-key", s.proxy.providerKeys[s.provider][s.key])
	}
	provider := s.proxy.providers[s.provider]
	provider.Overrides = nil
	resp, prepared, err := s.proxy.doProviderRequestWithPayload(req, provider, s.provider, s.proxy.providerKeys[s.provider][s.key], path, newRequestPayload(payload))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, errProviderTestTimeout
		}
		if !prepared {
			return nil, nil, &ProviderTestFailure{Reason: "prepare", message: "could not prepare the provider request; check credentials and provider configuration"}
		}
		return nil, nil, &ProviderTestFailure{Reason: "network", message: "could not complete the upstream request; check the network, proxy and upstream response"}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		_, reason, _, _ := classifyUpstreamFailure(resp.StatusCode, resp.Header, body, false)
		if code := providerTestModelError(body); code != "" && (reason == "" || resp.StatusCode == http.StatusForbidden) {
			reason = code
		}
		status := fmt.Sprintf("upstream HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		if reason != "" {
			status += " (" + reason + ")"
		}
		var upstreamError any
		var detail string
		if json.Unmarshal(body, &upstreamError) == nil {
			_, _, msg := extractErrorFields(upstreamError)
			accessToken := ""
			if resp.Request != nil {
				accessToken = strings.TrimPrefix(resp.Request.Header.Get("Authorization"), "Bearer ")
			}
			detail = diagnostic.Message(msg, 512, s.proxy.providerKeys[s.provider][s.key], accessToken)
		}
		return nil, nil, &ProviderTestFailure{Reason: reason, UpstreamStatus: resp.StatusCode, message: status, Detail: detail}
	}
	return req, resp, nil
}

func providerTestModelError(body []byte) string {
	var upstreamError any
	if json.Unmarshal(body, &upstreamError) != nil {
		return ""
	}
	code, _, _ := extractErrorFields(upstreamError)
	switch code {
	case "model_not_found", "unsupported_model", "unsupported_parameter", "invalid_value", "context_length_exceeded":
		return code
	default:
		return ""
	}
}

type providerCatalog struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Models []struct {
		Name    string   `json:"name"`
		Methods []string `json:"supportedGenerationMethods"`
	} `json:"models"`
	NextPageToken string          `json:"nextPageToken"`
	HasMore       bool            `json:"has_more"`
	LastID        string          `json:"last_id"`
	Error         json.RawMessage `json:"error"`
}

func (s *providerTestSession) fetchModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, providerTestTimeout)
	defer cancel()
	path := "/v1/models"
	if s.proxy.clientType == ClientGoogle {
		path = "/v1beta/models"
	}
	var models []string
	query := ""
	for page := 0; page < 10; page++ {
		_, resp, err := s.request(ctx, http.MethodGet, path, query, nil)
		if err != nil {
			return nil, err
		}
		var catalog providerCatalog
		if err := decodeProviderTestJSON(resp, &catalog); err != nil {
			return nil, err
		}
		gemini := s.proxy.clientType == ClientGoogle
		if hasProviderTestError(catalog.Error) || (gemini && catalog.Models == nil) || (!gemini && catalog.Data == nil) {
			return nil, errors.New("the upstream did not return a valid model list")
		}
		if gemini {
			for _, model := range catalog.Models {
				id := strings.TrimPrefix(model.Name, "models/")
				if validGeminiTestModel(id) && (model.Methods == nil || slices.Contains(model.Methods, "generateContent")) {
					models = append(models, id)
				}
			}
		} else {
			for _, model := range catalog.Data {
				if strings.TrimSpace(model.ID) != "" {
					models = append(models, model.ID)
				}
			}
		}
		switch {
		case s.proxy.clientType == ClientGoogle && catalog.NextPageToken != "":
			query = url.Values{"pageToken": {catalog.NextPageToken}}.Encode()
		case s.proxy.clientType == ClientClaude && catalog.HasMore && catalog.LastID != "":
			query = url.Values{"after_id": {catalog.LastID}}.Encode()
		default:
			if len(models) == 0 {
				return nil, &ProviderTestFailure{Reason: "empty_catalog", message: "the upstream returned no models suitable for testing"}
			}
			slices.Sort(models)
			return slices.Compact(models), nil
		}
	}
	return nil, errors.New("the upstream model list exceeded the test's pagination limit")
}

var geminiTestModelID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validGeminiTestModel(id string) bool {
	return id != "." && id != ".." && geminiTestModelID.MatchString(id)
}

func hasProviderTestError(value json.RawMessage) bool {
	return len(value) != 0 && string(value) != "null"
}

func decodeProviderTestJSON(resp *http.Response, dest any) error {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerTestLimit+1))
	if errors.Is(err, context.DeadlineExceeded) {
		return errProviderTestTimeout
	}
	if err != nil || len(body) > providerTestLimit || json.Unmarshal(body, dest) != nil {
		return errors.New("the upstream returned an incomplete, oversized or invalid JSON response")
	}
	return nil
}

func (s *providerTestSession) generate(ctx context.Context, model, protocol string) error {
	ctx, cancel := context.WithTimeout(ctx, providerTestTimeout)
	defer cancel()
	const prompt = "Reply with OK."
	messages := []map[string]string{{"role": "user", "content": prompt}}
	if protocol != "" && (s.proxy.clientType != ClientOpenAI || (protocol != "responses" && protocol != "chat")) {
		return ErrProviderTestProtocol
	}
	var path string
	var body any
	switch s.proxy.clientType {
	case ClientOpenAI:
		if s.proxy.providers[s.provider].UsesOAuth() {
			if protocol == "chat" {
				return ErrProviderTestProtocol
			}
			path = "/v1/responses"
			body = map[string]any{"model": model, "input": prompt, "stream": true}
		} else if protocol == "chat" {
			path = "/v1/chat/completions"
			body = map[string]any{"model": model, "messages": messages, "max_completion_tokens": 32}
		} else {
			path = "/v1/responses"
			body = map[string]any{"model": model, "input": prompt, "max_output_tokens": 64}
		}
	case ClientClaude:
		path = "/v1/messages"
		body = map[string]any{"model": model, "messages": messages, "max_tokens": 32}
	case ClientGoogle:
		path = "/v1beta/models/" + model + ":generateContent"
		body = map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
			"generationConfig": map[string]int{"maxOutputTokens": 64},
		}
	default:
		return ErrProviderTestProtocol
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, resp, err := s.request(ctx, http.MethodPost, path, "", payload)
	if err != nil {
		return err
	}
	if path == "/v1/responses" && isEventStreamContentType(resp.Header.Get("Content-Type")) {
		return verifyProviderTestStream(s.proxy.clientType, req, resp)
	}
	var result struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Status     string `json:"status"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := decodeProviderTestJSON(resp, &result); err != nil {
		return err
	}
	if !hasProviderTestError(result.Error) {
		switch path {
		case "/v1/chat/completions":
			if len(result.Choices) > 0 && slices.Contains([]string{"stop", "length"}, result.Choices[0].FinishReason) {
				return nil
			}
		case "/v1/responses":
			if result.Status == "completed" {
				return nil
			}
		case "/v1/messages":
			if result.Type == "message" && result.StopReason != "" && result.StopReason != "refusal" {
				return nil
			}
		default:
			if len(result.Candidates) > 0 && slices.Contains([]string{"STOP", "MAX_TOKENS"}, result.Candidates[0].FinishReason) {
				return nil
			}
		}
	}
	return &ProviderTestFailure{Reason: providerTestModelError(result.Error), message: "the upstream did not return a completed generation"}
}

func verifyProviderTestStream(client ClientType, req *http.Request, resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	tracker := newProtocolTracker(client, req, resp)
	reader := &io.LimitedReader{R: resp.Body, N: providerTestLimit + 1}
	buffer := make([]byte, 8*1024)
	for reader.N > 0 {
		count, err := reader.Read(buffer)
		_, terminal := tracker.append(buffer[:count])
		if terminal || err == io.EOF {
			tracker.finishEOF()
			if tracker.finalStatus() == protocolCompleted {
				return nil
			}
			if tracker.finalStatus() == protocolFailed {
				return errors.New("the upstream reported a generation failure")
			}
			break
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return errProviderTestTimeout
		}
		if err != nil {
			break
		}
	}
	return errors.New("the upstream stream ended without a successful completion event")
}
