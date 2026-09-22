package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	fetchLatestReleaseFunc = fetchLatestRelease
	osExecutableFunc       = os.Executable
	filepathAbsFunc        = filepath.Abs
	newHTTPClientFunc      = func(timeout time.Duration) *http.Client {
		return &http.Client{Timeout: timeout}
	}
	downloadToTempFileFunc = downloadToTempFile
	sha256FileFunc         = sha256File
	applyUnixFunc          = applyUnix
	applyWindowsFunc       = applyWindows
	copyFileContentsFunc   = io.Copy
)

type Plan struct {
	CurrentVersion string
	LatestVersion  string
	BinaryAsset    Asset
	ChecksumsAsset Asset
	ExecutablePath string
}

func BuildPlan(ctx context.Context, client *http.Client, currentVersion string) (*Plan, error) {
	rel, err := fetchLatestReleaseFunc(ctx, client)
	if err != nil {
		return nil, err
	}

	binName, err := expectedBinaryAssetName()
	if err != nil {
		return nil, err
	}
	bin, ok := findAssetByName(rel.Assets, binName)
	if !ok {
		return nil, fmt.Errorf("release %s missing asset %q", rel.TagName, binName)
	}
	chk, ok := findAssetByName(rel.Assets, ChecksumsAssetName)
	if !ok {
		return nil, fmt.Errorf("release %s missing asset %q", rel.TagName, ChecksumsAssetName)
	}

	exe, err := osExecutableFunc()
	if err != nil {
		return nil, err
	}
	exe, err = filepathAbsFunc(exe)
	if err != nil {
		return nil, err
	}

	return &Plan{
		CurrentVersion: strings.TrimSpace(currentVersion),
		LatestVersion:  strings.TrimSpace(rel.TagName),
		BinaryAsset:    *bin,
		ChecksumsAsset: *chk,
		ExecutablePath: exe,
	}, nil
}

func NeedsUpdate(currentVersion string, latestTag string) (needs bool, comparable bool) {
	cur := strings.TrimSpace(currentVersion)
	lat := strings.TrimSpace(latestTag)
	if cur == "" {
		return true, false
	}
	if cur == "dev" {
		return true, false
	}
	cmp, ok := compareVersions(cur, lat)
	if !ok {
		return true, false
	}
	return cmp < 0, true
}

func Update(ctx context.Context, currentVersion string, opts Options) (*Plan, bool, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	client := newHTTPClientFunc(opts.Timeout)
	emitProgress(opts.Progress, ProgressEvent{
		Phase:   ProgressCheckingRelease,
		Message: "Checking latest release",
	})

	plan, err := BuildPlan(ctx, client, currentVersion)
	if err != nil {
		return nil, false, fmt.Errorf("check latest release: %w", err)
	}

	needs, comparable := NeedsUpdate(plan.CurrentVersion, plan.LatestVersion)
	if !needs && !opts.Force {
		return plan, false, nil
	}
	if comparable {
		if cmp, ok := compareVersions(plan.CurrentVersion, plan.LatestVersion); ok && cmp > 0 && !opts.Force {
			return plan, false, fmt.Errorf("current version %s is newer than latest %s (use --force to downgrade)", plan.CurrentVersion, plan.LatestVersion)
		}
	}

	if opts.Check || opts.DryRun {
		return plan, needs, nil
	}

	emitProgress(opts.Progress, ProgressEvent{
		Phase:      ProgressDownloadingChecksum,
		Message:    "Downloading " + plan.ChecksumsAsset.Name,
		TotalBytes: plan.ChecksumsAsset.Size,
	})
	checksumsPath, err := downloadToTempFileFunc(
		ctx,
		client,
		plan.ChecksumsAsset.BrowserDownloadURL,
		"clipal-checksums-",
		plan.ChecksumsAsset.Size,
		downloadProgress(opts.Progress, ProgressDownloadingChecksum, "Downloading "+plan.ChecksumsAsset.Name),
	)
	if err != nil {
		return plan, false, fmt.Errorf("download checksums: %w", err)
	}
	defer func() { _ = os.Remove(checksumsPath) }()

	checksumsBytes, err := os.ReadFile(checksumsPath)
	if err != nil {
		return plan, false, err
	}
	sumMap, err := parseChecksums(checksumsBytes)
	if err != nil {
		return plan, false, err
	}

	wantSum, ok := sumMap[plan.BinaryAsset.Name]
	if !ok {
		return plan, false, fmt.Errorf("checksums.txt missing entry for %q", plan.BinaryAsset.Name)
	}

	emitProgress(opts.Progress, ProgressEvent{
		Phase:      ProgressDownloadingBinary,
		Message:    "Downloading " + plan.BinaryAsset.Name,
		TotalBytes: plan.BinaryAsset.Size,
	})
	newBinPath, err := downloadToTempFileFunc(
		ctx,
		client,
		plan.BinaryAsset.BrowserDownloadURL,
		"clipal-bin-",
		plan.BinaryAsset.Size,
		downloadProgress(opts.Progress, ProgressDownloadingBinary, "Downloading "+plan.BinaryAsset.Name),
	)
	if err != nil {
		return plan, false, fmt.Errorf("download binary: %w", err)
	}
	cleanupNew := runtime.GOOS != "windows"
	defer func() {
		if cleanupNew {
			_ = os.Remove(newBinPath)
		}
	}()

	emitProgress(opts.Progress, ProgressEvent{
		Phase:   ProgressVerifyingChecksum,
		Message: "Verifying SHA256 checksum",
	})
	gotSum, err := sha256FileFunc(newBinPath)
	if err != nil {
		return plan, false, fmt.Errorf("verify SHA256 checksum: %w", err)
	}
	if !strings.EqualFold(gotSum, wantSum) {
		return plan, false, fmt.Errorf("sha256 mismatch for %s: got %s want %s", plan.BinaryAsset.Name, gotSum, wantSum)
	}

	emitProgress(opts.Progress, ProgressEvent{
		Phase:   ProgressInstalling,
		Message: "Installing update",
	})
	switch runtime.GOOS {
	case "windows":
		if err := applyWindowsFunc(plan.ExecutablePath, newBinPath, opts.Relaunch, opts.Progress == nil); err != nil {
			return plan, false, fmt.Errorf("schedule update installation: %w", err)
		}
	default:
		if err := applyUnixFunc(plan.ExecutablePath, newBinPath); err != nil {
			return plan, false, fmt.Errorf("install update: %w", err)
		}
	}

	return plan, true, nil
}

func emitProgress(progress func(ProgressEvent), event ProgressEvent) {
	if progress != nil {
		progress(event)
	}
}

func downloadProgress(progress func(ProgressEvent), phase ProgressPhase, message string) func(int64, int64) {
	if progress == nil {
		return nil
	}
	return func(completed int64, total int64) {
		progress(ProgressEvent{
			Phase:          phase,
			Message:        message,
			CompletedBytes: completed,
			TotalBytes:     total,
		})
	}
}

func copyFile(dst string, src string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := copyFileContentsFunc(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
