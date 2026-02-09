package lsp

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

const ActivateToolName = "LSP_ACTIVATE"

// NewActivate creates the fixed activation tool for progressive disclosure.
func NewActivate() (tool.Tool, error) {
	type args struct {
		Language     string   `json:"language"`
		Capabilities []string `json:"capabilities,omitempty"`
		Workspace    string   `json:"workspace,omitempty"`
	}
	type result struct {
		Language           string   `json:"language"`
		ToolsetID          string   `json:"toolset_id"`
		Activated          bool     `json:"activated"`
		AddedTools         []string `json:"added_tools,omitempty"`
		ActiveToolsets     []string `json:"active_toolsets"`
		AvailableLanguages []string `json:"available_languages"`
	}
	return tool.NewFunction[args, result](ActivateToolName, "Activate LSP tools for one language in current session.",
		func(ctx context.Context, in args) (result, error) {
			controller, ok := ctx.(lspbroker.ActivationController)
			if !ok {
				return result{}, fmt.Errorf("tool: LSP activation controller is not available")
			}
			language := strings.TrimSpace(strings.ToLower(in.Language))
			if language == "" {
				return result{}, fmt.Errorf("tool: arg %q is required", "language")
			}
			out, err := controller.ActivateLSP(ctx, lspbroker.ActivateRequest{
				Language:     language,
				Capabilities: in.Capabilities,
				Workspace:    in.Workspace,
			})
			if err != nil {
				return result{}, err
			}
			return result{
				Language:           out.Language,
				ToolsetID:          out.ToolsetID,
				Activated:          out.Activated,
				AddedTools:         out.AddedTools,
				ActiveToolsets:     out.ActiveToolsets,
				AvailableLanguages: controller.AvailableLSP(),
			}, nil
		})
}
