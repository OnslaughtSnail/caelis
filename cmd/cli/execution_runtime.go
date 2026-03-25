package main

import (
	"fmt"
	"strings"
	"sync"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

var cliExecRuntimeBuilder = func(cfg toolexec.Config) (toolexec.Runtime, error) {
	return toolexec.NewModeSwitchable(cfg)
}

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

func (r *swappableRuntime) FileSystem() toolexec.FileSystem {
	if current := r.Current(); current != nil {
		return current.FileSystem()
	}
	return nil
}

func (r *swappableRuntime) HostRunner() toolexec.CommandRunner {
	if current := r.Current(); current != nil {
		return current.HostRunner()
	}
	return nil
}

func (r *swappableRuntime) SandboxRunner() toolexec.CommandRunner {
	if current := r.Current(); current != nil {
		return current.SandboxRunner()
	}
	return nil
}

func (r *swappableRuntime) DecideRoute(command string, sandboxPermission toolexec.SandboxPermission) toolexec.CommandDecision {
	if current := r.Current(); current != nil {
		return current.DecideRoute(command, sandboxPermission)
	}
	return toolexec.CommandDecision{}
}

func newExecutionRuntime(mode toolexec.PermissionMode, sandboxType string, sandboxHelperPath string) (toolexec.Runtime, error) {
	return cliExecRuntimeBuilder(toolexec.Config{
		PermissionMode:    mode,
		SandboxType:       normalizeSandboxType(strings.TrimSpace(sandboxType)),
		SandboxHelperPath: strings.TrimSpace(sandboxHelperPath),
	})
}

func validateExplicitSandboxType(sandboxType string, sandboxHelperPath string) error {
	rt, err := newExecutionRuntime(toolexec.PermissionModeDefault, sandboxType, sandboxHelperPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = toolexec.Close(rt)
	}()
	if rt.FallbackToHost() {
		return fmt.Errorf("sandbox type %q is unavailable: %s", sandboxType, rt.FallbackReason())
	}
	return nil
}
