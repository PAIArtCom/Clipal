package outbound

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	// Linux has no universal desktop proxy API. Read GNOME's published settings
	// when available; headless and other desktops continue using environment policy.
	binary, err := exec.LookPath("gsettings")
	if err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(req.Context(), time.Second)
	defer cancel()
	read := func(schema, key string) (string, error) {
		data, err := exec.CommandContext(ctx, binary, "get", schema, key).Output()
		return strings.TrimSpace(string(data)), err
	}
	mode, err := read("org.gnome.system.proxy", "mode")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exit, ok := err.(*exec.ExitError); ok && strings.Contains(string(exit.Stderr), "No such schema") {
			return nil, nil
		}
		return nil, fmt.Errorf("read desktop proxy settings: %w", err)
	}
	if mode == "'none'" {
		return nil, nil
	}
	if mode != "'manual'" {
		return nil, fmt.Errorf("automatic desktop proxy requires a PAC-capable system resolver")
	}
	return gnomeSystemProxy(req, read)
}
