package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

type antigravityModel struct {
	ThinkingBudget *float64
	ThinkingLevel  string
}

type antigravityCatalogCall struct {
	done      chan struct{}
	models    map[string]antigravityModel
	err       error
	expiresAt time.Time
}

func antigravityThinkingConfig(root map[string]any) map[string]any {
	generation, _ := antigravityOAuthField(root, "generationConfig", "generation_config")
	config, _ := generation.(map[string]any)
	thinking, _ := antigravityOAuthField(config, "thinkingConfig", "thinking_config")
	result, _ := thinking.(map[string]any)
	return result
}

func antigravityModelNeedsCatalog(model string, thinking map[string]any) bool {
	_, level := antigravityOAuthField(thinking, "thinkingLevel", "thinking_level")
	_, budget := antigravityOAuthField(thinking, "thinkingBudget", "thinking_budget")
	return level || budget || strings.HasSuffix(model, "-preview")
}

// Resolve only aliases established by this account's catalog. No version list,
// default model, approximate budget, or cross-family fallback is inferred.
func resolveAntigravityModel(model string, thinking map[string]any, catalog map[string]antigravityModel) (string, error) {
	if _, ok := catalog[model]; ok {
		return model, nil
	}
	base := strings.TrimSuffix(model, "-preview")
	if _, ok := catalog[base]; ok {
		return base, nil
	}
	level, _ := antigravityOAuthStringField(thinking, "thinkingLevel", "thinking_level")
	level = strings.ToLower(level)
	budgetValue, budgetPresent := antigravityOAuthField(thinking, "thinkingBudget", "thinking_budget")
	budget, budgetOK := budgetValue.(float64)
	match := ""
	for id, entry := range catalog {
		boundary := strings.LastIndexByte(id, '-')
		if boundary < 0 || id[:boundary] != base || entry.ThinkingLevel == "" {
			continue
		}
		if level != "" {
			if entry.ThinkingLevel != level {
				continue
			}
		} else if !budgetPresent || !budgetOK || entry.ThinkingBudget == nil || *entry.ThinkingBudget != budget {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("ambiguous antigravity model %q for the requested thinking configuration", model)
		}
		match = id
	}
	if match != "" {
		return match, nil
	}
	return model, nil
}

func parseAntigravityCatalog(raw map[string]any) map[string]antigravityModel {
	models := make(map[string]antigravityModel, len(raw))
	for id, value := range raw {
		id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
		if id == "" {
			continue
		}
		fields, _ := value.(map[string]any)
		entry := antigravityModel{}
		if budget, ok := fields["thinkingBudget"].(float64); ok {
			entry.ThinkingBudget = &budget
		}
		if boundary := strings.LastIndexByte(id, '-'); boundary >= 0 {
			suffix := id[boundary+1:]
			switch suffix {
			case "minimal", "low", "medium", "high", "xhigh", "tiered":
				if fields["supportsThinking"] == true {
					entry.ThinkingLevel = suffix
				}
			}
		}
		models[id] = entry
	}
	return models
}

func (cp *ClientProxy) antigravityModelCatalog(ctx context.Context, cred *oauthpkg.Credential, providerIndex int, httpClient *http.Client) (map[string]antigravityModel, error) {
	// Include transport selection, token, project, and account environment. Changes
	// cannot reuse a catalog from another account or outbound route.
	identity, _ := json.Marshal(struct {
		Ref, Token, Project string
		Environment         *oauthpkg.RequestEnvironment
	}{cred.Ref, cred.AccessToken, cred.Metadata[geminiOAuthProjectMetadata], cred.Environment})
	key := fmt.Sprintf("%d:%p:%x", providerIndex, httpClient, sha256.Sum256(identity))
	now := time.Now()
	cp.mu.Lock()
	if call := cp.antigravityCatalogs[key]; call != nil && (call.expiresAt.IsZero() || now.Before(call.expiresAt)) {
		cp.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return call.models, call.err
		}
	}
	if cp.antigravityCatalogs == nil {
		cp.antigravityCatalogs = make(map[string]*antigravityCatalogCall)
	}
	for existing, entry := range cp.antigravityCatalogs {
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			delete(cp.antigravityCatalogs, existing)
		}
	}
	call := &antigravityCatalogCall{done: make(chan struct{})}
	cp.antigravityCatalogs[key] = call
	cp.mu.Unlock()
	raw, err := cp.oauth.AntigravityModelsWithHTTPClient(ctx, cred, httpClient)
	models := parseAntigravityCatalog(raw)
	cp.mu.Lock()
	call.models, call.err = models, err
	call.expiresAt = time.Now().Add(5 * time.Minute)
	if err != nil {
		delete(cp.antigravityCatalogs, key)
	}
	close(call.done)
	cp.mu.Unlock()
	return models, err
}

func (cp *ClientProxy) resolveAntigravityRequestModel(original *http.Request, cred *oauthpkg.Credential, providerIndex int, httpClient *http.Client, path string, body []byte, originalRoot map[string]any) ([]byte, error) {
	model, err := geminiModelFromPath(path)
	if err != nil {
		// Model listing has no model path.
		return body, nil
	}
	// Reuse the parsed downstream payload; large inline data needs no second
	// JSON decode when model resolution is unnecessary or leaves the ID intact.
	thinking := antigravityThinkingConfig(originalRoot)
	if !antigravityModelNeedsCatalog(model, thinking) {
		return body, nil
	}
	models, err := cp.antigravityModelCatalog(original.Context(), cred, providerIndex, httpClient)
	if err != nil {
		return nil, fmt.Errorf("discover antigravity model: %w", err)
	}
	resolved, err := resolveAntigravityModel(model, thinking, models)
	if err != nil {
		return nil, err
	}
	if resolved == model {
		return body, nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	request, _ := envelope["request"].(map[string]any)
	if _, ok := envelope["model"]; ok {
		envelope["model"] = resolved
	} else {
		request["model"] = "models/" + resolved
	}
	return json.Marshal(envelope)
}
