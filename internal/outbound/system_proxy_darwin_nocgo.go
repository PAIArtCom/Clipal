//go:build darwin && !cgo

package outbound

import (
	"context"
	"net/http"
	"net/url"
	"os/exec"
	"time"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(req.Context(), time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return nil, err
	}
	return scutilSystemProxy(req, string(data))
}
