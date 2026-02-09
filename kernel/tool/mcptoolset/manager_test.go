package mcptoolset

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExposedToolNameLength(t *testing.T) {
	name := exposedToolName("very-long-server-name-0123456789-abcdef", "tool-name-0123456789-abcdef-0123456789")
	if len(name) > 64 {
		t.Fatalf("tool name too long: %d (%q)", len(name), name)
	}
	if !strings.Contains(name, "__") {
		t.Fatalf("expected namespaced tool name, got %q", name)
	}
}

func TestNormalizeSchemaFallback(t *testing.T) {
	got := normalizeSchema(struct {
		Type string `json:"type"`
	}{
		Type: "object",
	})
	if got["type"] != "object" {
		t.Fatalf("unexpected schema: %#v", got)
	}
}

func TestMCPToolRunSuccess(t *testing.T) {
	session, cleanup := setupClientSession(t, "echo", func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		_ = ctx
		_ = req
		return nil, map[string]any{
			"echo": args["text"],
		}, nil
	})
	defer cleanup()

	tool := &mcpTool{
		name:         "demo__echo",
		originalName: "echo",
		serverName:   "demo",
		description:  "demo",
		parameters:   map[string]any{"type": "object"},
		getSession: func(context.Context) (*mcp.ClientSession, error) {
			return session, nil
		},
	}
	out, err := tool.Run(context.Background(), map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("run tool: %v", err)
	}
	if out["echo"] != "hello" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestMCPToolRunError(t *testing.T) {
	session, cleanup := setupClientSession(t, "boom", func(ctx context.Context, req *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		_ = ctx
		_ = req
		_ = args
		return nil, nil, fmt.Errorf("boom")
	})
	defer cleanup()

	tool := &mcpTool{
		name:         "demo__boom",
		originalName: "boom",
		serverName:   "demo",
		description:  "demo",
		parameters:   map[string]any{"type": "object"},
		getSession: func(context.Context) (*mcp.ClientSession, error) {
			return session, nil
		},
	}
	_, err := tool.Run(context.Background(), map[string]any{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func setupClientSession(
	t *testing.T,
	toolName string,
	handler func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error),
) (*mcp.ClientSession, func()) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "v0.0.1",
	}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: toolName,
		InputSchema: map[string]any{
			"type": "object",
		},
	}, handler)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v0.0.1",
	}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	}
}
