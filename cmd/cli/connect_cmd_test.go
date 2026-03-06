package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/modelcatalog"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

type stubChoicePrompter struct {
	choices []string
	lines   []string
	choiceI int
	lineI   int
}

func (s *stubChoicePrompter) ReadLine(prompt string) (string, error) {
	_ = prompt
	if s.lineI >= len(s.lines) {
		return "", errInputEOF
	}
	line := s.lines[s.lineI]
	s.lineI++
	return line, nil
}

func (s *stubChoicePrompter) ReadSecret(prompt string) (string, error) {
	return s.ReadLine(prompt)
}

func (s *stubChoicePrompter) RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error) {
	_ = prompt
	_ = defaultChoice
	_ = filterable
	if s.choiceI >= len(s.choices) {
		return "", errInputEOF
	}
	value := s.choices[s.choiceI]
	s.choiceI++
	for _, choice := range choices {
		if choice.Value == value {
			return value, nil
		}
	}
	return "", io.EOF
}

func (s *stubChoicePrompter) RequestMultiChoicePrompt(prompt string, choices []tuievents.PromptChoice, selectedChoices []string, filterable bool) (string, error) {
	_ = prompt
	_ = selectedChoices
	_ = filterable
	if s.choiceI >= len(s.choices) {
		return "", errInputEOF
	}
	raw := s.choices[s.choiceI]
	s.choiceI++
	parts := splitArrayInput(raw)
	if len(parts) == 0 {
		return "", io.EOF
	}
	allowed := map[string]struct{}{}
	for _, choice := range choices {
		allowed[choice.Value] = struct{}{}
	}
	for _, part := range parts {
		if _, ok := allowed[part]; !ok {
			return "", io.EOF
		}
	}
	return raw, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

func TestHandleConnectRejectsPositionalArgs(t *testing.T) {
	c := &cliConsole{modelFactory: modelproviders.NewFactory()}
	_, err := handleConnect(c, []string{"openai"})
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: /connect") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildConnectModelChoicesIncludesCatalogAndCustom(t *testing.T) {
	got := buildConnectModelChoices("deepseek", []modelproviders.RemoteModel{
		{Name: "deepseek-chat"},
	})
	foundChat := false
	foundReasoner := false
	foundCustom := false
	for _, item := range got {
		switch item.Name {
		case "deepseek-chat":
			foundChat = true
		case "deepseek-reasoner":
			foundReasoner = true
		case connectCustomModelValue:
			foundCustom = true
		}
	}
	if !foundChat || !foundReasoner || !foundCustom {
		t.Fatalf("unexpected model choices: %+v", got)
	}
}

func TestHandleConnect_InteractiveMultiModel(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{
			{Name: "deepseek-chat"},
			{Name: "deepseek-reasoner"},
		}, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.InitModelCatalogWithStatus(context.Background(), &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, io.EOF
			}),
		}, "")
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	store := &appConfigStore{path: filepath.Join(t.TempDir(), "config.json"), data: defaultAppConfig()}
	prompter := &stubChoicePrompter{
		choices: []string{"deepseek", "deepseek-chat,deepseek-reasoner"},
		lines:   []string{"sk-test"},
	}
	var out bytes.Buffer
	c := &cliConsole{
		baseCtx:      context.Background(),
		modelFactory: modelproviders.NewFactory(),
		configStore:  store,
		prompter:     prompter,
		ui:           newUI(&out, true, false),
		out:          &out,
	}
	_, err := handleConnect(c, nil)
	if err != nil {
		t.Fatalf("handleConnect failed: %v", err)
	}
	if c.modelAlias != "deepseek/deepseek-chat" {
		t.Fatalf("unexpected current model %q", c.modelAlias)
	}
	gotModels := c.modelFactory.ListModels()
	if len(gotModels) != 2 {
		t.Fatalf("expected 2 registered models, got %v", gotModels)
	}
	if store.DefaultModel() != "deepseek/deepseek-chat" {
		t.Fatalf("unexpected default model %q", store.DefaultModel())
	}
}

func TestHandleConnect_UnknownModelPromptsAdvancedDefaults(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return nil, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.InitModelCatalogWithStatus(context.Background(), &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, io.EOF
			}),
		}, "")
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"openai", connectCustomModelValue, reasoningModeEffort, "low,high"},
		lines:   []string{"sk-test", "gpt-custom", "", "", "minimal,xhigh"},
	}
	var out bytes.Buffer
	c := &cliConsole{
		baseCtx:      context.Background(),
		modelFactory: modelproviders.NewFactory(),
		prompter:     prompter,
		ui:           newUI(&out, true, false),
		out:          &out,
	}
	_, err := handleConnect(c, nil)
	if err != nil {
		t.Fatalf("handleConnect failed: %v", err)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("openai/gpt-custom")
	if !ok {
		t.Fatal("expected unknown model to be registered")
	}
	if cfg.ContextWindowTokens != 128000 || cfg.MaxOutputTok != 4096 {
		t.Fatalf("unexpected advanced defaults %+v", cfg)
	}
	if cfg.ReasoningMode != reasoningModeEffort {
		t.Fatalf("expected reasoning mode effort, got %q", cfg.ReasoningMode)
	}
	wantEfforts := []string{"low", "high", "minimal", "xhigh"}
	if len(cfg.SupportedReasoningEfforts) != len(wantEfforts) {
		t.Fatalf("unexpected supported efforts %+v", cfg.SupportedReasoningEfforts)
	}
	for i := range wantEfforts {
		if cfg.SupportedReasoningEfforts[i] != wantEfforts[i] {
			t.Fatalf("unexpected supported efforts %+v", cfg.SupportedReasoningEfforts)
		}
	}
	if cfg.DefaultReasoningEffort != "low" {
		t.Fatalf("expected default reasoning effort low, got %q", cfg.DefaultReasoningEffort)
	}
	wantLevels := []string{"none", "low", "high", "minimal", "xhigh"}
	if len(cfg.ReasoningLevels) != len(wantLevels) {
		t.Fatalf("unexpected reasoning levels %+v", cfg.ReasoningLevels)
	}
	for i := range wantLevels {
		if cfg.ReasoningLevels[i] != wantLevels[i] {
			t.Fatalf("unexpected reasoning levels %+v", cfg.ReasoningLevels)
		}
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
