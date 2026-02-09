package main

import (
	"context"
	"fmt"
	"iter"

	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type echoArgs struct {
	Text string `json:"text"`
}

type echoResult struct {
	Echo string `json:"echo"`
}

type scriptedLLM struct {
	name    string
	handler func(*model.Request) (*model.Response, error)
}

func (l *scriptedLLM) Name() string { return l.name }
func (l *scriptedLLM) ContextWindowTokens() int {
	return 64000
}
func (l *scriptedLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	_ = ctx
	return func(yield func(*model.Response, error) bool) {
		resp, err := l.handler(req)
		if err != nil {
			yield(nil, err)
			return
		}
		resp.Model = l.name
		resp.Provider = "demo-provider"
		resp.TurnComplete = true
		yield(resp, nil)
	}
}

func main() {
	echoTool, err := tool.NewFunction("echo", "Echo text", func(ctx context.Context, args echoArgs) (echoResult, error) {
		_ = ctx
		return echoResult{Echo: args.Text}, nil
	})
	if err != nil {
		panic(err)
	}

	llm := &scriptedLLM{name: "demo-fake", handler: func(req *model.Request) (*model.Response, error) {
		if len(req.Messages) == 0 {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "empty"}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleUser {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call_1", Name: "echo", Args: map[string]any{"text": "hello from tool"}}}}}, nil
		}
		if last.Role == model.RoleTool && last.ToolResponse != nil {
			return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: fmt.Sprintf("tool result: %v", last.ToolResponse.Result)}}, nil
		}
		return &model.Response{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil
	}}

	ag, err := llmagent.New(llmagent.Config{Name: "demo"})
	if err != nil {
		panic(err)
	}

	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		panic(err)
	}

	for ev, runErr := range rt.Run(context.Background(), runtime.RunRequest{
		AppName:   "demo-app",
		UserID:    "u1",
		SessionID: "s1",
		Input:     "hi",
		Agent:     ag,
		Model:     llm,
		Tools:     []tool.Tool{echoTool},
	}) {
		if runErr != nil {
			panic(runErr)
		}
		if ev == nil {
			continue
		}
		if ev.Message.ToolResponse != nil {
			fmt.Printf("tool -> %v\n", ev.Message.ToolResponse.Result)
			continue
		}
		if ev.Message.Text != "" {
			fmt.Printf("%s -> %s\n", ev.Message.Role, ev.Message.Text)
		}
	}
}
