package acpmeta

import (
	"context"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	coremeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func SessionMetaFromState(state map[string]any) map[string]any {
	return coremeta.SessionMetaFromState(state)
}

func SessionMetaFromContext(ctx context.Context) map[string]any {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || stateCtx.Session == nil || stateCtx.StateStore == nil {
		return nil
	}
	values, err := stateCtx.StateStore.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return nil
	}
	return coremeta.SessionMetaFromState(values)
}
