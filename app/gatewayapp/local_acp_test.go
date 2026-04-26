package gatewayapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	headlessadapter "github.com/OnslaughtSnail/caelis/gateway/adapter/headless"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
	spawntool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/spawn"
	tasktool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/task"
)

func TestLocalStackGatewayACPMainE2E(t *testing.T) {
	repo := repoRootForGatewayAppTest(t)
	root := t.TempDir()
	workdir := t.TempDir()

	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "user-1",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly: sdkplugin.ResolvedAssembly{
			Agents: []sdkplugin.AgentConfig{{
				Name:        "codex",
				Description: "ACP main controller.",
				Command:     "go",
				Args:        []string{"run", "./acpbridge/cmd/e2eagent"},
				WorkDir:     repo,
				Env: map[string]string{
					"SDK_ACP_STUB_REPLY":   "gateway acp main ok",
					"SDK_ACP_SESSION_ROOT": filepath.Join(root, "controller-sessions"),
					"SDK_ACP_TASK_ROOT":    filepath.Join(root, "controller-tasks"),
				},
			}},
		},
		Model: ModelConfig{
			Provider: "minimax",
			Model:    "MiniMax-M2",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}

	session, err := stack.StartSession(context.Background(), "gateway-acp-main", "surface-acp-main")
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}

	updated, err := stack.Gateway.HandoffController(context.Background(), appgateway.HandoffControllerRequest{
		SessionRef: session.SessionRef,
		Kind:       sdksession.ControllerKindACP,
		Agent:      "codex",
		Source:     "test",
		Reason:     "delegate main control",
	})
	if err != nil {
		t.Fatalf("HandoffController() error = %v", err)
	}
	if updated.Controller.Kind != sdksession.ControllerKindACP {
		t.Fatalf("controller kind = %q, want %q", updated.Controller.Kind, sdksession.ControllerKindACP)
	}

	state, err := stack.Gateway.ControlPlaneState(context.Background(), appgateway.ControlPlaneStateRequest{
		SessionRef: session.SessionRef,
	})
	if err != nil {
		t.Fatalf("ControlPlaneState() error = %v", err)
	}
	if state.Controller.Kind != sdksession.ControllerKindACP || strings.TrimSpace(state.Controller.EpochID) == "" {
		t.Fatalf("control state = %+v", state)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	result, err := headlessadapter.RunOnce(ctx, stack.Gateway, appgateway.BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "run through acp controller",
		Surface:    "headless-acp-main-e2e",
	}, headlessadapter.Options{})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if got := strings.TrimSpace(result.Output); got != "gateway acp main ok" {
		t.Fatalf("RunOnce() output = %q, want %q", got, "gateway acp main ok")
	}

	loaded, err := stack.Sessions.LoadSession(ctx, sdksession.LoadSessionRequest{
		SessionRef: session.SessionRef,
	})
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	var sawACPAssistant bool
	for _, event := range loaded.Events {
		if event == nil || sdksession.EventTypeOf(event) != sdksession.EventTypeAssistant || event.Scope == nil {
			continue
		}
		if event.Scope.Controller.Kind == sdksession.ControllerKindACP && strings.TrimSpace(event.Text) == "gateway acp main ok" {
			sawACPAssistant = true
			break
		}
	}
	if !sawACPAssistant {
		t.Fatalf("loaded events missing ACP-scoped assistant reply: %#v", loaded.Events)
	}
}

func TestLocalStackInjectsSpawnForSelfAndAttachedACPAgents(t *testing.T) {
	ctx := context.Background()
	withAgents, session := newStackWithAssemblyForToolTest(t, sdkplugin.ResolvedAssembly{
		Agents: []sdkplugin.AgentConfig{{
			Name:        "helper",
			Description: "bounded ACP helper",
			Command:     "go",
			Args:        []string{"run", "./acpbridge/cmd/e2eagent"},
			WorkDir:     repoRootForGatewayAppTest(t),
		}},
	})
	attachAgentForToolTest(t, withAgents, session.SessionRef, "helper")
	resolved, err := withAgents.Gateway.Resolver().ResolveTurn(ctx, appgateway.TurnIntent{SessionRef: session.SessionRef})
	if err != nil {
		t.Fatalf("ResolveTurn(with agents) error = %v", err)
	}
	if !toolSetHas(resolved.RunRequest.AgentSpec.Tools, spawntool.ToolName) {
		t.Fatalf("tools missing %s when assembly agents exist", spawntool.ToolName)
	}
	if !toolSetHas(resolved.RunRequest.AgentSpec.Tools, tasktool.ToolName) {
		t.Fatalf("tools missing %s", tasktool.ToolName)
	}
	systemPrompt, _ := resolved.RunRequest.AgentSpec.Metadata["system_prompt"].(string)
	if !strings.Contains(systemPrompt, "SPAWN for bounded child ACP work") {
		t.Fatalf("system prompt missing delegation guidance: %q", systemPrompt)
	}

	withoutAgents, session := newStackWithAssemblyForToolTest(t, sdkplugin.ResolvedAssembly{})
	resolved, err = withoutAgents.Gateway.Resolver().ResolveTurn(ctx, appgateway.TurnIntent{SessionRef: session.SessionRef})
	if err != nil {
		t.Fatalf("ResolveTurn(without agents) error = %v", err)
	}
	if !toolSetHas(resolved.RunRequest.AgentSpec.Tools, spawntool.ToolName) {
		t.Fatalf("tools missing %s for default self spawn", spawntool.ToolName)
	}
	systemPrompt, _ = resolved.RunRequest.AgentSpec.Metadata["system_prompt"].(string)
	if !strings.Contains(systemPrompt, "SPAWN for bounded child ACP work") {
		t.Fatalf("system prompt missing delegation guidance for default self spawn: %q", systemPrompt)
	}
	for _, want := range []string{"self", "codex", "copilot", "gemini"} {
		if !agentConfigSetHas(withoutAgents.runtime.Assembly.Agents, want) {
			t.Fatalf("built-in agent %q missing from assembly: %#v", want, withoutAgents.runtime.Assembly.Agents)
		}
	}
	attachAgentForToolTest(t, withoutAgents, session.SessionRef, "codex")
	resolved, err = withoutAgents.Gateway.Resolver().ResolveTurn(ctx, appgateway.TurnIntent{SessionRef: session.SessionRef})
	if err != nil {
		t.Fatalf("ResolveTurn(with built-in attached) error = %v", err)
	}
	if !toolSetHas(resolved.RunRequest.AgentSpec.Tools, spawntool.ToolName) {
		t.Fatalf("tools missing %s after built-in ACP agent attachment", spawntool.ToolName)
	}
}

func newStackWithAssemblyForToolTest(t *testing.T, assembly sdkplugin.ResolvedAssembly) (*Stack, sdksession.Session) {
	t.Helper()
	workdir := t.TempDir()
	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "tool-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       assembly,
		Model: ModelConfig{
			Provider: "ollama",
			Model:    "llama3",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	session, err := stack.StartSession(context.Background(), "", "surface-tool-test")
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	return stack, session
}

func toolSetHas(tools []sdktool.Tool, name string) bool {
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tool.Definition().Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func attachAgentForToolTest(t *testing.T, stack *Stack, ref sdksession.SessionRef, agent string) {
	t.Helper()
	_, err := stack.Sessions.PutParticipant(context.Background(), sdksession.PutParticipantRequest{
		SessionRef: ref,
		Binding: sdksession.ParticipantBinding{
			ID:        "sidecar-" + strings.ToLower(strings.TrimSpace(agent)),
			Kind:      sdksession.ParticipantKindACP,
			Role:      sdksession.ParticipantRoleSidecar,
			Label:     strings.TrimSpace(agent),
			SessionID: "remote-" + strings.ToLower(strings.TrimSpace(agent)),
			Source:    "test_attach",
		},
	})
	if err != nil {
		t.Fatalf("PutParticipant(%q) error = %v", agent, err)
	}
}

func agentConfigSetHas(agents []sdkplugin.AgentConfig, name string) bool {
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func repoRootForGatewayAppTest(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
