package acpprojector

import (
	"strings"
	"testing"
)

func TestFormatToolResult_ExtractsACPTextContent(t *testing.T) {
	got := FormatToolResult("SEARCHING", nil, map[string]any{
		"content":         `{"type":"text","text":{"value":"今天上海天气晴，11到19度。"}}`,
		"detailedContent": `{"type":"text","text":{"value":"今天上海天气晴，11到19度。"}}`,
	}, "completed")

	if !strings.Contains(got, "今天上海天气晴，11到19度。") {
		t.Fatalf("expected readable ACP text content, got %q", got)
	}
	if strings.Contains(got, `{"type":`) || strings.Contains(got, `\"type\"`) {
		t.Fatalf("did not expect raw JSON blob, got %q", got)
	}
}

func TestFormatToolStart_SuppressesNullPlaceholderArgs(t *testing.T) {
	if got := FormatToolStart("SPAWN", map[string]any{"_display": "null"}); got != "" {
		t.Fatalf("expected null display placeholder to be suppressed, got %q", got)
	}
	if got := FormatToolStart("SPAWN", map[string]any{"_display": "{}"}); got != "" {
		t.Fatalf("expected empty json placeholder to be suppressed, got %q", got)
	}
}
