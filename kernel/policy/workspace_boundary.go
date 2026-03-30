package policy

import (
	"context"
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/fsboundary"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

// WorkspaceBoundaryConfig configures the workspace boundary enforcement hook.
type WorkspaceBoundaryConfig struct {
	Runtime toolexec.Runtime
}

type workspaceBoundaryHook struct {
	name    string
	runtime toolexec.Runtime
}

// WorkspaceBoundary returns a policy hook that requires approval for file
// write operations targeting paths outside the declared writable roots.
// This closes the gap where WRITE/PATCH tools bypass the seatbelt sandbox
// which only wraps BASH command execution.
func WorkspaceBoundary(cfg WorkspaceBoundaryConfig) Hook {
	return workspaceBoundaryHook{
		name:    "workspace_boundary",
		runtime: cfg.Runtime,
	}
}

func (h workspaceBoundaryHook) Name() string {
	return h.name
}

func (h workspaceBoundaryHook) BeforeModel(ctx context.Context, in ModelInput) (ModelInput, error) {
	_ = ctx
	return in, nil
}

func (h workspaceBoundaryHook) BeforeTool(ctx context.Context, in ToolInput) (ToolInput, error) {
	_ = ctx
	if h.runtime == nil {
		return in, nil
	}

	// Only intercept file-write operations (WRITE, PATCH, etc.)
	if !in.Capability.HasOperation(capability.OperationFileWrite) {
		return in, nil
	}

	policy := h.runtime.SandboxPolicy()

	// No boundary enforcement for full-access or external sandbox modes.
	switch policy.Type {
	case toolexec.SandboxPolicyDangerFull, toolexec.SandboxPolicyExternal:
		return in, nil
	}

	// Also skip if permission is full_control.
	if h.runtime.PermissionMode() == toolexec.PermissionModeFullControl {
		return in, nil
	}

	// No writable roots declared → cannot enforce.
	if len(policy.WritableRoots) == 0 {
		return in, nil
	}

	args := resolveToolInputArgs(in)
	rawTargetPath, _ := args["path"].(string)
	targetPath := fsboundary.ResolveAbsPath(rawTargetPath, h.runtime.FileSystem())
	if targetPath == "" {
		// No path arg — let the tool itself handle the missing arg error.
		return in, nil
	}

	if fsboundary.IsWithinReadOnlySubpaths(targetPath, policy.ReadOnlySubpaths, h.runtime.FileSystem()) {
		return ToolInput{}, fmt.Errorf("tool %q targets read-only path %q under current sandbox policy", in.Call.Name, targetPath)
	}
	if isWithinWritableRoots(targetPath, policy.WritableRoots, h.runtime.FileSystem()) {
		return in, nil
	}

	// Path is outside all writable roots — require approval.
	authorizer, ok := ToolAuthorizerFromContext(ctx)
	if !ok {
		return ToolInput{}, &toolexec.ApprovalRequiredError{
			Reason: fmt.Sprintf("tool %q targets %q which is outside workspace writable roots", in.Call.Name, targetPath),
		}
	}

	allowed, err := authorizer.AuthorizeTool(ctx, externalWriteAuthorizationRequest(in.Call.Name, args, h.runtime, targetPath))
	if err != nil {
		return ToolInput{}, err
	}
	if !allowed {
		return ToolInput{}, &toolexec.ApprovalAbortedError{
			Reason: fmt.Sprintf("tool %q write to %q outside workspace denied", in.Call.Name, targetPath),
		}
	}
	return in, nil
}

func (h workspaceBoundaryHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	_ = ctx
	return out, nil
}

func (h workspaceBoundaryHook) BeforeOutput(ctx context.Context, out Output) (Output, error) {
	_ = ctx
	return out, nil
}

// isWithinWritableRoots checks whether the target path falls within any of the
// declared writable roots. Roots that are relative are resolved against the
// filesystem working directory.
func isWithinWritableRoots(target string, roots []string, fs toolexec.FileSystem) bool {
	return fsboundary.IsWithinRoots(target, roots, fs) || fsboundary.IsWithinScratchRoots(target, fs)
}
