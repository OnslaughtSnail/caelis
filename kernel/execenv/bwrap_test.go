package execenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBwrapFactoryBuildsRunner(t *testing.T) {
	factory := bwrapSandboxFactory{}
	runner, err := factory.Build(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("expected non-nil bwrap runner")
	}
}

func TestBwrapRunner_ProbeUnsupportedPlatform(t *testing.T) {
	r := &bwrapRunner{goos: "darwin"}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected unsupported-platform error")
	}
	if !strings.Contains(err.Error(), "only supported on linux") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBwrapRunner_ProbeMissingBinary(t *testing.T) {
	r := &bwrapRunner{
		goos:     "linux",
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected missing-binary error")
	}
	if !strings.Contains(err.Error(), "bwrap not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBwrapRunner_ProbeRunsBwrap(t *testing.T) {
	var call string
	r := &bwrapRunner{
		goos:     "linux",
		lookPath: func(string) (string, error) { return "/usr/bin/bwrap", nil },
		policy:   SandboxPolicy{NetworkAccess: false},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			call = name + " " + strings.Join(args, " ")
			if name != "bwrap" {
				t.Fatalf("expected bwrap, got %q", name)
			}
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if err := r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(call, "--ro-bind") {
		t.Fatalf("expected --ro-bind in probe command: %s", call)
	}
	for _, flag := range []string{"--unshare-user", "--unshare-pid", "--unshare-net", "--new-session", "--die-with-parent"} {
		if !strings.Contains(call, flag) {
			t.Fatalf("expected %s in probe command: %s", flag, call)
		}
	}
}

func TestBwrapRunner_ProbeSkipsUnshareNetWhenNetworkEnabled(t *testing.T) {
	var call string
	r := &bwrapRunner{
		goos:     "linux",
		lookPath: func(string) (string, error) { return "/usr/bin/bwrap", nil },
		policy:   SandboxPolicy{NetworkAccess: true},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			call = name + " " + strings.Join(args, " ")
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if err := r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(call, "--unshare-net") {
		t.Fatalf("probe should NOT include --unshare-net when NetworkAccess=true: %s", call)
	}
	// Other namespace flags must still be present
	for _, flag := range []string{"--unshare-user", "--unshare-pid"} {
		if !strings.Contains(call, flag) {
			t.Fatalf("expected %s in probe command: %s", flag, call)
		}
	}
}

func TestBwrapRunner_RunBuildsArgsAndExecutes(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	r := &bwrapRunner{
		goos: "linux",
		policy: SandboxPolicy{
			Type:             SandboxPolicyWorkspaceWrite,
			NetworkAccess:    true,
			WritableRoots:    []string{"."},
			ReadOnlySubpaths: []string{".git"},
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo bwrap-ok")
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "bwrap-ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if capturedName != "bwrap" {
		t.Fatalf("expected bwrap command, got %q", capturedName)
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / in args, got %q", joined)
	}
	if !strings.Contains(joined, "--unshare-user") {
		t.Fatalf("expected --unshare-user in args, got %q", joined)
	}
	if !strings.Contains(joined, "--unshare-pid") {
		t.Fatalf("expected --unshare-pid in args, got %q", joined)
	}
	if strings.Contains(joined, "--unshare-net") {
		t.Fatalf("did not expect --unshare-net with network access enabled")
	}
	// Should have writable bind for the workdir
	if !strings.Contains(joined, "--bind") {
		t.Fatalf("expected writable --bind in args, got %q", joined)
	}
}

func TestBwrapRunner_RunReadOnlyDisablesNetwork(t *testing.T) {
	var capturedArgs []string
	r := &bwrapRunner{
		goos: "linux",
		policy: SandboxPolicy{
			Type:             SandboxPolicyReadOnly,
			NetworkAccess:    false,
			WritableRoots:    nil,
			ReadOnlySubpaths: nil,
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if _, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("expected --unshare-net in readonly args: %q", joined)
	}
	// Should not have writable --bind (only --ro-bind)
	bindCount := 0
	for i, arg := range capturedArgs {
		if arg == "--bind" && i+2 < len(capturedArgs) {
			bindCount++
		}
	}
	if bindCount > 0 {
		t.Fatalf("did not expect writable --bind in readonly profile, got %d binds in: %q", bindCount, joined)
	}
}

func TestBwrapRunner_RunDangerFullAlwaysWrapped(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	r := &bwrapRunner{
		goos: "linux",
		policy: SandboxPolicy{
			Type:          SandboxPolicyDangerFull,
			NetworkAccess: true,
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo danger-ok")
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "danger-ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	// DangerFull+network MUST still use bwrap (no host bypass)
	if capturedName != "bwrap" {
		t.Fatalf("expected bwrap even for DangerFull+network, got %q", capturedName)
	}
	joined := strings.Join(capturedArgs, " ")
	// DangerFull uses scoped writable roots, not --bind / /
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / for DangerFull (scoped writes), got %q", joined)
	}
	if strings.Contains(joined, "--unshare-net") {
		t.Fatalf("did not expect --unshare-net with network access enabled")
	}
}

func TestBwrapRunner_RunDangerFullNoNetworkUsesBwrap(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	r := &bwrapRunner{
		goos: "linux",
		policy: SandboxPolicy{
			Type:          SandboxPolicyDangerFull,
			NetworkAccess: false,
		},
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if _, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	if capturedName != "bwrap" {
		t.Fatalf("expected bwrap for DangerFull without network, got %q", capturedName)
	}
	joined := strings.Join(capturedArgs, " ")
	// DangerFull uses scoped writable roots, not --bind / /
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / for DangerFull (scoped writes), got %q", joined)
	}
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("expected --unshare-net for no-network DangerFull, got %q", joined)
	}
}

func TestBwrapRunner_RunTimeout(t *testing.T) {
	r := &bwrapRunner{
		goos:   "linux",
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
	if !IsErrorCode(err, ErrorCodeSandboxCommandTimeout) {
		t.Fatalf("expected timeout error code %q, got %q", ErrorCodeSandboxCommandTimeout, ErrorCodeOf(err))
	}
}

func TestBwrapRunner_RunIdleTimeout(t *testing.T) {
	r := &bwrapRunner{
		goos:   "linux",
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
	if !IsErrorCode(err, ErrorCodeSandboxIdleTimeout) {
		t.Fatalf("expected idle-timeout error code %q, got %q", ErrorCodeSandboxIdleTimeout, ErrorCodeOf(err))
	}
}

func TestBuildBwrapArgsWorkspaceWrite(t *testing.T) {
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	args := buildBwrapArgs(SandboxPolicy{
		Type:             SandboxPolicyWorkspaceWrite,
		NetworkAccess:    true,
		WritableRoots:    []string{tmpDir},
		ReadOnlySubpaths: []string{gitDir},
	}, tmpDir)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / in args, got %q", joined)
	}
	if !strings.Contains(joined, "--dev /dev") {
		t.Fatalf("expected --dev /dev in args, got %q", joined)
	}
	if !strings.Contains(joined, "--proc /proc") {
		t.Fatalf("expected --proc /proc in args, got %q", joined)
	}
	if strings.Contains(joined, "--unshare-net") {
		t.Fatalf("did not expect --unshare-net with network access")
	}
	if !strings.Contains(joined, "--ro-bind "+gitDir) {
		t.Fatalf("expected read-only bind for .git in args, got %q", joined)
	}
}

func TestBuildBwrapArgsDangerFullNetwork(t *testing.T) {
	args := buildBwrapArgs(SandboxPolicy{
		Type:          SandboxPolicyDangerFull,
		NetworkAccess: true,
	}, "/tmp")
	if args == nil {
		t.Fatal("expected non-nil args for DangerFull+network (always wrapped)")
	}
	joined := strings.Join(args, " ")
	// DangerFull uses --ro-bind / / + scoped writable roots, matching seatbelt
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / for DangerFull+network, got %q", joined)
	}
	if strings.Contains(joined, "--unshare-net") {
		t.Fatalf("did not expect --unshare-net with network access")
	}
}

func TestBuildBwrapArgsDangerFullNoNetwork(t *testing.T) {
	args := buildBwrapArgs(SandboxPolicy{
		Type:          SandboxPolicyDangerFull,
		NetworkAccess: false,
	}, "/tmp")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("expected --ro-bind / / for DangerFull no-network, got %q", joined)
	}
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("expected --unshare-net for DangerFull no-network, got %q", joined)
	}
}

func TestBuildBwrapArgsDangerFullGetsScopedWritableRoots(t *testing.T) {
	// DangerFull with WritableRoots=nil (as set by deriveSandboxPolicy) should
	// still get temp/cache writable roots, not full disk write.
	args := buildBwrapArgs(SandboxPolicy{
		Type:          SandboxPolicyDangerFull,
		NetworkAccess: true,
		WritableRoots: nil,
	}, "/tmp")
	joined := strings.Join(args, " ")
	// Must have --ro-bind / / (not --bind / /)
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("DangerFull must use --ro-bind / /, got %q", joined)
	}
	// Must have writable bind for /tmp (temp dirs are always added)
	if !strings.Contains(joined, "--bind /tmp /tmp") {
		t.Fatalf("DangerFull should have writable --bind /tmp, got %q", joined)
	}
}

func TestBwrapWritableRootsIncludesTmpAndCache(t *testing.T) {
	roots := bwrapWritableRoots(SandboxPolicy{
		Type:          SandboxPolicyWorkspaceWrite,
		NetworkAccess: true,
		WritableRoots: []string{"/tmp"},
	}, "/tmp")

	has := func(needle string) bool {
		for _, r := range roots {
			if r == needle {
				return true
			}
		}
		return false
	}

	if !has("/tmp") {
		t.Error("expected /tmp in writable roots")
	}
	// /var/tmp should be present if it exists on the host
	if _, err := os.Stat("/var/tmp"); err == nil {
		if !has("/var/tmp") {
			t.Error("expected /var/tmp in writable roots")
		}
	}
	// ~/.cache should only be present if it actually exists
	home, err := os.UserHomeDir()
	if err == nil {
		cacheDir := filepath.Join(home, ".cache")
		if _, statErr := os.Stat(cacheDir); statErr == nil {
			if !has(cacheDir) {
				t.Errorf("expected %s in writable roots (exists on host)", cacheDir)
			}
		}
	}
}

func TestBwrapWritableRootsReadOnlyIsNil(t *testing.T) {
	roots := bwrapWritableRoots(SandboxPolicy{
		Type: SandboxPolicyReadOnly,
	}, "/tmp")
	if roots != nil {
		t.Fatalf("expected nil writable roots for ReadOnly policy, got %v", roots)
	}
}

func TestBwrapReadOnlySubpathsResolved(t *testing.T) {
	// Use paths that exist on the host so filterExistingPaths doesn't drop them.
	tmpDir := t.TempDir()
	sub1 := filepath.Join(tmpDir, "sub1")
	sub2 := filepath.Join(tmpDir, "sub2")
	if err := os.Mkdir(sub1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub2, 0o755); err != nil {
		t.Fatal(err)
	}

	subs := bwrapReadOnlySubpaths(SandboxPolicy{
		Type:             SandboxPolicyWorkspaceWrite,
		ReadOnlySubpaths: []string{sub1, sub2},
	}, tmpDir)

	if len(subs) != 2 {
		t.Fatalf("expected 2 read-only subpaths, got %v", subs)
	}
	if subs[0] != sub1 {
		t.Fatalf("expected %q, got %q", sub1, subs[0])
	}
	if subs[1] != sub2 {
		t.Fatalf("expected %q, got %q", sub2, subs[1])
	}
}

func TestResolveBwrapPath(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		value   string
		want    string
	}{
		{"absolute", "/tmp", "/usr/bin", "/usr/bin"},
		{"relative", "/home/user", "project", "/home/user/project"},
		{"dot", "/home/user/project", ".", "/home/user/project"},
		{"empty", "/tmp", "", ""},
		{"empty base relative", "", "project", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveBwrapPath(tc.base, tc.value)
			if got != tc.want {
				t.Fatalf("resolveBwrapPath(%q, %q) = %q, want %q", tc.base, tc.value, got, tc.want)
			}
		})
	}
}

func TestFilterExistingPaths(t *testing.T) {
	// /tmp always exists; pick a path that definitely doesn't
	missing := "/nonexistent_path_for_bwrap_test_8675309"
	result := filterExistingPaths([]string{"/tmp", missing, "/var/tmp"})
	for _, p := range result {
		if p == missing {
			t.Fatalf("filterExistingPaths should have dropped %q", missing)
		}
	}
	has := func(needle string) bool {
		for _, p := range result {
			if p == needle {
				return true
			}
		}
		return false
	}
	if !has("/tmp") {
		t.Error("expected /tmp to survive filtering")
	}
}

func TestBwrapWritableRootsSkipsMissingPaths(t *testing.T) {
	missing := "/nonexistent_path_for_bwrap_test_8675309"
	roots := bwrapWritableRoots(SandboxPolicy{
		Type:          SandboxPolicyWorkspaceWrite,
		NetworkAccess: true,
		WritableRoots: []string{missing},
	}, "/tmp")
	for _, r := range roots {
		if r == missing {
			t.Fatalf("nonexistent path %q should have been filtered out", missing)
		}
	}
}

func TestBwrapReadOnlySubpathsSkipsMissingPaths(t *testing.T) {
	subs := bwrapReadOnlySubpaths(SandboxPolicy{
		Type:             SandboxPolicyWorkspaceWrite,
		ReadOnlySubpaths: []string{".nonexistent_dir_bwrap_test"},
	}, "/tmp")
	if len(subs) != 0 {
		t.Fatalf("expected no read-only subpaths for nonexistent dirs, got %v", subs)
	}
}
