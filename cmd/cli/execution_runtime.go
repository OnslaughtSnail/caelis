package main

import (
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

var cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
	return toolexec.New(cfg)
}

func newExecutionRuntime(mode toolexec.PermissionMode, sandboxType string, sandboxHelperPath string) (toolexec.Runtime, error) {
	return cliExecRuntimeBuilder(toolexec.Config{
		PermissionMode:    mode,
		SandboxType:       strings.TrimSpace(sandboxType),
		SandboxHelperPath: strings.TrimSpace(sandboxHelperPath),
	})
}

func validateExplicitSandboxType(sandboxType string, sandboxHelperPath string) error {
	rt, err := newExecutionRuntime(toolexec.PermissionModeDefault, sandboxType, sandboxHelperPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = toolexec.Close(rt)
	}()
	if rt.FallbackToHost() {
		return fmt.Errorf("sandbox type %q is unavailable: %s", sandboxType, rt.FallbackReason())
	}
	return nil
}
