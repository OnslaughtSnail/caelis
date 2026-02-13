package policy

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

// ModelInput is the mutable request envelope for BeforeModel hooks.
type ModelInput struct {
	Messages []model.Message
	Tools    []model.ToolDefinition
}

// ToolInput is the mutable request envelope for BeforeTool hooks.
type ToolInput struct {
	Call       model.ToolCall
	Capability toolcap.Capability
	Decision   Decision
}

// ToolOutput is the mutable response envelope for AfterTool hooks.
type ToolOutput struct {
	Call       model.ToolCall
	Capability toolcap.Capability
	Decision   Decision
	Result     map[string]any
	Err        error
}

// Output is the mutable envelope before final response emission.
type Output struct {
	Message model.Message
}

// Hook defines policy interception points.
type Hook interface {
	Name() string
	BeforeModel(context.Context, ModelInput) (ModelInput, error)
	BeforeTool(context.Context, ToolInput) (ToolInput, error)
	AfterTool(context.Context, ToolOutput) (ToolOutput, error)
	BeforeOutput(context.Context, Output) (Output, error)
}

// NoopHook is the default pass-through implementation.
type NoopHook struct {
	HookName string
}

func (h NoopHook) Name() string {
	if h.HookName == "" {
		return "noop"
	}
	return h.HookName
}

func (h NoopHook) BeforeModel(ctx context.Context, in ModelInput) (ModelInput, error) {
	_ = ctx
	return in, nil
}

func (h NoopHook) BeforeTool(ctx context.Context, in ToolInput) (ToolInput, error) {
	_ = ctx
	return in, nil
}

func (h NoopHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	_ = ctx
	return out, nil
}

func (h NoopHook) BeforeOutput(ctx context.Context, out Output) (Output, error) {
	_ = ctx
	return out, nil
}
