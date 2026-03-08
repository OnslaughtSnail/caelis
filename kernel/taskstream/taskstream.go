package taskstream

import (
	"context"
	"fmt"
	"strings"
)

const (
	toolResultMetadataKey = "metadata"
	taskStreamMetadataKey = "task_stream"
)

type contextKey struct{}

// Event is one UI-facing task stream update for long-running work.
type Event struct {
	Label  string
	TaskID string
	CallID string
	Stream string
	Chunk  string
	State  string
	Reset  bool
	Final  bool
}

type Streamer interface {
	StreamTask(context.Context, Event)
}

type StreamerFunc func(context.Context, Event)

func (f StreamerFunc) StreamTask(ctx context.Context, ev Event) {
	if f != nil {
		f(ctx, ev)
	}
}

func WithStreamer(ctx context.Context, streamer Streamer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if streamer == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, streamer)
}

func StreamerFromContext(ctx context.Context) (Streamer, bool) {
	if ctx == nil {
		return nil, false
	}
	streamer, ok := ctx.Value(contextKey{}).(Streamer)
	return streamer, ok
}

func Emit(ctx context.Context, ev Event) {
	streamer, ok := StreamerFromContext(ctx)
	if !ok || streamer == nil {
		return
	}
	streamer.StreamTask(ctx, normalizeEvent(ev))
}

func AppendResultEvent(result map[string]any, ev Event) map[string]any {
	ev = normalizeEvent(ev)
	result = ensureMetadata(result)
	meta, _ := result[toolResultMetadataKey].(map[string]any)
	items, _ := meta[taskStreamMetadataKey].([]any)
	meta[taskStreamMetadataKey] = append(items, map[string]any{
		"label":   ev.Label,
		"task_id": ev.TaskID,
		"call_id": ev.CallID,
		"stream":  ev.Stream,
		"chunk":   ev.Chunk,
		"state":   ev.State,
		"reset":   ev.Reset,
		"final":   ev.Final,
	})
	return result
}

func EventsFromResult(result map[string]any) []Event {
	if len(result) == 0 {
		return nil
	}
	meta, _ := result[toolResultMetadataKey].(map[string]any)
	if len(meta) == 0 {
		return nil
	}
	rawItems, _ := meta[taskStreamMetadataKey].([]any)
	if len(rawItems) == 0 {
		return nil
	}
	out := make([]Event, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		if len(item) == 0 {
			continue
		}
		out = append(out, normalizeEvent(Event{
			Label:  asString(item["label"]),
			TaskID: asString(item["task_id"]),
			CallID: asString(item["call_id"]),
			Stream: asString(item["stream"]),
			Chunk:  asString(item["chunk"]),
			State:  asString(item["state"]),
			Reset:  asBool(item["reset"]),
			Final:  asBool(item["final"]),
		}))
	}
	return out
}

func ensureMetadata(result map[string]any) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	meta, ok := result[toolResultMetadataKey].(map[string]any)
	if ok {
		return result
	}
	if result[toolResultMetadataKey] != nil {
		meta = map[string]any{
			"raw_value": fmt.Sprint(result[toolResultMetadataKey]),
		}
	} else {
		meta = map[string]any{}
	}
	result[toolResultMetadataKey] = meta
	return result
}

func normalizeEvent(ev Event) Event {
	ev.Label = strings.TrimSpace(ev.Label)
	ev.TaskID = strings.TrimSpace(ev.TaskID)
	ev.CallID = strings.TrimSpace(ev.CallID)
	ev.Stream = strings.TrimSpace(ev.Stream)
	ev.State = strings.TrimSpace(ev.State)
	return ev
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func asBool(value any) bool {
	raw, ok := value.(bool)
	return ok && raw
}
