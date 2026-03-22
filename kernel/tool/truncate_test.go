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

func TestTruncateText_DoesNotDuplicateExistingHeader(t *testing.T) {
	in := "Total output lines: 400\n\n" + strings.Repeat("abcdef\n", 200)
	out, removed := TruncateText(in, TruncationPolicy{MaxTokens: 50})
	if removed == 0 {
		t.Fatal("expected truncation")
	}
	if got := strings.Count(out, "Total output lines:"); got != 1 {
		t.Fatalf("expected single total-lines header, got %d in %q", got, out)
	}
}

func TestAddTruncationMeta_MarksOutputMeta(t *testing.T) {
	long := strings.Repeat("abcdef", 3000)
	in := map[string]any{
		"stdout": long,
		"output_meta": map[string]any{
			"model_truncated": false,
		},
	}
	out, info := TruncateMap(in, TruncationPolicy{MaxTokens: 100})
	out = AddTruncationMeta(out, info)
	meta, _ := out["output_meta"].(map[string]any)
	if meta["model_truncated"] != true {
		t.Fatalf("expected output_meta.model_truncated=true, got %#v", out)
	}
}
