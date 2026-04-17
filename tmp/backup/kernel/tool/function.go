package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// Handler is a typed function tool handler.
type Handler[TArgs, TResult any] func(context.Context, TArgs) (TResult, error)

type functionTool[TArgs, TResult any] struct {
	name        string
	description string
	handler     Handler[TArgs, TResult]
}

// NewFunction creates a typed function-backed tool.
func NewFunction[TArgs, TResult any](name, description string, handler Handler[TArgs, TResult]) (Tool, error) {
	if name == "" {
		return nil, fmt.Errorf("tool: name is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("tool: handler is nil")
	}
	return &functionTool[TArgs, TResult]{
		name:        name,
		description: description,
		handler:     handler,
	}, nil
}

func (t *functionTool[TArgs, TResult]) Name() string {
	return t.name
}

func (t *functionTool[TArgs, TResult]) Description() string {
	return t.description
}

func (t *functionTool[TArgs, TResult]) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.name,
		Description: t.description,
		Parameters:  schemaForType[TArgs](),
	}
}

func (t *functionTool[TArgs, TResult]) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	var typedArgs TArgs
	if err := convertViaJSON(args, &typedArgs); err != nil {
		return nil, fmt.Errorf("tool: decode args for %q: %w", t.name, err)
	}
	out, err := t.handler(ctx, typedArgs)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := convertViaJSON(out, &result); err == nil {
		return result, nil
	}
	return map[string]any{"result": out}, nil
}

func convertViaJSON(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
