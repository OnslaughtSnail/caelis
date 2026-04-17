package main

import (
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestValidateExplicitSandboxType_RejectsFallbackRuntime(t *testing.T) {
	prevSelector := cliSandboxSelector
	t.Cleanup(func() {
		cliSandboxSelector = prevSelector
	})
	cliSandboxSelector = func(cfg toolexec.Config) (toolexec.CommandRunner, toolexec.SandboxDiagnostics, error) {
		return nil, toolexec.SandboxDiagnostics{
			RequestedType:  cfg.SandboxType,
			ResolvedType:   cfg.SandboxType,
			FallbackToHost: true,
			FallbackReason: "probe failed",
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

	_, err := newExecutionRuntime(toolexec.PermissionModeDefault, "landlock", "/tmp/helper", toolexec.SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if captured.SandboxHelperPath != "/tmp/helper" {
		t.Fatalf("expected helper path to be forwarded, got %q", captured.SandboxHelperPath)
	}
}

func TestNewExecutionRuntime_NormalizesAutoSandboxSelection(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})

	var captured toolexec.Config
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		captured = cfg
		return fakeRuntime{permissionMode: cfg.PermissionMode, sandboxType: cfg.SandboxType}, nil
	}

	_, err := newExecutionRuntime(toolexec.PermissionModeDefault, "auto", "/tmp/helper", toolexec.SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if captured.SandboxType != normalizeSandboxType("auto") {
		t.Fatalf("expected normalized sandbox type %q, got %q", normalizeSandboxType("auto"), captured.SandboxType)
	}
}

func TestNewExecutionRuntime_PassesSandboxPolicy(t *testing.T) {
	prevBuilder := cliExecRuntimeBuilder
	t.Cleanup(func() {
		cliExecRuntimeBuilder = prevBuilder
	})

	want := toolexec.SandboxPolicy{
		ReadableRoots:    []string{"."},
		WritableRoots:    []string{"."},
		ReadOnlySubpaths: []string{".git"},
	}
	var captured toolexec.Config
	cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
		captured = cfg
		return fakeRuntime{permissionMode: cfg.PermissionMode, sandboxType: cfg.SandboxType}, nil
	}

	_, err := newExecutionRuntime(toolexec.PermissionModeDefault, "landlock", "/tmp/helper", want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(captured.SandboxPolicy.ReadableRoots, ",") != "." {
		t.Fatalf("expected readable roots forwarded, got %#v", captured.SandboxPolicy)
	}
	if strings.Join(captured.SandboxPolicy.WritableRoots, ",") != "." {
		t.Fatalf("expected writable roots forwarded, got %#v", captured.SandboxPolicy)
	}
	if strings.Join(captured.SandboxPolicy.ReadOnlySubpaths, ",") != ".git" {
		t.Fatalf("expected readonly subpaths forwarded, got %#v", captured.SandboxPolicy)
	}
}
