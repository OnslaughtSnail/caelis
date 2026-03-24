package llmagent

import (
	"context"
	"iter"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type testLLM struct {
	name    string
	handler func(*model.Request) (*model.Response, error)
}

type seqResult struct {
	resp *model.Response
	err  error
}

type seqLLM struct {
	name    string
	handler func(*model.Request) []seqResult
}

func newTestLLM(name string, handler func(*model.Request) (*model.Response, error)) model.LLM {
	if name == "" {
		name = "test-model"
	}
	return &testLLM{name: name, handler: handler}
}

func (l *testLLM) Name() string {
	return l.name
}

func (l *testLLM) ContextWindowTokens() int {
	return 64000
}

func (l *testLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	_ = ctx
	return func(yield func(*model.StreamEvent, error) bool) {
		if l == nil {
			yield(nil, nil)
			return
		}
		resp, err := l.handle(req)
		if err != nil {
			yield(nil, err)
			return
		}
		if resp == nil {
			resp = &model.Response{Message: model.NewTextMessage(model.RoleAssistant, "")}
		}
		if resp.Model == "" {
			resp.Model = l.name
		}
		if resp.Provider == "" {
			resp.Provider = "test-provider"
		}
		resp.TurnComplete = true
		yield(model.StreamEventFromResponse(resp), nil)
	}
}

func (l *testLLM) handle(req *model.Request) (*model.Response, error) {
	if l.handler == nil {
		return &model.Response{
			Message: model.NewTextMessage(model.RoleAssistant, "ok"),
		}, nil
	}
	return l.handler(req)
}

func newSeqLLM(name string, handler func(*model.Request) []seqResult) model.LLM {
	if name == "" {
		name = "test-model"
	}
	return &seqLLM{name: name, handler: handler}
}

func (l *seqLLM) Name() string {
	return l.name
}

func (l *seqLLM) ContextWindowTokens() int {
	return 64000
}

func (l *seqLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	_ = ctx
	return func(yield func(*model.StreamEvent, error) bool) {
		if l == nil || l.handler == nil {
			return
		}
		for _, step := range l.handler(req) {
			if step.err != nil {
				if !yield(nil, step.err) {
					return
				}
				continue
			}
			if step.resp != nil && !step.resp.TurnComplete {
				// Emit intermediate results as PartDelta events so
				// collectLast recognises them as partial output.
				text := step.resp.Message.TextContent()
				if text != "" {
					evt := &model.StreamEvent{
						Type: model.StreamEventPartDelta,
						PartDelta: &model.PartDelta{
							Kind:      model.PartKindText,
							TextDelta: text,
						},
					}
					if !yield(evt, nil) {
						return
					}
					continue
				}
			}
			if !yield(model.StreamEventFromResponse(step.resp), step.err) {
				return
			}
		}
	}
}
