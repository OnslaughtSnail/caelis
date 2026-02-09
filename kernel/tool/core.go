package tool

import (
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

const (
	// ReadToolName is the mandatory built-in read tool name.
	ReadToolName = "READ"
)

// CoreToolsConfig configures mandatory kernel tools.
type CoreToolsConfig struct {
	Read    filesystem.ReadConfig
	Runtime toolexec.Runtime
}

// EnsureCoreTools injects mandatory kernel tools and returns a new tool list.
func EnsureCoreTools(userTools []Tool, cfg CoreToolsConfig) ([]Tool, error) {
	for _, t := range userTools {
		if t == nil {
			continue
		}
		if t.Name() == ReadToolName {
			return nil, fmt.Errorf("tool: %q is reserved as kernel core tool", ReadToolName)
		}
	}
	readTool, err := filesystem.NewReadWithRuntime(cfg.Read, cfg.Runtime)
	if err != nil {
		return nil, err
	}

	out := make([]Tool, 0, len(userTools)+1)
	out = append(out, readTool)
	out = append(out, userTools...)
	return out, nil
}
