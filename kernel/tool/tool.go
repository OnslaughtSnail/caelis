package tool

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// Tool is the executable tool contract.
type Tool interface {
	Name() string
	Description() string
	Declaration() model.ToolDefinition
	Run(context.Context, map[string]any) (map[string]any, error)
}

// BuildMap creates a name-indexed tool lookup map.
func BuildMap(tools []Tool) (map[string]Tool, error) {
	out := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := t.Name()
		if name == "" {
			return nil, fmt.Errorf("tool: empty name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("tool: duplicate tool %q", name)
		}
		out[name] = t
	}
	return out, nil
}

// Declarations returns model-visible declarations for tools.
func Declarations(tools []Tool) []model.ToolDefinition {
	decls := make([]model.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		decls = append(decls, t.Declaration())
	}
	return decls
}
