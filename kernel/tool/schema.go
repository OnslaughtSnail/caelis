package tool

import (
	"encoding/json"
	"reflect"
	"strings"
)

func schemaForType[T any]() map[string]any {
	var zero T
	return schemaForReflectType(reflect.TypeOf(zero))
}

func schemaForReflectType(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{"type": "object"}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if t == nil {
			return map[string]any{"type": "object"}
		}
	}

	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Name
			if tag := field.Tag.Get("json"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if strings.TrimSpace(parts[0]) != "" {
					name = strings.TrimSpace(parts[0])
				}
				if !contains(parts[1:], "omitempty") {
					required = append(required, name)
				}
			} else {
				required = append(required, name)
			}
			schema := schemaForReflectType(field.Type)
			applyFieldSchemaTags(schema, field)
			properties[name] = schema
		}
		out := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": schemaForReflectType(t.Elem()),
		}
	case reflect.Map:
		return map[string]any{
			"type": "object",
		}
	default:
		return map[string]any{"type": "string"}
	}
}

func applyFieldSchemaTags(schema map[string]any, field reflect.StructField) {
	if len(schema) == 0 {
		return
	}
	description := strings.TrimSpace(field.Tag.Get("desc"))
	if extra := strings.TrimSpace(field.Tag.Get("required_if")); extra != "" {
		if description != "" {
			description += " "
		}
		description += "Required when " + extra + "."
	}
	if extra := strings.TrimSpace(field.Tag.Get("conflicts_with")); extra != "" {
		if description != "" {
			description += " "
		}
		description += "Do not use with " + extra + "."
	}
	if description != "" {
		schema["description"] = description
	}
	if enumValues := parseEnumTag(field.Tag.Get("enum")); len(enumValues) > 0 {
		schema["enum"] = enumValues
	}
	if defaultValue := parseSchemaLiteral(field.Tag.Get("default")); defaultValue != nil {
		schema["default"] = defaultValue
	}
	if exampleValue := parseSchemaLiteral(field.Tag.Get("example")); exampleValue != nil {
		schema["examples"] = []any{exampleValue}
	}
}

func parseEnumTag(raw string) []any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func parseSchemaLiteral(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		return value
	}
	return raw
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}
