package lspbroker

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type fakeAdapter struct {
	language string
	tools    []tool.Tool
}

func (a fakeAdapter) Language() string {
	return a.language
}

func (a fakeAdapter) BuildToolSet(ctx context.Context, req ActivateRequest) (*ToolSet, error) {
	_ = ctx
	return &ToolSet{
		ID:       "lsp:" + req.Language,
		Language: req.Language,
		Tools:    a.tools,
	}, nil
}

func TestBroker_RegisterAndResolve(t *testing.T) {
	broker := New()
	echoTool, err := tool.NewFunction[struct{}, struct{}]("LSP_DIAGNOSTICS", "", func(ctx context.Context, args struct{}) (struct{}, error) {
		_ = ctx
		_ = args
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.RegisterAdapter(fakeAdapter{language: "go", tools: []tool.Tool{echoTool}}); err != nil {
		t.Fatal(err)
	}

	toolset, err := broker.Resolve(context.Background(), ActivateRequest{Language: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if toolset.ID != "lsp:go" {
		t.Fatalf("unexpected toolset id %q", toolset.ID)
	}
	if len(toolset.Tools) != 1 || toolset.Tools[0].Name() != "LSP_DIAGNOSTICS" {
		t.Fatalf("unexpected tools: %#v", toolset.Tools)
	}
}

func TestBroker_ResolveUnsupportedLanguage(t *testing.T) {
	broker := New()
	_, err := broker.Resolve(context.Background(), ActivateRequest{Language: "python"})
	if err == nil {
		t.Fatal("expected resolve error for unsupported language")
	}
}
