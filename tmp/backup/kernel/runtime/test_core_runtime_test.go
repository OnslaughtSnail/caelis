package runtime

import (
	"context"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type noopExecRunner struct{}

func (noopExecRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func newCoreRuntime(t *testing.T) toolexec.Runtime {
	t.Helper()
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxRunner:  noopExecRunner{},
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt
}
