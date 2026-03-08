package tool

import (
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

const (
	// ReadToolName is the mandatory built-in read tool name.
	ReadToolName = "READ"
)

// CoreToolsConfig configures mandatory kernel tools.
type CoreToolsConfig struct {
	Read            filesystem.ReadConfig
	Runtime         toolexec.Runtime
	TaskRegistry    *task.Registry
	DisableDelegate bool
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
		if t.Name() == DelegateTaskToolName {
			return nil, fmt.Errorf("tool: %q is reserved as kernel core tool", DelegateTaskToolName)
		}
		if t.Name() == TaskToolName {
			return nil, fmt.Errorf("tool: %q is reserved as kernel core tool", TaskToolName)
		}
	}
	readTool, err := filesystem.NewReadWithRuntime(cfg.Read, cfg.Runtime)
	if err != nil {
		return nil, err
	}

	extra := 2
	if !cfg.DisableDelegate {
		extra++
	}
	out := make([]Tool, 0, len(userTools)+extra)
	out = append(out, readTool)
	taskTool, err := NewTaskTool()
	if err != nil {
		return nil, err
	}
	if !cfg.DisableDelegate {
		delegateTool, err := NewDelegateTask()
		if err != nil {
			return nil, err
		}
		out = append(out, delegateTool)
	}
	out = append(out, taskTool)
	out = append(out, userTools...)
	return out, nil
}
