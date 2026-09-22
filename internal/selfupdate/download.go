package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func downloadToTempFile(
	ctx context.Context,
	client *http.Client,
	url string,
	prefix string,
	expectedSize int64,
	progress func(completed int64, total int64),
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if tok := strings.TrimSpace(getToken()); tok != "" {
		// GitHub release asset URLs are public, but keep the token for higher rate limits if used.
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = expectedSize
	}
	if total < 0 {
		total = 0
	}
	if progress != nil {
		progress(0, total)
	}

	f, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() {
		_ = f.Close()
	}()

	_, err = io.Copy(f, &progressReader{
		reader:   resp.Body,
		total:    total,
		progress: progress,
	})
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

type progressReader struct {
	reader    io.Reader
	completed int64
	total     int64
	progress  func(completed int64, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.completed += int64(n)
		if r.progress != nil {
			r.progress(r.completed, r.total)
		}
	}
	return n, err
}
