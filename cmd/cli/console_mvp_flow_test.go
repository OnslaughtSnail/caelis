package main

import (
	"context"
	"encoding/json"
	"iter"
	"testing"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/app/acpadapter"
	"github.com/OnslaughtSnail/caelis/internal/app/acpext"
	appbootstrap "github.com/OnslaughtSnail/caelis/internal/app/bootstrap"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func newConsoleFlowAdapterFactory(rt *runtime.Runtime, store session.Store, execRT toolexec.Runtime, ag agent.Agent, llm model.LLM) acpext.AdapterFactory {
	return func(conn *internalacp.Conn) (internalacp.Adapter, error) {
		return acpadapter.New(acpadapter.Config{
			Runtime:           rt,
			Store:             store,
			Model:             llm,
			AppName:           "app",
			UserID:            "u",
			WorkspaceRoot:     "/workspace",
			BuildSystemPrompt: func(string) (string, error) { return "console flow self acp prompt", nil },
			NewAgent: func(bool, string, string, internalacp.AgentSessionConfig) (agent.Agent, error) {
				return ag, nil
			},
			NewSessionResources: func(_ context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				return &internalacp.SessionResources{
					Runtime: internalacp.NewRuntime(execRT, conn, sessionID, "/workspace", sessionCWD, caps, modeResolver),
				}, nil
			},
			EnablePlan:      true,
			EnableSelfSpawn: true,
		})
	}
}

func TestConsoleGatewaySpawnAttachBackContinueFlow(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &consoleFlowLLM{}

	serviceSet, err := appbootstrap.Build(appbootstrap.Config{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnablePlan:      true,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: acpext.NewACPSubagentRunnerFactory(acpext.Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    newConsoleFlowAdapterFactory(rt, store, execRT, ag, llm),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	channel := appgateway.ChannelRef{
		ID:           "wk\x00app\x00u\x00cli",
		AppName:      "app",
		UserID:       "u",
		WorkspaceKey: "wk",
		WorkspaceCWD: "/workspace",
	}

	first, err := serviceSet.Gateway.RunTurn(context.Background(), appgateway.RunTurnRequest{
		Channel: channel,
		Input:   "delegate please",
		Agent:   ag,
		Model:   llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstTexts, firstErrs := drainGatewayTurn(first.Handle.Events())
	if err := first.Handle.Close(); err != nil {
		t.Fatal(err)
	}
	if len(firstErrs) > 0 {
		t.Fatalf("unexpected first turn errors: %v", firstErrs)
	}
	parentID := first.Session.SessionID
	if parentID == "" {
		t.Fatal("expected parent session id")
	}
	if !containsText(firstTexts, "delegated complete") {
		t.Fatalf("expected delegated completion text, got %v", firstTexts)
	}

	delegations, err := serviceSet.SessionService.ListDelegations(context.Background(), first.Session.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected one delegation, got %d", len(delegations))
	}

	console := &cliConsole{
		baseCtx:        context.Background(),
		appName:        "app",
		userID:         "u",
		sessionID:      parentID,
		workspace:      workspaceContext{Key: "wk", CWD: "/workspace"},
		sessionStore:   store,
		sessionService: serviceSet.SessionService,
		gateway:        serviceSet.Gateway,
	}

	if _, err := handleAttach(console, []string{delegations[0].ChildSessionID}); err != nil {
		t.Fatal(err)
	}
	childID := console.sessionID
	if childID == "" || childID == parentID {
		t.Fatalf("expected attached child session, got %q", childID)
	}

	second, err := serviceSet.Gateway.RunTurn(context.Background(), appgateway.RunTurnRequest{
		Channel: channel,
		Input:   "continue child",
		Agent:   ag,
		Model:   llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondTexts, secondErrs := drainGatewayTurn(second.Handle.Events())
	if err := second.Handle.Close(); err != nil {
		t.Fatal(err)
	}
	if len(secondErrs) > 0 {
		t.Fatalf("unexpected child turn errors: %v", secondErrs)
	}
	if second.Session.SessionID != childID {
		t.Fatalf("expected child-bound run, got %q", second.Session.SessionID)
	}
	if !containsText(secondTexts, "child continued") {
		t.Fatalf("expected child continuation text, got %v", secondTexts)
	}

	if _, err := handleBack(console, nil); err != nil {
		t.Fatal(err)
	}
	if console.sessionID != parentID {
		t.Fatalf("expected parent session restored, got %q", console.sessionID)
	}

	third, err := serviceSet.Gateway.RunTurn(context.Background(), appgateway.RunTurnRequest{
		Channel: channel,
		Input:   "resume parent",
		Agent:   ag,
		Model:   llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	thirdTexts, thirdErrs := drainGatewayTurn(third.Handle.Events())
	if err := third.Handle.Close(); err != nil {
		t.Fatal(err)
	}
	if len(thirdErrs) > 0 {
		t.Fatalf("unexpected parent resume errors: %v", thirdErrs)
	}
	if third.Session.SessionID != parentID {
		t.Fatalf("expected parent-bound run, got %q", third.Session.SessionID)
	}
	if !containsText(thirdTexts, "parent resumed") {
		t.Fatalf("expected parent continuation text, got %v", thirdTexts)
	}
}

type consoleFlowLLM struct{}

func (l *consoleFlowLLM) Name() string { return "console-flow" }

func (l *consoleFlowLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate please":
				args, _ := json.Marshal(map[string]any{"task": "child task", "yield_seconds": 0})
				yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-spawn-1",
							Name: tool.SpawnToolName,
							Args: string(args),
						}},
					},
					TurnComplete: true,
				}, nil)
				return
			case "child task":
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "child done"},
					TurnComplete: true,
				}, nil)
				return
			case "continue child":
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "child continued"},
					TurnComplete: true,
				}, nil)
				return
			case "resume parent":
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "parent resumed"},
					TurnComplete: true,
				}, nil)
				return
			}
		case model.RoleTool:
			if last.ToolResponse != nil && last.ToolResponse.Name == tool.SpawnToolName {
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "delegated complete"},
					TurnComplete: true,
				}, nil)
				return
			}
		}
		yield(&model.Response{
			Message:      model.Message{Role: model.RoleAssistant, Text: "fallback"},
			TurnComplete: true,
		}, nil)
	}
}

func drainGatewayTurn(seq iter.Seq2[*session.Event, error]) ([]string, []error) {
	texts := make([]string, 0)
	errs := make([]error, 0)
	for ev, err := range seq {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ev == nil || ev.Message.Role != model.RoleAssistant {
			continue
		}
		if text := ev.Message.TextContent(); text != "" {
			texts = append(texts, text)
		}
	}
	return texts, errs
}

func containsText(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
