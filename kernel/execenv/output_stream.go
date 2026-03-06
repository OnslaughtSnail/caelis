package execenv

import "context"

type outputStreamerContextKey struct{}
type toolCallInfoContextKey struct{}

type OutputChunk struct {
	ToolName   string
	ToolCallID string
	Stream     string
	Text       string
}

type OutputStreamer interface {
	StreamOutput(context.Context, OutputChunk)
}

type OutputStreamerFunc func(context.Context, OutputChunk)

func (f OutputStreamerFunc) StreamOutput(ctx context.Context, chunk OutputChunk) {
	if f != nil {
		f(ctx, chunk)
	}
}

type ToolCallInfo struct {
	Name string
	ID   string
}

func WithOutputStreamer(ctx context.Context, streamer OutputStreamer) context.Context {
	if ctx == nil || streamer == nil {
		return ctx
	}
	return context.WithValue(ctx, outputStreamerContextKey{}, streamer)
}

func OutputStreamerFromContext(ctx context.Context) (OutputStreamer, bool) {
	if ctx == nil {
		return nil, false
	}
	streamer, ok := ctx.Value(outputStreamerContextKey{}).(OutputStreamer)
	return streamer, ok
}

func WithToolCallInfo(ctx context.Context, name, id string) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, toolCallInfoContextKey{}, ToolCallInfo{
		Name: name,
		ID:   id,
	})
}

func ToolCallInfoFromContext(ctx context.Context) (ToolCallInfo, bool) {
	if ctx == nil {
		return ToolCallInfo{}, false
	}
	info, ok := ctx.Value(toolCallInfoContextKey{}).(ToolCallInfo)
	return info, ok
}

func EmitOutputChunk(ctx context.Context, chunk CommandOutputChunk) {
	streamer, ok := OutputStreamerFromContext(ctx)
	if !ok || streamer == nil {
		return
	}
	info, _ := ToolCallInfoFromContext(ctx)
	streamer.StreamOutput(ctx, OutputChunk{
		ToolName:   info.Name,
		ToolCallID: info.ID,
		Stream:     chunk.Stream,
		Text:       chunk.Text,
	})
}
