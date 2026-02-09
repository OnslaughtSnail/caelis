package runtime

import (
	"context"
	"iter"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type runtimeTestLLM struct {
	name string
}

func newRuntimeTestLLM(name string) model.LLM {
	if name == "" {
		name = "test-model"
	}
	return &runtimeTestLLM{name: name}
}

func (l *runtimeTestLLM) Name() string {
	return l.name
}

func (l *runtimeTestLLM) ContextWindowTokens() int {
	return 64000
}

func (l *runtimeTestLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	_ = ctx
	_ = req
	return func(yield func(*model.Response, error) bool) {
		yield(&model.Response{
			Message:      model.Message{Role: model.RoleAssistant, Text: "ok"},
			Model:        l.name,
			Provider:     "test-provider",
			TurnComplete: true,
		}, nil)
	}
}
