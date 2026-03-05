package main

import (
	"strings"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestDescribeRemoteModel(t *testing.T) {
	got := describeRemoteModel("deepseek", modelproviders.RemoteModel{
		Name:                "deepseek-chat",
		ContextWindowTokens: 64000,
		MaxOutputTokens:     4096,
		Capabilities:        []string{"tools", "reasoning"},
	})
	if !strings.Contains(got, "deepseek/deepseek-chat") {
		t.Fatalf("expected model ref in output, got %q", got)
	}
	if !strings.Contains(got, "ctx=64000") || !strings.Contains(got, "out=4096") {
		t.Fatalf("expected token metadata in output, got %q", got)
	}
	if !strings.Contains(got, "cap=tools|reasoning") {
		t.Fatalf("expected capabilities in output, got %q", got)
	}
}

func TestDescribeRemoteModelWithoutMetadata(t *testing.T) {
	got := describeRemoteModel("openai", modelproviders.RemoteModel{Name: "gpt-4o-mini"})
	if got != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected output: %q", got)
	}
	if strings.Contains(got, "(") {
		t.Fatalf("did not expect metadata suffix when fields are empty, got %q", got)
	}
}

func TestCommonModelsForProvider(t *testing.T) {
	got := commonModelsForProvider("deepseek")
	if len(got) == 0 {
		t.Fatal("expected common models for deepseek")
	}
	found := false
	for _, one := range got {
		if one == "deepseek-chat" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected deepseek-chat in common models: %v", got)
	}
}

func TestHandleConnectRejectsInvalidTimeoutArg(t *testing.T) {
	c := &cliConsole{modelFactory: modelproviders.NewFactory()}
	_, err := handleConnect(c, []string{"openai", "gpt-4o", "https://api.openai.com/v1", "abc"})
	if err == nil {
		t.Fatal("expected invalid timeout error")
	}
	if !strings.Contains(err.Error(), "invalid timeout_seconds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseConnectCLIArgs_WithAllFields(t *testing.T) {
	got, err := parseConnectCLIArgs([]string{
		"openai", "gpt-4o", "https://api.openai.com/v1", "60", "sk-test", "200000", "8192", "minimal,high",
	})
	if err != nil {
		t.Fatalf("parseConnectCLIArgs failed: %v", err)
	}
	if !got.quickMode || got.provider != "openai" || got.model != "gpt-4o" || got.baseURL != "https://api.openai.com/v1" {
		t.Fatalf("unexpected parsed args: %+v", got)
	}
	if !got.hasTimeout || got.timeoutSeconds != 60 {
		t.Fatalf("unexpected timeout parse: %+v", got)
	}
	if got.apiKey != "sk-test" {
		t.Fatalf("unexpected api key parse: %+v", got)
	}
	if !got.hasContextWindow || got.contextWindowTokens != 200000 {
		t.Fatalf("unexpected context parse: %+v", got)
	}
	if !got.hasMaxOutput || got.maxOutputTokens != 8192 {
		t.Fatalf("unexpected max_output parse: %+v", got)
	}
	if !got.hasReasoningLevels || got.reasoningLevelsRaw != "minimal,high" {
		t.Fatalf("unexpected reasoning parse: %+v", got)
	}
}

func TestParseConnectCLIArgs_NoAuthPlaceholder(t *testing.T) {
	got, err := parseConnectCLIArgs([]string{
		"ollama", "qwen2.5:7b", "http://localhost:11434", "30", "-", "32768", "4096", "-",
	})
	if err != nil {
		t.Fatalf("parseConnectCLIArgs failed: %v", err)
	}
	if got.apiKey != "" {
		t.Fatalf("expected empty api key for '-' placeholder, got %+v", got)
	}
	if !got.hasContextWindow || got.contextWindowTokens != 32768 || !got.hasMaxOutput || got.maxOutputTokens != 4096 {
		t.Fatalf("unexpected token limits: %+v", got)
	}
	if !got.hasReasoningLevels || got.reasoningLevelsRaw != "-" {
		t.Fatalf("unexpected reasoning parse: %+v", got)
	}
}

func TestParseReasoningLevelsInput_CommaSpaceTab(t *testing.T) {
	got, err := parseReasoningLevelsInput("minimal,low\tmedium high")
	if err != nil {
		t.Fatalf("parseReasoningLevelsInput failed: %v", err)
	}
	want := []string{"minimal", "low", "medium", "high"}
	if len(got) != len(want) {
		t.Fatalf("unexpected levels: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected levels: %v", got)
		}
	}
}

func TestParseReasoningLevelsInput_NormalizeAndDedup(t *testing.T) {
	got, err := parseReasoningLevelsInput("mimimal minimal very-high x-high")
	if err != nil {
		t.Fatalf("parseReasoningLevelsInput failed: %v", err)
	}
	want := []string{"minimal", "xhigh"}
	if len(got) != len(want) {
		t.Fatalf("unexpected levels: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected levels: %v", got)
		}
	}
}

func TestParseReasoningLevelsInput_Invalid(t *testing.T) {
	_, err := parseReasoningLevelsInput("minimal,unknown")
	if err == nil {
		t.Fatal("expected invalid reasoning level error")
	}
}
