package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

var cliExecRuntimeBuilder = toolexec.New
var cliSandboxSelector = toolexec.SelectSandbox

type swappableRuntime struct {
	mu      sync.RWMutex
	current toolexec.Runtime
}

func newSwappableRuntime(rt toolexec.Runtime) *swappableRuntime {
	return &swappableRuntime{current: rt}
}

func (r *swappableRuntime) Set(next toolexec.Runtime) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.current = next
	r.mu.Unlock()
}

func (r *swappableRuntime) Current() toolexec.Runtime {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

func (r *swappableRuntime) PermissionMode() toolexec.PermissionMode {
	if current := r.Current(); current != nil {
		return current.PermissionMode()
	}
	return toolexec.PermissionModeDefault
}

func (r *swappableRuntime) SandboxType() string {
	if current := r.Current(); current != nil {
		return current.SandboxType()
	}
	return ""
}

func (r *swappableRuntime) SandboxPolicy() toolexec.SandboxPolicy {
	if current := r.Current(); current != nil {
		return current.SandboxPolicy()
	}
	return toolexec.SandboxPolicy{}
}

func (r *swappableRuntime) FallbackToHost() bool {
	if current := r.Current(); current != nil {
		return current.FallbackToHost()
	}
	return false
}

func (r *swappableRuntime) FallbackReason() string {
	if current := r.Current(); current != nil {
		return current.FallbackReason()
	}
	return ""
}

func (r *swappableRuntime) Diagnostics() toolexec.SandboxDiagnostics {
	if current := r.Current(); current != nil {
		return current.Diagnostics()
	}
	return toolexec.SandboxDiagnostics{}
}

func (r *swappableRuntime) State() toolexec.RuntimeState {
	if current := r.Current(); current != nil {
		return current.State()
	}
	return toolexec.RuntimeState{}
}

func (r *swappableRuntime) FileSystem() toolexec.FileSystem {
	if current := r.Current(); current != nil {
		return current.FileSystem()
	}
	return nil
}

func (r *swappableRuntime) Execute(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	if current := r.Current(); current != nil {
		return current.Execute(ctx, req)
	}
	return toolexec.CommandResult{}, fmt.Errorf("runtime unavailable")
}

func (r *swappableRuntime) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	if current := r.Current(); current != nil {
		return current.Start(ctx, req)
	}
	return nil, fmt.Errorf("runtime unavailable")
}

func (r *swappableRuntime) OpenSession(ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	if current := r.Current(); current != nil {
		return current.OpenSession(ref)
	}
	return nil, fmt.Errorf("runtime unavailable")
}

func (r *swappableRuntime) Decide(ctx context.Context, req toolexec.RouteRequest) (toolexec.CommandDecision, error) {
	_ = ctx
	if current := r.Current(); current != nil {
		return current.DecideRoute(req.Command, req.SandboxPermission), nil
	}
	return toolexec.CommandDecision{}, fmt.Errorf("runtime unavailable")
}

func (r *swappableRuntime) DecideRoute(command string, sandboxPermission toolexec.SandboxPermission) toolexec.CommandDecision {
	decision, _ := r.Decide(context.Background(), toolexec.RouteRequest{
		Command:           command,
		SandboxPermission: sandboxPermission,
	})
	return decision
}

func newExecutionRuntime(mode toolexec.PermissionMode, sandboxType string, sandboxHelperPath string, sandboxPolicy toolexec.SandboxPolicy) (toolexec.Runtime, error) {
	return cliExecRuntimeBuilder(toolexec.Config{
		PermissionMode:    mode,
		SandboxType:       normalizeSandboxType(strings.TrimSpace(sandboxType)),
		SandboxPolicy:     sandboxPolicy,
		SandboxHelperPath: strings.TrimSpace(sandboxHelperPath),
	})
}

func validateExplicitSandboxType(sandboxType string, sandboxHelperPath string) error {
	runner, diagnostics, err := cliSandboxSelector(toolexec.Config{
		PermissionMode:    toolexec.PermissionModeDefault,
		SandboxType:       normalizeSandboxType(strings.TrimSpace(sandboxType)),
		SandboxHelperPath: strings.TrimSpace(sandboxHelperPath),
	})
	if err != nil {
		return err
	}
	if closer, ok := runner.(interface{ Close() error }); ok {
		defer func() {
			_ = closer.Close()
		}()
	}
	if diagnostics.FallbackToHost {
		return fmt.Errorf("sandbox type %q is unavailable: %s", sandboxType, diagnostics.FallbackReason)
	}
	return nil
}
