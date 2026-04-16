package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestCloneRequestIsolatesMutableFields(t *testing.T) {
	t.Parallel()

	req := CommandRequest{
		Command: "  echo ok  ",
		Dir:     " /tmp/project ",
		Env: map[string]string{
			"FOO": "bar",
		},
		Stdin:      []byte("hello"),
		Permission: PermissionWorkspaceWrite,
		RouteHint:  RouteSandbox,
	}

	cloned := CloneRequest(req)
	cloned.Env["FOO"] = "mutated"
	cloned.Stdin[0] = 'H'

	if got := cloned.Command; got != "echo ok" {
		t.Fatalf("cloned.Command = %q, want %q", got, "echo ok")
	}
	if got := cloned.Dir; got != "/tmp/project" {
		t.Fatalf("cloned.Dir = %q, want %q", got, "/tmp/project")
	}
	if got := req.Env["FOO"]; got != "bar" {
		t.Fatalf("req.Env[FOO] = %q, want %q", got, "bar")
	}
	if got := string(req.Stdin); got != "hello" {
		t.Fatalf("req.Stdin = %q, want %q", got, "hello")
	}
}

func TestFuncRunnerClonesRequestBeforeInvoke(t *testing.T) {
	t.Parallel()

	runner := FuncRunner(func(_ context.Context, req CommandRequest) (CommandResult, error) {
		req.Env["FOO"] = "mutated"
		req.Stdin[0] = 'H'
		return CommandResult{
			Stdout:   "ok\n",
			ExitCode: 0,
			Route:    RouteSandbox,
			Backend:  "seatbelt",
		}, nil
	})

	req := CommandRequest{
		Command: "echo ok",
		Env: map[string]string{
			"FOO": "bar",
		},
		Stdin: []byte("hello"),
	}

	result, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Backend; got != "seatbelt" {
		t.Fatalf("result.Backend = %q, want %q", got, "seatbelt")
	}
	if got := req.Env["FOO"]; got != "bar" {
		t.Fatalf("req.Env[FOO] = %q, want %q", got, "bar")
	}
	if got := string(req.Stdin); got != "hello" {
		t.Fatalf("req.Stdin = %q, want %q", got, "hello")
	}
}

func TestCloneSessionStatusNormalizesSessionRef(t *testing.T) {
	t.Parallel()

	status := CloneSessionStatus(SessionStatus{
		SessionRef: SessionRef{
			Backend:   " sandbox ",
			SessionID: " sess-1 ",
		},
		Running:   true,
		ExitCode:  -1,
		StartedAt: time.Unix(10, 0),
		UpdatedAt: time.Unix(20, 0),
	})

	if got := status.Backend; got != "sandbox" {
		t.Fatalf("status.Backend = %q, want %q", got, "sandbox")
	}
	if got := status.SessionID; got != "sess-1" {
		t.Fatalf("status.SessionID = %q, want %q", got, "sess-1")
	}
	if !status.Running {
		t.Fatal("status.Running = false, want true")
	}
}

func TestEffectiveConstraintsMergesLegacyFields(t *testing.T) {
	t.Parallel()

	req := CommandRequest{
		Permission: PermissionWorkspaceWrite,
		RouteHint:  RouteSandbox,
		Backend:    BackendSeatbelt,
		Constraints: Constraints{
			Network: NetworkDisabled,
			PathRules: []PathRule{
				{Path: " /workspace ", Access: PathAccessReadWrite},
			},
		},
	}

	got := EffectiveConstraints(req)
	if got.Permission != PermissionWorkspaceWrite {
		t.Fatalf("Permission = %q, want %q", got.Permission, PermissionWorkspaceWrite)
	}
	if got.Route != RouteSandbox {
		t.Fatalf("Route = %q, want %q", got.Route, RouteSandbox)
	}
	if got.Backend != BackendSeatbelt {
		t.Fatalf("Backend = %q, want %q", got.Backend, BackendSeatbelt)
	}
	if got.Network != NetworkDisabled {
		t.Fatalf("Network = %q, want %q", got.Network, NetworkDisabled)
	}
	if len(got.PathRules) != 1 || got.PathRules[0].Path != "/workspace" {
		t.Fatalf("PathRules = %+v, want normalized workspace rule", got.PathRules)
	}
}
