package execenv

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerFactoryBuildsRunner(t *testing.T) {
	factory := dockerSandboxFactory{}
	runner, err := factory.Build(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil {
		t.Fatal("expected non-nil docker runner")
	}
}

func TestDockerFactoryBuild_AppliesSandboxPolicy(t *testing.T) {
	t.Setenv(dockerNetEnvKey, "bridge")
	factory := dockerSandboxFactory{}
	runner, err := factory.Build(Config{
		SandboxPolicy: SandboxPolicy{
			Type:          SandboxPolicyReadOnly,
			NetworkAccess: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := runner.(*dockerRunner)
	if !ok {
		t.Fatalf("expected dockerRunner, got %T", runner)
	}
	if typed.network != "none" {
		t.Fatalf("expected network=none from policy, got %q", typed.network)
	}
	if !typed.readOnly {
		t.Fatal("expected readonly mount mode for read_only sandbox policy")
	}
}

func TestNewDockerRunner_DefaultNetworkBridge(t *testing.T) {
	t.Setenv(dockerNetEnvKey, "")
	t.Setenv(dockerImageEnvKey, "")

	runner, ok := newDockerRunner().(*dockerRunner)
	if !ok {
		t.Fatal("expected dockerRunner concrete type")
	}
	if runner.network != dockerDefaultNet {
		t.Fatalf("expected default network %q, got %q", dockerDefaultNet, runner.network)
	}
}

func TestNewDockerRunner_UsesEnvNetwork(t *testing.T) {
	t.Setenv(dockerNetEnvKey, "none")
	t.Setenv(dockerImageEnvKey, "")

	runner, ok := newDockerRunner().(*dockerRunner)
	if !ok {
		t.Fatal("expected dockerRunner concrete type")
	}
	if runner.network != "none" {
		t.Fatalf("expected env network none, got %q", runner.network)
	}
}

func TestDockerRunner_ProbeBuildsDockerVersionCommand(t *testing.T) {
	var calls []string
	r := &dockerRunner{
		image: "alpine:3.20",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			if joined != "version --format {{.Server.Version}}" &&
				joined != "image inspect alpine:3.20" &&
				joined != "run --rm --network none alpine:3.20 sh -lc echo sandbox-ready" {
				t.Fatalf("unexpected docker probe args: %s", joined)
			}
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if err := r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 probe calls, got %d (%v)", len(calls), calls)
	}
}

func TestDockerRunner_ProbePullsImageWhenMissing(t *testing.T) {
	var calls []string
	r := &dockerRunner{
		image: "alpine:3.20",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			switch joined {
			case "version --format {{.Server.Version}}":
				return exec.Command("bash", "-lc", "echo ok")
			case "image inspect alpine:3.20":
				return exec.Command("bash", "-lc", "echo not-found >&2; exit 1")
			case "pull alpine:3.20":
				return exec.Command("bash", "-lc", "echo pull-ok")
			case "run --rm --network none alpine:3.20 sh -lc echo sandbox-ready":
				return exec.Command("bash", "-lc", "echo ok")
			default:
				t.Fatalf("unexpected command: %s", joined)
				return exec.Command("bash", "-lc", "exit 1")
			}
		},
	}
	if err := r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("expected 4 probe calls, got %d (%v)", len(calls), calls)
	}
}

func TestDockerRunner_ProbeFailsWhenImageCannotRunShell(t *testing.T) {
	r := &dockerRunner{
		image: "alpine:3.20",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			switch joined {
			case "version --format {{.Server.Version}}":
				return exec.Command("bash", "-lc", "echo ok")
			case "image inspect alpine:3.20":
				return exec.Command("bash", "-lc", "echo ok")
			case "run --rm --network none alpine:3.20 sh -lc echo sandbox-ready":
				return exec.Command("bash", "-lc", "echo sh-not-found >&2; exit 1")
			default:
				t.Fatalf("unexpected command: %s", joined)
				return exec.Command("bash", "-lc", "exit 1")
			}
		},
	}
	err := r.Probe(context.Background())
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if !strings.Contains(err.Error(), "not runnable for shell sandbox") {
		t.Fatalf("unexpected probe error: %v", err)
	}
}

func TestDockerRunner_RunStartsSessionThenExec(t *testing.T) {
	var calls []string
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		container: "caelis-test",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			switch {
			case strings.HasPrefix(joined, "run -d --rm"):
				if !strings.Contains(joined, "--name caelis-test") {
					t.Fatalf("missing container name in startup call: %s", joined)
				}
				if !strings.Contains(joined, "-w "+dockerWorkspaceDir) {
					t.Fatalf("missing startup workdir: %s", joined)
				}
				return exec.Command("bash", "-lc", "echo started")
			case strings.HasPrefix(joined, "exec "):
				if !strings.Contains(joined, "-w "+dockerWorkspaceDir) {
					t.Fatalf("missing exec workdir: %s", joined)
				}
				if !strings.Contains(joined, "caelis-test sh -lc echo hi") {
					t.Fatalf("unexpected exec payload: %s", joined)
				}
				return exec.Command("bash", "-lc", "echo ok")
			default:
				t.Fatalf("unexpected docker args: %s", joined)
				return exec.Command("bash", "-lc", "exit 1")
			}
		},
	}
	res, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if len(calls) != 2 {
		t.Fatalf("expected startup+exec, got %d calls: %v", len(calls), calls)
	}
}

func TestDockerRunner_RunReusesSessionContainer(t *testing.T) {
	var startupCalls int
	var execCalls int
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		container: "caelis-test",
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			joined := name + " " + strings.Join(args, " ")
			switch {
			case strings.Contains(joined, " run -d --rm ") || strings.HasPrefix(joined, "docker run -d --rm "):
				startupCalls++
				return exec.Command("bash", "-lc", "echo started")
			case strings.Contains(joined, " exec ") || strings.HasPrefix(joined, "docker exec "):
				execCalls++
				return exec.Command("bash", "-lc", "echo ok")
			default:
				t.Fatalf("unexpected command: %s", joined)
				return exec.Command("bash", "-lc", "exit 1")
			}
		},
	}
	if _, err := r.Run(context.Background(), CommandRequest{Command: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), CommandRequest{Command: "echo hi2"}); err != nil {
		t.Fatal(err)
	}
	if startupCalls != 1 {
		t.Fatalf("expected exactly one startup call, got %d", startupCalls)
	}
	if execCalls != 2 {
		t.Fatalf("expected two exec calls, got %d", execCalls)
	}
}

func TestDockerRunner_RunFallsBackToOneShotOutsideSessionRoot(t *testing.T) {
	insideRoot := t.TempDir()
	outsideRoot := t.TempDir()
	var sawOneShot bool
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		container: "caelis-test",
		rootDir:   insideRoot,
		started:   true,
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			if name != "docker" {
				t.Fatalf("expected docker command, got %q", name)
			}
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "exec ") {
				t.Fatalf("expected one-shot run for outside dir, got exec: %s", joined)
			}
			if strings.HasPrefix(joined, "run --rm") {
				sawOneShot = true
				if !strings.Contains(joined, filepath.Clean(outsideRoot)+":"+dockerWorkspaceDir) {
					t.Fatalf("expected outside dir mounted, got: %s", joined)
				}
				return exec.Command("bash", "-lc", "echo ok")
			}
			t.Fatalf("unexpected command: %s", joined)
			return exec.Command("bash", "-lc", "exit 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{Command: "echo hi", Dir: outsideRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !sawOneShot {
		t.Fatal("expected one-shot docker run")
	}
}

func TestDockerRunner_CloseStopsSessionContainer(t *testing.T) {
	var calls []string
	r := &dockerRunner{
		container: "caelis-test",
		started:   true,
		execCommand: func(_ context.Context, name string, args ...string) *exec.Cmd {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return exec.Command("bash", "-lc", "echo ok")
		},
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one cleanup call, got %d", len(calls))
	}
	if !strings.Contains(calls[0], "docker rm -f caelis-test") {
		t.Fatalf("unexpected cleanup command: %s", calls[0])
	}
}

func TestDockerRunner_RunTimeout(t *testing.T) {
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		container: "caelis-test",
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "run -d --rm") {
				return exec.Command("bash", "-lc", "echo started")
			}
			return exec.CommandContext(ctx, "bash", "-lc", "sleep 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{
		Command: "echo hi",
		Timeout: 250 * time.Millisecond,
	})
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

func TestDockerRunner_RunTimeoutExcludesSessionStartup(t *testing.T) {
	var startupCalls int
	var execCalls int
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		setupTTL:  2 * time.Second,
		container: "caelis-test",
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "run -d --rm") {
				startupCalls++
				// Startup intentionally slower than per-command timeout.
				return exec.CommandContext(ctx, "bash", "-lc", "sleep 0.15 && echo started")
			}
			execCalls++
			return exec.CommandContext(ctx, "bash", "-lc", "sleep 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{
		Command: "echo hi",
		Timeout: 80 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if startupCalls != 1 {
		t.Fatalf("expected one startup call, got %d", startupCalls)
	}
	if execCalls != 1 {
		t.Fatalf("expected command exec to run once after startup, got %d", execCalls)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("expected timeout message, got %v", err)
	}
	if !IsErrorCode(err, ErrorCodeSandboxCommandTimeout) {
		t.Fatalf("expected timeout error code %q, got %q", ErrorCodeSandboxCommandTimeout, ErrorCodeOf(err))
	}
}

func TestDockerRunner_RunIdleTimeout(t *testing.T) {
	r := &dockerRunner{
		image:     "alpine:3.20",
		network:   "bridge",
		container: "caelis-test",
		execCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			_ = name
			joined := strings.Join(args, " ")
			if strings.HasPrefix(joined, "run -d --rm") {
				return exec.Command("bash", "-lc", "echo started")
			}
			return exec.CommandContext(ctx, "bash", "-lc", "echo hello && sleep 1")
		},
	}
	_, err := r.Run(context.Background(), CommandRequest{
		Command:     "echo hi",
		Timeout:     3 * time.Second,
		IdleTimeout: 120 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected idle-timeout error")
	}
	if !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("expected idle-timeout message, got %v", err)
	}
	if !IsErrorCode(err, ErrorCodeSandboxIdleTimeout) {
		t.Fatalf("expected idle-timeout error code %q, got %q", ErrorCodeSandboxIdleTimeout, ErrorCodeOf(err))
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

func TestRelWithinRoot(t *testing.T) {
	root := filepath.Clean("/tmp/work")
	if rel, ok := relWithinRoot(root, filepath.Join(root, "a", "b")); !ok || rel != filepath.Join("a", "b") {
		t.Fatalf("expected inside root, got rel=%q ok=%v", rel, ok)
	}
	if _, ok := relWithinRoot(root, "/tmp/other"); ok {
		t.Fatal("expected outside root to be rejected")
	}
}
