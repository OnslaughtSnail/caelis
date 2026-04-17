package tool

import "testing"

func TestSchemaForType(t *testing.T) {
	type args struct {
		Text   string `json:"text" desc:"Text to echo back."`
		Offset int    `json:"offset,omitempty"`
	}
	schema := schemaForType[args]()
	if schema["type"] != "object" {
		t.Fatalf("unexpected schema type: %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties")
	}
	if _, ok := props["text"]; !ok {
		t.Fatalf("missing property text")
	}
	textProp, _ := props["text"].(map[string]any)
	if got := textProp["description"]; got != "Text to echo back." {
		t.Fatalf("unexpected text description: %#v", got)
	}
}

func TestSchemaForType_AppliesCompactFieldTags(t *testing.T) {
	type args struct {
		Action string `json:"action" desc:"Control action." enum:"wait,status,write" example:"\"wait\""`
		Input  string `json:"input,omitempty" required_if:"action=write" conflicts_with:"action=list"`
		Limit  int    `json:"limit,omitempty" default:"50"`
	}
	schema := schemaForType[args]()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties")
	}
	action, _ := props["action"].(map[string]any)
	enum, _ := action["enum"].([]any)
	if len(enum) != 3 || enum[0] != "wait" || enum[2] != "write" {
		t.Fatalf("unexpected enum: %#v", action["enum"])
	}
	examples, _ := action["examples"].([]any)
	if len(examples) != 1 || examples[0] != "wait" {
		t.Fatalf("unexpected examples: %#v", action["examples"])
	}
	input, _ := props["input"].(map[string]any)
	desc, _ := input["description"].(string)
	if desc != "Required when action=write. Do not use with action=list." {
		t.Fatalf("unexpected input description: %q", desc)
	}
	limit, _ := props["limit"].(map[string]any)
	if got := limit["default"]; got != float64(50) {
		t.Fatalf("unexpected default: %#v", got)
	}
}
