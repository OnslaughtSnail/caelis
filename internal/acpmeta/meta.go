package acpmeta

import "strings"

const (
	metaKeyRoot           = "caelis"
	metaKeyDelegatedChild = "delegatedChild"
)

func CloneMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		if nested, ok := value.(map[string]any); ok {
			child := make(map[string]any, len(nested))
			for childKey, childValue := range nested {
				child[childKey] = childValue
			}
			out[key] = child
			continue
		}
		out[key] = value
	}
	return out
}

func IsDelegatedChild(meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	root, ok := meta[strings.TrimSpace(metaKeyRoot)].(map[string]any)
	if !ok || len(root) == 0 {
		return false
	}
	return metaBoolValue(root[metaKeyDelegatedChild])
}

func WithDelegatedChild(meta map[string]any, delegated bool) map[string]any {
	out := CloneMeta(meta)
	if out == nil {
		out = map[string]any{}
	}
	root, _ := out[metaKeyRoot].(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	root[metaKeyDelegatedChild] = delegated
	out[metaKeyRoot] = root
	return out
}

func metaBoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}
