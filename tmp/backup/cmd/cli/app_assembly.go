package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appprompting "github.com/OnslaughtSnail/caelis/internal/app/prompting"
	"github.com/OnslaughtSnail/caelis/internal/app/storage/localstore"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/pkg/acpagent"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
)

type buildAgentInput struct {
	AppName                     string
	PromptRole                  string
	WorkspaceDir                string
	EnableExperimentalLSPPrompt bool
	BasePrompt                  string
	FrozenPrompt                string
	SkillDirs                   []string
	MainAgent                   string
	DefaultAgent                string
	AgentDescriptors            []appagents.Descriptor
	StreamModel                 bool
	ThinkingBudget              int
	ReasoningEffort             string
	ModelProvider               string
	ModelName                   string
	ModelConfig                 modelproviders.Config
	WorkspaceRoot               string
	ExecutionRuntime            toolexec.Runtime
	AppVersion                  string
}

const (
	promptRoleMainSession = "main_session"
	promptRoleACPServer   = "acp_server"
)

func buildAgent(in buildAgentInput) (*llmagent.Agent, error) {
	systemPrompt, err := resolveSystemPrompt(in)
	if err != nil {
		return nil, err
	}
	return buildResolvedLLMAgent(in, systemPrompt)
}

func buildResolvedLLMAgent(in buildAgentInput, systemPrompt string) (*llmagent.Agent, error) {
	reasoning, err := parseReasoningEffortForConfig(in.ReasoningEffort, in.ThinkingBudget, in.ModelProvider, in.ModelName, in.ModelConfig)
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name:              "main",
		SystemPrompt:      systemPrompt,
		StreamModel:       in.StreamModel,
		Reasoning:         reasoning,
		EmitPartialEvents: in.StreamModel,
	})
}

func buildMainSessionAgent(in buildAgentInput) (agent.Agent, error) {
	desc, usesACP, err := resolveMainSessionAgentDescriptor(in)
	if err != nil {
		return nil, err
	}
	if !usesACP {
		systemPrompt, err := resolveSystemPrompt(in)
		if err != nil {
			return nil, err
		}
		return buildResolvedLLMAgent(in, systemPrompt)
	}
	// ACP main sessions follow the remote server's prompt contract and do not
	// receive locally assembled system prompts.
	return acpagent.New(acpagent.Config{
		ID:                desc.ID,
		Name:              desc.Name,
		Command:           desc.Command,
		Args:              append([]string(nil), desc.Args...),
		Env:               copyStringMap(desc.Env),
		WorkDir:           desc.WorkDir,
		WorkspaceRoot:     firstNonEmptyString(in.WorkspaceRoot, in.WorkspaceDir),
		SessionCWD:        in.WorkspaceDir,
		Runtime:           in.ExecutionRuntime,
		ClientInfoVersion: in.AppVersion,
	})
}

func resolveMainSessionAgentDescriptor(in buildAgentInput) (appagents.Descriptor, bool, error) {
	mainAgent := strings.TrimSpace(strings.ToLower(in.MainAgent))
	if mainAgent == "" || mainAgent == "self" {
		return appagents.Descriptor{}, false, nil
	}
	reg := appagents.NewRegistry(in.AgentDescriptors...)
	desc, ok := reg.Lookup(mainAgent)
	if !ok {
		return appagents.Descriptor{}, false, fmt.Errorf("unknown mainAgent %q; add it under config.agents or reset mainAgent to self", mainAgent)
	}
	if desc.Transport != appagents.TransportACP {
		return desc, false, nil
	}
	return desc, true, nil
}

func resolveSystemPrompt(in buildAgentInput) (string, error) {
	if frozen := strings.TrimSpace(in.FrozenPrompt); frozen != "" {
		return frozen, nil
	}
	promptInput, err := buildPromptAssembleSpec(in)
	if err != nil {
		return "", err
	}
	assembled, err := appprompting.Assemble(promptInput.Spec)
	if err != nil {
		return "", err
	}
	for _, warn := range promptInput.Warnings {
		fmt.Fprintf(os.Stderr, "warn: %v\n", warn)
	}
	return assembled.Prompt, nil
}

func splitCSV(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func appendProviderIfMissing(providers []string, name string) []string {
	if includesProvider(providers, name) {
		return providers
	}
	return append(providers, name)
}

func includesProvider(providers []string, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, provider := range providers {
		if strings.TrimSpace(provider) == name {
			return true
		}
	}
	return false
}

func resolveProviderName(factory *modelproviders.Factory, alias string) string {
	if factory == nil || alias == "" {
		return ""
	}
	cfg, ok := factory.ConfigForAlias(alias)
	if !ok {
		return ""
	}
	return cfg.Provider
}

func resolveModelName(factory *modelproviders.Factory, alias string) string {
	if factory == nil || alias == "" {
		return ""
	}
	cfg, ok := factory.ConfigForAlias(alias)
	if !ok {
		return ""
	}
	return cfg.Model
}

func buildRuntimePromptHint(execRuntime toolexec.Runtime) string {
	if execRuntime == nil {
		return ""
	}
	mode := strings.TrimSpace(string(execRuntime.PermissionMode()))
	if mode == "" {
		return ""
	}
	lines := []string{
		"## Runtime Execution",
		"- Informational runtime hints; higher-priority instructions may override.",
	}
	if policyHint := runtimePolicyHint(execRuntime.SandboxPolicy()); policyHint != "" {
		lines = append(lines, "- "+policyHint)
	}
	switch execRuntime.PermissionMode() {
	case toolexec.PermissionModeFullControl:
		lines = append(lines, "- permission_mode=full_control route=host")
		lines = append(lines, "- Rule: BASH commands run on host directly with no approval gate.")
	default:
		lines = append(lines, fmt.Sprintf("- permission_mode=default sandbox_type=%s", sandboxTypeDisplayLabel(execRuntime.SandboxType())))
		if execRuntime.FallbackToHost() {
			lines = append(lines, "- Rule: sandbox is unavailable; all BASH commands require approval then run on host.")
			if reason := strings.TrimSpace(execRuntime.FallbackReason()); reason != "" {
				lines = append(lines, fmt.Sprintf("- Fallback reason: %s", truncateInline(reason, 160)))
			}
			lines = append(lines, "- Escalation: use require_escalated=true only when sandbox limits are blocking a necessary next step.")
		} else {
			lines = append(lines, "- Rule: commands run in sandbox by default; use require_escalated=true only when sandbox limits are blocking a necessary next step.")
			lines = append(lines, "- Escalate for cases like browser/GUI launch, downloads that sandbox blocks, or writes/access outside sandbox; do not escalate preemptively.")
			lines = append(lines, "- Safe inspection commands may auto-pass host escalation without user approval.")
		}
	}
	return strings.Join(lines, "\n")
}

func runtimePolicyHint(policy toolexec.SandboxPolicy) string {
	policyType := strings.TrimSpace(string(policy.Type))
	if policyType == "" {
		return ""
	}
	network := "off"
	if policy.NetworkAccess {
		network = "on"
	}
	return fmt.Sprintf(
		"sandbox_policy=%s network=%s readable_roots=%s writable_roots=%s read_only_subpaths=%s",
		policyType,
		network,
		csvOrDash(policy.ReadableRoots),
		csvOrDash(policy.WritableRoots),
		csvOrDash(policy.ReadOnlySubpaths),
	)
}

func csvOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	filtered := make([]string, 0, len(items))
	for _, one := range items {
		trimmed := strings.TrimSpace(one)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	if len(filtered) == 0 {
		return "-"
	}
	return strings.Join(filtered, ",")
}

func nextConversationSessionID() string {
	return idutil.NewSessionID()
}

func flagProvided(args []string, flagName string) bool {
	flagName = strings.TrimSpace(flagName)
	if flagName == "" {
		return false
	}
	short := "-" + flagName
	long := "--" + flagName
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == short || trimmed == long {
			return true
		}
		if strings.HasPrefix(trimmed, short+"=") || strings.HasPrefix(trimmed, long+"=") {
			return true
		}
	}
	return false
}

func rejectRemovedExecutionFlags(args []string) error {
	removed := map[string]string{
		"exec-mode":      "-permission-mode",
		"bash-strategy":  "-permission-mode",
		"bash-allowlist": "sandbox policy and host escalation approval flow",
		"bash-deny-meta": "-permission-mode",
	}
	for _, arg := range args {
		for flagName, replacement := range removed {
			short := "-" + flagName
			long := "--" + flagName
			if arg == short || arg == long || strings.HasPrefix(arg, short+"=") || strings.HasPrefix(arg, long+"=") {
				return fmt.Errorf("flag %q has been removed, use %s instead", flagName, replacement)
			}
		}
	}
	return nil
}

func hasLSPTools(tools []tool.Tool) bool {
	for _, one := range tools {
		if one == nil {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(one.Name())), "LSP_") {
			return true
		}
	}
	return false
}

func parseReasoning(mode string, budget int, effort string, provider string, modelName string) (model.ReasoningConfig, error) {
	return parseReasoningForConfig(mode, budget, effort, provider, modelName, modelproviders.Config{})
}

func parseReasoningForConfig(mode string, budget int, effort string, provider string, modelName string, providerCfg modelproviders.Config) (model.ReasoningConfig, error) {
	rawEffort := normalizeReasoningLevel(effort)
	selection := normalizeReasoningSelection(mode)
	selectionCfg := providerCfg
	if strings.TrimSpace(selectionCfg.Provider) == "" {
		selectionCfg.Provider = provider
	}
	if strings.TrimSpace(selectionCfg.Model) == "" {
		selectionCfg.Model = modelName
	}
	switch selection {
	case "", "auto":
	case "on":
		if rawEffort == "" {
			if opt, err := resolveModelReasoningOption(selectionCfg, "on"); err == nil {
				rawEffort = opt.ReasoningEffort
			}
		}
	case "off":
		rawEffort = "none"
	default:
		return model.ReasoningConfig{}, fmt.Errorf("invalid thinking-mode %q, expected auto|on|off", mode)
	}
	return parseReasoningEffortForConfig(rawEffort, budget, provider, modelName, providerCfg)
}

func parseReasoningEffortForConfig(effort string, budget int, provider string, modelName string, providerCfg modelproviders.Config) (model.ReasoningConfig, error) {
	cfg := model.ReasoningConfig{Effort: normalizeReasoningLevel(effort)}
	if budget > 0 {
		cfg.BudgetTokens = budget
	}
	profile := reasoningProfileForConfig(providerCfg)
	if profile.Mode == reasoningModeNone {
		profile = reasoningProfileForModel(provider, modelName)
	}
	switch profile.Mode {
	case reasoningModeNone:
		cfg.Effort = ""
		cfg.BudgetTokens = 0
	case reasoningModeFixed:
		cfg.Effort = ""
		cfg.BudgetTokens = 0
	case reasoningModeToggle:
		switch cfg.Effort {
		case "":
			if cfg.Effort == "" {
				cfg.BudgetTokens = 0
			}
		case "none":
			cfg.BudgetTokens = 0
		default:
			if profile.DefaultEffort != "" {
				cfg.Effort = profile.DefaultEffort
			} else {
				cfg.Effort = "medium"
			}
		}
	case reasoningModeEffort:
		if cfg.Effort == "none" {
			if len(profile.SupportedEfforts) > 0 && !catalogSupportsReasoningEffortList(profile.SupportedEfforts, "none") {
				cfg.Effort = profile.DefaultEffort
			} else {
				cfg.BudgetTokens = 0
				break
			}
		}
		if cfg.Effort != "" && !catalogSupportsReasoningEffortList(profile.SupportedEfforts, cfg.Effort) {
			cfg.Effort = profile.DefaultEffort
		}
	}
	return cfg, nil
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func buildModelFactory(configStore *appConfigStore, credentials *credentialStore) *modelproviders.Factory {
	factory := modelproviders.NewFactory()
	for _, providerCfg := range configStore.ProviderConfigs() {
		providerCfg = hydrateProviderAuthToken(providerCfg, credentials)
		modelcatalogApplyConfigDefaults(&providerCfg)
		if registerErr := factory.Register(providerCfg); registerErr != nil {
			fmt.Fprintf(os.Stderr, "warn: skip provider %q: %v\n", providerCfg.Alias, registerErr)
		}
	}
	return factory
}

func resolveModelAliasFromConfig(alias string, configStore *appConfigStore) string {
	alias = strings.TrimSpace(strings.ToLower(alias))
	if alias == "" {
		alias = configStore.DefaultModel()
	}
	if configStore != nil && alias != "" {
		alias = configStore.ResolveModelAlias(alias)
	}
	return alias
}

type sessionRuntimeResult struct {
	Store      session.Store
	TaskStore  task.Store
	Index      *sessionIndex
	DB         *localstore.Database
	Runtime    *runtime.Runtime
	ACPStore   session.Store
	ACPRuntime *runtime.Runtime
}

func openLocalStore(ctx context.Context, storeDir, sessionIndexFile string) (*localstore.Database, error) {
	_ = ctx
	return localstore.Open(storeDir, sessionIndexFile) //nolint:contextcheck // localstore.Open does not accept a context.
}

func newSessionIndexWithDBContext(ctx context.Context, path string, db *sql.DB) (*sessionIndex, error) {
	_ = ctx
	return newSessionIndexWithDB(path, db) //nolint:contextcheck // session index initialization does not support context propagation.
}

func setupSessionRuntime(ctx context.Context, storeDir, workspaceKey, _, _ string, sessionIndexFile string, compactWatermark float64, workspace workspaceContext) (*sessionRuntimeResult, error) {
	db, err := openLocalStore(ctx, storeDir, sessionIndexFile)
	if err != nil {
		return nil, err
	}
	index, err := newSessionIndexWithDBContext(ctx, sessionIndexFile, db.SQLDB())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	mainStore := db.Scope(localstore.Workspace{Key: workspaceKey, CWD: workspace.CWD}, localstore.ScopeMain)
	if err := mainStore.Backfill(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warn: backfill local session catalog failed: %v\n", err)
	}
	acpStore := db.Scope(localstore.Workspace{Key: workspaceKey, CWD: workspace.CWD}, localstore.ScopeACPRemote)
	if err := acpStore.Backfill(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warn: backfill ACP session catalog failed: %v\n", err)
	}
	rt, err := runtime.New(runtime.Config{
		LogStore:   mainStore,
		StateStore: mainStore,
		TaskStore:  mainStore,
		Compaction: runtime.CompactionConfig{
			WatermarkRatio: compactWatermark,
		},
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	acpRuntime, err := runtime.New(runtime.Config{
		LogStore:   acpStore,
		StateStore: acpStore,
		TaskStore:  acpStore,
		Compaction: runtime.CompactionConfig{
			WatermarkRatio: compactWatermark,
		},
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sessionRuntimeResult{
		Store:      mainStore,
		TaskStore:  mainStore,
		Index:      index,
		DB:         db,
		Runtime:    rt,
		ACPStore:   acpStore,
		ACPRuntime: acpRuntime,
	}, nil
}
