package proxy

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	claudeOAuthAnthropicVersion        = "2023-06-01"
	claudeOAuthAppVersion              = "2.1.207"
	claudeOAuthDefaultEntrypoint       = "sdk-cli"
	claudeOAuthUserAgent               = "claude-cli/" + claudeOAuthAppVersion + " (external, " + claudeOAuthDefaultEntrypoint + ")"
	claudeOAuthClientApp               = "claude-code"
	claudeOAuthAppName                 = "claude-code"
	claudeOAuthXApp                    = "cli"
	claudeOAuthAccept                  = "application/json"
	claudeOAuthAcceptEncoding          = "gzip, deflate, br, zstd"
	claudeOAuthStainlessRetryCount     = "0"
	claudeOAuthStainlessHelperMethod   = "stream"
	claudeOAuthStainlessRuntime        = "node"
	claudeOAuthStainlessLang           = "js"
	claudeOAuthStainlessTimeout        = "300"
	claudeOAuthStainlessPackageVersion = "0.94.0"
	claudeOAuthStainlessRuntimeVersion = "v26.3.0"
	claudeOAuthStainlessOS             = "MacOS"
	claudeOAuthStainlessArch           = "arm64"
	claudeOAuthBillingVersionSalt      = "59cf53e54c78"
	claudeOAuthBillingCCHSeed          = uint64(0x6E52736AC806831E)
)

func claudeOAuthBillingVersionForText(text string) string {
	sum := sha256.Sum256([]byte(claudeOAuthBillingVersionSalt + claudeOAuthBillingFingerprintSegment(text) + claudeOAuthAppVersion))
	return fmt.Sprintf("%s.%03x", claudeOAuthAppVersion, uint16(sum[0])<<4|uint16(sum[1])>>4)
}

func claudeOAuthBillingFingerprintSegment(text string) string {
	codeUnits := utf16.Encode([]rune(text))
	picks := []int{4, 7, 20}
	var builder strings.Builder
	builder.Grow(len(picks))
	for _, idx := range picks {
		if idx >= 0 && idx < len(codeUnits) {
			_, _ = builder.WriteRune(rune(codeUnits[idx]))
			continue
		}
		_ = builder.WriteByte('0')
	}
	return builder.String()
}
