package acp

import coremeta "github.com/OnslaughtSnail/caelis/internal/acpmeta"

const DefaultSelfSpawnMaxDepth = coremeta.DefaultSelfSpawnMaxDepth

func CloneMeta(meta map[string]any) map[string]any {
	return coremeta.CloneMeta(meta)
}

func SelfSpawnDepthFromMeta(meta map[string]any) int {
	return coremeta.SelfSpawnDepthFromMeta(meta)
}

func WithSelfSpawnDepth(meta map[string]any, depth int) map[string]any {
	return coremeta.WithSelfSpawnDepth(meta, depth)
}
