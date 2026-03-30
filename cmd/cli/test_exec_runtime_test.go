package main

import (
	"context"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type noopExecRunner struct{}

func (noopExecRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func newCLITestExecRuntime(t *testing.T, mode toolexec.PermissionMode) toolexec.Runtime {
	t.Helper()
	cfg := toolexec.Config{
		PermissionMode: mode,
		SandboxRunner:  noopExecRunner{},
	}
	if mode == toolexec.PermissionModeDefault {
		cfg.SandboxType = cliTestSandboxType()
	}
	rt, err := toolexec.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(rt) })
	return rt
}
