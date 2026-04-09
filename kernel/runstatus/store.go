package runstatus

import (
	"maps"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func NewStore(store session.StateStore) (*session.MapSessionStateStore[State], error) {
	return session.NewMapSessionStateStore[State](store, stateCodec{})
}

type stateCodec struct{}

func (stateCodec) LoadState(values map[string]any) (State, error) {
	if state, ok := StateFromSnapshot(values); ok {
		return state, nil
	}
	return State{}, nil
}

func (stateCodec) StoreState(values map[string]any, state State) (map[string]any, error) {
	if values == nil {
		values = map[string]any{}
	} else {
		values = maps.Clone(values)
	}
	if !state.HasLifecycle {
		delete(values, StateKey)
		return values, nil
	}
	snapshot := StateSnapshot(state)
	values[StateKey] = snapshot[StateKey]
	return values, nil
}
