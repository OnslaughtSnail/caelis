package runtime

import (
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func newCoreRuntime(t *testing.T) toolexec.Runtime {
	t.Helper()
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt
}
