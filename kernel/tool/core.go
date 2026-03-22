package tool

import (
	"fmt"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

const (
	// ReadToolName is the mandatory built-in read tool name.
	ReadToolName = "READ"
	// SpawnToolName is the conventional self-spawn tool name.
	SpawnToolName = "SPAWN"
)

func isReservedCoreToolName(name string) bool {
	switch name {
	case ReadToolName, filesystem.WriteToolName, filesystem.PatchToolName, toolshell.BashToolName, TaskToolName:
		return true
	default:
		return false
	}
}

func isBuiltinCoreTool(t Tool) bool {
	switch t.(type) {
	case *filesystem.ReadTool, *filesystem.WriteTool, *filesystem.PatchTool, *toolshell.BashTool, *taskTool:
		return true
	default:
		return false
	}
}

// CoreToolsConfig configures mandatory kernel tools.
type CoreToolsConfig struct {
	Read         filesystem.ReadConfig
	Runtime      toolexec.Runtime
	TaskRegistry *task.Registry
}

// EnsureCoreTools injects mandatory kernel tools and returns a new tool list.
func EnsureCoreTools(userTools []Tool, cfg CoreToolsConfig) ([]Tool, error) {
	filteredTools := make([]Tool, 0, len(userTools))
	for _, t := range userTools {
		if t == nil {
			continue
		}
		if isReservedCoreToolName(t.Name()) {
			if isBuiltinCoreTool(t) {
				continue
			}
			return nil, fmt.Errorf("tool: %q is reserved by the core runtime and cannot be overridden", t.Name())
		}
		filteredTools = append(filteredTools, t)
	}
	readTool, err := filesystem.NewReadWithRuntime(cfg.Read, cfg.Runtime)
	if err != nil {
		return nil, err
	}
	writeTool, err := filesystem.NewWriteWithRuntime(cfg.Runtime)
	if err != nil {
		return nil, err
	}
	patchTool, err := filesystem.NewPatchWithRuntime(cfg.Runtime)
	if err != nil {
		return nil, err
	}
	bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: cfg.Runtime})
	if err != nil {
		return nil, err
	}

	extra := 5
	out := make([]Tool, 0, len(filteredTools)+extra)
	out = append(out, readTool)
	out = append(out, writeTool)
	out = append(out, patchTool)
	out = append(out, bashTool)
	taskTool, err := NewTaskTool()
	if err != nil {
		return nil, err
	}
	out = append(out, taskTool)
	out = append(out, filteredTools...)
	return out, nil
}
