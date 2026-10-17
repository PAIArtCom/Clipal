//go:build !darwin && !windows && !linux

package outbound

import (
	"net/http"
	"net/url"
)

func systemProxy(*http.Request) (*url.URL, error) { return nil, nil }
