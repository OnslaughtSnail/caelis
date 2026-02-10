package execenv

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestNew_DockerSandboxFactory(t *testing.T) {
	rt, err := New(Config{Mode: ModeSandbox, SandboxType: dockerSandboxType})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Mode() != ModeSandbox {
		t.Fatalf("expected sandbox mode, got %q", rt.Mode())
	}
	if rt.SandboxType() != dockerSandboxType {
		t.Fatalf("expected sandbox_type docker, got %q", rt.SandboxType())
	}
	if rt.BashPolicy().Strategy != BashStrategyAgentDecide {
		t.Fatalf("expected agent_decided policy, got %q", rt.BashPolicy().Strategy)
	}
}

func TestDockerRunner_BuildsDockerCommand(t *testing.T) {
	r := &dockerRunner{
		image: "alpine:3.20",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			all := append([]string{name}, args...)
			if strings.Join(all, " ") == "" {
				t.Fatal("unexpected empty command")
			}
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "run --rm --network none") {
				t.Fatalf("missing docker sandbox flags: %s", joined)
			}
			if !strings.Contains(joined, "-w "+dockerWorkspaceDir) {
				t.Fatalf("missing docker workdir: %s", joined)
			}
			if !strings.Contains(joined, "alpine:3.20 sh -lc echo hi") {
				t.Fatalf("missing container shell command: %s", joined)
			}
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}

func TestResolveHostWorkDir_Relative(t *testing.T) {
	got, err := resolveHostWorkDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if got == "." || got == "" {
		t.Fatalf("expected absolute cleaned path, got %q", got)
	}
}
