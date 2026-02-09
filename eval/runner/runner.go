package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/eval/cases"
	evalproviders "github.com/OnslaughtSnail/caelis/eval/providers"
	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

// Options controls eval runner behavior.
type Options struct {
	Suite           string
	Model           string
	Models          string
	StreamModes     string
	ThinkingModes   string
	ThinkingBudget  int
	ReasoningEffort string
}

type CaseResult struct {
	Model       string `json:"model"`
	Suite       string `json:"suite"`
	CaseName    string `json:"case_name"`
	Stream      bool   `json:"stream"`
	Thinking    string `json:"thinking"`
	Passed      bool   `json:"passed"`
	Error       string `json:"error,omitempty"`
	Latency     int64  `json:"latency_ms"`
	EventCount  int    `json:"event_count"`
	ToolInvokes int    `json:"tool_invokes"`
}

type Summary struct {
	Suite      string       `json:"suite"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	Results    []CaseResult `json:"results"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
}

func Run(ctx context.Context, opts Options) (*Summary, error) {
	suite := strings.ToLower(strings.TrimSpace(opts.Suite))
	if suite == "" {
		suite = "light"
	}
	modelAliases := resolveModelAliases(opts)
	streamModes := resolveStreamModes(opts.StreamModes)
	thinkingModes := resolveThinkingModes(opts.ThinkingModes)

	selectedCases := cases.Light()
	if suite == "nightly" {
		selectedCases = cases.Nightly()
	}

	summary := &Summary{Suite: suite, StartedAt: time.Now()}
	for _, baseAlias := range modelAliases {
		for _, streamMode := range streamModes {
			for _, thinkingMode := range thinkingModes {
				alias, reasoning := resolveReasoning(baseAlias, thinkingMode, opts.ThinkingBudget, opts.ReasoningEffort)
				llm, err := evalproviders.NewByAlias(alias)
				if err != nil {
					return nil, err
				}
				for _, c := range selectedCases {
					res := CaseResult{
						Model:    alias,
						Suite:    suite,
						CaseName: c.Name,
						Stream:   streamMode,
						Thinking: thinkingMode,
					}
					start := time.Now()
					events, toolCount, err := runOne(ctx, c, llm, streamMode, reasoning)
					res.Latency = time.Since(start).Milliseconds()
					res.EventCount = events
					res.ToolInvokes = toolCount
					if err != nil {
						res.Passed = false
						res.Error = err.Error()
						summary.Failed++
					} else {
						res.Passed = true
						summary.Passed++
					}
					summary.Results = append(summary.Results, res)
				}
			}
		}
	}
	summary.FinishedAt = time.Now()
	if err := writeReport(summary); err != nil {
		return nil, err
	}
	if summary.Failed > 0 {
		return summary, fmt.Errorf("eval: %d cases failed", summary.Failed)
	}
	return summary, nil
}

func runOne(ctx context.Context, c cases.Case, llm model.LLM, stream bool, reasoning model.ReasoningConfig) (int, int, error) {
	resolved, err := bootstrap.Assemble(ctx, bootstrap.AssembleSpec{
		ToolProviders:   []string{pluginbuiltin.ProviderLocalTools},
		PolicyProviders: []string{pluginbuiltin.ProviderDefaultPolicy},
	})
	if err != nil {
		return 0, 0, err
	}
	ag, err := llmagent.New(llmagent.Config{
		Name:         "eval-agent",
		SystemPrompt: "You are a reliable evaluator assistant.",
		MaxSteps:     8,
		StreamModel:  stream,
		Reasoning:    reasoning,
	})
	if err != nil {
		return 0, 0, err
	}
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		return 0, 0, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	for _, err := range rt.Run(runCtx, runtime.RunRequest{
		AppName:   "eval",
		UserID:    "eval-user",
		SessionID: c.Name,
		Input:     c.Prompt,
		Agent:     ag,
		Model:     llm,
		Tools:     resolved.Tools,
		Policies:  resolved.Policies,
	}) {
		if err != nil {
			return 0, 0, err
		}
	}
	events, err := store.ListEvents(runCtx, &session.Session{AppName: "eval", UserID: "eval-user", ID: c.Name})
	if err != nil {
		return 0, 0, err
	}
	toolCount := 0
	for _, ev := range events {
		if ev != nil && ev.Message.ToolResponse != nil {
			toolCount++
		}
	}
	if err := c.Validate(events); err != nil {
		return len(events), toolCount, err
	}
	return len(events), toolCount, nil
}

func writeReport(summary *Summary) error {
	reportDir := filepath.Join(".tmp", "reports")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102_150405")
	jsonPath := filepath.Join(reportDir, fmt.Sprintf("eval_%s_%s.json", summary.Suite, ts))
	mdPath := filepath.Join(reportDir, fmt.Sprintf("eval_%s_%s.md", summary.Suite, ts))

	raw, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Eval Summary (%s)\n\n", summary.Suite)
	fmt.Fprintf(&b, "- Started: %s\n", summary.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Finished: %s\n", summary.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Passed: %d\n", summary.Passed)
	fmt.Fprintf(&b, "- Failed: %d\n\n", summary.Failed)
	b.WriteString("| Model | Case | Stream | Thinking | Passed | Events | Tools | Latency(ms) | Error |\n")
	b.WriteString("| --- | --- | --- | --- | --- | ---: | ---: | ---: | --- |\n")
	for _, r := range summary.Results {
		fmt.Fprintf(&b, "| %s | %s | %t | %s | %t | %d | %d | %d | %s |\n",
			r.Model, r.CaseName, r.Stream, r.Thinking, r.Passed, r.EventCount, r.ToolInvokes, r.Latency, strings.ReplaceAll(r.Error, "|", "/"))
	}
	if err := os.WriteFile(mdPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return nil
}

func resolveModelAliases(opts Options) []string {
	if raw := strings.TrimSpace(opts.Models); raw != "" {
		parts := splitCSV(raw)
		if len(parts) > 0 {
			return parts
		}
	}
	if strings.TrimSpace(opts.Model) != "" {
		return []string{strings.TrimSpace(opts.Model)}
	}
	return evalproviders.DefaultModelAliases()
}

func resolveStreamModes(raw string) []bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "stream":
		return []bool{true}
	case "both":
		return []bool{false, true}
	default:
		return []bool{false}
	}
}

func resolveThinkingModes(raw string) []string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true":
		return []string{"on"}
	case "both":
		return []string{"off", "on"}
	default:
		return []string{"off"}
	}
}

func resolveReasoning(alias, mode string, budget int, effort string) (string, model.ReasoningConfig) {
	cfg := model.ReasoningConfig{
		Effort: strings.TrimSpace(effort),
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "on":
		enabled := true
		cfg.Enabled = &enabled
		if budget > 0 {
			cfg.BudgetTokens = budget
		}
		if strings.HasPrefix(strings.ToLower(alias), "deepseek") && cfg.Effort == "" {
			cfg.Effort = "high"
		}
	case "off":
		enabled := false
		cfg.Enabled = &enabled
		cfg.BudgetTokens = 0
		if strings.EqualFold(alias, "deepseek-reasoner") {
			alias = "deepseek-chat"
		}
	}
	return alias, cfg
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
