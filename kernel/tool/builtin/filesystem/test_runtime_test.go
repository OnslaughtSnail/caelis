package filesystem

import (
	"runtime"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func newTestRuntime(t *testing.T) toolexec.Runtime {
	t.Helper()
	sandboxType := "bwrap"
	if runtime.GOOS == "darwin" {
		sandboxType = "seatbelt"
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxType:    sandboxType,
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt
}
