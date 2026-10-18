package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cloudCodeOperationConfig struct {
	Client                      *http.Client
	BaseURL, Version, UserAgent string
	Now                         func() time.Time
	Sleep                       func(time.Duration)
}

type cloudCodeHTTPError struct {
	Status int
	Body   string
}

func (e *cloudCodeHTTPError) Error() string {
	return fmt.Sprintf("request failed with status %d: %s", e.Status, e.Body)
}

// Poll Cloud Code operations using the caller’s service origin, authorization,
// and transport identity.
func pollCloudCodeOperation(ctx context.Context, token, name string, c cloudCodeOperationConfig) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("operation name is required")
	}
	parsed, err := url.Parse(name)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid operation name")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == ".." || segment == "." {
			return nil, fmt.Errorf("invalid operation name")
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, defaultGoogleOperationPollTimeout)
	defer cancel()
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/" + strings.Trim(c.Version, "/") + "/" + strings.TrimLeft(name, "/")
	for attempt := 0; attempt < defaultGoogleOperationPollMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := waitCloudCode(ctx, defaultGoogleOperationPollDelay, c.Sleep); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.UserAgent)
		resp, err := c.Client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(body) > 1<<20 {
			return nil, fmt.Errorf("operation response is too large")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt+1 < defaultGoogleOperationPollMaxAttempts && googleCloudCodeShouldRetry(resp.StatusCode) {
				if err := waitCloudCode(ctx, googleCloudCodeRetryDelay(resp.Header, c.Now()), c.Sleep); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &cloudCodeHTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		}
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
		if err := cloudCodeOperationError(data); err != nil {
			return nil, err
		}
		if done, _ := data["done"].(bool); done {
			return data, nil
		}
	}
	return nil, fmt.Errorf("operation %q did not complete after %d polls", name, defaultGoogleOperationPollMaxAttempts)
}

func cloudCodeOperationError(data map[string]any) error {
	raw, exists := data["error"]
	if !exists || raw == nil {
		return nil
	}
	detail, _ := raw.(map[string]any)
	message, _ := detail["message"].(string)
	if strings.TrimSpace(message) == "" {
		message = "unspecified upstream error"
	}
	return fmt.Errorf("project operation failed: %s", message)
}

func waitCloudCode(ctx context.Context, delay time.Duration, sleep func(time.Duration)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sleep != nil {
		sleep(delay)
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
