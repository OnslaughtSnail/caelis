package tool

import (
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

// RebindRuntime clones runtime-bound tools onto the provided runtime while
// leaving runtime-agnostic tools untouched.
func RebindRuntime(tools []Tool, runtime toolexec.Runtime) ([]Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	rebound := make([]Tool, 0, len(tools))
	for _, one := range tools {
		if one == nil {
			continue
		}
		switch typed := one.(type) {
		case *filesystem.ReadTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *filesystem.WriteTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *filesystem.PatchTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *filesystem.ListTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *filesystem.GlobTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *filesystem.SearchTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		case *toolshell.BashTool:
			toolWithRuntime, err := typed.WithRuntime(runtime)
			if err != nil {
				return nil, err
			}
			rebound = append(rebound, toolWithRuntime)
		default:
			rebound = append(rebound, one)
		}
	}
	return rebound, nil
}
