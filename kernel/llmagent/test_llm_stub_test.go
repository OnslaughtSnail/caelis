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

func (l *testLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	_ = ctx
	return func(yield func(*model.Response, error) bool) {
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
			resp = &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: ""}}
		}
		if resp.Model == "" {
			resp.Model = l.name
		}
		if resp.Provider == "" {
			resp.Provider = "test-provider"
		}
		resp.TurnComplete = true
		yield(resp, nil)
	}
}

func (l *testLLM) handle(req *model.Request) (*model.Response, error) {
	if l.handler == nil {
		return &model.Response{
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "ok",
			},
		}, nil
	}
	return l.handler(req)
}
