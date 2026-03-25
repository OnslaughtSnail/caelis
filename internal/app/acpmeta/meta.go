package acpmeta

import (
	"context"

	coremeta "github.com/OnslaughtSnail/caelis/internal/acpmeta"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func SessionMetaFromState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	acpState, _ := state["acp"].(map[string]any)
	if len(acpState) == 0 {
		return nil
	}
	meta, _ := acpState["meta"].(map[string]any)
	return coremeta.CloneMeta(meta)
}

func SessionMetaFromContext(ctx context.Context) map[string]any {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || stateCtx.Session == nil || stateCtx.Store == nil {
		return nil
	}
	values, err := stateCtx.Store.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return nil
	}
	return SessionMetaFromState(values)
}

func SelfSpawnDepthFromContext(ctx context.Context) int {
	return coremeta.SelfSpawnDepthFromMeta(SessionMetaFromContext(ctx))
}
