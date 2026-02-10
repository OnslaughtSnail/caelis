package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type noopCommandRunner struct{}

func (noopCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return toolexec.CommandResult{}, nil
}

type failingProbeRunner struct{}

func (failingProbeRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return toolexec.CommandResult{}, nil
}

func (failingProbeRunner) Probe(context.Context) error {
	return errors.New("docker is unavailable")
}

func TestRejectRemovedExecutionFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "exec-mode", args: []string{"-exec-mode", "sandbox"}, want: "-permission-mode"},
		{name: "bash-strategy", args: []string{"--bash-strategy=strict"}, want: "-permission-mode"},
		{name: "bash-allowlist", args: []string{"-bash-allowlist=ls,pwd"}, want: "-safe-commands"},
		{name: "bash-deny-meta", args: []string{"--bash-deny-meta=false"}, want: "-permission-mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectRemovedExecutionFlags(tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected hint %q in error %q", tc.want, err.Error())
			}
		})
	}
}

func TestRejectRemovedExecutionFlags_AcceptsNewFlags(t *testing.T) {
	err := rejectRemovedExecutionFlags([]string{"-permission-mode=default", "-safe-commands=ls,pwd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRuntimePromptHint_IncludesPolicySummary(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    "docker",
		SandboxRunner:  noopCommandRunner{},
		SafeCommands:   []string{"cat", "head", "grep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hint := buildRuntimePromptHint(rt)
	if !strings.Contains(hint, "sandbox_policy=workspace_write") {
		t.Fatalf("expected workspace_write policy hint, got %q", hint)
	}
	if !strings.Contains(hint, "commands run in sandbox by default") {
		t.Fatalf("expected sandbox default rule, got %q", hint)
	}
	if !strings.Contains(hint, "safe_commands=cat,head,grep") {
		t.Fatalf("expected safe command summary, got %q", hint)
	}
	if !strings.Contains(hint, "Approval UX: host escalation uses y/a/n") {
		t.Fatalf("expected approval UX hint, got %q", hint)
	}
}

func TestBuildRuntimePromptHint_FullControl(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	hint := buildRuntimePromptHint(rt)
	if !strings.Contains(hint, "permission_mode=full_control route=host") {
		t.Fatalf("expected full control route hint, got %q", hint)
	}
	if !strings.Contains(hint, "sandbox_policy=danger_full_access") {
		t.Fatalf("expected danger_full_access policy hint, got %q", hint)
	}
}

func TestBuildRuntimePromptHint_DefaultFallbackIncludesReason(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    "docker",
		SandboxRunner:  failingProbeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	hint := buildRuntimePromptHint(rt)
	if !strings.Contains(hint, "sandbox is unavailable; all BASH commands require approval then run on host") {
		t.Fatalf("expected fallback rule, got %q", hint)
	}
	if !strings.Contains(hint, "Fallback reason:") {
		t.Fatalf("expected fallback reason, got %q", hint)
	}
}

func TestSummarizeSafeCommands(t *testing.T) {
	got := summarizeSafeCommands([]string{"cat", "head", "cat", "grep"}, 2)
	if got != "cat,head,+1 more" {
		t.Fatalf("unexpected summary: %q", got)
	}
}
