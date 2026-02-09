package tool

import "testing"

func TestSchemaForType(t *testing.T) {
	type args struct {
		Text   string `json:"text"`
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
}
