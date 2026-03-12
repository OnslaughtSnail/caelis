package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolVersion_UnmarshalAcceptsIntegerOnly(t *testing.T) {
	var got ProtocolVersion
	if err := json.Unmarshal([]byte(`1`), &got); err != nil {
		t.Fatalf("unmarshal protocolVersion: %v", err)
	}
	if got != CurrentProtocolVersion {
		t.Fatalf("unexpected protocolVersion: got %d want %d", got, CurrentProtocolVersion)
	}
}

func TestProtocolVersion_UnmarshalRejectsLegacyForms(t *testing.T) {
	tests := []string{`0.2`, `"0.2.0"`, `"1"`}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			var got ProtocolVersion
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatalf("expected protocolVersion %s to be rejected", raw)
			}
		})
	}
}

func TestInitializeResponse_MarshalsSchemaFieldsOnly(t *testing.T) {
	raw, err := json.Marshal(InitializeResponse{
		ProtocolVersion: CurrentProtocolVersion,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			MCP:         McpCapabilities{HTTP: true, SSE: true},
		},
	})
	if err != nil {
		t.Fatalf("marshal initialize response: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"protocolVersion":1`) {
		t.Fatalf("expected integer protocolVersion, got %s", text)
	}
	if !strings.Contains(text, `"mcpCapabilities":{"http":true,"sse":true}`) {
		t.Fatalf("expected schema mcpCapabilities field, got %s", text)
	}
	if strings.Contains(text, `"mcp":{"http":true,"sse":true}`) {
		t.Fatalf("did not expect legacy mcp field, got %s", text)
	}
}
