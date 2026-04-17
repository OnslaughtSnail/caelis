package session

import "context"

type stateContextKey struct{}

type StateContext struct {
	Session      *Session
	LogStore     LogStore
	StateStore   StateStore
	StateUpdater StateUpdateStore
}

func WithStateContext(ctx context.Context, sess *Session, store Store) context.Context {
	return WithStoresContext(ctx, sess, store, store)
}

func WithStoresContext(ctx context.Context, sess *Session, logStore LogStore, stateStore StateStore) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	var updater StateUpdateStore
	if stateStore != nil {
		updater, _ = stateStore.(StateUpdateStore)
	}
	return context.WithValue(ctx, stateContextKey{}, StateContext{
		Session:      sess,
		LogStore:     logStore,
		StateStore:   stateStore,
		StateUpdater: updater,
	})
}

func StateContextFromContext(ctx context.Context) (StateContext, bool) {
	if ctx == nil {
		return StateContext{}, false
	}
	value, ok := ctx.Value(stateContextKey{}).(StateContext)
	if !ok || value.Session == nil || value.StateStore == nil {
		return StateContext{}, false
	}
	return value, true
}
