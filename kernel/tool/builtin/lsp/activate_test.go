package lsp

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
)

type testController struct {
	context.Context
}

func (c testController) ActivateLSP(ctx context.Context, req lspbroker.ActivateRequest) (lspbroker.ActivateResult, error) {
	_ = ctx
	return lspbroker.ActivateResult{
		Language:       req.Language,
		ToolsetID:      "lsp:" + req.Language,
		Activated:      true,
		AddedTools:     []string{"LSP_DIAGNOSTICS"},
		ActiveToolsets: []string{"lsp:" + req.Language},
	}, nil
}

func (c testController) ActivatedToolsets() []string {
	return []string{"lsp:go"}
}

func (c testController) AvailableLSP() []string {
	return []string{"go"}
}

func TestActivateTool_Run(t *testing.T) {
	activateTool, err := NewActivate()
	if err != nil {
		t.Fatal(err)
	}
	ctx := testController{Context: context.Background()}
	result, err := activateTool.Run(ctx, map[string]any{"language": "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result["toolset_id"] != "lsp:go" {
		t.Fatalf("unexpected toolset_id: %#v", result["toolset_id"])
	}
}
