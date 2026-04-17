package outputmeta

import "encoding/json"

// CompactVisible returns the minimal truncation signal that is meaningful to
// the model. If no truncation happened, the metadata is omitted entirely.
func CompactVisible(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	if !truncated(meta) {
		return nil
	}
	return map[string]any{"truncated": true}
}

func truncated(meta map[string]any) bool {
	for _, key := range []string{
		"truncated",
		"capture_truncated",
		"model_truncated",
		"stdout_cap_reached",
		"stderr_cap_reached",
	} {
		if boolValue(meta[key]) {
			return true
		}
	}
	for _, key := range []string{
		"stdout_dropped_bytes",
		"stderr_dropped_bytes",
		"stdout_earliest_marker",
		"stderr_earliest_marker",
	} {
		if intValue(meta[key]) > 0 {
			return true
		}
	}
	return false
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch typed {
		case "1", "true", "TRUE", "yes", "YES", "on", "ON":
			return true
		}
	case json.Number:
		return typed == "1"
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	}
	return false
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 0
}
