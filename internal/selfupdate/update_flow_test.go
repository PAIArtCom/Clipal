package selfupdate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withSelfUpdateStubs(t *testing.T) {
	t.Helper()
	origFetch := fetchLatestReleaseFunc
	origExe := osExecutableFunc
	origAbs := filepathAbsFunc
	origClient := newHTTPClientFunc
	origDownload := downloadToTempFileFunc
	origSHA := sha256FileFunc
	origApplyUnix := applyUnixFunc
	origApplyWindows := applyWindowsFunc
	origCopy := copyFileContentsFunc
	t.Cleanup(func() {
		fetchLatestReleaseFunc = origFetch
		osExecutableFunc = origExe
		filepathAbsFunc = origAbs
		newHTTPClientFunc = origClient
		downloadToTempFileFunc = origDownload
		sha256FileFunc = origSHA
		applyUnixFunc = origApplyUnix
		applyWindowsFunc = origApplyWindows
		copyFileContentsFunc = origCopy
		_ = os.Unsetenv("GITHUB_TOKEN")
		_ = os.Unsetenv("GH_TOKEN")
	})
}

func testRelease(tag string) *Release {
	binName, err := expectedBinaryAssetName()
	if err != nil {
		panic(err)
	}
	return &Release{
		TagName: tag,
		Assets: []Asset{
			{Name: binName, BrowserDownloadURL: "https://example.com/bin"},
			{Name: ChecksumsAssetName, BrowserDownloadURL: "https://example.com/checksums"},
		},
	}
}

func TestBuildPlanFailures(t *testing.T) {
	withSelfUpdateStubs(t)

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{TagName: "v1.0.0", Assets: []Asset{{Name: ChecksumsAssetName}}}, nil
	}
	if _, err := BuildPlan(context.Background(), &http.Client{}, "v0.9.0"); err == nil || !strings.Contains(err.Error(), "missing asset") {
		t.Fatalf("expected missing binary asset error, got %v", err)
	}

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		binName, err := expectedBinaryAssetName()
		if err != nil {
			return nil, err
		}
		return &Release{TagName: "v1.0.0", Assets: []Asset{{Name: binName}}}, nil
	}
	if _, err := BuildPlan(context.Background(), &http.Client{}, "v0.9.0"); err == nil || !strings.Contains(err.Error(), ChecksumsAssetName) {
		t.Fatalf("expected missing checksums asset error, got %v", err)
	}

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return testRelease("v1.0.0"), nil
	}
	osExecutableFunc = func() (string, error) {
		return "", errors.New("no executable")
	}
	if _, err := BuildPlan(context.Background(), &http.Client{}, "v0.9.0"); err == nil || !strings.Contains(err.Error(), "no executable") {
		t.Fatalf("expected executable error, got %v", err)
	}
}

func TestNeedsUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		current        string
		latest         string
		wantNeeds      bool
		wantComparable bool
	}{
		{"EmptyCurrent", "", "v1.0.0", true, false},
		{"Dev", "dev", "v1.0.0", true, false},
		{"Downgrade", "v2.0.0", "v1.0.0", false, true},
		{"Upgrade", "v1.0.0", "v1.1.0", true, true},
		{"Uncomparable", "main", "v1.1.0", true, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			needs, comparable := NeedsUpdate(tt.current, tt.latest)
			if needs != tt.wantNeeds || comparable != tt.wantComparable {
				t.Fatalf("NeedsUpdate(%q,%q) = (%v,%v), want (%v,%v)", tt.current, tt.latest, needs, comparable, tt.wantNeeds, tt.wantComparable)
			}
		})
	}
}

func TestUpdateCheckDryRunForceAndFailures(t *testing.T) {
	withSelfUpdateStubs(t)

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return testRelease("v1.1.0"), nil
	}
	osExecutableFunc = func() (string, error) { return "/tmp/clipal", nil }
	filepathAbsFunc = func(path string) (string, error) { return path, nil }

	plan, changed, err := Update(context.Background(), "v1.0.0", Options{Check: true, Timeout: time.Second})
	if err != nil || !changed || plan.LatestVersion != "v1.1.0" {
		t.Fatalf("check update = (%#v,%v,%v)", plan, changed, err)
	}

	plan, changed, err = Update(context.Background(), "v2.0.0", Options{DryRun: true, Force: true, Timeout: time.Second})
	if err != nil || changed {
		t.Fatalf("force dry run = (%#v,%v,%v)", plan, changed, err)
	}

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{TagName: "v1.1.0", Assets: []Asset{{Name: "clipal-darwin-arm64"}}}, nil
	}
	if _, _, err := Update(context.Background(), "v1.0.0", Options{Timeout: time.Second}); err == nil {
		t.Fatalf("expected missing asset error")
	}

	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return testRelease("v1.1.0"), nil
	}
	downloadToTempFileFunc = func(ctx context.Context, client *http.Client, url string, prefix string, expectedSize int64, progress func(int64, int64)) (string, error) {
		return "", errors.New("download failed")
	}
	if _, _, err := Update(context.Background(), "v1.0.0", Options{Timeout: time.Second}); err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("expected download error, got %v", err)
	}

	var downloadCalls int
	binName, err := expectedBinaryAssetName()
	if err != nil {
		t.Fatalf("expectedBinaryAssetName: %v", err)
	}
	downloadToTempFileFunc = func(ctx context.Context, client *http.Client, url string, prefix string, expectedSize int64, progress func(int64, int64)) (string, error) {
		downloadCalls++
		tmp, err := os.CreateTemp("", prefix)
		if err != nil {
			return "", err
		}
		defer func() { _ = tmp.Close() }()
		switch downloadCalls {
		case 1:
			_, _ = tmp.WriteString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  " + binName + "\n")
		case 2:
			_, _ = tmp.WriteString("binary")
		}
		return tmp.Name(), nil
	}
	sha256FileFunc = func(path string) (string, error) {
		return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil
	}
	if _, _, err := Update(context.Background(), "v1.0.0", Options{Timeout: time.Second}); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestUpdateReportsProgressPhases(t *testing.T) {
	withSelfUpdateStubs(t)

	binName, err := expectedBinaryAssetName()
	if err != nil {
		t.Fatalf("expectedBinaryAssetName: %v", err)
	}
	fetchLatestReleaseFunc = func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{
			TagName: "v1.1.0",
			Assets: []Asset{
				{Name: binName, BrowserDownloadURL: "https://example.com/bin", Size: 6},
				{Name: ChecksumsAssetName, BrowserDownloadURL: "https://example.com/checksums", Size: 64},
			},
		}, nil
	}
	osExecutableFunc = func() (string, error) { return "/tmp/clipal", nil }
	filepathAbsFunc = func(path string) (string, error) { return path, nil }

	var downloadCalls int
	downloadToTempFileFunc = func(ctx context.Context, client *http.Client, url string, prefix string, expectedSize int64, progress func(int64, int64)) (string, error) {
		downloadCalls++
		file, err := os.CreateTemp("", prefix)
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		if progress != nil {
			progress(expectedSize, expectedSize)
		}
		if downloadCalls == 1 {
			_, _ = file.WriteString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  " + binName + "\n")
		} else {
			_, _ = file.WriteString("binary")
		}
		return file.Name(), nil
	}
	sha256FileFunc = func(path string) (string, error) {
		return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
	}
	applyUnixFunc = func(exePath string, newBinPath string) error { return nil }
	applyWindowsFunc = func(exePath string, newBinPath string, relaunch bool, quiet bool) error { return nil }

	var phases []ProgressPhase
	var previous ProgressPhase
	_, changed, err := Update(context.Background(), "v1.0.0", Options{
		Timeout: time.Second,
		Progress: func(event ProgressEvent) {
			if event.Phase != previous {
				phases = append(phases, event.Phase)
				previous = event.Phase
			}
		},
	})
	if err != nil || !changed {
		t.Fatalf("Update() = changed %v, err %v", changed, err)
	}
	want := []ProgressPhase{
		ProgressCheckingRelease,
		ProgressDownloadingChecksum,
		ProgressDownloadingBinary,
		ProgressVerifyingChecksum,
		ProgressInstalling,
	}
	if len(phases) != len(want) {
		t.Fatalf("progress phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("progress phases = %v, want %v", phases, want)
		}
	}
}

func TestDownloadToTempFile(t *testing.T) {
	t.Run("Non2xx", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer srv.Close()
		if _, err := downloadToTempFile(context.Background(), srv.Client(), srv.URL, "clipal-test-", 0, nil); err == nil {
			t.Fatalf("expected non-2xx error")
		}
	})

	t.Run("ContextCanceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := downloadToTempFile(ctx, http.DefaultClient, "http://example.com", "clipal-test-", 0, nil); err == nil {
			t.Fatalf("expected context error")
		}
	})

	t.Run("AuthorizationHeader", func(t *testing.T) {
		_ = os.Setenv("GITHUB_TOKEN", "secret")
		defer func() { _ = os.Unsetenv("GITHUB_TOKEN") }()

		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, "ok")
		}))
		defer srv.Close()

		path, err := downloadToTempFile(context.Background(), srv.Client(), srv.URL, "clipal-test-", 0, nil)
		if err != nil {
			t.Fatalf("downloadToTempFile: %v", err)
		}
		defer func() { _ = os.Remove(path) }()
		if gotAuth != "Bearer secret" {
			t.Fatalf("Authorization = %q", gotAuth)
		}
	})

	t.Run("ReportsProgressWithExpectedSizeFallback", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Transfer-Encoding", "chunked")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = io.WriteString(w, "hello")
		}))
		defer srv.Close()

		var events [][2]int64
		path, err := downloadToTempFile(context.Background(), srv.Client(), srv.URL, "clipal-test-", 5, func(completed int64, total int64) {
			events = append(events, [2]int64{completed, total})
		})
		if err != nil {
			t.Fatalf("downloadToTempFile: %v", err)
		}
		defer func() { _ = os.Remove(path) }()
		if len(events) < 2 {
			t.Fatalf("progress events = %v", events)
		}
		if got := events[0]; got != [2]int64{0, 5} {
			t.Fatalf("first progress event = %v, want [0 5]", got)
		}
		if got := events[len(events)-1]; got != [2]int64{5, 5} {
			t.Fatalf("last progress event = %v, want [5 5]", got)
		}
	})
}

func TestCopyFile(t *testing.T) {
	withSelfUpdateStubs(t)

	if err := copyFile(filepath.Join(t.TempDir(), "dst"), filepath.Join(t.TempDir(), "missing"), 0o755); err == nil {
		t.Fatalf("expected source open failure")
	}

	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	copyFileContentsFunc = func(dst io.Writer, src io.Reader) (int64, error) {
		return 0, errors.New("copy failed")
	}
	if err := copyFile(filepath.Join(t.TempDir(), "dst"), src, 0o755); err == nil || !strings.Contains(err.Error(), "copy failed") {
		t.Fatalf("expected copy failure, got %v", err)
	}
}

func TestFetchLatestRelease(t *testing.T) {
	_ = os.Setenv("GH_TOKEN", "tok")
	defer func() { _ = os.Unsetenv("GH_TOKEN") }()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.3","assets":[{"name":"a","browser_download_url":"https://example.com/a","size":1}]}`)
	}))
	defer srv.Close()

	client := srv.Client()
	fetchLatestReleaseFunc = fetchLatestRelease
	origTransport := client.Transport
	if origTransport == nil {
		origTransport = http.DefaultTransport
	}
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return origTransport.RoundTrip(req)
	})

	rel, err := fetchLatestRelease(context.Background(), client)
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if rel.TagName != "v1.2.3" || gotAuth != "Bearer tok" {
		t.Fatalf("release = %#v auth=%q", rel, gotAuth)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
