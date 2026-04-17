package acp

import coremeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"

func CloneMeta(meta map[string]any) map[string]any {
	return coremeta.CloneMeta(meta)
}

func IsDelegatedChild(meta map[string]any) bool {
	return coremeta.IsDelegatedChild(meta)
}

func WithDelegatedChild(meta map[string]any, delegated bool) map[string]any {
	return coremeta.WithDelegatedChild(meta, delegated)
}
