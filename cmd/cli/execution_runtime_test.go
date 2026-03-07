package main

import (
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestValidateExplicitSandboxType_RejectsFallbackRuntime(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		return fakeRuntime{
			permissionMode: cfg.PermissionMode,
			sandboxType:    cfg.SandboxType,
			fallbackToHost: true,
			fallbackReason: "probe failed",
		}, nil
	}

	err := validateExplicitSandboxType("bwrap", "/tmp/helper")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `sandbox type "bwrap" is unavailable`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewExecutionRuntime_PassesSandboxHelperPath(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})

	var captured toolexec.Config
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		captured = cfg
		return fakeRuntime{permissionMode: cfg.PermissionMode, sandboxType: cfg.SandboxType}, nil
	}

	_, err := newExecutionRuntime(toolexec.PermissionModeDefault, "landlock", "/tmp/helper")
	if err != nil {
		t.Fatal(err)
	}
	if captured.SandboxHelperPath != "/tmp/helper" {
		t.Fatalf("expected helper path to be forwarded, got %q", captured.SandboxHelperPath)
	}
}
