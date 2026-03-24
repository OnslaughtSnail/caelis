package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	choices       []string
	lines         []string
	choiceI       int
	lineI         int
	readPrompts   []string
	choicePrompts []string
	multiPrompts  []string
}

func (s *stubChoicePrompter) ReadLine(prompt string) (string, error) {
	s.readPrompts = append(s.readPrompts, prompt)
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
	s.choicePrompts = append(s.choicePrompts, prompt)
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
	allowed := make([]string, 0, len(choices))
	for _, choice := range choices {
		allowed = append(allowed, choice.Value)
	}
	return "", fmt.Errorf("invalid choice %q for prompt %q (allowed=%v)", value, prompt, allowed)
}

func (s *stubChoicePrompter) RequestMultiChoicePrompt(prompt string, choices []tuievents.PromptChoice, selectedChoices []string, filterable bool) (string, error) {
	s.multiPrompts = append(s.multiPrompts, prompt)
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
			return "", fmt.Errorf("invalid multi choice %q for prompt %q", part, prompt)
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

func TestDescribeRemoteModel_OpenRouterUsesRawModelID(t *testing.T) {
	got := describeRemoteModel("openrouter", modelproviders.RemoteModel{Name: "openai/gpt-4o-mini"})
	if got != "openai/gpt-4o-mini" {
		t.Fatalf("expected raw openrouter model id, got %q", got)
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

func TestBuildConnectModelChoicesIncludesRemoteAndCommonModels(t *testing.T) {
	got := buildConnectModelChoices("deepseek", []modelproviders.RemoteModel{
		{Name: "deepseek-chat"},
	}, []string{"deepseek-chat", "deepseek-reasoner"})
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

func TestBuildConnectModelChoices_OpenRouterDisplaysRawModelID(t *testing.T) {
	got := buildConnectModelChoices("openrouter", []modelproviders.RemoteModel{
		{Name: "openai/gpt-4o-mini"},
		{Name: "openrouter/healer-alpha"},
	}, nil)
	for _, item := range got {
		switch item.Name {
		case "openai/gpt-4o-mini":
			if item.Display != "openai/gpt-4o-mini" {
				t.Fatalf("expected raw routed model id, got %+v", item)
			}
		case "openrouter/healer-alpha":
			if item.Display != "openrouter/healer-alpha" {
				t.Fatalf("expected raw native model id, got %+v", item)
			}
		}
	}
}

func TestNormalizeConnectModelName_OpenRouter(t *testing.T) {
	got, err := normalizeConnectModelName("openrouter", "openrouter/openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("normalizeConnectModelName failed: %v", err)
	}
	if got != "openai/gpt-4o-mini" {
		t.Fatalf("expected display prefix stripped for routed model, got %q", got)
	}

	got, err = normalizeConnectModelName("openrouter", "openrouter/healer-alpha")
	if err != nil {
		t.Fatalf("normalizeConnectModelName failed: %v", err)
	}
	if got != "openrouter/healer-alpha" {
		t.Fatalf("expected native openrouter prefix preserved, got %q", got)
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
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

func TestHandleConnect_ReinitializesReasoningDefaultsAndOmitsNotes(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{Name: "deepseek-reasoner"}}, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	store := &appConfigStore{path: filepath.Join(t.TempDir(), "config.json"), data: defaultAppConfig()}
	if err := store.UpsertProvider(modelproviders.Config{
		Alias:           "deepseek/deepseek-reasoner",
		Provider:        "deepseek",
		API:             modelproviders.APIDeepSeek,
		BaseURL:         "https://api.deepseek.com/v1",
		Model:           "deepseek-reasoner",
		ReasoningEffort: "none",
	}); err != nil {
		t.Fatalf("seed config failed: %v", err)
	}
	prompter := &stubChoicePrompter{
		choices: []string{"deepseek", "deepseek-reasoner"},
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
	}
	if c.reasoningEffort != "" {
		t.Fatalf("expected connect to clear configurable reasoning for fixed model, got %q", c.reasoningEffort)
	}
	settings := store.ModelRuntimeSettings("deepseek/deepseek-reasoner")
	if settings.ReasoningEffort != "" {
		t.Fatalf("expected persisted reasoning effort cleared, got %q", settings.ReasoningEffort)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("deepseek/deepseek-reasoner")
	if !ok {
		t.Fatal("expected connected model config")
	}
	if cfg.ReasoningEffort != "" {
		t.Fatalf("expected registered config reasoning effort cleared, got %q", cfg.ReasoningEffort)
	}
	if strings.Contains(out.String(), "credential_ref") {
		t.Fatalf("expected credential note removed, got %q", out.String())
	}
}

func TestHandleConnect_VolcengineStandardUsesManualModelWithoutDiscovery(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevRefresh := connectModelCatalogRefreshFn
	discoverCalled := false
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		discoverCalled = true
		return nil, errors.New("should not discover volcengine models")
	}
	connectModelCatalogRefreshFn = func(baseCtx context.Context) (modelcatalog.CatalogInitStatus, bool) {
		return modelcatalog.InitModelCatalogWithStatus(context.Background(), &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, io.EOF
			}),
		}, ""), true
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		connectModelCatalogRefreshFn = prevRefresh
	})

	store := &appConfigStore{path: filepath.Join(t.TempDir(), "config.json"), data: defaultAppConfig()}
	prompter := &stubChoicePrompter{
		choices: []string{"volcengine", connectVolcengineStandardValue},
		lines:   []string{"sk-test", "doubao-seed-2.0-pro"},
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
	}
	if discoverCalled {
		t.Fatal("expected standard volcengine connect to skip list_models discovery")
	}
	if c.modelAlias != "volcengine/doubao-seed-2.0-pro" {
		t.Fatalf("unexpected current model %q", c.modelAlias)
	}
}

func TestHandleConnect_TUIReportsTransientCatalogFallbackHint(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevRefresh := connectModelCatalogRefreshFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{Name: "deepseek-chat"}}, nil
	}
	connectModelCatalogRefreshFn = func(baseCtx context.Context) (modelcatalog.CatalogInitStatus, bool) {
		return modelcatalog.CatalogInitStatus{RemoteError: errors.New("models.dev timeout")}, true
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		connectModelCatalogRefreshFn = prevRefresh
	})

	store := &appConfigStore{path: filepath.Join(t.TempDir(), "config.json"), data: defaultAppConfig()}
	sender := &testSender{}
	prompter := &stubChoicePrompter{
		choices: []string{"deepseek", "deepseek-chat"},
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
		tuiSender:    sender,
	}

	_, err := handleConnect(c, nil)
	if err != nil {
		t.Fatalf("handleConnect failed: %v", err)
	}
	var hint tuievents.SetHintMsg
	var found bool
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.SetHintMsg)
		if !ok {
			continue
		}
		hint = msg
		found = true
	}
	if !found {
		t.Fatal("expected transient catalog fallback hint")
	}
	if hint.Hint != "models.dev unavailable; using bundled model snapshot" {
		t.Fatalf("unexpected hint text %q", hint.Hint)
	}
	if hint.ClearAfter <= 0 {
		t.Fatalf("expected auto-clearing hint, got %s", hint.ClearAfter)
	}
	if strings.Contains(out.String(), "models.dev unavailable") {
		t.Fatalf("expected no persistent fallback warning in TUI output, got %q", out.String())
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
		choices: []string{"openai", connectCustomModelValue, "yes", "low,high,minimal,xhigh"},
		lines:   []string{"sk-test", "gpt-custom", "", ""},
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
	if cfg.ContextWindowTokens != 128000 || cfg.MaxOutputTok != 8192 {
		t.Fatalf("unexpected advanced defaults %+v", cfg)
	}
	if strings.Contains(out.String(), "缺少完整能力定义") {
		t.Fatalf("expected advanced-model note removed, got %q", out.String())
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
	wantLevels := []string{"low", "high", "minimal", "xhigh"}
	if len(cfg.ReasoningLevels) != len(wantLevels) {
		t.Fatalf("unexpected reasoning levels %+v", cfg.ReasoningLevels)
	}
	for i := range wantLevels {
		if cfg.ReasoningLevels[i] != wantLevels[i] {
			t.Fatalf("unexpected reasoning levels %+v", cfg.ReasoningLevels)
		}
	}
}

func TestHandleConnect_NoDiscoveredModelsPromptsManualInputAndStripsProviderPrefix(t *testing.T) {
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
		choices: []string{"openai-compatible", connectCustomModelValue},
		lines:   []string{"https://example.invalid/v1", "sk-test", "openai-compatible/gpt-4o-mini"},
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
	cfg, ok := c.modelFactory.ConfigForAlias("openai-compatible/gpt-4o-mini")
	if !ok {
		t.Fatal("expected model to be registered from manual input")
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Fatalf("expected stripped model name, got %q", cfg.Model)
	}
}

func TestHandleConnect_AnthropicCompatiblePromptsBaseURLAndManualModel(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{
			Name:                "test-model",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     8192,
			Capabilities:        []string{"reasoning", "tools"},
		}}, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"anthropic-compatible", "test-model", reasoningModeNone},
		lines:   []string{"https://example.invalid/anthropic", "sk-test"},
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
	cfg, ok := c.modelFactory.ConfigForAlias("anthropic-compatible/test-model")
	if !ok {
		t.Fatal("expected anthropic-compatible model to be registered")
	}
	if cfg.API != modelproviders.APIAnthropicCompatible {
		t.Fatalf("expected anthropic-compatible api, got %q", cfg.API)
	}
	if cfg.BaseURL != "https://example.invalid/anthropic" {
		t.Fatalf("unexpected base_url %q", cfg.BaseURL)
	}
	if cfg.Model != "test-model" {
		t.Fatalf("unexpected model %q", cfg.Model)
	}
	if len(prompter.readPrompts) < 2 || !strings.HasPrefix(prompter.readPrompts[0], "base_url") || !strings.HasPrefix(prompter.readPrompts[1], "api_key") {
		t.Fatalf("expected base_url then api_key prompts, got %q", prompter.readPrompts)
	}
}

func TestHandleConnect_MiniMaxUsesBundledModelsWhenDiscoveryFails(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return nil, fmt.Errorf("http status 404")
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	tpl, ok := findProviderTemplate("minimax")
	if !ok || len(tpl.commonModels) == 0 {
		t.Fatal("expected minimax provider template with bundled models")
	}
	selectedModel := tpl.commonModels[0]

	prompter := &stubChoicePrompter{
		choices: []string{"minimax", selectedModel, "yes", "low,medium,high"},
		lines:   []string{"sk-test", "204800", "8192"},
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("minimax/" + strings.ToLower(selectedModel))
	if !ok {
		t.Fatal("expected minimax model to be registered")
	}
	if cfg.API != modelproviders.APIAnthropicCompatible {
		t.Fatalf("expected minimax to use anthropic-compatible api, got %q", cfg.API)
	}
	if cfg.BaseURL != "https://api.minimaxi.com/anthropic" {
		t.Fatalf("unexpected minimax base_url %q", cfg.BaseURL)
	}
	if cfg.Model != selectedModel {
		t.Fatalf("unexpected minimax model %q", cfg.Model)
	}
	for _, prompt := range prompter.readPrompts {
		if prompt == "base_url" {
			t.Fatalf("did not expect minimax to prompt for base_url: %q", prompter.readPrompts)
		}
	}
}

func TestHandleConnect_OpenRouterUsesDefaultBaseURL(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return nil, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"openrouter", connectCustomModelValue},
		lines:   []string{"sk-test", "openai/gpt-4o-mini"},
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
	cfg, ok := c.modelFactory.ConfigForAlias("openrouter/openai/gpt-4o-mini")
	if !ok {
		t.Fatal("expected openrouter model to be registered")
	}
	if cfg.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("expected default openrouter base url, got %q", cfg.BaseURL)
	}
	if cfg.API != modelproviders.APIOpenRouter {
		t.Fatalf("expected openrouter api type, got %q", cfg.API)
	}
	if cfg.Model != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected openrouter model name %q", cfg.Model)
	}
}

func TestHandleConnect_OpenRouterDiscoveredNativeModelPreservesModelID(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{
			Name:                "openrouter/healer-alpha",
			ContextWindowTokens: 262144,
			MaxOutputTokens:     65536,
			Capabilities:        []string{"reasoning", "tools", "response_format"},
		}}, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"openrouter", "openrouter/healer-alpha", "none,minimal,low,medium,high,xhigh"},
		lines:   []string{"sk-test"},
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("openrouter/healer-alpha")
	if !ok {
		t.Fatal("expected native openrouter model to be registered")
	}
	if cfg.Model != "openrouter/healer-alpha" {
		t.Fatalf("expected native openrouter model id preserved, got %q", cfg.Model)
	}
	if cfg.ContextWindowTokens != 262144 || cfg.MaxOutputTok != 65536 {
		t.Fatalf("expected remote limits used as first-hand metadata, got ctx=%d out=%d", cfg.ContextWindowTokens, cfg.MaxOutputTok)
	}
	if len(prompter.readPrompts) != 1 {
		t.Fatalf("expected only api_key text prompt when remote metadata is available, got %q", prompter.readPrompts)
	}
}

func TestHandleConnect_OpenRouterRemotePartialCapabilitiesFallsBackToManualReasoning(t *testing.T) {
	const modelName = "codex-openrouter-partial-capability-fallback"
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{
			Name:                modelName,
			ContextWindowTokens: 262144,
			MaxOutputTokens:     65536,
		}}, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"openrouter", modelName, "yes", "none,minimal,low,medium,high,xhigh"},
		lines:   []string{"sk-test"},
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
		t.Fatalf("handleConnect failed: %v (choiceI=%d lineI=%d choicePrompts=%q multiPrompts=%q readPrompts=%q)", err, prompter.choiceI, prompter.lineI, prompter.choicePrompts, prompter.multiPrompts, prompter.readPrompts)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("openrouter/" + modelName)
	if !ok {
		t.Fatal("expected native openrouter model to be registered")
	}
	if cfg.ContextWindowTokens != 262144 || cfg.MaxOutputTok != 65536 {
		t.Fatalf("expected remote token limits preserved, got ctx=%d out=%d", cfg.ContextWindowTokens, cfg.MaxOutputTok)
	}
	if cfg.ReasoningMode != reasoningModeEffort {
		t.Fatalf("expected reasoning mode from manual fallback, got %q", cfg.ReasoningMode)
	}
	if !strings.Contains(strings.Join(prompter.choicePrompts, "\n"), "Does this model support reasoning? for "+modelName) {
		t.Fatalf("expected reasoning manual prompt when remote capabilities are partial, got %q", prompter.choicePrompts)
	}
}

func TestHandleConnect_UsesModelScopedAdvancedPrompts(t *testing.T) {
	const modelName = "codex-test-unknown-openrouter-model"

	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return nil, nil
	}
	initModelCatalogFn = func(baseCtx context.Context) modelcatalog.CatalogInitStatus {
		return modelcatalog.CatalogInitStatus{}
	}
	t.Cleanup(func() {
		discoverModelsFn = prevDiscover
		initModelCatalogFn = prevInit
	})

	prompter := &stubChoicePrompter{
		choices: []string{"openrouter", connectCustomModelValue, "yes", "none,minimal,low,medium,high,xhigh"},
		lines:   []string{"sk-test", modelName, "", "", ""},
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
	joinedReads := strings.Join(prompter.readPrompts, "\n")
	joinedChoices := strings.Join(prompter.choicePrompts, "\n")
	joinedMulti := strings.Join(prompter.multiPrompts, "\n")
	if !strings.Contains(joinedReads, "context_window_tokens for "+modelName+"(k)") {
		t.Fatalf("expected context prompt scoped to model, got %q", joinedReads)
	}
	if !strings.Contains(joinedReads, "max_output_tokens for "+modelName+"(k)") {
		t.Fatalf("expected max output prompt scoped to model, got %q", joinedReads)
	}
	if !strings.Contains(joinedChoices, "Does this model support reasoning? for "+modelName) {
		t.Fatalf("expected reasoning support prompt scoped to model, got %q", joinedChoices)
	}
	if !strings.Contains(joinedMulti, "Select supported reasoning_effort values for "+modelName) {
		t.Fatalf("expected effort prompt scoped to model, got %q", joinedMulti)
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

func TestConnectReasoningCapabilityChoices_UsesUserFacingLabels(t *testing.T) {
	choices := connectReasoningCapabilityChoices()
	if len(choices) != 3 {
		t.Fatalf("unexpected choices: %+v", choices)
	}
	if choices[0].Label != "None" || choices[1].Label != "Toggle" || choices[2].Label != "Effort levels" {
		t.Fatalf("unexpected labels: %+v", choices)
	}
	if choices[0].Value != reasoningModeNone || choices[1].Value != reasoningModeToggle || choices[2].Value != reasoningModeEffort {
		t.Fatalf("unexpected values: %+v", choices)
	}
}

func TestParseTokenCountInput(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{input: "128k", want: 128 * 1024},
		{input: "128", want: 128 * 1024},
		{input: "32768", want: 32768},
		{input: "1m", want: 1024 * 1024},
	}
	for _, tc := range cases {
		got, err := parseTokenCountInput(tc.input)
		if err != nil {
			t.Fatalf("parseTokenCountInput(%q) failed: %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("parseTokenCountInput(%q)=%d want %d", tc.input, got, tc.want)
		}
	}
}

func TestFormatTokenCountDefault(t *testing.T) {
	if got := formatTokenCountDefault(128 * 1024); got != "128k" {
		t.Fatalf("unexpected formatted default %q", got)
	}
	if got := formatTokenCountDefault(32768); got != "32k" {
		t.Fatalf("unexpected formatted default %q", got)
	}
	if got := formatTokenCountDefault(12345); got != "12345" {
		t.Fatalf("unexpected formatted default %q", got)
	}
}
