package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
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
	if !in.Capability.HasOperation(toolcap.OperationFileWrite) {
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
	targetPath := resolveAbsPath(rawTargetPath, h.runtime.FileSystem())
	if targetPath == "" {
		// No path arg — let the tool itself handle the missing arg error.
		return in, nil
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
	absTarget := resolvePathWithSymlinks(resolveAbsPath(target, fs))
	for _, root := range roots {
		absRoot := resolvePathWithSymlinks(resolveAbsRoot(root, fs))
		if absRoot == "" {
			continue
		}
		if pathIsUnder(absTarget, absRoot) {
			return true
		}
	}
	// Also allow temp dir writes.
	if tmp := strings.TrimSpace(os.TempDir()); tmp != "" {
		if pathIsUnder(absTarget, resolvePathWithSymlinks(filepath.Clean(tmp))) {
			return true
		}
	}
	// On macOS /tmp is a symlink to /private/tmp; os.TempDir() returns
	// $TMPDIR which does NOT cover /tmp. Allow /tmp explicitly.
	for _, tmpRoot := range []string{"/tmp", "/private/tmp"} {
		if pathIsUnder(absTarget, tmpRoot) {
			return true
		}
	}
	return false
}

// resolvePathWithSymlinks returns a cleaned path with symlinks resolved on the
// deepest existing segment. This closes lexical-prefix bypasses such as writing
// through "<workspace>/link/..." where "link" points outside the workspace.
func resolvePathWithSymlinks(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}

	cur := clean
	suffix := make([]string, 0, 4)
	for {
		if cur == "" {
			return clean
		}
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return clean
			}
			resolved = filepath.Clean(resolved)
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

// resolveAbsPath normalizes a path to absolute form. It expands ~ and
// makes relative paths absolute from the filesystem working directory.
func resolveAbsPath(path string, fs toolexec.FileSystem) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if fs != nil {
			home, err := fs.UserHomeDir()
			if err == nil {
				path = filepath.Join(home, path[2:])
			}
		} else {
			home, err := os.UserHomeDir()
			if err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if !filepath.IsAbs(path) {
		var wd string
		if fs != nil {
			wd, _ = fs.Getwd()
		}
		if wd == "" {
			wd, _ = os.Getwd()
		}
		path = filepath.Join(wd, path)
	}
	return filepath.Clean(path)
}

// resolveAbsRoot resolves a writable root to absolute path. Relative roots
// are resolved against the filesystem working directory.
func resolveAbsRoot(root string, fs toolexec.FileSystem) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	// Relative root — resolve against cwd.
	var wd string
	if fs != nil {
		wd, _ = fs.Getwd()
	}
	if wd == "" {
		wd, _ = os.Getwd()
	}
	if wd == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(wd, root))
}

// pathIsUnder returns true if target is path-equal to root or is within root.
func pathIsUnder(target, root string) bool {
	if target == root {
		return true
	}
	// Ensure root ends with separator for proper prefix matching.
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(target, prefix)
}
