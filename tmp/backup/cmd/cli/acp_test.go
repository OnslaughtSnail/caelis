package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestBuildACPSessionConfigOptions_ExposeModeModelAndReasoning(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	cfg := modelproviders.Config{
		Alias:                  "openai/o3",
		Provider:               "openai",
		API:                    modelproviders.APIOpenAI,
		Model:                  "o3",
		DefaultReasoningEffort: "medium",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	if err := store.UpsertProvider(cfg); err != nil {
		t.Fatal(err)
	}
	factory := modelproviders.NewFactory()
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatal(err)
	}

	options := buildACPSessionConfigOptions([]internalacp.SessionMode{
		{ID: "default", Name: "Default", Description: "Default mode"},
		{ID: "plan", Name: "Plan", Description: "Plan mode"},
	}, factory, store, "openai/o3")
	if len(options) != 3 {
		t.Fatalf("expected 3 config options, got %+v", options)
	}
	if options[0].ID != "mode" || options[0].Category != "mode" {
		t.Fatalf("expected mode option first, got %+v", options[0])
	}
	if options[1].ID != "model" || options[1].DefaultValue != "openai/o3" {
		t.Fatalf("expected model option with default alias, got %+v", options[1])
	}
	if options[2].ID != "reasoning_effort" || options[2].Category != "thought_level" {
		t.Fatalf("expected reasoning option, got %+v", options[2])
	}
}

func TestBuildACPSessionList_UsesIndexedHistory(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{Key: "ws-key", CWD: "/workspace"}
	now := time.Date(2026, 3, 12, 4, 15, 5, 0, time.UTC)
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-1", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "caelis", "tester", "s-1", &session.Event{
		Time:    now,
		Message: model.NewTextMessage(model.RoleUser, "inspect acp parity"),
	}, now); err != nil {
		t.Fatal(err)
	}

	resp := buildACPSessionList(context.Background(), idx, workspace, internalacp.SessionListRequest{})
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected one session, got %+v", resp)
	}
	if resp.Sessions[0].Title != "inspect acp parity" {
		t.Fatalf("unexpected session title %+v", resp.Sessions[0])
	}
	if resp.Sessions[0].UpdatedAt != now.Format(time.RFC3339) {
		t.Fatalf("unexpected updatedAt %+v", resp.Sessions[0])
	}
}

func TestBuildACPSessionList_FiltersEmptySessions(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{Key: "ws-key", CWD: "/workspace"}
	now := time.Date(2026, 3, 12, 4, 15, 5, 0, time.UTC)
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-empty", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-live", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "caelis", "tester", "s-live", &session.Event{
		Time:    now.Add(time.Second),
		Message: model.NewTextMessage(model.RoleUser, "non-empty session"),
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	resp := buildACPSessionList(context.Background(), idx, workspace, internalacp.SessionListRequest{})
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected one non-empty session, got %+v", resp)
	}
	if resp.Sessions[0].SessionID != "s-live" {
		t.Fatalf("unexpected session list %+v", resp.Sessions)
	}
}

func TestBuildACPSessionList_UsesIndexedSessions(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{Key: "ws-key", CWD: "/workspace"}
	now := time.Date(2026, 3, 12, 4, 15, 5, 0, time.UTC)
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-root", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "caelis", "tester", "s-root", &session.Event{
		Time:    now,
		Message: model.NewTextMessage(model.RoleUser, "visible root session"),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-child", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "caelis", "tester", "s-child", &session.Event{
		Time:    now.Add(time.Second),
		Message: model.NewTextMessage(model.RoleAssistant, "hidden child session"),
		Meta: map[string]any{
			"parent_session_id": "s-root",
			"child_session_id":  "s-child",
			"delegation_id":     "dlg-1",
		},
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	resp := buildACPSessionList(context.Background(), idx, workspace, internalacp.SessionListRequest{})
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "s-root" {
		t.Fatalf("expected delegated child sessions to be hidden from ACP list, got %+v", resp.Sessions)
	}
}

func TestBuildACPSessionList_FiltersByCWD(t *testing.T) {
	idx, err := newSessionIndex(filepath.Join(t.TempDir(), "session_index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = idx.Close()
	})
	workspace := workspaceContext{Key: "ws-key", CWD: "/workspace"}
	now := time.Date(2026, 3, 12, 4, 15, 5, 0, time.UTC)
	other := workspaceContext{Key: "ws-key", CWD: "/workspace/other"}
	if err := idx.UpsertSession(workspace, "caelis", "tester", "s-root", now); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(workspace, "caelis", "tester", "s-root", &session.Event{
		Time:    now,
		Message: model.NewTextMessage(model.RoleUser, "root session"),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertSession(other, "caelis", "tester", "s-other", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := idx.TouchEvent(other, "caelis", "tester", "s-other", &session.Event{
		Time:    now.Add(time.Second),
		Message: model.NewTextMessage(model.RoleUser, "other session"),
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	resp := buildACPSessionList(context.Background(), idx, workspace, internalacp.SessionListRequest{CWD: "/workspace"})
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected one cwd-filtered session, got %+v", resp.Sessions)
	}
	if resp.Sessions[0].SessionID != "s-root" {
		t.Fatalf("unexpected filtered session %+v", resp.Sessions[0])
	}
}

func TestBuildACPSessionConfigState_ReasoningOptionsFollowModel(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	fixedCfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-reasoner",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	effortCfg := modelproviders.Config{
		Alias:    "openai/o3",
		Provider: "openai",
		API:      modelproviders.APIOpenAI,
		Model:    "o3",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	for _, cfg := range []modelproviders.Config{fixedCfg, effortCfg} {
		if err := store.UpsertProvider(cfg); err != nil {
			t.Fatal(err)
		}
	}
	factory := modelproviders.NewFactory()
	modelcatalogApplyConfigDefaults(&fixedCfg)
	modelcatalogApplyConfigDefaults(&effortCfg)
	if err := factory.Register(fixedCfg); err != nil {
		t.Fatal(err)
	}
	if err := factory.Register(effortCfg); err != nil {
		t.Fatal(err)
	}

	templates := buildACPSessionConfigOptions([]internalacp.SessionMode{
		{ID: "default", Name: "Default", Description: "Default mode"},
	}, factory, store, "deepseek/deepseek-reasoner")

	fixedState := buildACPSessionConfigState(templates, factory, store, "deepseek/deepseek-reasoner", internalacp.AgentSessionConfig{
		ModeID: "default",
		ConfigValues: map[string]string{
			"model": "deepseek/deepseek-reasoner",
		},
	})
	var fixedReasoning internalacp.SessionConfigOption
	for _, item := range fixedState {
		if item.ID == "reasoning_effort" {
			fixedReasoning = item
			break
		}
	}
	if len(fixedReasoning.Options) != 1 || fixedReasoning.Options[0].Value != "none" {
		t.Fatalf("expected unavailable reasoning options for fixed model, got %+v", fixedReasoning.Options)
	}

	effortState := buildACPSessionConfigState(templates, factory, store, "deepseek/deepseek-reasoner", internalacp.AgentSessionConfig{
		ModeID: "default",
		ConfigValues: map[string]string{
			"model":            "openai/o3",
			"reasoning_effort": "high",
		},
	})
	var effortReasoning internalacp.SessionConfigOption
	for _, item := range effortState {
		if item.ID == "reasoning_effort" {
			effortReasoning = item
			break
		}
	}
	if effortReasoning.CurrentValue != "high" {
		t.Fatalf("expected current effort high, got %+v", effortReasoning)
	}
	values := make([]string, 0, len(effortReasoning.Options))
	for _, one := range effortReasoning.Options {
		values = append(values, one.Value)
	}
	if got := strings.Join(values, ","); got != "low,medium,high,xhigh" {
		t.Fatalf("unexpected effort options %q", got)
	}
}
