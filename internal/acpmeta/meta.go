package acpmeta

import "strings"

const (
	metaKeyRoot           = "caelis"
	metaKeySelfSpawnDepth = "selfSpawnDepth"

	DefaultSelfSpawnMaxDepth = 1
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

func SelfSpawnDepthFromMeta(meta map[string]any) int {
	if len(meta) == 0 {
		return 0
	}
	root, ok := meta[strings.TrimSpace(metaKeyRoot)].(map[string]any)
	if !ok || len(root) == 0 {
		return 0
	}
	return metaIntValue(root[metaKeySelfSpawnDepth])
}

func WithSelfSpawnDepth(meta map[string]any, depth int) map[string]any {
	if depth < 0 {
		depth = 0
	}
	out := CloneMeta(meta)
	if out == nil {
		out = map[string]any{}
	}
	root, _ := out[metaKeyRoot].(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	root[metaKeySelfSpawnDepth] = depth
	out[metaKeyRoot] = root
	return out
}

func metaIntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
