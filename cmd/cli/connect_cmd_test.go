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
