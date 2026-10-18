package web

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
		"TestHandleListIntegrations_ReturnsSupportedProducts",
		"TestHandleIntegrationApply_Claude",
		"TestHandleIntegrationRollback_Codex",
		"TestIntegrationListAndRollbackRecoverMalformedClient",
	)
}
