package gateway

import (
	"context"
	"fmt"
	"strings"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

type ModelResolution struct {
	Model                  sdkmodel.LLM
	ReasoningEffort        string
	DefaultReasoningEffort string
}

type ModelLookup interface {
	ResolveModel(context.Context, string, int) (ModelResolution, error)
}

type AssemblyResolverConfig struct {
	Sessions interface {
		SnapshotState(context.Context, sdksession.SessionRef) (map[string]any, error)
	}
	Assembly          sdkplugin.ResolvedAssembly
	DefaultModelAlias string
	ContextWindow     int
	ModelLookup       ModelLookup
	Tools             []sdktool.Tool
	AgentName         string
	BaseMetadata      map[string]any
}

type AssemblyResolver struct {
	sessions interface {
		SnapshotState(context.Context, sdksession.SessionRef) (map[string]any, error)
	}
	assembly          sdkplugin.ResolvedAssembly
	defaultModelAlias string
	contextWindow     int
	modelLookup       ModelLookup
	tools             []sdktool.Tool
	agentName         string
	baseMetadata      map[string]any
}

func NewAssemblyResolver(cfg AssemblyResolverConfig) (*AssemblyResolver, error) {
	if cfg.ModelLookup == nil {
		return nil, fmt.Errorf("gateway: model lookup is required")
	}
	agentName := strings.TrimSpace(cfg.AgentName)
	if agentName == "" {
		agentName = "main"
	}
	return &AssemblyResolver{
		sessions:          cfg.Sessions,
		assembly:          sdkplugin.CloneResolvedAssembly(cfg.Assembly),
		defaultModelAlias: strings.TrimSpace(cfg.DefaultModelAlias),
		contextWindow:     cfg.ContextWindow,
		modelLookup:       cfg.ModelLookup,
		tools:             append([]sdktool.Tool(nil), cfg.Tools...),
		agentName:         agentName,
		baseMetadata:      cloneMap(cfg.BaseMetadata),
	}, nil
}

func (r *AssemblyResolver) ResolveTurn(ctx context.Context, intent TurnIntent) (ResolvedTurn, error) {
	alias := strings.TrimSpace(intent.ModelHint)
	if alias == "" {
		alias = r.defaultModelAlias
	}
	model, err := r.modelLookup.ResolveModel(ctx, alias, r.contextWindow)
	if err != nil {
		return ResolvedTurn{}, err
	}

	state, err := r.snapshotState(ctx, intent.SessionRef)
	if err != nil {
		return ResolvedTurn{}, err
	}
	metadata, err := r.resolveMetadata(intent, state, model)
	if err != nil {
		return ResolvedTurn{}, err
	}

	return ResolvedTurn{
		RunRequest: sdkruntime.RunRequest{
			SessionRef:   intent.SessionRef,
			Input:        intent.Input,
			ContentParts: append([]sdkmodel.ContentPart(nil), intent.ContentParts...),
			AgentSpec: sdkruntime.AgentSpec{
				Name:     r.agentName,
				Model:    model.Model,
				Tools:    append([]sdktool.Tool(nil), r.tools...),
				Metadata: metadata,
			},
		},
	}, nil
}

func (r *AssemblyResolver) snapshotState(ctx context.Context, ref sdksession.SessionRef) (map[string]any, error) {
	if r == nil || r.sessions == nil || strings.TrimSpace(ref.SessionID) == "" {
		return nil, nil
	}
	state, err := r.sessions.SnapshotState(ctx, ref)
	if err != nil {
		return nil, wrapSessionError(err)
	}
	return state, nil
}

func (r *AssemblyResolver) resolveMetadata(intent TurnIntent, state map[string]any, model ModelResolution) (map[string]any, error) {
	metadata := cloneMap(r.baseMetadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if err := applyAssemblySelections(metadata, r.assembly, strings.TrimSpace(intent.ModeName), state); err != nil {
		return nil, err
	}
	if reasoning := firstNonEmptyString(
		stringMetadata(metadata, "reasoning_effort"),
		model.ReasoningEffort,
		model.DefaultReasoningEffort,
	); reasoning != "" {
		metadata["reasoning_effort"] = reasoning
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	return metadata, nil
}

func applyAssemblySelections(metadata map[string]any, assembly sdkplugin.ResolvedAssembly, requestedMode string, state map[string]any) error {
	if len(assembly.Modes) == 0 && len(assembly.Configs) == 0 {
		return nil
	}

	modeID := requestedMode
	if modeID == "" {
		modeID = sdkplugin.CurrentModeID(state)
	}
	if modeID == "" {
		modeID = defaultAssemblyModeID(assembly)
	}
	if modeID != "" {
		mode, ok := sdkplugin.LookupMode(assembly, modeID)
		if !ok {
			return &Error{
				Kind:        KindValidation,
				Code:        CodeInvalidRequest,
				UserVisible: true,
				Message:     fmt.Sprintf("gateway: unknown mode %q", modeID),
			}
		}
		applyRuntimeOverrides(metadata, mode.Runtime)
	}

	for _, selection := range assemblyConfigSelections(assembly, state) {
		option, ok := sdkplugin.LookupConfigSelectOption(assembly, selection.ID, selection.Value)
		if !ok {
			continue
		}
		applyRuntimeOverrides(metadata, option.Runtime)
	}
	return nil
}

type assemblyConfigSelection struct {
	ID    string
	Value string
}

func assemblyConfigSelections(assembly sdkplugin.ResolvedAssembly, state map[string]any) []assemblyConfigSelection {
	selected := sdkplugin.CurrentConfigValues(state)
	out := make([]assemblyConfigSelection, 0, len(assembly.Configs))
	for _, config := range assembly.Configs {
		configID := strings.TrimSpace(config.ID)
		if configID == "" {
			continue
		}
		value := assemblyConfigValue(config, strings.TrimSpace(selected[configID]))
		if value == "" {
			continue
		}
		out = append(out, assemblyConfigSelection{ID: configID, Value: value})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func assemblyConfigValue(config sdkplugin.ConfigOption, selected string) string {
	if assemblyConfigHasValue(config, selected) {
		return selected
	}
	if def := strings.TrimSpace(config.DefaultValue); assemblyConfigHasValue(config, def) {
		return def
	}
	for _, option := range config.Options {
		if value := strings.TrimSpace(option.Value); value != "" {
			return value
		}
	}
	return ""
}

func assemblyConfigHasValue(config sdkplugin.ConfigOption, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range config.Options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

func defaultAssemblyModeID(assembly sdkplugin.ResolvedAssembly) string {
	for _, one := range assembly.Modes {
		if strings.EqualFold(strings.TrimSpace(one.ID), "default") {
			return strings.TrimSpace(one.ID)
		}
	}
	for _, one := range assembly.Modes {
		if trimmed := strings.TrimSpace(one.ID); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func applyRuntimeOverrides(metadata map[string]any, overrides sdkplugin.RuntimeOverrides) {
	if metadata == nil {
		return
	}
	if trimmed := strings.TrimSpace(overrides.PolicyMode); trimmed != "" {
		metadata["policy_mode"] = trimmed
	}
	if trimmed := strings.TrimSpace(overrides.SystemPrompt); trimmed != "" {
		metadata["system_prompt"] = trimmed
	}
	if trimmed := strings.TrimSpace(overrides.Reasoning.Effort); trimmed != "" {
		metadata["reasoning_effort"] = trimmed
	}
	if overrides.Reasoning.BudgetTokens > 0 {
		metadata["reasoning_budget_tokens"] = overrides.Reasoning.BudgetTokens
	}
	if len(overrides.ExtraReadRoots) > 0 {
		metadata["policy_extra_read_roots"] = mergeStringSliceMetadata(metadata["policy_extra_read_roots"], overrides.ExtraReadRoots)
	}
	if len(overrides.ExtraWriteRoots) > 0 {
		metadata["policy_extra_write_roots"] = mergeStringSliceMetadata(metadata["policy_extra_write_roots"], overrides.ExtraWriteRoots)
	}
}

func mergeStringSliceMetadata(existing any, values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	appendOne := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	switch typed := existing.(type) {
	case []string:
		for _, one := range typed {
			appendOne(one)
		}
	case []any:
		for _, one := range typed {
			text, _ := one.(string)
			appendOne(text)
		}
	}
	for _, one := range values {
		appendOne(one)
	}
	return out
}

func stringMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
