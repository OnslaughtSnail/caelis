package session

import "context"

type stateContextKey struct{}

type StateContext struct {
	Session *Session
	Store   Store
}

func WithStateContext(ctx context.Context, sess *Session, store Store) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stateContextKey{}, StateContext{
		Session: sess,
		Store:   store,
	})
}

func StateContextFromContext(ctx context.Context) (StateContext, bool) {
	if ctx == nil {
		return StateContext{}, false
	}
	value, ok := ctx.Value(stateContextKey{}).(StateContext)
	if !ok || value.Session == nil || value.Store == nil {
		return StateContext{}, false
	}
	return value, true
}
