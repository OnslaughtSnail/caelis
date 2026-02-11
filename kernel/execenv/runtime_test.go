package execenv

import (
	"context"
	"errors"
	stdruntime "runtime"
	"strings"
	"testing"
)

type probeRunner struct {
	probeErr error
}

func (r probeRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	_ = ctx
	_ = req
	return CommandResult{}, nil
}

func (r probeRunner) Probe(ctx context.Context) error {
	_ = ctx
	return r.probeErr
}

type noopRunner struct{}

func (r noopRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	_ = ctx
	_ = req
	return CommandResult{}, nil
}

type closeableRunner struct {
	closed int
}

func (r *closeableRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	_ = ctx
	_ = req
	return CommandResult{}, nil
}

func (r *closeableRunner) Close() error {
	r.closed++
	return nil
}

type staticFactory struct {
	typ    string
	runner CommandRunner
	err    error
}

func (f staticFactory) Type() string {
	return f.typ
}

func (f staticFactory) Build(cfg Config) (CommandRunner, error) {
	_ = cfg
	if f.err != nil {
		return nil, f.err
	}
	return f.runner, nil
}

func TestNew_FullControlRoutesToHost(t *testing.T) {
	rt, err := New(Config{PermissionMode: PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	if rt.PermissionMode() != PermissionModeFullControl {
		t.Fatalf("expected full_control mode, got %q", rt.PermissionMode())
	}
	decision := rt.DecideRoute("python3 app.py", SandboxPermissionAuto)
	if decision.Route != ExecutionRouteHost {
		t.Fatalf("expected host route, got %q", decision.Route)
	}
	if decision.Escalation != nil {
		t.Fatalf("expected no escalation in full_control, got %+v", decision.Escalation)
	}
}

func TestNew_DefaultRoutesSafeCommandToSandbox(t *testing.T) {
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  noopRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt.FallbackToHost() {
		t.Fatalf("expected sandbox enabled, fallback reason=%q", rt.FallbackReason())
	}
	decision := rt.DecideRoute("ls -la", SandboxPermissionAuto)
	if decision.Route != ExecutionRouteSandbox {
		t.Fatalf("expected sandbox route, got %q", decision.Route)
	}
	if decision.Escalation != nil {
		t.Fatalf("expected no escalation, got %+v", decision.Escalation)
	}
}

func TestNew_DefaultRunsAnyCommandInSandbox(t *testing.T) {
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  noopRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := rt.DecideRoute("python3 app.py", SandboxPermissionAuto)
	if decision.Route != ExecutionRouteSandbox {
		t.Fatalf("expected sandbox route, got %q", decision.Route)
	}
	if decision.Escalation != nil {
		t.Fatalf("expected no escalation, got %+v", decision.Escalation)
	}
}

func TestNew_DefaultRequireEscalatedForcesHost(t *testing.T) {
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  noopRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := rt.DecideRoute("ls -la", SandboxPermissionRequireEscalated)
	if decision.Route != ExecutionRouteHost {
		t.Fatalf("expected host route, got %q", decision.Route)
	}
	if decision.Escalation == nil {
		t.Fatal("expected escalation reason")
	}
}

func TestNew_DefaultFallbackWhenSandboxProbeFails(t *testing.T) {
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  probeRunner{probeErr: errors.New("daemon unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rt.FallbackToHost() {
		t.Fatal("expected fallback to host")
	}
	decision := rt.DecideRoute("ls", SandboxPermissionAuto)
	if decision.Route != ExecutionRouteHost {
		t.Fatalf("expected host route after fallback, got %q", decision.Route)
	}
	if decision.Escalation == nil {
		t.Fatal("expected escalation for fallback host path")
	}
}

func TestNew_DefaultDerivesWorkspaceWritePolicy(t *testing.T) {
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  noopRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := rt.SandboxPolicy()
	if policy.Type != SandboxPolicyWorkspaceWrite {
		t.Fatalf("expected workspace_write policy, got %q", policy.Type)
	}
	if !policy.NetworkAccess {
		t.Fatal("expected network access enabled for workspace_write policy")
	}
	if len(policy.WritableRoots) == 0 || policy.WritableRoots[0] != "." {
		t.Fatalf("expected default writable root '.', got %v", policy.WritableRoots)
	}
}

func TestNew_FullControlDerivesDangerFullPolicy(t *testing.T) {
	rt, err := New(Config{PermissionMode: PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	policy := rt.SandboxPolicy()
	if policy.Type != SandboxPolicyDangerFull {
		t.Fatalf("expected danger_full_access policy, got %q", policy.Type)
	}
	if !policy.NetworkAccess {
		t.Fatal("expected network access on danger_full_access policy")
	}
}

func TestClose_ClosesRuntimeResources(t *testing.T) {
	runner := &closeableRunner{}
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    dockerSandboxType,
		SandboxRunner:  runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(rt); err != nil {
		t.Fatal(err)
	}
	if err := Close(rt); err != nil {
		t.Fatal(err)
	}
	if runner.closed != 1 {
		t.Fatalf("expected runner closed once, got %d", runner.closed)
	}
}

func TestNew_DefaultSandboxTypeFollowsPlatform(t *testing.T) {
	oldGoos := runtimeGOOS
	runtimeGOOS = stdruntime.GOOS
	defer func() {
		runtimeGOOS = oldGoos
	}()
	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxRunner:  noopRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := dockerSandboxType
	if stdruntime.GOOS == "darwin" {
		want = seatbeltSandboxType
	}
	if rt.SandboxType() != want {
		t.Fatalf("expected default sandbox type %q, got %q", want, rt.SandboxType())
	}
}

func TestNew_DarwinSeatbeltFallsBackToDocker(t *testing.T) {
	oldGoos := runtimeGOOS
	oldFactories := sandboxFactories
	runtimeGOOS = "darwin"
	sandboxFactoriesMu.Lock()
	sandboxFactories = map[string]SandboxFactory{
		seatbeltSandboxType: staticFactory{
			typ:    seatbeltSandboxType,
			runner: probeRunner{probeErr: errors.New("seatbelt unavailable")},
		},
		dockerSandboxType: staticFactory{
			typ:    dockerSandboxType,
			runner: noopRunner{},
		},
	}
	sandboxFactoriesMu.Unlock()
	defer func() {
		runtimeGOOS = oldGoos
		sandboxFactoriesMu.Lock()
		sandboxFactories = oldFactories
		sandboxFactoriesMu.Unlock()
	}()

	rt, err := New(Config{
		PermissionMode: PermissionModeDefault,
		SandboxType:    seatbeltSandboxType,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt.FallbackToHost() {
		t.Fatalf("expected fallback to docker sandbox, got host fallback: %s", rt.FallbackReason())
	}
	if rt.SandboxType() != dockerSandboxType {
		t.Fatalf("expected sandbox type %q after fallback, got %q", dockerSandboxType, rt.SandboxType())
	}
}

func TestNew_DarwinDefaultAllSandboxesUnavailableFallbackToHost(t *testing.T) {
	oldGoos := runtimeGOOS
	oldFactories := sandboxFactories
	runtimeGOOS = "darwin"
	sandboxFactoriesMu.Lock()
	sandboxFactories = map[string]SandboxFactory{
		seatbeltSandboxType: staticFactory{
			typ:    seatbeltSandboxType,
			runner: probeRunner{probeErr: errors.New("seatbelt unavailable")},
		},
		dockerSandboxType: staticFactory{
			typ:    dockerSandboxType,
			runner: probeRunner{probeErr: errors.New("docker unavailable")},
		},
	}
	sandboxFactoriesMu.Unlock()
	defer func() {
		runtimeGOOS = oldGoos
		sandboxFactoriesMu.Lock()
		sandboxFactories = oldFactories
		sandboxFactoriesMu.Unlock()
	}()

	rt, err := New(Config{PermissionMode: PermissionModeDefault})
	if err != nil {
		t.Fatal(err)
	}
	if !rt.FallbackToHost() {
		t.Fatal("expected host fallback when seatbelt and docker are unavailable")
	}
	reason := rt.FallbackReason()
	if !strings.Contains(reason, "seatbelt") || !strings.Contains(reason, "docker") {
		t.Fatalf("expected fallback reason to include both candidates, got %q", reason)
	}
}

func TestSandboxTypeCandidatesForPlatform(t *testing.T) {
	cases := []struct {
		name     string
		request  string
		goos     string
		expected []string
	}{
		{name: "darwin default", request: "", goos: "darwin", expected: []string{"seatbelt", "docker"}},
		{name: "linux default", request: "", goos: "linux", expected: []string{"docker"}},
		{name: "darwin explicit seatbelt", request: "seatbelt", goos: "darwin", expected: []string{"seatbelt", "docker"}},
		{name: "darwin explicit docker", request: "docker", goos: "darwin", expected: []string{"docker"}},
		{name: "linux explicit seatbelt", request: "seatbelt", goos: "linux", expected: []string{"seatbelt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sandboxTypeCandidatesForPlatform(tc.request, tc.goos)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
			for i := range tc.expected {
				if got[i] != tc.expected[i] {
					t.Fatalf("expected %v, got %v", tc.expected, got)
				}
			}
		})
	}
}
