package shell

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type fakeRuntime struct {
	policy toolexec.BashPolicy
	runner toolexec.CommandRunner
}

func (r *fakeRuntime) Mode() toolexec.Mode {
	return toolexec.ModeNoSandbox
}

func (r *fakeRuntime) SandboxType() string {
	return ""
}

func (r *fakeRuntime) FileSystem() toolexec.FileSystem {
	return nil
}

func (r *fakeRuntime) Runner() toolexec.CommandRunner {
	return r.runner
}

func (r *fakeRuntime) BashPolicy() toolexec.BashPolicy {
	return r.policy
}

type fakeRunner struct {
	result toolexec.CommandResult
	err    error
}

func (r *fakeRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return r.result, r.err
}

type noopFS struct{}

func (n noopFS) Getwd() (string, error)                                     { return "", nil }
func (n noopFS) UserHomeDir() (string, error)                               { return "", nil }
func (n noopFS) Open(path string) (*os.File, error)                         { return nil, nil }
func (n noopFS) ReadDir(path string) ([]os.DirEntry, error)                 { return nil, nil }
func (n noopFS) Stat(path string) (os.FileInfo, error)                      { return nil, nil }
func (n noopFS) ReadFile(path string) ([]byte, error)                       { return nil, nil }
func (n noopFS) WriteFile(path string, data []byte, perm os.FileMode) error { return nil }
func (n noopFS) Glob(pattern string) ([]string, error)                      { return nil, nil }
func (n noopFS) WalkDir(root string, fn fs.WalkDirFunc) error               { return nil }

type fixedApprover struct {
	allow bool
}

func (a fixedApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	_ = req
	return a.allow, nil
}

func TestBash_StrictRequiresApprovalForMetaCommand(t *testing.T) {
	tool, err := NewBash(BashConfig{
		Runtime: &fakeRuntime{
			policy: toolexec.BashPolicy{
				Strategy:      toolexec.BashStrategyStrict,
				Allowlist:     []string{"echo"},
				DenyMetaChars: true,
			},
			runner: &fakeRunner{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
		"command": "echo hi | cat",
	})
	if err == nil {
		t.Fatal("expected approval required")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected approval-required error, got: %v", err)
	}
}

func TestBash_AgentDecidedReturnsApprovalRequired(t *testing.T) {
	tool, err := NewBash(BashConfig{
		Runtime: &fakeRuntime{
			policy: toolexec.BashPolicy{
				Strategy:      toolexec.BashStrategyAgentDecide,
				Allowlist:     []string{"ls"},
				DenyMetaChars: true,
			},
			runner: &fakeRunner{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
		"command": "python3 app.py",
	})
	if err == nil {
		t.Fatal("expected approval required")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected approval-required error, got: %v", err)
	}
}

func TestBash_FullAccessRunsCommand(t *testing.T) {
	tool, err := NewBash(BashConfig{
		Runtime: &fakeRuntime{
			policy: toolexec.BashPolicy{
				Strategy: toolexec.BashStrategyFullAccess,
			},
			runner: &fakeRunner{
				result: toolexec.CommandResult{
					Stdout: "ok",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"command": "cat <<'EOF' > a.txt\nx\nEOF",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != "ok" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_AgentDecidedApproveAllowsCommand(t *testing.T) {
	tool, err := NewBash(BashConfig{
		Runtime: &fakeRuntime{
			policy: toolexec.BashPolicy{
				Strategy:      toolexec.BashStrategyAgentDecide,
				Allowlist:     []string{"ls"},
				DenyMetaChars: true,
			},
			runner: &fakeRunner{
				result: toolexec.CommandResult{
					Stdout: "approved",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{
		"command": "python3 app.py",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != "approved" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}

func TestBash_StrictApproveAllowsCommand(t *testing.T) {
	tool, err := NewBash(BashConfig{
		Runtime: &fakeRuntime{
			policy: toolexec.BashPolicy{
				Strategy:      toolexec.BashStrategyStrict,
				Allowlist:     []string{"ls"},
				DenyMetaChars: true,
			},
			runner: &fakeRunner{
				result: toolexec.CommandResult{
					Stdout: "approved-by-strict",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := toolexec.WithApprover(context.Background(), fixedApprover{allow: true})
	out, err := tool.Run(ctx, map[string]any{
		"command": "python3 app.py",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["stdout"] != "approved-by-strict" {
		t.Fatalf("unexpected stdout: %v", out["stdout"])
	}
}
