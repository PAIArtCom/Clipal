package integration

import (
	"os"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithClientEnvironment(m.Run))
}

func TestInheritedClientEnvironmentIsIsolated(t *testing.T) {
	testutil.CheckClientEnvironmentIsolation(t,
		"TestTakeoverCyclesPreserveBaselineAcrossUpdates",
		"TestOpenCodeStatus_NotConfiguredWhenFileMissing",
		"TestGooseStatus_NotConfiguredWhenFileMissing",
		"TestTakeoverTargetsRespectEnvironmentAndSelection",
		"TestClientDirectoryOverridesAndSelection",
		"TestClientsRejectMalformedBeforeCreatingBackup",
		"TestOtherClientPathsFollowClientContracts",
		"TestAntigravityFreshShellLoadsAndClearsTakeover",
	)
}
