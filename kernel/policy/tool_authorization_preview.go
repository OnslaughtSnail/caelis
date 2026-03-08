package policy

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

func externalWriteAuthorizationRequest(toolName string, args map[string]any, runtime toolexec.Runtime, targetPath string) ToolAuthorizationRequest {
	req := ToolAuthorizationRequest{
		ToolName:   strings.TrimSpace(toolName),
		Permission: "write outside workspace writable roots",
		Reason:     "write target is outside workspace writable roots",
		Path:       strings.TrimSpace(targetPath),
	}
	if preview, err := toolfs.BuildMutationPreview(runtime, toolName, args); err == nil {
		req.Path = strings.TrimSpace(preview.Path)
		req.Preview = strings.TrimSpace(preview.Preview)
	}
	req.ScopeKey = approvalScopeKeyForPath(req.Path)
	return req
}

func toolAuthorizationRequest(toolName string, args map[string]any, reason string) ToolAuthorizationRequest {
	name := strings.TrimSpace(toolName)
	req := ToolAuthorizationRequest{
		ToolName: name,
		Reason:   strings.TrimSpace(reason),
		Target:   summarizeAuthorizationTarget(args),
		Preview:  summarizeAuthorizationPreview(args),
		ScopeKey: approvalScopeKeyForTool(name, args),
	}
	switch {
	case strings.HasPrefix(strings.ToUpper(name), "MCP__"):
		req.Permission = "external MCP tool call"
	default:
		req.Permission = "tool authorization"
	}
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

func approvalScopeKeyForTool(toolName string, args map[string]any) string {
	if host := approvalScopeHost(args); host != "" {
		return host
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return ""
	}
	return strings.ToUpper(name)
}

func approvalScopeHost(args map[string]any) string {
	for _, key := range []string{"url", "uri", "endpoint"} {
		raw, _ := args[key].(string)
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || strings.TrimSpace(parsed.Host) == "" {
			continue
		}
		return strings.ToLower(strings.TrimSpace(parsed.Host))
	}
	return ""
}

func summarizeAuthorizationTarget(args map[string]any) string {
	for _, key := range []string{"url", "uri", "endpoint"} {
		if value := strings.TrimSpace(stringValue(args[key])); value != "" {
			return value
		}
	}
	for _, key := range []string{"query", "q", "task", "prompt"} {
		if value := strings.TrimSpace(stringValue(args[key])); value != "" {
			return summarizeInline(value, 160)
		}
	}
	return ""
}

func summarizeAuthorizationPreview(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := prioritizedAuthorizationKeys(args)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(stringValue(args[key]))
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, summarizeInline(value, 120)))
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func prioritizedAuthorizationKeys(args map[string]any) []string {
	priority := []string{"url", "uri", "endpoint", "method", "query", "q", "task", "prompt", "domains"}
	out := make([]string, 0, len(args))
	seen := map[string]struct{}{}
	for _, key := range priority {
		if _, ok := args[key]; !ok {
			continue
		}
		out = append(out, key)
		seen[key] = struct{}{}
	}
	rest := make([]string, 0, len(args))
	for key := range args {
		if _, ok := seen[key]; ok {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func summarizeInline(input string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	rs := []rune(text)
	if limit <= 0 || len(rs) <= limit {
		return text
	}
	if limit <= 3 {
		return string(rs[:limit])
	}
	return string(rs[:limit-3]) + "..."
}
