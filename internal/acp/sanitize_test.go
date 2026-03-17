package acp

import (
	"reflect"
	"testing"
)

// ----- sanitizeToolResultForACP unit tests -----

func TestSanitizeToolResultForACP_NilInput(t *testing.T) {
	got := sanitizeToolResultForACP(nil)
	if got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}

func TestSanitizeToolResultForACP_EmptyInput(t *testing.T) {
	got := sanitizeToolResultForACP(map[string]any{})
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestSanitizeToolResultForACP_RemovesUIPrefix(t *testing.T) {
	input := map[string]any{
		"path":        "/tmp/a.txt",
		"ok":          true,
		"_ui_preview": "diff content",
		"_ui_note":    "internal",
	}
	got := sanitizeToolResultForACP(input)
	if _, ok := got["_ui_preview"]; ok {
		t.Fatal("expected _ui_preview to be removed")
	}
	if _, ok := got["_ui_note"]; ok {
		t.Fatal("expected _ui_note to be removed")
	}
	if got["path"] != "/tmp/a.txt" || got["ok"] != true {
		t.Fatalf("expected visible fields preserved, got %v", got)
	}
}

func TestSanitizeToolResultForACP_RemovesTopLevelMetadata(t *testing.T) {
	input := map[string]any{
		"path": "file.go",
		"metadata": map[string]any{
			"internal": "data",
		},
	}
	got := sanitizeToolResultForACP(input)
	if _, ok := got["metadata"]; ok {
		t.Fatal("expected top-level metadata to be removed")
	}
	if got["path"] != "file.go" {
		t.Fatal("expected path preserved")
	}
}

func TestSanitizeToolResultForACP_PreservesNestedMetadata(t *testing.T) {
	input := map[string]any{
		"payload": map[string]any{
			"metadata": map[string]any{
				"keep": "yes",
			},
			"value": "ok",
		},
	}
	got := sanitizeToolResultForACP(input)
	payload, ok := got["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %T", got["payload"])
	}
	nested, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatal("expected nested metadata preserved")
	}
	if nested["keep"] != "yes" {
		t.Fatalf("expected nested metadata value preserved, got %v", nested)
	}
}

func TestSanitizeToolResultForACP_RecursiveUIRemoval(t *testing.T) {
	input := map[string]any{
		"outer": map[string]any{
			"_ui_hidden": "internal",
			"visible":    "keep",
			"inner": map[string]any{
				"_ui_deep": "also hidden",
				"data":     42,
			},
		},
	}
	got := sanitizeToolResultForACP(input)
	outer, ok := got["outer"].(map[string]any)
	if !ok {
		t.Fatalf("expected outer map, got %T", got["outer"])
	}
	if _, ok := outer["_ui_hidden"]; ok {
		t.Fatal("expected _ui_hidden removed from nested map")
	}
	if outer["visible"] != "keep" {
		t.Fatal("expected visible preserved in nested map")
	}
	inner, ok := outer["inner"].(map[string]any)
	if !ok {
		t.Fatal("expected inner map")
	}
	if _, ok := inner["_ui_deep"]; ok {
		t.Fatal("expected _ui_deep removed from deeply nested map")
	}
	if inner["data"] != 42 {
		t.Fatal("expected data preserved in deeply nested map")
	}
}

func TestSanitizeToolResultForACP_ArrayRecursion(t *testing.T) {
	input := map[string]any{
		"items": []any{
			map[string]any{
				"name":       "item1",
				"_ui_hidden": "internal",
			},
			"scalar_value",
			42,
		},
	}
	got := sanitizeToolResultForACP(input)
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", got["items"])
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map in array, got %T", items[0])
	}
	if first["name"] != "item1" {
		t.Fatal("expected name preserved in array element")
	}
	if _, ok := first["_ui_hidden"]; ok {
		t.Fatal("expected _ui_hidden removed from array element")
	}
	if items[1] != "scalar_value" || items[2] != 42 {
		t.Fatal("expected scalar values preserved in array")
	}
}

func TestSanitizeToolResultForACP_ScalarResultPreserved(t *testing.T) {
	input := map[string]any{
		"status":  "ok",
		"count":   5,
		"enabled": true,
		"ratio":   3.14,
	}
	got := sanitizeToolResultForACP(input)
	if got["status"] != "ok" || got["count"] != 5 || got["enabled"] != true || got["ratio"] != 3.14 {
		t.Fatalf("expected all scalar values preserved, got %v", got)
	}
}

// ----- hasToolError tests -----

func TestHasToolError_NilResult(t *testing.T) {
	if hasToolError(nil) {
		t.Fatal("nil result should not have error")
	}
}

func TestHasToolError_EmptyError(t *testing.T) {
	if hasToolError(map[string]any{"error": ""}) {
		t.Fatal("empty error string should not count as error")
	}
}

func TestHasToolError_NilError(t *testing.T) {
	if hasToolError(map[string]any{"error": nil}) {
		t.Fatal("nil error should not count as error")
	}
}

func TestHasToolError_WithError(t *testing.T) {
	if !hasToolError(map[string]any{"error": "something failed"}) {
		t.Fatal("non-empty error should be detected")
	}
}

func TestHasToolError_NoErrorKey(t *testing.T) {
	if hasToolError(map[string]any{"result": "ok"}) {
		t.Fatal("result without error key should not have error")
	}
}

// ----- SessionConfigOptionTemplate.supports tests -----

func TestSessionConfigOptionTemplate_Supports(t *testing.T) {
	tmpl := SessionConfigOptionTemplate{
		ID:           "mode",
		Name:         "Mode",
		DefaultValue: "auto",
		Options: []SessionConfigSelectOption{
			{Value: "auto", Name: "Auto"},
			{Value: "on", Name: "On"},
			{Value: "off", Name: "Off"},
		},
	}
	if !tmpl.supports("auto") {
		t.Fatal("expected auto to be supported")
	}
	if !tmpl.supports("on") {
		t.Fatal("expected on to be supported")
	}
	if tmpl.supports("unknown") {
		t.Fatal("expected unknown to not be supported")
	}
	if tmpl.supports("") {
		t.Fatal("expected empty string to not be supported")
	}
}

// ----- anyMap tests -----

func TestAnyMap_MapStringAny(t *testing.T) {
	input := map[string]any{"key": "value"}
	got := anyMap(input)
	if !reflect.DeepEqual(got, input) {
		t.Fatalf("expected same map, got %v", got)
	}
}

func TestAnyMap_MapStringString(t *testing.T) {
	input := map[string]string{"key": "value"}
	got := anyMap(input)
	if got["key"] != "value" {
		t.Fatalf("expected converted map, got %v", got)
	}
}

func TestAnyMap_UnsupportedType(t *testing.T) {
	got := anyMap("not a map")
	if got != nil {
		t.Fatalf("expected nil for unsupported type, got %v", got)
	}
}

func TestAnyMap_Nil(t *testing.T) {
	got := anyMap(nil)
	if got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}
