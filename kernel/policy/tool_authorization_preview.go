package policy

import (
	"path/filepath"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

func externalWriteAuthorizationRequest(toolName string, args map[string]any, runtime toolexec.Runtime, targetPath string) ToolAuthorizationRequest {
	req := ToolAuthorizationRequest{
		ToolName: strings.TrimSpace(toolName),
		Reason:   "write target is outside workspace writable roots",
		Path:     strings.TrimSpace(targetPath),
	}
	if preview, err := toolfs.BuildMutationPreview(runtime, toolName, args); err == nil {
		req.Path = strings.TrimSpace(preview.Path)
		req.Preview = strings.TrimSpace(preview.Preview)
	}
	req.ScopeKey = approvalScopeKeyForPath(req.Path)
	return req
}

func approvalScopeKeyForPath(targetPath string) string {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return ""
	}
	scope := filepath.Dir(targetPath)
	if strings.TrimSpace(scope) == "" || scope == "." {
		return targetPath
	}
	return filepath.Clean(scope)
}
