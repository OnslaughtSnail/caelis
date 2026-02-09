package tool

import (
	"strings"
	"testing"
)

func TestTruncateMap_NoTruncation(t *testing.T) {
	in := map[string]any{"msg": "hello"}
	out, info := TruncateMap(in, TruncationPolicy{MaxTokens: 100})
	if info.Truncated {
		t.Fatal("expected not truncated")
	}
	if out["msg"] != "hello" {
		t.Fatalf("unexpected output: %v", out)
	}
}

func TestTruncateMap_WithMeta(t *testing.T) {
	long := strings.Repeat("abcdef", 3000)
	in := map[string]any{"stdout": long}
	out, info := TruncateMap(in, TruncationPolicy{MaxTokens: 100})
	if !info.Truncated {
		t.Fatal("expected truncated")
	}
	out = AddTruncationMeta(out, info)
	meta, ok := out["_tool_truncation"].(map[string]any)
	if !ok {
		t.Fatalf("expected truncation meta, got: %#v", out["_tool_truncation"])
	}
	if meta["truncated"] != true {
		t.Fatalf("expected truncated=true, got: %#v", meta["truncated"])
	}
	stdout, _ := out["stdout"].(string)
	if stdout == "" || !strings.Contains(stdout, "truncated") {
		t.Fatalf("expected truncated stdout marker, got: %q", stdout)
	}
}
