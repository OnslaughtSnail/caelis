package bootstrap

import (
	"context"
	"io"
	"iter"
	"strings"
	"testing"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestBuildProvidesGatewayAndACPAdapterAgainstSameStore(t *testing.T) {
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

	set, err := Build(Config{
		Runtime:      rt,
		Store:        store,
		AppName:      "app",
		UserID:       "u",
		WorkspaceCWD: "/workspace",
		Execution:    execRT,
		ACP: &ACPConfig{
			WorkspaceRoot: "/workspace",
			SessionModes: []internalacp.SessionMode{
				{ID: "default", Name: "Default"},
				{ID: "plan", Name: "Plan"},
			},
			DefaultModeID: "default",
			SessionConfig: []internalacp.SessionConfigOptionTemplate{
				{
					ID:           "mode",
					Name:         "Mode",
					Category:     "mode",
					DefaultValue: "default",
					Options: []internalacp.SessionConfigSelectOption{
						{Value: "default", Name: "Default"},
						{Value: "plan", Name: "Plan"},
					},
				},
			},
			BuildSystemPrompt: func(string) (string, error) { return "test", nil },
			NewModel: func(internalacp.AgentSessionConfig) (model.LLM, error) {
				return &bootstrapScriptedLLM{}, nil
			},
			NewAgent: func(stream bool, _ string, systemPrompt string, _ internalacp.AgentSessionConfig) (agent.Agent, error) {
				if strings.TrimSpace(systemPrompt) == "" {
					systemPrompt = "test"
				}
				return llmagent.New(llmagent.Config{
					Name:              "test-agent",
					SystemPrompt:      systemPrompt,
					StreamModel:       stream,
					EmitPartialEvents: stream,
				})
			},
			NewSessionResources: func(context.Context, *internalacp.Conn, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error) {
				return &internalacp.SessionResources{Runtime: execRT}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := set.Gateway.StartSession(context.Background(), appgateway.StartSessionRequest{
		Channel: appgateway.ChannelRef{
			ID:           "ch",
			AppName:      "app",
			UserID:       "u",
			WorkspaceKey: "wk",
			WorkspaceCWD: "/workspace",
		},
		PreferredSessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}

	adapter, err := set.NewACPAdapter(internalacp.NewConn(strings.NewReader(""), io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := adapter.LoadSession(context.Background(), internalacp.LoadSessionRequest{
		SessionID: "sess-1",
		CWD:       "/workspace",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.SessionID != "sess-1" {
		t.Fatalf("expected shared session id, got %q", loaded.Session.SessionID)
	}
	if loaded.Session.Modes == nil || loaded.Session.Modes.CurrentModeID != "default" {
		t.Fatalf("expected default mode from shared bootstrap config, got %+v", loaded.Session.Modes)
	}
}

type bootstrapScriptedLLM struct{}

func (l *bootstrapScriptedLLM) Name() string { return "bootstrap-scripted" }

func (l *bootstrapScriptedLLM) Generate(context.Context, *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		yield(&model.Response{
			Message:      model.Message{Role: model.RoleAssistant, Text: "ok"},
			TurnComplete: true,
		}, nil)
	}
}
