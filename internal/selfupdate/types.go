package selfupdate

import "time"

const (
	Owner = "lansespirit"
	Repo  = "Clipal"
)

type Options struct {
	Check    bool
	Force    bool
	DryRun   bool
	Timeout  time.Duration
	Relaunch bool // Windows: relaunch the updated binary after replacing.
	Progress func(ProgressEvent)
}

type ProgressPhase string

const (
	ProgressCheckingRelease     ProgressPhase = "checking_release"
	ProgressDownloadingChecksum ProgressPhase = "downloading_checksums"
	ProgressDownloadingBinary   ProgressPhase = "downloading_binary"
	ProgressVerifyingChecksum   ProgressPhase = "verifying_checksum"
	ProgressInstalling          ProgressPhase = "installing"
)

// ProgressEvent reports an observable stage of an update. Byte counts are set
// for downloads; TotalBytes is zero when the server does not disclose a size.
type ProgressEvent struct {
	Phase          ProgressPhase
	Message        string
	CompletedBytes int64
	TotalBytes     int64
}

type Release struct {
	TagName string
	Assets  []Asset
}

type Asset struct {
	Name               string
	BrowserDownloadURL string
	Size               int64
}
