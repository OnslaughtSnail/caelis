package session

import "github.com/OnslaughtSnail/caelis/kernel/model"

// CloneEvent deep-copies one event so runtime mutations do not leak across
// persistence, replay, or live stream views.
func CloneEvent(ev *Event) *Event {
	if ev == nil {
		return nil
	}
	cp := *ev
	cp.Message = CloneMessage(ev.Message)
	cp.Meta = cloneMap(ev.Meta)
	return &cp
}

func CloneMessage(msg model.Message) model.Message {
	return model.CloneMessage(msg)
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneSlice(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		return cloneSlice(typed)
	default:
		return typed
	}
}
