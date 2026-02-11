package execenv

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSeatbeltFactoryBuildsRunner(t *testing.T) {
	factory := seatbeltSandboxFactory{}
	runner, err := factory.Build(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("expected non-nil seatbelt runner")
	}
}

func TestSeatbeltRunner_ProbeUnsupportedPlatform(t *testing.T) {
	r := &seatbeltRunner{goos: "linux"}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected unsupported-platform error")
	}
	if !strings.Contains(err.Error(), "only supported on darwin") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSeatbeltRunner_ProbeMissingBinary(t *testing.T) {
	r := &seatbeltRunner{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected missing-binary error")
	}
	if !strings.Contains(err.Error(), "sandbox-exec not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSeatbeltRunner_ProbeRunsSandboxExec(t *testing.T) {
	var call string
	r := &seatbeltRunner{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "/usr/bin/sandbox-exec", nil },
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			call = name + " " + strings.Join(args, " ")
			if name != "sandbox-exec" {
				t.Fatalf("expected sandbox-exec, got %q", name)
			}
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if err := r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(call, "sandbox-exec -p") {
		t.Fatalf("unexpected probe command: %s", call)
	}
}

func TestSeatbeltRunner_RunBuildsProfileAndExecutes(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	r := &seatbeltRunner{
		goos: "darwin",
		policy: SandboxPolicy{
			Type:             SandboxPolicyWorkspaceWrite,
			NetworkAccess:    true,
			WritableRoots:    []string{"."},
			ReadOnlySubpaths: []string{".git"},
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo seatbelt-ok")
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "seatbelt-ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if capturedName != "sandbox-exec" {
		t.Fatalf("expected sandbox-exec command, got %q", capturedName)
	}
	if len(capturedArgs) < 5 || capturedArgs[0] != "-p" {
		t.Fatalf("unexpected seatbelt args: %v", capturedArgs)
	}
	profile := capturedArgs[1]
	if !strings.Contains(profile, "(allow network*)") {
		t.Fatalf("expected network allow in profile, got %q", profile)
	}
	if !strings.Contains(profile, "(allow file-write*") {
		t.Fatalf("expected write roots in profile, got %q", profile)
	}
	if !strings.Contains(profile, "(deny file-write* (subpath") || !strings.Contains(profile, ".git") {
		t.Fatalf("expected readonly .git in profile, got %q", profile)
	}
}

func TestSeatbeltRunner_RunReadOnlyDisablesNetwork(t *testing.T) {
	var profile string
	r := &seatbeltRunner{
		goos: "darwin",
		policy: SandboxPolicy{
			Type:             SandboxPolicyReadOnly,
			NetworkAccess:    false,
			WritableRoots:    nil,
			ReadOnlySubpaths: nil,
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			if len(args) < 2 {
				t.Fatalf("unexpected args: %v", args)
			}
			profile = args[1]
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if _, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile, "(allow network*)") {
		t.Fatalf("did not expect network allow in readonly profile: %q", profile)
	}
	if strings.Contains(profile, "(allow file-write*") {
		t.Fatalf("did not expect writable roots in readonly profile: %q", profile)
	}
}

func TestSeatbeltRunner_RunTimeout(t *testing.T) {
	r := &seatbeltRunner{
		goos:   "darwin",
		policy: SandboxPolicy{Type: SandboxPolicyWorkspaceWrite, NetworkAccess: true, WritableRoots: []string{"."}},
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			_ = args
			return exec.CommandContext(ctx, "bash", "-lc", "sleep 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{Command: "echo hi", Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("expected timeout message, got %v", err)
	}
}

func TestSeatbeltRunner_RunIdleTimeout(t *testing.T) {
	r := &seatbeltRunner{
		goos:   "darwin",
		policy: SandboxPolicy{Type: SandboxPolicyWorkspaceWrite, NetworkAccess: true, WritableRoots: []string{"."}},
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			_ = args
			return exec.CommandContext(ctx, "bash", "-lc", "echo hi && sleep 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{Command: "echo hi", Timeout: 3 * time.Second, IdleTimeout: 120 * time.Millisecond})
	if err == nil {
		t.Fatal("expected idle timeout error")
	}
	if !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("expected idle timeout message, got %v", err)
	}
}

func TestBuildSeatbeltProfileIncludesSystemRules(t *testing.T) {
	profile := buildSeatbeltProfile(SandboxPolicy{Type: SandboxPolicyWorkspaceWrite, NetworkAccess: true, WritableRoots: []string{"."}}, "/tmp/work")
	if !strings.Contains(profile, "(import \"system.sb\")") {
		t.Fatalf("expected system import, got %q", profile)
	}
	if !strings.Contains(profile, "(allow process*)") {
		t.Fatalf("expected process allow, got %q", profile)
	}
}
