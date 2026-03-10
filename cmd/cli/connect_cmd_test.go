package main

import (
	"bytes"
	"context"
	"errors"
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

func TestBuildConnectModelChoicesIncludesRemoteAndCustomOnly(t *testing.T) {
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
	if !foundChat || foundReasoner || !foundCustom {
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

func TestHandleConnect_ReinitializesReasoningDefaultsAndOmitsNotes(t *testing.T) {
	prevDiscover := discoverModelsFn
	prevInit := initModelCatalogFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		return []modelproviders.RemoteModel{{Name: "deepseek-chat"}}, nil
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
		Alias:        "deepseek/deepseek-chat",
		Provider:     "deepseek",
		API:          modelproviders.APIDeepSeek,
		BaseURL:      "https://api.deepseek.com/v1",
		Model:        "deepseek-chat",
		ThinkingMode: "off",
	}); err != nil {
		t.Fatalf("seed config failed: %v", err)
	}
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
	}

	_, err := handleConnect(c, nil)
	if err != nil {
		t.Fatalf("handleConnect failed: %v", err)
	}
	if c.thinkingMode != "on" {
		t.Fatalf("expected connect to initialize reasoning on, got %q", c.thinkingMode)
	}
	settings := store.ModelRuntimeSettings("deepseek/deepseek-chat")
	if settings.ThinkingMode != "on" {
		t.Fatalf("expected persisted thinking mode on, got %q", settings.ThinkingMode)
	}
	cfg, ok := c.modelFactory.ConfigForAlias("deepseek/deepseek-chat")
	if !ok {
		t.Fatal("expected connected model config")
	}
	if cfg.ThinkingMode != "on" {
		t.Fatalf("expected registered config thinking mode on, got %q", cfg.ThinkingMode)
	}
	if strings.Contains(out.String(), "credential_ref") {
		t.Fatalf("expected credential note removed, got %q", out.String())
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
		choices: []string{"openai", reasoningModeEffort, "low,high"},
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
		choices: []string{"openai-compatible"},
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
