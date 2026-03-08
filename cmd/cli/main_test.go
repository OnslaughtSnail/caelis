package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type noopCommandRunner struct{}

func cliTestSandboxType() string {
	if runtime.GOOS == "darwin" {
		return "seatbelt"
	}
	if runtime.GOOS == "linux" {
		return "landlock"
	}
	return "bwrap"
}

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
	return errors.New("sandbox is unavailable")
}

func TestRejectRemovedExecutionFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "exec-mode", args: []string{"-exec-mode", "sandbox"}, want: "-permission-mode"},
		{name: "bash-strategy", args: []string{"--bash-strategy=strict"}, want: "-permission-mode"},
		{name: "bash-allowlist", args: []string{"-bash-allowlist=ls,pwd"}, want: "host escalation approval flow"},
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
	err := rejectRemovedExecutionFlags([]string{"-permission-mode=default", "-sandbox-type=landlock"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildRuntimePromptHint_IncludesPolicySummary(t *testing.T) {
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    cliTestSandboxType(),
		SandboxRunner:  noopCommandRunner{},
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
	if !strings.Contains(hint, "use require_escalated=true only when sandbox limits are blocking a necessary next step") {
		t.Fatalf("expected escalation guidance, got %q", hint)
	}
	if !strings.Contains(hint, "Safe inspection commands may auto-pass host escalation without user approval") {
		t.Fatalf("expected safe-command escalation hint, got %q", hint)
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
		SandboxType:    cliTestSandboxType(),
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

func TestFlagProvided(t *testing.T) {
	if !flagProvided([]string{"-session", "abc"}, "session") {
		t.Fatal("expected short flag to be detected")
	}
	if !flagProvided([]string{"--session=abc"}, "session") {
		t.Fatal("expected long equals flag to be detected")
	}
	if flagProvided([]string{"-model", "x"}, "session") {
		t.Fatal("did not expect unrelated flag to be detected")
	}
}

func TestNextConversationSessionID(t *testing.T) {
	a := nextConversationSessionID()
	b := nextConversationSessionID()
	if !strings.HasPrefix(a, "s-") || !strings.HasPrefix(b, "s-") {
		t.Fatalf("expected session ids to have s- prefix, got %q, %q", a, b)
	}
	if len(a) > 16 || len(b) > 16 {
		t.Fatalf("expected compact session ids, got %q (%d), %q (%d)", a, len(a), b, len(b))
	}
	if a == b {
		t.Fatalf("expected unique session ids, got duplicated %q", a)
	}
}
