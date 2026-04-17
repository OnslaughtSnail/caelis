package delegation

import (
	"context"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func SessionMeta(ctx context.Context, store session.StateStore, sess *session.Session) map[string]any {
	if store == nil || sess == nil {
		return nil
	}
	meta, err := acpmeta.SessionMetaFromStore(ctx, store, sess)
	if err != nil {
		return nil
	}
	return meta
}

func ChildSessionMeta(ctx context.Context, store session.StateStore, parent *session.Session, child *session.Session, agentName string) map[string]any {
	if meta := SessionMeta(ctx, store, child); len(meta) > 0 {
		return meta
	}
	meta := acpmeta.CloneMeta(SessionMeta(ctx, store, parent))
	if strings.EqualFold(strings.TrimSpace(agentName), "self") {
		return acpmeta.WithDelegatedChild(meta, true)
	}
	return meta
}

func SeedChildSessionMeta(ctx context.Context, logStore session.LogStore, stateStore session.StateStore, parent *session.Session, child *session.Session, agentName string) error {
	if logStore == nil || stateStore == nil || child == nil {
		return nil
	}
	child.ID = strings.TrimSpace(child.ID)
	if child.ID == "" {
		return nil
	}
	meta := ChildSessionMeta(ctx, stateStore, parent, child, agentName)
	if len(meta) == 0 {
		return nil
	}
	if _, err := logStore.GetOrCreate(ctx, child); err != nil {
		return err
	}
	return acpmeta.UpdateSessionMeta(ctx, stateStore, child, func(map[string]any) map[string]any {
		return acpmeta.CloneMeta(meta)
	})
}
