package tool

import (
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
			properties[name] = schemaForReflectType(field.Type)
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

func contains(items []string, target string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}
