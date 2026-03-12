package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"iter"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	sessionfile "github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	sessionmem "github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

func TestServer_InitializeNewPromptAndLoad(t *testing.T) {
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{Message: model.Message{Role: model.RoleAssistant, Text: "hello from acp"}}},
			},
		},
	})
	defer h.close()

	var initResp InitializeResponse
	if err := h.client.Call(context.Background(), MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
		ClientCapabilities: ClientCapabilities{
			FS: FileSystemCapabilities{},
		},
	}, &initResp); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initResp.ProtocolVersion != CurrentProtocolVersion {
		t.Fatalf("expected protocolVersion %d, got %d", CurrentProtocolVersion, initResp.ProtocolVersion)
	}
	if !initResp.AgentCapabilities.LoadSession {
		t.Fatal("expected loadSession capability")
	}

	var newResp NewSessionResponse
	if err := h.client.Call(context.Background(), MethodSessionNew, NewSessionRequest{
		CWD:        h.workspace,
		MCPServers: nil,
	}, &newResp); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if strings.TrimSpace(newResp.SessionID) == "" {
		t.Fatal("expected session id")
	}

	h.resetNotifications()
	var promptResp PromptResponse
	if err := h.client.Call(context.Background(), MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "hi"})},
	}, &promptResp); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	h.waitNotifications(t, 1)
	got := h.notificationTypes()
	if !containsAll(got, UpdateAgentMessage) {
		t.Fatalf("expected agent update, got %v", got)
	}
	if containsAll(got, UpdateUserMessage) {
		t.Fatalf("did not expect live prompt to echo user update, got %v", got)
	}

	h.resetNotifications()
	if err := h.client.Call(context.Background(), MethodSessionLoad, LoadSessionRequest{
		SessionID:  newResp.SessionID,
		CWD:        h.workspace,
		MCPServers: nil,
	}, &LoadSessionResponse{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	h.waitNotifications(t, 2)
	if got := h.notificationTypes(); !containsAll(got, UpdateUserMessage, UpdateAgentMessage) {
		t.Fatalf("expected replayed user+agent updates, got %v", got)
	}
}

func TestServer_InitializeRejectsLegacyProtocolVersion(t *testing.T) {
	h := newHarness(t, harnessConfig{})
	defer h.close()

	var initResp InitializeResponse
	err := h.client.Call(context.Background(), MethodInitialize, map[string]any{
		"protocolVersion": 0.2,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{},
		},
	}, &initResp)
	if err == nil {
		t.Fatal("expected legacy protocolVersion to be rejected")
	}
}

func TestServer_InitializeAdvertisesImagePromptCapability(t *testing.T) {
	h := newHarness(t, harnessConfig{
		promptImageEnabled: func() bool { return true },
	})
	defer h.close()

	var initResp InitializeResponse
	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &initResp)
	if !initResp.AgentCapabilities.Prompt.Image {
		t.Fatal("expected image prompt capability")
	}
}

func TestServer_PromptWithImageBlockForwardsImageContentParts(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.Message{Role: model.RoleAssistant, Text: "seen"}}},
		},
	}
	h := newHarness(t, harnessConfig{
		llm:                llm,
		promptImageEnabled: func() bool { return true },
		supportsPromptImage: func(AgentSessionConfig) bool {
			return true
		},
	})
	defer h.close()

	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, makeACPTestPNG(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []json.RawMessage{
			mustRaw(t, ResourceLink{Type: "resource_link", Name: "clipboard.png", URI: "file://" + imagePath, MimeType: "image/png"}),
			mustRaw(t, TextContent{Type: "text", Text: "guess the app"}),
		},
	}, &PromptResponse{})

	if len(llm.reqs) == 0 {
		t.Fatal("expected model request")
	}
	found := false
	for _, msg := range llm.reqs[0].Messages {
		if msg.Role != model.RoleUser {
			continue
		}
		if len(msg.ContentParts) != 2 {
			continue
		}
		if msg.ContentParts[0].Type != model.ContentPartImage || strings.TrimSpace(msg.ContentParts[0].Data) == "" {
			continue
		}
		if msg.ContentParts[1].Type != model.ContentPartText || !strings.Contains(msg.ContentParts[1].Text, "guess the app") {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("expected user message with text+image content parts, got %+v", llm.reqs[0].Messages)
	}
}

func TestServer_PromptSilentlyDropsImagesWhenModelDoesNotSupportIt(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.Message{Role: model.RoleAssistant, Text: "text only"}}},
		},
	}
	h := newHarness(t, harnessConfig{
		llm:                llm,
		promptImageEnabled: func() bool { return true },
		supportsPromptImage: func(AgentSessionConfig) bool {
			return false
		},
	})
	defer h.close()

	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, makeACPTestPNG(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []json.RawMessage{
			mustRaw(t, ResourceLink{Type: "resource_link", Name: "clipboard.png", URI: "file://" + imagePath, MimeType: "image/png"}),
			mustRaw(t, TextContent{Type: "text", Text: "only keep this"}),
		},
	}, &PromptResponse{})

	if len(llm.reqs) == 0 {
		t.Fatal("expected model request")
	}
	found := false
	for _, msg := range llm.reqs[0].Messages {
		if msg.Role != model.RoleUser {
			continue
		}
		if len(msg.ContentParts) != 1 || msg.ContentParts[0].Type != model.ContentPartText {
			t.Fatalf("expected text-only content parts for unsupported model, got %+v", msg.ContentParts)
		}
		if !strings.Contains(msg.TextContent(), "only keep this") {
			t.Fatalf("expected prompt text to survive image filtering, got %+v", msg)
		}
		found = true
	}
	if !found {
		t.Fatalf("expected user message after filtering, got %+v", llm.reqs[0].Messages)
	}
}

func TestServer_PromptPreservesInterleavedTextImageOrder(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.Message{Role: model.RoleAssistant, Text: "ordered"}}},
		},
	}
	h := newHarness(t, harnessConfig{
		llm:                llm,
		promptImageEnabled: func() bool { return true },
		supportsPromptImage: func(AgentSessionConfig) bool {
			return true
		},
	})
	defer h.close()

	imagePath := filepath.Join(t.TempDir(), "ordered.png")
	if err := os.WriteFile(imagePath, makeACPTestPNG(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []json.RawMessage{
			mustRaw(t, TextContent{Type: "text", Text: "before"}),
			mustRaw(t, ResourceLink{Type: "resource_link", Name: "ordered.png", URI: "file://" + imagePath, MimeType: "image/png"}),
			mustRaw(t, TextContent{Type: "text", Text: "after"}),
		},
	}, &PromptResponse{})

	if len(llm.reqs) == 0 {
		t.Fatal("expected model request")
	}
	for _, msg := range llm.reqs[0].Messages {
		if msg.Role != model.RoleUser {
			continue
		}
		if len(msg.ContentParts) != 3 {
			t.Fatalf("expected 3 ordered content parts, got %+v", msg.ContentParts)
		}
		if msg.ContentParts[0].Type != model.ContentPartText || msg.ContentParts[0].Text != "before" {
			t.Fatalf("expected first text part preserved, got %+v", msg.ContentParts[0])
		}
		if msg.ContentParts[1].Type != model.ContentPartImage {
			t.Fatalf("expected image to remain in the middle, got %+v", msg.ContentParts[1])
		}
		if msg.ContentParts[2].Type != model.ContentPartText || msg.ContentParts[2].Text != "after" {
			t.Fatalf("expected trailing text part preserved, got %+v", msg.ContentParts[2])
		}
		return
	}
	t.Fatalf("expected user message with ordered content parts, got %+v", llm.reqs[0].Messages)
}

func TestServer_InitializeSerializesSchemaFields(t *testing.T) {
	h := newHarness(t, harnessConfig{})
	defer h.close()

	var initResp map[string]any
	err := h.client.Call(context.Background(), MethodInitialize, map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{},
			"terminal": true,
		},
	}, &initResp)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got, ok := initResp["protocolVersion"].(float64); !ok || got != float64(CurrentProtocolVersion) {
		t.Fatalf("expected numeric protocolVersion %d, got %#v", CurrentProtocolVersion, initResp["protocolVersion"])
	}
	rawCaps, ok := initResp["agentCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected agentCapabilities object, got %#v", initResp["agentCapabilities"])
	}
	value, exists := rawCaps["mcpCapabilities"]
	if !exists {
		t.Fatalf("expected %q in agentCapabilities, got %#v", "mcpCapabilities", rawCaps)
	}
	item, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected mcpCapabilities object, got %#v", value)
	}
	if item["http"] != true || item["sse"] != true {
		t.Fatalf("expected mcpCapabilities to advertise http/sse, got %#v", item)
	}
	if _, exists := rawCaps["mcp"]; exists {
		t.Fatalf("did not expect legacy mcp field, got %#v", rawCaps)
	}
}

func TestServer_InitializeAdvertisesSessionListAndListsSessions(t *testing.T) {
	h := newHarness(t, harnessConfig{
		listSessions: func(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
			_ = ctx
			if req.Cursor != "" {
				t.Fatalf("unexpected cursor %q", req.Cursor)
			}
			return SessionListResponse{
				Sessions: []SessionSummary{{
					SessionID: "s-1",
					CWD:       "/workspace",
					Title:     "recent session",
					UpdatedAt: "2026-03-12T04:15:05Z",
				}},
			}, nil
		},
	})
	defer h.close()

	var initResp InitializeResponse
	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &initResp)
	if initResp.AgentCapabilities.Session.List == nil {
		t.Fatalf("expected session list capability, got %+v", initResp.AgentCapabilities.Session)
	}

	var listResp SessionListResponse
	mustCall(t, h.client, MethodSessionList, SessionListRequest{}, &listResp)
	if len(listResp.Sessions) != 1 || listResp.Sessions[0].SessionID != "s-1" {
		t.Fatalf("unexpected session list response %+v", listResp)
	}
}

func TestServer_NewSessionAndPromptUseDynamicModelState(t *testing.T) {
	var captured AgentSessionConfig
	h := newHarness(t, harnessConfig{
		newModel: func(cfg AgentSessionConfig) (model.LLM, error) {
			captured = cfg
			return &scriptedLLM{
				calls: [][]*model.Response{
					{{Message: model.Message{Role: model.RoleAssistant, Text: "hello from selected model"}}},
				},
			}, nil
		},
		sessionConfig: []SessionConfigOptionTemplate{
			{
				ID:           "model",
				Name:         "Model",
				Category:     "model",
				DefaultValue: "model-a",
				Options: []SessionConfigSelectOption{
					{Value: "model-a", Name: "model-a"},
					{Value: "model-b", Name: "model-b"},
				},
			},
		},
		sessionModels: func(cfg AgentSessionConfig) *SessionModelState {
			return &SessionModelState{
				CurrentModelID: cfg.ConfigValues["model"],
				AvailableModels: []SessionModel{
					{ModelID: "model-a", Name: "model-a"},
					{ModelID: "model-b", Name: "model-b"},
				},
			}
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	if newResp.Models == nil || newResp.Models.CurrentModelID != "model-a" {
		t.Fatalf("unexpected initial model state %+v", newResp.Models)
	}

	mustCall(t, h.client, MethodSessionSetConfig, SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  "model",
		Value:     "model-b",
	}, &SetSessionConfigOptionResponse{})

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "use selected model"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	if captured.ConfigValues["model"] != "model-b" {
		t.Fatalf("expected selected model to reach dynamic model factory, got %+v", captured)
	}

	var loadResp LoadSessionResponse
	mustCall(t, h.client, MethodSessionLoad, LoadSessionRequest{
		SessionID: newResp.SessionID,
		CWD:       h.workspace,
	}, &loadResp)
	if loadResp.Models == nil || loadResp.Models.CurrentModelID != "model-b" {
		t.Fatalf("expected persisted model state, got %+v", loadResp.Models)
	}
}

func TestServer_PromptForwardsDelegatedChildSessionUpdates(t *testing.T) {
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call_delegate_1",
							Name: tool.DelegateTaskToolName,
							Args: `{"task":"child task","yield_time_ms":0}`,
						}},
					},
				}},
				{{Message: model.Message{Role: model.RoleAssistant, Text: "child done"}}},
				{{Message: model.Message{Role: model.RoleAssistant, Text: "delegated complete"}}},
			},
		},
	})
	defer h.close()

	var initResp InitializeResponse
	if err := h.client.Call(context.Background(), MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
		ClientCapabilities: ClientCapabilities{
			FS: FileSystemCapabilities{},
		},
	}, &initResp); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var newResp NewSessionResponse
	if err := h.client.Call(context.Background(), MethodSessionNew, NewSessionRequest{
		CWD: h.workspace,
	}, &newResp); err != nil {
		t.Fatalf("session/new: %v", err)
	}

	h.resetNotifications()
	var promptResp PromptResponse
	if err := h.client.Call(context.Background(), MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "delegate please"})},
	}, &promptResp); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}

	h.waitNotifications(t, 5)
	h.mu.Lock()
	defer h.mu.Unlock()
	var childSessionIDs []string
	for _, note := range h.notifications {
		if strings.TrimSpace(note.SessionID) == "" || note.SessionID == newResp.SessionID {
			continue
		}
		childSessionIDs = append(childSessionIDs, note.SessionID)
	}
	if len(childSessionIDs) == 0 {
		t.Fatalf("expected delegated child session notifications, got %+v", h.notifications)
	}
}

func TestServer_AuthenticateRequiredBeforeSessionMethods(t *testing.T) {
	h := newHarness(t, harnessConfig{
		authMethods: []AuthMethod{{
			ID:          "local_test",
			Name:        "Local test",
			Description: "test auth",
		}},
		authenticate: func(ctx context.Context, req AuthenticateRequest) error {
			_ = ctx
			if req.MethodID != "local_test" {
				return fmt.Errorf("unexpected method %q", req.MethodID)
			}
			return nil
		},
	})
	defer h.close()

	var initResp InitializeResponse
	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &initResp)
	if len(initResp.AuthMethods) != 1 || initResp.AuthMethods[0].ID != "local_test" {
		t.Fatalf("unexpected auth methods %+v", initResp.AuthMethods)
	}

	var newResp NewSessionResponse
	err := h.client.Call(context.Background(), MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("expected authentication required error, got %v", err)
	}

	mustCall(t, h.client, MethodAuthenticate, AuthenticateRequest{MethodID: "local_test"}, &AuthenticateResponse{})
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	if strings.TrimSpace(newResp.SessionID) == "" {
		t.Fatal("expected authenticated session id")
	}
}

func TestServer_SessionModeAndConfigPersistAcrossLoad(t *testing.T) {
	var captured AgentSessionConfig
	h := newHarness(t, harnessConfig{
		sessionModes: []SessionMode{
			{ID: "default", Name: "Default"},
			{ID: "plan", Name: "Plan"},
		},
		defaultModeID: "default",
		sessionConfig: []SessionConfigOptionTemplate{{
			ID:           "thinking_mode",
			Name:         "Thinking Mode",
			Category:     "thought_level",
			DefaultValue: "auto",
			Options: []SessionConfigSelectOption{
				{Value: "auto", Name: "auto"},
				{Value: "off", Name: "off"},
				{Value: "on", Name: "on"},
			},
		}},
		newAgent: func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error) {
			_ = sessionCWD
			captured = cfg
			return newLLMAgentFactory(t)(stream, sessionCWD, cfg)
		},
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{Message: model.Message{Role: model.RoleAssistant, Text: "configured"}}},
			},
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	if newResp.Modes == nil || newResp.Modes.CurrentModeID != "default" {
		t.Fatalf("unexpected initial modes %+v", newResp.Modes)
	}
	if len(newResp.ConfigOptions) != 1 || newResp.ConfigOptions[0].CurrentValue != "auto" {
		t.Fatalf("unexpected initial config %+v", newResp.ConfigOptions)
	}

	h.resetNotifications()
	mustCall(t, h.client, MethodSessionSetMode, SetSessionModeRequest{
		SessionID: newResp.SessionID,
		ModeID:    "plan",
	}, &SetSessionModeResponse{})
	h.waitNotifications(t, 1)
	if got := h.notificationTypes(); !containsAll(got, UpdateCurrentMode) {
		t.Fatalf("expected current mode update, got %v", got)
	}

	h.resetNotifications()
	var cfgResp SetSessionConfigOptionResponse
	mustCall(t, h.client, MethodSessionSetConfig, SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  "thinking_mode",
		Value:     "off",
	}, &cfgResp)
	if len(cfgResp.ConfigOptions) != 1 || cfgResp.ConfigOptions[0].CurrentValue != "off" {
		t.Fatalf("unexpected config response %+v", cfgResp.ConfigOptions)
	}
	h.waitNotifications(t, 1)
	if got := h.notificationTypes(); !containsAll(got, UpdateConfigOption) {
		t.Fatalf("expected config option update, got %v", got)
	}

	h.resetNotifications()
	var loadResp LoadSessionResponse
	mustCall(t, h.client, MethodSessionLoad, LoadSessionRequest{
		SessionID: newResp.SessionID,
		CWD:       h.workspace,
	}, &loadResp)
	if loadResp.Modes == nil || loadResp.Modes.CurrentModeID != "plan" {
		t.Fatalf("expected persisted mode, got %+v", loadResp.Modes)
	}
	if len(loadResp.ConfigOptions) != 1 || loadResp.ConfigOptions[0].CurrentValue != "off" {
		t.Fatalf("expected persisted config, got %+v", loadResp.ConfigOptions)
	}

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "use config"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	if captured.ModeID != "plan" || captured.ConfigValues["thinking_mode"] != "off" {
		t.Fatalf("unexpected agent config %+v", captured)
	}
}

func TestServer_Prompt_UsesACPFileSystemRead(t *testing.T) {
	h := newHarness(t, harnessConfig{
		clientCaps: ClientCapabilities{
			FS: FileSystemCapabilities{ReadTextFile: true},
		},
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-read",
							Name: "READ",
							Args: `{"path":"/workspace/app.txt"}`,
						}},
					},
				}},
				{{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}},
			},
		},
	})
	defer h.close()
	h.files["/workspace/app.txt"] = "remote file"

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion:    CurrentProtocolVersion,
		ClientCapabilities: h.clientCaps,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	h.resetNotifications()
	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "read it"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	if h.readRequests != 1 {
		t.Fatalf("expected ACP fs read, got %d requests", h.readRequests)
	}
	h.waitNotifications(t, 3)
	if !containsAll(h.notificationTypes(), UpdateToolCall, UpdateToolCallState, UpdateAgentMessage) {
		t.Fatalf("expected tool updates and final message, got %v", h.notificationTypes())
	}
}

func TestServer_Prompt_UsesACPTerminalForBash(t *testing.T) {
	h := newHarness(t, harnessConfig{
		clientCaps: ClientCapabilities{
			Terminal: true,
		},
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-bash",
							Name: "BASH",
							Args: `{"command":"echo hi"}`,
						}},
					},
				}},
				{{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}},
			},
		},
	})
	defer h.close()
	h.termOutput = "hi\n"

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion:    CurrentProtocolVersion,
		ClientCapabilities: h.clientCaps,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "run bash"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	if h.terminalCreates != 1 {
		t.Fatalf("expected terminal/create call, got %d", h.terminalCreates)
	}
}

func TestServer_Prompt_FiltersInternalToolMetadataFromACPUpdates(t *testing.T) {
	h := newHarness(t, harnessConfig{
		newAgent: func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error) {
			_ = stream
			_ = sessionCWD
			_ = cfg
			return scriptedAgent{
				name: "metadata-filter",
				events: []*session.Event{{
					Message: model.Message{
						Role: model.RoleTool,
						ToolResponse: &model.ToolResponse{
							ID:   "call_patch_1",
							Name: "PATCH",
							Result: map[string]any{
								"path":        "demo.txt",
								"ok":          true,
								"_ui_preview": "--- old\n+++ new",
								"metadata": map[string]any{
									"patch": map[string]any{
										"preview": "--- old\n+++ new",
									},
								},
								"payload": map[string]any{
									"metadata": map[string]any{
										"keep": "yes",
									},
									"_ui_note": "internal",
									"value":    "ok",
								},
							},
						},
					},
				}},
			}, nil
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{ProtocolVersion: CurrentProtocolVersion}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	h.resetNotifications()
	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "filter"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}

	h.waitNotifications(t, 1)
	h.mu.Lock()
	notes := append([]SessionNotification(nil), h.notifications...)
	h.mu.Unlock()
	for _, note := range notes {
		raw, err := json.Marshal(note.Update)
		if err != nil {
			t.Fatal(err)
		}
		var update struct {
			SessionUpdate string         `json:"sessionUpdate"`
			RawOutput     map[string]any `json:"rawOutput"`
		}
		if err := json.Unmarshal(raw, &update); err != nil {
			t.Fatal(err)
		}
		if update.SessionUpdate != UpdateToolCallState {
			continue
		}
		if _, ok := update.RawOutput["_ui_preview"]; ok {
			t.Fatalf("did not expect _ui metadata in ACP rawOutput: %#v", update.RawOutput)
		}
		if _, ok := update.RawOutput["metadata"]; ok {
			t.Fatalf("did not expect metadata in ACP rawOutput: %#v", update.RawOutput)
		}
		if update.RawOutput["path"] != "demo.txt" || update.RawOutput["ok"] != true {
			t.Fatalf("expected visible fields preserved, got %#v", update.RawOutput)
		}
		payload, _ := update.RawOutput["payload"].(map[string]any)
		if payload["value"] != "ok" {
			t.Fatalf("expected payload preserved, got %#v", payload)
		}
		if _, ok := payload["_ui_note"]; ok {
			t.Fatalf("did not expect nested _ui metadata in ACP rawOutput: %#v", payload)
		}
		nestedMeta, _ := payload["metadata"].(map[string]any)
		if nestedMeta["keep"] != "yes" {
			t.Fatalf("expected nested metadata preserved, got %#v", payload)
		}
		return
	}
	t.Fatal("expected tool call state notification")
}

func TestServer_Prompt_CoalescesAssistantPartialsIntoFinalMessage(t *testing.T) {
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Text: "hel"}},
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Text: "lo"}},
					{Message: model.Message{Role: model.RoleAssistant, Text: "hello"}},
				},
			},
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "stream"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}

	h.waitNotifications(t, 1)
	texts := h.notificationTexts(UpdateAgentMessage)
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("expected one coalesced assistant message, got %#v", texts)
	}
}

func TestServer_Prompt_FinalMessageOnlyEmitsUnsentSuffixAfterFlush(t *testing.T) {
	prefix := strings.Repeat("a", partialFlushHardLimit)
	suffix := "tail"
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Text: prefix}},
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Text: suffix}},
					{Message: model.Message{Role: model.RoleAssistant, Text: prefix + suffix}},
				},
			},
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "stream"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}

	h.waitNotifications(t, 2)
	texts := h.notificationTexts(UpdateAgentMessage)
	if len(texts) != 2 || !containsText(texts, prefix) || !containsText(texts, suffix) {
		t.Fatalf("expected flushed prefix and final suffix, got %#v", texts)
	}
}

func TestServer_Prompt_FlushesReasoningBeforeAnswerOnChannelSwitch(t *testing.T) {
	answer := strings.Repeat("a", partialFlushHardLimit)
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Reasoning: "reasoning first"}},
					{Partial: true, Message: model.Message{Role: model.RoleAssistant, Text: answer}},
					{Message: model.Message{Role: model.RoleAssistant, Reasoning: "reasoning first", Text: answer}},
				},
			},
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "stream"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}

	h.waitNotifications(t, 2)
	got := h.notificationTypes()
	if len(got) < 2 {
		t.Fatalf("expected thought/message notifications, got %#v", got)
	}
	if got[0] != UpdateAgentThought || got[1] != UpdateAgentMessage {
		t.Fatalf("expected reasoning to flush before answer on channel switch, got %#v", got)
	}
}

func TestServer_LoadSessionSuppressesPersistedPartialChunks(t *testing.T) {
	store := sessionmem.New()
	h := newHarness(t, harnessConfig{store: store})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	sess := &session.Session{AppName: "caelis", UserID: "tester", ID: "persisted-session"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatalf("get or create session: %v", err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		Message: model.Message{Role: model.RoleUser, Text: "hello"},
	}); err != nil {
		t.Fatalf("append user event: %v", err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "hel"},
		Meta:    map[string]any{"partial": true, "channel": "answer"},
	}); err != nil {
		t.Fatalf("append partial event: %v", err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
	}); err != nil {
		t.Fatalf("append final event: %v", err)
	}

	h.resetNotifications()
	mustCall(t, h.client, MethodSessionLoad, LoadSessionRequest{
		SessionID: sess.ID,
		CWD:       h.workspace,
	}, &LoadSessionResponse{})
	h.waitNotifications(t, 2)

	if texts := h.notificationTexts(UpdateAgentMessage); len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("expected only final assistant replay, got %#v", texts)
	}
}

func TestServer_Prompt_PermissionRejectReturnsCancelled(t *testing.T) {
	h := newHarness(t, harnessConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-bash",
							Name: "BASH",
							Args: `{"command":"echo hi","require_escalated":true}`,
						}},
					},
				}},
			},
		},
	})
	defer h.close()
	h.permissionResponse = RequestPermissionResponse{
		Outcome: mustRaw(t, SelectedPermissionOutcome{
			Outcome:  "selected",
			OptionID: "reject_once",
		}),
	}

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "approve?"})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonCancelled {
		t.Fatalf("expected cancelled, got %q", promptResp.StopReason)
	}
	if h.permissionRequests != 1 {
		t.Fatalf("expected one permission request, got %d", h.permissionRequests)
	}
}

func TestSummarizeToolCallTitle_TaskUsesFriendlyActionSummary(t *testing.T) {
	if got := summarizeToolCallTitle("TASK", map[string]any{
		"action": "wait",
	}); got != "WAIT 5 s" {
		t.Fatalf("expected WAIT summary, got %q", got)
	}
	if got := summarizeToolCallTitle("TASK", map[string]any{
		"action":  "cancel",
		"task_id": "t-1234567890ab",
	}); got != "CANCEL t-12345678" {
		t.Fatalf("expected cancel summary with short task id, got %q", got)
	}
	if got := summarizeToolCallTitle("TASK", map[string]any{
		"action":  "write",
		"task_id": "t-1234567890ab",
	}); got != "WRITE {task=t-12345678}" {
		t.Fatalf("expected write summary with short task id, got %q", got)
	}
}

func TestToolCallContentForResult_UsesTerminalSessionID(t *testing.T) {
	got := toolCallContentForResult("BASH", map[string]any{
		"session_id": "term-123",
	})
	if len(got) != 1 {
		t.Fatalf("expected one terminal content item, got %#v", got)
	}
	if got[0].Type != "terminal" || got[0].TerminalID != "term-123" {
		t.Fatalf("unexpected terminal content %#v", got[0])
	}
}

func TestToolCallContentForResult_TaskDoesNotAttachTerminal(t *testing.T) {
	got := toolCallContentForResult("TASK", map[string]any{
		"session_id": "term-123",
	})
	if len(got) != 0 {
		t.Fatalf("expected TASK result not to attach terminal content, got %#v", got)
	}
}

func TestSupplementalToolCallUpdates_TaskCancelCompletesOriginBash(t *testing.T) {
	sess := &serverSession{}
	sess.rememberToolCall("call-bash", "BASH", map[string]any{
		"command": "sleep 30",
	})
	sess.rememberAsyncToolResult("BASH", "call-bash", map[string]any{
		"task_id":    "t-1234567890ab",
		"session_id": "term-123",
	})
	sess.rememberToolCall("call-task-cancel", "TASK", map[string]any{
		"action":  "cancel",
		"task_id": "t-1234567890ab",
	})

	updates := supplementalToolCallUpdates(sess, &model.ToolResponse{
		ID:   "call-task-cancel",
		Name: "TASK",
		Result: map[string]any{
			"task_id":       "t-1234567890ab",
			"session_id":    "term-123",
			"state":         "cancelled",
			"latest_output": "5\n6\n",
		},
	})
	if len(updates) != 1 {
		t.Fatalf("expected one supplemental update, got %#v", updates)
	}
	update := updates[0]
	if update.ToolCallID != "call-bash" {
		t.Fatalf("expected supplemental update for original bash call, got %q", update.ToolCallID)
	}
	if update.Status == nil || *update.Status != ToolStatusCompleted {
		t.Fatalf("expected completed status, got %#v", update.Status)
	}
	raw, ok := update.RawOutput.(map[string]any)
	if !ok {
		t.Fatalf("expected rawOutput map, got %#v", update.RawOutput)
	}
	if raw["state"] != "cancelled" || raw["cancelled"] != true {
		t.Fatalf("expected cancelled markers, got %#v", raw)
	}
	if raw["task_id"] != "t-1234567890ab" || raw["session_id"] != "term-123" {
		t.Fatalf("expected task/session linkage preserved, got %#v", raw)
	}
}

func TestServer_SessionCancelInterruptsPrompt(t *testing.T) {
	blocker := make(chan struct{})
	h := newHarness(t, harnessConfig{
		newAgent: func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error) {
			_ = sessionCWD
			_ = cfg
			return blockingAgent{name: "block", blocker: blocker}, nil
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	done := make(chan PromptResponse, 1)
	go func() {
		var resp PromptResponse
		_ = h.client.Call(context.Background(), MethodSessionPrompt, PromptRequest{
			SessionID: newResp.SessionID,
			Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "block"})},
		}, &resp)
		done <- resp
	}()

	time.Sleep(50 * time.Millisecond)
	if err := h.client.Notify(MethodSessionCancel, CancelNotification{SessionID: newResp.SessionID}); err != nil {
		t.Fatalf("session/cancel: %v", err)
	}
	select {
	case resp := <-done:
		if resp.StopReason != StopReasonCancelled {
			t.Fatalf("expected cancelled stop reason, got %q", resp.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled prompt response")
	}
	close(blocker)
}

func TestServer_SessionLoadKeepsCancelForActiveRun(t *testing.T) {
	blocker := make(chan struct{})
	h := newHarness(t, harnessConfig{
		newAgent: func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error) {
			_ = sessionCWD
			_ = cfg
			return blockingAgent{name: "block", blocker: blocker}, nil
		},
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)

	done := make(chan PromptResponse, 1)
	go func() {
		var resp PromptResponse
		_ = h.client.Call(context.Background(), MethodSessionPrompt, PromptRequest{
			SessionID: newResp.SessionID,
			Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "block"})},
		}, &resp)
		done <- resp
	}()

	time.Sleep(50 * time.Millisecond)
	mustCall(t, h.client, MethodSessionLoad, LoadSessionRequest{
		SessionID: newResp.SessionID,
		CWD:       h.workspace,
	}, &LoadSessionResponse{})
	if err := h.client.Notify(MethodSessionCancel, CancelNotification{SessionID: newResp.SessionID}); err != nil {
		t.Fatalf("session/cancel: %v", err)
	}

	select {
	case resp := <-done:
		if resp.StopReason != StopReasonCancelled {
			t.Fatalf("expected cancelled stop reason, got %q", resp.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled prompt response after session/load")
	}
	close(blocker)
}

func TestServer_SessionLoadUnknownIDReturnsNotFound(t *testing.T) {
	store, err := sessionfile.New(t.TempDir())
	if err != nil {
		t.Fatalf("new filestore: %v", err)
	}
	h := newHarness(t, harnessConfig{store: store})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var loadResp LoadSessionResponse
	err = h.client.Call(context.Background(), MethodSessionLoad, LoadSessionRequest{
		SessionID:  "missing-session",
		CWD:        h.workspace,
		MCPServers: nil,
	}, &loadResp)
	if err == nil || !strings.Contains(err.Error(), session.ErrSessionNotFound.Error()) {
		t.Fatalf("expected session not found error, got %v", err)
	}
}

func TestServer_SessionNewAllowsWorkspaceRootSubdir(t *testing.T) {
	h := newHarness(t, harnessConfig{
		workspaceRoot: "/workspace",
		workspace:     "/workspace/subdir",
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{
		CWD: "/workspace/subdir/project",
	}, &newResp)
	if strings.TrimSpace(newResp.SessionID) == "" {
		t.Fatal("expected session id")
	}
}

func TestServer_SessionRejectsCWDOutsideWorkspaceRoot(t *testing.T) {
	h := newHarness(t, harnessConfig{
		workspaceRoot: "/workspace",
		workspace:     "/workspace/subdir",
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var newResp NewSessionResponse
	err := h.client.Call(context.Background(), MethodSessionNew, NewSessionRequest{
		CWD: "/outside",
	}, &newResp)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("expected workspace root validation error, got %v", err)
	}
}

func TestPathWithinRootRejectsSymlinkEscape(t *testing.T) {
	if stdruntime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link-out")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if pathWithinRoot(root, link) {
		t.Fatalf("expected symlink target outside root to be rejected")
	}
	if pathWithinRoot(root, filepath.Join(link, "child")) {
		t.Fatalf("expected descendant of external symlink target to be rejected")
	}
}

func TestServer_SessionLoadRejectsPersistedCWDMismatch(t *testing.T) {
	h := newHarness(t, harnessConfig{
		workspaceRoot: "/workspace",
		workspace:     "/workspace/subdir",
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: CurrentProtocolVersion,
	}, &InitializeResponse{})

	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{
		CWD: "/workspace/subdir/project-a",
	}, &newResp)

	var loadResp LoadSessionResponse
	err := h.client.Call(context.Background(), MethodSessionLoad, LoadSessionRequest{
		SessionID: newResp.SessionID,
		CWD:       "/workspace/subdir/project-b",
	}, &loadResp)
	if err == nil || !strings.Contains(err.Error(), "persisted session cwd") {
		t.Fatalf("expected persisted cwd mismatch error, got %v", err)
	}
}

type harnessConfig struct {
	clientCaps          ClientCapabilities
	llm                 model.LLM
	newAgent            AgentFactory
	newModel            ModelFactory
	store               session.Store
	authMethods         []AuthMethod
	authenticate        AuthValidator
	sessionModes        []SessionMode
	defaultModeID       string
	sessionConfig       []SessionConfigOptionTemplate
	listSessions        SessionListFactory
	sessionModels       SessionModelStateFactory
	promptImageEnabled  func() bool
	supportsPromptImage func(AgentSessionConfig) bool
	workspaceRoot       string
	workspace           string
}

type harness struct {
	t *testing.T

	client     *Conn
	cancel     context.CancelFunc
	workspace  string
	clientCaps ClientCapabilities

	mu                 sync.Mutex
	notifications      []SessionNotification
	files              map[string]string
	readRequests       int
	terminalCreates    int
	termOutput         string
	permissionRequests int
	permissionResponse RequestPermissionResponse
}

func newHarness(t *testing.T, cfg harnessConfig) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	serverConn := NewConn(c2sR, s2cW)
	clientConn := NewConn(s2cR, c2sW)

	store := cfg.store
	if store == nil {
		store = sessionmem.New()
	}
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatalf("new runtime core: %v", err)
	}
	h := &harness{
		t:          t,
		client:     clientConn,
		cancel:     cancel,
		workspace:  "/workspace",
		clientCaps: cfg.clientCaps,
		files:      map[string]string{},
		termOutput: "ok\n",
		permissionResponse: RequestPermissionResponse{
			Outcome: mustRaw(t, SelectedPermissionOutcome{
				Outcome:  "selected",
				OptionID: "allow_once",
			}),
		},
	}
	if root := strings.TrimSpace(cfg.workspaceRoot); root != "" {
		h.workspace = filepath.Clean(root)
	}
	if workspace := strings.TrimSpace(cfg.workspace); workspace != "" {
		h.workspace = filepath.Clean(workspace)
	}
	workspaceRoot := h.workspace
	if root := strings.TrimSpace(cfg.workspaceRoot); root != "" {
		workspaceRoot = filepath.Clean(root)
	}
	agentFactory := cfg.newAgent
	if agentFactory == nil {
		agentFactory = newLLMAgentFactory(t)
	}
	modelImpl := cfg.llm
	if modelImpl == nil {
		modelImpl = &scriptedLLM{}
	}
	server, err := NewServer(ServerConfig{
		Conn:                serverConn,
		Runtime:             rt,
		Store:               store,
		Model:               modelImpl,
		NewModel:            cfg.newModel,
		AppName:             "caelis",
		UserID:              "tester",
		WorkspaceRoot:       workspaceRoot,
		AuthMethods:         cfg.authMethods,
		Authenticate:        cfg.authenticate,
		SessionModes:        cfg.sessionModes,
		DefaultModeID:       cfg.defaultModeID,
		SessionConfig:       cfg.sessionConfig,
		NewAgent:            agentFactory,
		ListSessions:        cfg.listSessions,
		SessionModels:       cfg.sessionModels,
		PromptImageEnabled:  cfg.promptImageEnabled,
		SupportsPromptImage: cfg.supportsPromptImage,
		NewSessionResources: func(ctx context.Context, sessionID string, sessionCWD string, caps ClientCapabilities, _ []MCPServer, modeResolver func() string) (*SessionResources, error) {
			execRuntime := NewRuntime(baseRuntime, serverConn, sessionID, workspaceRoot, sessionCWD, caps, modeResolver)
			tools := make([]tool.Tool, 0, 1)
			bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: execRuntime})
			if err != nil {
				return nil, err
			}
			tools = append(tools, bashTool)
			return &SessionResources{
				Runtime: execRuntime,
				Tools:   tools,
				Policies: []policy.Hook{
					policy.DefaultSecurityBaseline(),
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() {
		_ = server.Serve(ctx)
	}()
	go func() {
		_ = clientConn.Serve(ctx, h.handleRequest, h.handleNotification)
	}()
	t.Cleanup(func() {
		cancel()
		_ = toolexec.Close(baseRuntime)
	})
	return h
}

func (h *harness) close() {
	h.cancel()
}

func (h *harness) handleRequest(ctx context.Context, msg Message) (any, *RPCError) {
	switch msg.Method {
	case MethodReadTextFile:
		var req ReadTextFileRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.readRequests++
		return ReadTextFileResponse{Content: h.files[req.Path]}, nil
	case MethodWriteTextFile:
		var req WriteTextFileRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		h.mu.Lock()
		h.files[req.Path] = req.Content
		h.mu.Unlock()
		return WriteTextFileResponse{}, nil
	case MethodTerminalCreate:
		h.mu.Lock()
		h.terminalCreates++
		h.mu.Unlock()
		return CreateTerminalResponse{TerminalID: "term-1"}, nil
	case MethodTerminalWaitForExit:
		code := 0
		return WaitForTerminalExitResponse{ExitCode: &code}, nil
	case MethodTerminalOutput:
		h.mu.Lock()
		output := h.termOutput
		h.mu.Unlock()
		return TerminalOutputResponse{Output: output, Truncated: false}, nil
	case MethodTerminalRelease:
		return map[string]any{}, nil
	case MethodSessionReqPermission:
		h.mu.Lock()
		h.permissionRequests++
		resp := h.permissionResponse
		h.mu.Unlock()
		return resp, nil
	default:
		return nil, &RPCError{Code: -32601, Message: fmt.Sprintf("unexpected client method %s", msg.Method)}
	}
}

func (h *harness) handleNotification(ctx context.Context, msg Message) {
	_ = ctx
	if msg.Method != MethodSessionUpdate {
		return
	}
	var note SessionNotification
	if err := decodeParams(msg.Params, &note); err != nil {
		return
	}
	h.mu.Lock()
	h.notifications = append(h.notifications, note)
	h.mu.Unlock()
}

func (h *harness) resetNotifications() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notifications = nil
}

func (h *harness) waitNotifications(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		count := len(h.notifications)
		h.mu.Unlock()
		if count >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t.Fatalf("expected at least %d notifications, got %d", n, len(h.notifications))
}

func (h *harness) notificationTypes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.notifications))
	for _, note := range h.notifications {
		raw, _ := json.Marshal(note.Update)
		var marker struct {
			SessionUpdate string `json:"sessionUpdate"`
		}
		_ = json.Unmarshal(raw, &marker)
		out = append(out, marker.SessionUpdate)
	}
	return out
}

func (h *harness) notificationTexts(updateType string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, note := range h.notifications {
		raw, err := json.Marshal(note.Update)
		if err != nil {
			continue
		}
		var update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &update); err != nil {
			continue
		}
		if update.SessionUpdate == updateType {
			out = append(out, update.Content.Text)
		}
	}
	return out
}

func makeACPTestPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

type scriptedLLM struct {
	mu    sync.Mutex
	calls [][]*model.Response
	reqs  []*model.Request
}

func (s *scriptedLLM) Name() string { return "scripted" }

func (s *scriptedLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	s.mu.Lock()
	var batch []*model.Response
	if len(s.calls) > 0 {
		batch = s.calls[0]
		s.calls = s.calls[1:]
	}
	if req != nil {
		clone := *req
		clone.Messages = append([]model.Message(nil), req.Messages...)
		s.reqs = append(s.reqs, &clone)
	}
	s.mu.Unlock()
	return func(yield func(*model.Response, error) bool) {
		_ = ctx
		_ = req
		for _, one := range batch {
			if one != nil && !one.Partial && !one.TurnComplete {
				clone := *one
				clone.TurnComplete = true
				one = &clone
			}
			if !yield(one, nil) {
				return
			}
		}
	}
}

func TestServer_PlanModeInjectsHiddenPromptButLoadReplaysVisibleText(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.Message{Role: model.RoleAssistant, Text: "planned"}}},
		},
	}
	h := newHarness(t, harnessConfig{
		llm: llm,
		sessionModes: []SessionMode{
			{ID: "default", Name: "Default"},
			{ID: "plan", Name: "Plan"},
		},
		defaultModeID: "default",
	})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{ProtocolVersion: CurrentProtocolVersion}, &InitializeResponse{})
	var newResp NewSessionResponse
	mustCall(t, h.client, MethodSessionNew, NewSessionRequest{CWD: h.workspace}, &newResp)
	mustCall(t, h.client, MethodSessionSetMode, SetSessionModeRequest{
		SessionID: newResp.SessionID,
		ModeID:    "plan",
	}, &SetSessionModeResponse{})

	var promptResp PromptResponse
	mustCall(t, h.client, MethodSessionPrompt, PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []json.RawMessage{mustRaw(t, TextContent{Type: "text", Text: "Inspect the repo."})},
	}, &promptResp)
	if promptResp.StopReason != StopReasonEndTurn {
		t.Fatalf("expected end_turn, got %q", promptResp.StopReason)
	}
	if len(llm.reqs) == 0 {
		t.Fatal("expected model request")
	}
	foundInjected := false
	for _, msg := range llm.reqs[0].Messages {
		if msg.Role == model.RoleUser && strings.Contains(msg.TextContent(), "mode=\"plan\"") {
			foundInjected = true
			break
		}
	}
	if !foundInjected {
		t.Fatalf("expected injected plan-mode prompt in request, got %+v", llm.reqs[0].Messages)
	}

	h.resetNotifications()
	mustCall(t, h.client, MethodSessionLoad, LoadSessionRequest{
		SessionID: newResp.SessionID,
		CWD:       h.workspace,
	}, &LoadSessionResponse{})
	h.waitNotifications(t, 2)
	foundVisible := false
	h.mu.Lock()
	notes := append([]SessionNotification(nil), h.notifications...)
	h.mu.Unlock()
	for _, note := range notes {
		raw, err := json.Marshal(note.Update)
		if err != nil {
			continue
		}
		var update map[string]any
		if err := json.Unmarshal(raw, &update); err != nil {
			continue
		}
		if update["sessionUpdate"] == UpdateUserMessage {
			content, _ := update["content"].(map[string]any)
			if text, _ := content["text"].(string); text == "Inspect the repo." {
				foundVisible = true
			}
		}
	}
	if !foundVisible {
		t.Fatal("expected stripped user message in session/load replay")
	}
}

func newLLMAgentFactory(t *testing.T) AgentFactory {
	t.Helper()
	return func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error) {
		_ = sessionCWD
		_ = cfg
		return llmagent.New(llmagent.Config{
			Name:              "test",
			SystemPrompt:      "test",
			StreamModel:       stream,
			EmitPartialEvents: stream,
		})
	}
}

type blockingAgent struct {
	name    string
	blocker <-chan struct{}
}

func (a blockingAgent) Name() string { return a.name }

func (a blockingAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		select {
		case <-ctx.Done():
			yield(nil, ctx.Err())
		case <-a.blocker:
		}
	}
}

type scriptedAgent struct {
	name   string
	events []*session.Event
}

func (a scriptedAgent) Name() string { return a.name }

func (a scriptedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		for _, ev := range a.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

type stubRunner struct{}

func (stubRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	_ = req
	return toolexec.CommandResult{Stdout: "stub\n"}, nil
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	return raw
}

func mustCall(t *testing.T, conn *Conn, method string, params any, out any) {
	t.Helper()
	if err := conn.Call(context.Background(), method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func containsAll(items []string, want ...string) bool {
	set := map[string]struct{}{}
	for _, item := range items {
		set[item] = struct{}{}
	}
	for _, one := range want {
		if _, ok := set[one]; !ok {
			return false
		}
	}
	return true
}

func containsText(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func testSandboxType() string {
	if stdruntime.GOOS == "darwin" {
		return "seatbelt"
	}
	if stdruntime.GOOS == "linux" {
		return "landlock"
	}
	return "bwrap"
}
