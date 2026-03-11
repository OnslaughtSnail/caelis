package acp

import (
	"context"
	"encoding/json"
	"fmt"
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
		ProtocolVersion: "0.2.0",
		ClientCapabilities: ClientCapabilities{
			FS: FileSystemCapabilities{},
		},
	}, &initResp); err != nil {
		t.Fatalf("initialize: %v", err)
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

func TestServer_InitializeAcceptsNumericProtocolVersion(t *testing.T) {
	h := newHarness(t, harnessConfig{})
	defer h.close()

	var initResp InitializeResponse
	err := h.client.Call(context.Background(), MethodInitialize, map[string]any{
		"protocolVersion": 0.2,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{},
		},
	}, &initResp)
	if err != nil {
		t.Fatalf("initialize with numeric protocolVersion: %v", err)
	}
	if !initResp.AgentCapabilities.LoadSession {
		t.Fatal("expected loadSession capability")
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion:    "0.2.0",
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
		ProtocolVersion:    "0.2.0",
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

	mustCall(t, h.client, MethodInitialize, InitializeRequest{ProtocolVersion: "0.2.0"}, &InitializeResponse{})
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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

func TestServer_LoadSessionSuppressesPersistedPartialChunks(t *testing.T) {
	store := sessionmem.New()
	h := newHarness(t, harnessConfig{store: store})
	defer h.close()

	mustCall(t, h.client, MethodInitialize, InitializeRequest{
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
		ProtocolVersion: "0.2.0",
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
	clientCaps    ClientCapabilities
	llm           model.LLM
	newAgent      AgentFactory
	store         session.Store
	authMethods   []AuthMethod
	authenticate  AuthValidator
	sessionModes  []SessionMode
	defaultModeID string
	sessionConfig []SessionConfigOptionTemplate
	workspaceRoot string
	workspace     string
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
		Conn:          serverConn,
		Runtime:       rt,
		Store:         store,
		Model:         modelImpl,
		AppName:       "caelis",
		UserID:        "tester",
		WorkspaceRoot: workspaceRoot,
		AuthMethods:   cfg.authMethods,
		Authenticate:  cfg.authenticate,
		SessionModes:  cfg.sessionModes,
		DefaultModeID: cfg.defaultModeID,
		SessionConfig: cfg.sessionConfig,
		NewAgent:      agentFactory,
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

	mustCall(t, h.client, MethodInitialize, InitializeRequest{ProtocolVersion: "0.2.0"}, &InitializeResponse{})
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
