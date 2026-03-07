//go:build linux

package execenv

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLandlockFactoryBuildsRunner(t *testing.T) {
	factory := landlockSandboxFactory{}
	runner, err := factory.Build(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("expected non-nil landlock runner")
	}
}

func TestLandlockRunner_ProbeUnsupportedPlatform(t *testing.T) {
	r := &landlockRunner{goos: "darwin"}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected unsupported-platform error")
	}
	if !strings.Contains(err.Error(), "only supported on linux") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLandlockRunner_ProbeUnavailable(t *testing.T) {
	r := &landlockRunner{
		goos:  "linux",
		probe: func() error { return errors.New("landlock is disabled") },
	}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected unavailable error")
	}
	if !strings.Contains(err.Error(), "landlock sandbox unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLandlockRunner_ProbeRejectsUnavailableHelper(t *testing.T) {
	r := &landlockRunner{
		goos:       "linux",
		helperPath: "/custom/helper",
		probe:      func() error { return nil },
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "/custom/helper" {
				t.Fatalf("unexpected helper path: %s", name)
			}
			if len(args) != 2 || args[0] != internalHelperCommand || args[1] != "--probe" {
				t.Fatalf("unexpected helper probe args: %v", args)
			}
			return exec.Command("bash", "-lc", "echo helper-missing >&2; exit 1")
		},
	}

	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected helper probe error")
	}
	if !strings.Contains(err.Error(), "helper probe failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLandlockRunner_RunExecutesHelper(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	r := &landlockRunner{
		goos: "linux",
		policy: SandboxPolicy{
			Type:          SandboxPolicyWorkspaceWrite,
			NetworkAccess: true,
			WritableRoots: []string{"."},
		},
		helperPath:     "/custom/helper",
		executablePath: func() (string, error) { return "/proc/self/exe", nil },
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo landlock-ok")
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi", Dir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "landlock-ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if capturedName != "/custom/helper" {
		t.Fatalf("expected helper executable path, got %q", capturedName)
	}
	joined := strings.Join(capturedArgs, " ")
	for _, want := range []string{
		internalHelperCommand,
		"--policy-json",
		"--policy-cwd /tmp",
		"--command-cwd /tmp",
		"--command echo hi",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in helper args %q", want, joined)
		}
	}
}

func TestBuildLandlockHelperArgs(t *testing.T) {
	args, err := buildLandlockHelperArgs(
		SandboxPolicy{Type: SandboxPolicyReadOnly, NetworkAccess: false},
		"/repo",
		"/repo",
		"printf hi",
	)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		internalHelperCommand,
		"--policy-json",
		"\"type\":\"read_only\"",
		"--policy-cwd /repo",
		"--command-cwd /repo",
		"--command printf hi",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in %q", want, joined)
		}
	}
}

func TestRunInternalHelper_ProbeMode(t *testing.T) {
	if err := runInternalHelper([]string{"--probe"}); err != nil {
		t.Fatal(err)
	}
}

func TestLandlockReadWriteMaskForABI_GuardsTruncate(t *testing.T) {
	if mask := landlockReadWriteMaskForABI(2); mask&unix.LANDLOCK_ACCESS_FS_TRUNCATE != 0 {
		t.Fatalf("truncate should be disabled for abi 2: %#x", mask)
	}
	if mask := landlockReadWriteMaskForABI(3); mask&unix.LANDLOCK_ACCESS_FS_TRUNCATE == 0 {
		t.Fatalf("truncate should be enabled for abi 3: %#x", mask)
	}
}

func TestLandlockFileReadWriteMaskForABI_GuardsTruncate(t *testing.T) {
	if mask := landlockFileReadWriteMaskForABI(2); mask&unix.LANDLOCK_ACCESS_FS_TRUNCATE != 0 {
		t.Fatalf("truncate should be disabled for abi 2: %#x", mask)
	}
	if mask := landlockFileReadWriteMaskForABI(3); mask&unix.LANDLOCK_ACCESS_FS_TRUNCATE == 0 {
		t.Fatalf("truncate should be enabled for abi 3: %#x", mask)
	}
}
