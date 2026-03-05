package main

import (
	"bytes"
	"os"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestHandleModel_UpdatesReasoningAndPersists(t *testing.T) {
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
		Alias:    "deepseek/deepseek-chat",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-chat",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	if err := store.UpsertProvider(cfg); err != nil {
		t.Fatal(err)
	}

	factory := modelproviders.NewFactory()
	if err := factory.Register(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory: factory,
		configStore:  store,
		out:          &out,
	}
	if _, err := handleModel(console, []string{"deepseek/deepseek-chat", "on"}); err != nil {
		t.Fatal(err)
	}
	if console.thinkingMode != "on" {
		t.Fatalf("expected thinking mode on, got %q", console.thinkingMode)
	}
	settings := store.ModelRuntimeSettings("deepseek/deepseek-chat")
	if settings.ThinkingMode != "on" {
		t.Fatalf("expected persisted thinking mode on, got %q", settings.ThinkingMode)
	}

	if _, err := handleModel(console, []string{"deepseek/deepseek-chat", "false"}); err != nil {
		t.Fatal(err)
	}
	if console.thinkingMode != "off" {
		t.Fatalf("expected thinking mode off, got %q", console.thinkingMode)
	}
	settings = store.ModelRuntimeSettings("deepseek/deepseek-chat")
	if settings.ThinkingMode != "off" {
		t.Fatalf("expected persisted thinking mode off, got %q", settings.ThinkingMode)
	}
}

func TestHandleModel_InvalidReasoningDoesNotSwitchModel(t *testing.T) {
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
	openaiCfg := modelproviders.Config{
		Alias:    "openai/o3",
		Provider: "openai",
		API:      modelproviders.APIOpenAI,
		Model:    "o3",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	deepseekCfg := modelproviders.Config{
		Alias:           "deepseek/deepseek-chat",
		Provider:        "deepseek",
		API:             modelproviders.APIDeepSeek,
		Model:           "deepseek-chat",
		ReasoningLevels: []string{"none", "low"},
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	if err := store.UpsertProvider(openaiCfg); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(deepseekCfg); err != nil {
		t.Fatal(err)
	}

	factory := modelproviders.NewFactory()
	if err := factory.Register(openaiCfg); err != nil {
		t.Fatal(err)
	}
	if err := factory.Register(deepseekCfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory: factory,
		configStore:  store,
		out:          &out,
	}
	if _, err := handleModel(console, []string{"openai/o3", "on"}); err != nil {
		t.Fatal(err)
	}
	if console.modelAlias != "openai/o3" {
		t.Fatalf("expected model alias openai/o3, got %q", console.modelAlias)
	}

	if _, err := handleModel(console, []string{"deepseek/deepseek-chat", "high"}); err == nil {
		t.Fatal("expected invalid reasoning option error")
	}
	if console.modelAlias != "openai/o3" {
		t.Fatalf("expected model alias unchanged after failed command, got %q", console.modelAlias)
	}
	if got := store.DefaultModel(); got != "openai/o3" {
		t.Fatalf("expected persisted default model unchanged, got %q", got)
	}
}
