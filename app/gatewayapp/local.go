package gatewayapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdkcompact "github.com/OnslaughtSnail/caelis/sdk/compact"
	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkminimax "github.com/OnslaughtSnail/caelis/sdk/model/providers/minimax"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdkpolicy "github.com/OnslaughtSnail/caelis/sdk/policy/presets"
	"github.com/OnslaughtSnail/caelis/sdk/runtime/agents/chat"
	localruntime "github.com/OnslaughtSnail/caelis/sdk/runtime/local"
	sdksandbox "github.com/OnslaughtSnail/caelis/sdk/sandbox"
	_ "github.com/OnslaughtSnail/caelis/sdk/sandbox/bwrap"
	_ "github.com/OnslaughtSnail/caelis/sdk/sandbox/host"
	_ "github.com/OnslaughtSnail/caelis/sdk/sandbox/landlock"
	_ "github.com/OnslaughtSnail/caelis/sdk/sandbox/seatbelt"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sessionfile "github.com/OnslaughtSnail/caelis/sdk/session/file"
	taskfile "github.com/OnslaughtSnail/caelis/sdk/task/file"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
	sdkbuiltin "github.com/OnslaughtSnail/caelis/sdk/tool/builtin"
	spawntool "github.com/OnslaughtSnail/caelis/sdk/tool/builtin/spawn"
)

type Config struct {
	AppName        string
	UserID         string
	StoreDir       string
	WorkspaceKey   string
	WorkspaceCWD   string
	PermissionMode string
	ContextWindow  int
	SystemPrompt   string
	Assembly       sdkplugin.ResolvedAssembly
	Model          ModelConfig
	Sandbox        SandboxConfig
}

type ModelConfig struct {
	Alias    string
	Provider string
	API      sdkproviders.APIType
	Model    string
	BaseURL  string
	// Token is an in-memory secret used for the current process. It is not
	// persisted unless PersistToken is explicitly enabled.
	Token    string
	TokenEnv string
	// PersistToken explicitly opts into persisting Token in plaintext config.
	// Prefer TokenEnv instead.
	PersistToken           bool
	AuthType               sdkproviders.AuthType
	HeaderKey              string
	ContextWindowTokens    int
	ReasoningEffort        string
	DefaultReasoningEffort string
	ReasoningLevels        []string
	ReasoningMode          string
	MaxOutputTok           int
	Timeout                time.Duration
}

type Stack struct {
	Gateway   *appgateway.Gateway
	Sessions  sdksession.Service
	AppName   string
	UserID    string
	Workspace sdksession.WorkspaceRef
	lookup    *modelLookup
	store     *appConfigStore
	storeDir  string
	mu        sync.RWMutex
	runtime   stackRuntimeConfig
	sandbox   SandboxConfig
	exec      sdksandbox.Runtime
	engine    *localruntime.Runtime
	taskStore *taskfile.Store
}

type SessionRuntimeState struct {
	ModelAlias  string
	SessionMode string
	SandboxMode string
}

type SandboxStatus struct {
	RequestedBackend string
	ResolvedBackend  string
	Route            string
	FallbackReason   string
	SecuritySummary  string
}

type ACPAgentInfo struct {
	Name        string
	Description string
}

type stackRuntimeConfig struct {
	PermissionMode string
	ContextWindow  int
	Assembly       sdkplugin.ResolvedAssembly
	BaseMetadata   map[string]any
}

func NewLocalStack(cfg Config) (*Stack, error) {
	appName := firstNonEmpty(strings.TrimSpace(cfg.AppName), "caelis")
	userID := firstNonEmpty(strings.TrimSpace(cfg.UserID), "local-user")
	workspaceCWD := firstNonEmpty(strings.TrimSpace(cfg.WorkspaceCWD), mustGetwd())
	workspaceKey := firstNonEmpty(strings.TrimSpace(cfg.WorkspaceKey), "workspace")
	storeDir := strings.TrimSpace(cfg.StoreDir)
	if storeDir == "" {
		storeDir = defaultStoreDir()
	}
	cfg.Assembly = withDefaultACPAgents(cfg.Assembly, defaultSelfACPAgent(defaultSelfACPAgentConfig{
		Config:       cfg,
		AppName:      appName,
		UserID:       userID,
		StoreDir:     storeDir,
		WorkspaceKey: workspaceKey,
		WorkspaceCWD: workspaceCWD,
	}))
	configStore := newAppConfigStore(storeDir)
	sessions := sessionfile.NewService(sessionfile.NewStore(sessionfile.Config{
		RootDir: filepath.Join(storeDir, "sessions"),
	}))
	taskStore := taskfile.NewStore(taskfile.Config{RootDir: filepath.Join(storeDir, "tasks")})
	lookup, err := newModelLookup(configStore, cfg.Model, cfg.ContextWindow)
	if err != nil {
		return nil, err
	}
	baseMetadata := map[string]any{}
	systemPrompt, err := buildSystemPrompt(promptConfig{
		AppName:      appName,
		WorkspaceDir: workspaceCWD,
		BasePrompt:   cfg.SystemPrompt,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(systemPrompt) != "" {
		baseMetadata["system_prompt"] = systemPrompt
	}
	if reasoning := strings.TrimSpace(cfg.Model.ReasoningEffort); reasoning != "" {
		baseMetadata["reasoning_effort"] = reasoning
	}
	doc, err := configStore.Load()
	if err != nil {
		return nil, err
	}
	stack := &Stack{
		Sessions: sessions,
		AppName:  appName,
		UserID:   userID,
		Workspace: sdksession.WorkspaceRef{
			Key: workspaceKey,
			CWD: workspaceCWD,
		},
		lookup:    lookup,
		store:     configStore,
		storeDir:  storeDir,
		taskStore: taskStore,
		runtime: stackRuntimeConfig{
			PermissionMode: cfg.PermissionMode,
			ContextWindow:  cfg.ContextWindow,
			Assembly:       sdkplugin.CloneResolvedAssembly(cfg.Assembly),
			BaseMetadata:   cloneMap(baseMetadata),
		},
		sandbox: mergeSandboxConfig(doc.Sandbox, cfg.Sandbox),
	}
	if err := stack.rebuildGateway(); err != nil {
		return nil, err
	}
	return stack, nil
}

func delegationAgentsFromAssembly(assembly sdkplugin.ResolvedAssembly) []sdkdelegation.Agent {
	out := make([]sdkdelegation.Agent, 0, len(assembly.Agents))
	for _, one := range assembly.Agents {
		agent := sdkdelegation.NormalizeAgent(sdkdelegation.Agent{
			Name:        one.Name,
			Description: one.Description,
		})
		if agent.Name == "" {
			continue
		}
		out = append(out, agent)
	}
	return out
}

func delegationAgentsForSpawn(assembly sdkplugin.ResolvedAssembly, participants []sdksession.ParticipantBinding) []sdkdelegation.Agent {
	if len(assembly.Agents) == 0 {
		return nil
	}
	available := map[string]sdkdelegation.Agent{}
	for _, agent := range delegationAgentsFromAssembly(assembly) {
		name := strings.ToLower(strings.TrimSpace(agent.Name))
		if name != "" {
			available[name] = agent
		}
	}
	out := make([]sdkdelegation.Agent, 0, len(participants)+1)
	seen := map[string]struct{}{}
	if self, ok := available["self"]; ok {
		out = append(out, self)
		seen["self"] = struct{}{}
	}
	for _, participant := range participants {
		if participant.Kind != sdksession.ParticipantKindACP {
			continue
		}
		if participant.Role != "" && participant.Role != sdksession.ParticipantRoleSidecar {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(participant.Label))
		if name == "" {
			continue
		}
		agent, ok := available[name]
		if !ok {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		out = append(out, agent)
		seen[name] = struct{}{}
	}
	return out
}

func systemPromptWithDelegationGuidance(systemPrompt string) string {
	systemPrompt = strings.TrimRight(strings.TrimSpace(systemPrompt), "\n")
	guidance := "- Delegation: use SPAWN for bounded child ACP work that can run independently. Use TASK wait for progress, TASK cancel to stop a running child, and TASK write only for a follow-up prompt after a SPAWN child has completed."
	if strings.Contains(systemPrompt, "SPAWN for bounded child ACP work") {
		return systemPrompt
	}
	if systemPrompt == "" {
		return guidance
	}
	return systemPrompt + "\n" + guidance
}

func withDefaultACPAgents(assembly sdkplugin.ResolvedAssembly, self sdkplugin.AgentConfig) sdkplugin.ResolvedAssembly {
	out := sdkplugin.CloneResolvedAssembly(assembly)
	seen := map[string]struct{}{}
	for _, agent := range out.Agents {
		name := strings.ToLower(strings.TrimSpace(agent.Name))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	if name := strings.ToLower(strings.TrimSpace(self.Name)); name != "" {
		if _, exists := seen[name]; !exists {
			out.Agents = append(out.Agents, self)
			seen[name] = struct{}{}
		}
	}
	for _, agent := range builtInACPAgents() {
		name := strings.ToLower(strings.TrimSpace(agent.Name))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		out.Agents = append(out.Agents, agent)
		seen[name] = struct{}{}
	}
	return out
}

type defaultSelfACPAgentConfig struct {
	Config       Config
	AppName      string
	UserID       string
	StoreDir     string
	WorkspaceKey string
	WorkspaceCWD string
}

func defaultSelfACPAgent(cfg defaultSelfACPAgentConfig) sdkplugin.AgentConfig {
	if cmd := strings.TrimSpace(os.Getenv("CAELIS_ACP_SELF_AGENT_CMD")); cmd != "" {
		name := strings.TrimSpace(os.Getenv("CAELIS_ACP_SELF_AGENT_NAME"))
		if name == "" {
			name = "self"
		}
		return sdkplugin.AgentConfig{
			Name:        name,
			Description: firstNonEmpty(strings.TrimSpace(os.Getenv("CAELIS_ACP_SELF_AGENT_DESC")), "Caelis self ACP agent"),
			Command:     "bash",
			Args:        []string{"-lc", cmd},
			WorkDir:     strings.TrimSpace(os.Getenv("CAELIS_ACP_SELF_AGENT_WORKDIR")),
		}
	}
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		executable = os.Args[0]
	}
	return sdkplugin.AgentConfig{
		Name:        "self",
		Description: "Caelis self ACP agent",
		Command:     executable,
		Args: append([]string{
			"acp",
			"-app", strings.TrimSpace(cfg.AppName),
			"-user", strings.TrimSpace(cfg.UserID),
			"-store-dir", strings.TrimSpace(cfg.StoreDir),
			"-workspace-key", strings.TrimSpace(cfg.WorkspaceKey),
			"-workspace-cwd", strings.TrimSpace(cfg.WorkspaceCWD),
			"-permission-mode", strings.TrimSpace(cfg.Config.PermissionMode),
		}, selfRuntimeArgs(cfg.Config)...),
	}
}

func selfRuntimeArgs(cfg Config) []string {
	args := []string{}
	appendFlag := func(name string, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name, strings.TrimSpace(value))
		}
	}
	model := cfg.Model
	appendFlag("-model-alias", model.Alias)
	appendFlag("-provider", model.Provider)
	appendFlag("-api", string(model.API))
	appendFlag("-model", model.Model)
	appendFlag("-base-url", model.BaseURL)
	appendFlag("-token", model.Token)
	appendFlag("-token-env", model.TokenEnv)
	appendFlag("-auth-type", string(model.AuthType))
	appendFlag("-header-key", model.HeaderKey)
	if cfg.ContextWindow > 0 {
		args = append(args, "-context-window", fmt.Sprintf("%d", cfg.ContextWindow))
	}
	if model.MaxOutputTok > 0 {
		args = append(args, "-max-output-tokens", fmt.Sprintf("%d", model.MaxOutputTok))
	}
	return args
}

func builtInACPAgents() []sdkplugin.AgentConfig {
	return []sdkplugin.AgentConfig{
		{
			Name:        "codex",
			Description: "OpenAI Codex ACP agent",
			Command:     "npx",
			Args:        []string{"-y", "@zed-industries/codex-acp"},
		},
		{
			Name:        "copilot",
			Description: "GitHub Copilot ACP agent",
			Command:     "copilot",
			Args:        []string{"--acp", "--stdio"},
		},
		{
			Name:        "gemini",
			Description: "Gemini ACP agent",
			Command:     "gemini",
			Args:        []string{"--acp"},
		},
	}
}

func defaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".caelis")
	}
	cwd := mustGetwd()
	return filepath.Join(cwd, ".caelis")
}

func (s *Stack) StartSession(ctx context.Context, preferredSessionID string, bindingKey string) (sdksession.Session, error) {
	if s == nil || s.Gateway == nil {
		return sdksession.Session{}, fmt.Errorf("gatewayapp: stack is unavailable")
	}
	return s.Gateway.StartSession(ctx, appgateway.StartSessionRequest{
		AppName:            s.AppName,
		UserID:             s.UserID,
		Workspace:          s.Workspace,
		PreferredSessionID: strings.TrimSpace(preferredSessionID),
		BindingKey:         strings.TrimSpace(bindingKey),
		Binding: appgateway.BindingDescriptor{
			Surface: strings.TrimSpace(bindingKey),
			Owner:   s.AppName,
		},
	})
}

// Connect reconfigures the model provider on the live stack. The new config
// takes effect for subsequent turns.
func (s *Stack) Connect(cfg ModelConfig) (string, error) {
	if s == nil || s.Gateway == nil {
		return "", fmt.Errorf("gatewayapp: stack is unavailable")
	}
	if err := s.rejectReconfigureWhileActive("connect model"); err != nil {
		return "", err
	}
	if s.lookup == nil {
		return "", fmt.Errorf("gatewayapp: model lookup unavailable")
	}
	resolver := s.Gateway.Resolver()
	if resolver == nil {
		return "", fmt.Errorf("gatewayapp: resolver not available")
	}
	alias, err := s.lookup.Upsert(cfg)
	if err != nil {
		return "", fmt.Errorf("gatewayapp: invalid model config: %w", err)
	}
	resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
	if err := s.saveModelConfigs(); err != nil {
		return "", err
	}
	return alias, nil
}

// UseModel persists one per-session model alias override for subsequent turns.
func (s *Stack) UseModel(ctx context.Context, ref sdksession.SessionRef, alias string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	if err := s.rejectReconfigureWhileActive("switch model"); err != nil {
		return err
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	if s.lookup != nil && !s.lookup.HasAlias(alias) {
		return fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	if s.lookup != nil {
		s.lookup.SetDefault(alias)
		if resolver := s.Gateway.Resolver(); resolver != nil {
			resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
		}
		if err := s.saveModelConfigs(); err != nil {
			return err
		}
	}
	return s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		next[appgateway.StateCurrentModelAlias] = alias
		return next, nil
	})
}

// DeleteModel clears one per-session model alias override when it matches the
// supplied alias. This reverts the session back to the resolver default.
func (s *Stack) DeleteModel(ctx context.Context, ref sdksession.SessionRef, alias string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	if err := s.rejectReconfigureWhileActive("delete model"); err != nil {
		return err
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	if s.lookup == nil {
		return fmt.Errorf("gatewayapp: model lookup unavailable")
	}
	if err := s.lookup.Delete(alias); err != nil {
		return err
	}
	if resolver := s.Gateway.Resolver(); resolver != nil {
		resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
	}
	if err := s.saveModelConfigs(); err != nil {
		return err
	}
	return s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		current, _ := next[appgateway.StateCurrentModelAlias].(string)
		if alias == "" || strings.EqualFold(strings.TrimSpace(current), alias) {
			delete(next, appgateway.StateCurrentModelAlias)
		}
		return next, nil
	})
}

// SetSessionMode persists one per-session execution mode override for
// subsequent turns and returns the normalized display label.
func (s *Stack) SetSessionMode(ctx context.Context, ref sdksession.SessionRef, mode string) (string, error) {
	if s == nil || s.Sessions == nil {
		return "", fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	if err := s.rejectReconfigureWhileActive("change session mode"); err != nil {
		return "", err
	}
	normalized, err := normalizeSessionMode(mode)
	if err != nil {
		return "", err
	}
	err = s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		next[appgateway.StateCurrentSessionMode] = normalized
		delete(next, appgateway.StateCurrentSandboxMode)
		return next, nil
	})
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func (s *Stack) CycleSessionMode(ctx context.Context, ref sdksession.SessionRef) (string, error) {
	state, err := s.SessionRuntimeState(ctx, ref)
	if err != nil {
		return "", err
	}
	next := nextSessionMode(state.SessionMode)
	return s.SetSessionMode(ctx, ref, next)
}

// SetSandboxMode is the legacy compatibility wrapper. New callers should use
// SetSessionMode for mode changes and SetSandboxBackend for backend changes.
func (s *Stack) SetSandboxMode(ctx context.Context, ref sdksession.SessionRef, mode string) (string, error) {
	return s.SetSessionMode(ctx, ref, mode)
}

func (s *Stack) SetSandboxBackend(_ context.Context, backend string) (SandboxStatus, error) {
	if s == nil {
		return SandboxStatus{}, fmt.Errorf("gatewayapp: stack is unavailable")
	}
	if err := s.rejectReconfigureWhileActive("change sandbox backend"); err != nil {
		return SandboxStatus{}, err
	}
	normalized, err := normalizeSandboxBackend(backend)
	if err != nil {
		return SandboxStatus{}, err
	}
	s.mu.Lock()
	previous := s.sandbox
	s.sandbox.RequestedType = normalized
	s.mu.Unlock()
	if err := s.rebuildGateway(); err != nil {
		s.mu.Lock()
		s.sandbox = previous
		s.mu.Unlock()
		return SandboxStatus{}, err
	}
	if err := s.saveSandboxConfig(); err != nil {
		return SandboxStatus{}, err
	}
	return s.SandboxStatus(), nil
}

// SessionRuntimeState returns the current per-session runtime overrides backed
// by session state.
func (s *Stack) SessionRuntimeState(ctx context.Context, ref sdksession.SessionRef) (SessionRuntimeState, error) {
	if s == nil || s.Sessions == nil {
		return SessionRuntimeState{}, fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	state, err := s.Sessions.SnapshotState(ctx, ref)
	if err != nil {
		return SessionRuntimeState{}, err
	}
	modelAlias := appgateway.CurrentModelAlias(state)
	if s.lookup != nil && modelAlias != "" && !s.lookup.HasAlias(modelAlias) {
		modelAlias = ""
	}
	return SessionRuntimeState{
		ModelAlias:  modelAlias,
		SessionMode: appgateway.CurrentSessionMode(state),
		SandboxMode: appgateway.CurrentSandboxMode(state),
	}, nil
}

func (s *Stack) SandboxStatus() SandboxStatus {
	if s == nil {
		return SandboxStatus{}
	}
	s.mu.RLock()
	cfg := s.sandbox
	exec := s.exec
	s.mu.RUnlock()
	status := SandboxStatus{
		RequestedBackend: cfg.RequestedType,
		Route:            string(sdksandbox.RouteSandbox),
		SecuritySummary:  "sandbox",
	}
	if status.RequestedBackend == "" {
		status.RequestedBackend = "auto"
	}
	if exec == nil {
		status.ResolvedBackend = status.RequestedBackend
		return status
	}
	rtStatus := exec.Status()
	if strings.TrimSpace(string(rtStatus.RequestedBackend)) != "" {
		status.RequestedBackend = string(rtStatus.RequestedBackend)
	}
	if strings.TrimSpace(string(rtStatus.ResolvedBackend)) != "" {
		status.ResolvedBackend = string(rtStatus.ResolvedBackend)
	}
	status.FallbackReason = strings.TrimSpace(rtStatus.FallbackReason)
	if rtStatus.FallbackToHost {
		status.Route = string(sdksandbox.RouteHost)
		status.SecuritySummary = "host fallback"
		if status.ResolvedBackend == "" {
			status.ResolvedBackend = string(sdksandbox.BackendHost)
		}
	} else if status.ResolvedBackend != "" {
		status.SecuritySummary = status.ResolvedBackend
	}
	if status.ResolvedBackend == "" {
		status.ResolvedBackend = status.RequestedBackend
	}
	return status
}

// ListModelAliases returns the current session override plus resolver-known
// model aliases for picker surfaces such as the TUI `/model` command.
func (s *Stack) ListModelAliases(ctx context.Context, ref sdksession.SessionRef) ([]string, error) {
	if s == nil || s.Gateway == nil {
		return nil, fmt.Errorf("gatewayapp: stack is unavailable")
	}
	resolver := s.Gateway.Resolver()
	if resolver == nil {
		return nil, fmt.Errorf("gatewayapp: resolver not available")
	}
	return resolver.ListModelAliases(ctx, ref)
}

func (s *Stack) DefaultModelAlias() string {
	if s == nil || s.lookup == nil {
		return ""
	}
	return s.lookup.DefaultAlias()
}

func (s *Stack) HasModelAlias(alias string) bool {
	if s == nil || s.lookup == nil {
		return false
	}
	return s.lookup.HasAlias(alias)
}

// ListProviderModels returns configured raw model names for a provider.
func (s *Stack) ListProviderModels(provider string) []string {
	if s == nil || s.lookup == nil {
		return nil
	}
	return s.lookup.ListProviderModels(provider)
}

func (s *Stack) ListACPAgents() []ACPAgentInfo {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	agents := append([]sdkplugin.AgentConfig(nil), s.runtime.Assembly.Agents...)
	s.mu.RUnlock()
	if len(agents) == 0 {
		return nil
	}
	out := make([]ACPAgentInfo, 0, len(agents))
	for _, agent := range agents {
		name := strings.TrimSpace(agent.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "self") {
			continue
		}
		out = append(out, ACPAgentInfo{
			Name:        name,
			Description: strings.TrimSpace(agent.Description),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// CompactSession appends a compaction event to the given session. The note is
// stored as the compact summary text.
func (s *Stack) CompactSession(ctx context.Context, ref sdksession.SessionRef, note string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		note = "manual compaction"
	}
	compactEvent := &sdksession.Event{
		Type:       sdksession.EventTypeCompact,
		Visibility: sdksession.VisibilityCanonical,
		Time:       time.Now(),
		Text:       note,
		Meta: map[string]any{
			"trigger": "manual",
		},
	}
	_, err := s.Sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: ref,
		Event:      compactEvent,
	})
	return err
}

type modelLookup struct {
	mu            sync.RWMutex
	configs       map[string]ModelConfig
	contextWindow int
	defaultAlias  string
}

func newModelLookup(store *appConfigStore, cfg ModelConfig, contextWindow int) (*modelLookup, error) {
	lookup := &modelLookup{
		configs:       map[string]ModelConfig{},
		contextWindow: contextWindow,
	}
	if store != nil {
		doc, err := store.Load()
		if err != nil {
			return nil, err
		}
		for _, item := range doc.Models.Configs {
			if _, err := lookup.Upsert(item); err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(doc.Models.DefaultAlias) != "" {
			lookup.SetDefault(doc.Models.DefaultAlias)
		}
	}
	cfg = normalizeModelConfig(cfg)
	if cfg.Provider != "" && cfg.Model != "" {
		if _, err := lookup.Upsert(cfg); err != nil {
			return nil, err
		}
	}
	return lookup, nil
}

func (l *modelLookup) DefaultAlias() string {
	if l == nil {
		return ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.defaultAlias
}

func (l *modelLookup) ListModelAliases() []string {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	aliases := make([]string, 0, len(l.configs)+1)
	if l.defaultAlias != "" {
		aliases = append(aliases, l.defaultAlias)
	}
	rest := make([]string, 0, len(l.configs))
	for alias := range l.configs {
		if !strings.EqualFold(alias, l.defaultAlias) {
			rest = append(rest, alias)
		}
	}
	sort.Strings(rest)
	aliases = append(aliases, rest...)
	return dedupeNonEmptyStrings(aliases)
}

func (l *modelLookup) ListProviderModels(provider string) []string {
	if l == nil {
		return nil
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	models := make([]string, 0, len(l.configs))
	for _, cfg := range l.configs {
		if strings.EqualFold(strings.TrimSpace(cfg.Provider), provider) && strings.TrimSpace(cfg.Model) != "" {
			models = append(models, strings.TrimSpace(cfg.Model))
		}
	}
	sort.Strings(models)
	return dedupeNonEmptyStrings(models)
}

func (l *modelLookup) ResolveModel(ctx context.Context, alias string, contextWindow int) (appgateway.ModelResolution, error) {
	if l == nil {
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: model lookup is nil")
	}
	l.mu.RLock()
	alias = firstNonEmpty(strings.TrimSpace(alias), l.defaultAlias)
	if alias == "" || len(l.configs) == 0 {
		l.mu.RUnlock()
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: no model configured; use /connect")
	}
	cfg, ok := l.configs[strings.ToLower(alias)]
	fallbackContextWindow := l.contextWindow
	l.mu.RUnlock()
	if !ok {
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	if cfg.Provider == "minimax" {
		// MiniMax aliases still resolve through one concrete provider config.
		return appgateway.ModelResolution{
			Model: sdkminimax.New(sdkminimax.Config{
				Model:           cfg.Model,
				BaseURL:         cfg.BaseURL,
				APIKey:          cfg.Token,
				HeaderKey:       cfg.HeaderKey,
				Timeout:         cfg.Timeout,
				MaxTokens:       cfg.MaxOutputTok,
				ReasoningEffort: cfg.ReasoningEffort,
			}),
			ReasoningEffort:        cfg.ReasoningEffort,
			DefaultReasoningEffort: cfg.DefaultReasoningEffort,
		}, nil
	}
	effectiveContextWindow := fallbackContextWindow
	if cfg.ContextWindowTokens > 0 {
		effectiveContextWindow = cfg.ContextWindowTokens
	}
	if contextWindow > 0 {
		effectiveContextWindow = contextWindow
	}
	factory := sdkproviders.NewFactory()
	record := sdkproviders.Config{
		Alias:                     cfg.Alias,
		Provider:                  cfg.Provider,
		API:                       cfg.API,
		Model:                     cfg.Model,
		BaseURL:                   cfg.BaseURL,
		Timeout:                   cfg.Timeout,
		MaxOutputTok:              cfg.MaxOutputTok,
		ContextWindowTokens:       effectiveContextWindow,
		ReasoningLevels:           append([]string(nil), cfg.ReasoningLevels...),
		ReasoningMode:             cfg.ReasoningMode,
		DefaultReasoningEffort:    cfg.DefaultReasoningEffort,
		ReasoningEffort:           cfg.ReasoningEffort,
		SupportedReasoningEfforts: append([]string(nil), cfg.ReasoningLevels...),
		Auth: sdkproviders.AuthConfig{
			Type:      cfg.AuthType,
			Token:     cfg.Token,
			TokenEnv:  cfg.TokenEnv,
			HeaderKey: cfg.HeaderKey,
		},
	}
	if err := factory.Register(record); err != nil {
		return appgateway.ModelResolution{}, err
	}
	llm, err := factory.NewByAlias(alias)
	if err != nil {
		return appgateway.ModelResolution{}, err
	}
	return appgateway.ModelResolution{
		Model:                  llm,
		ReasoningEffort:        cfg.ReasoningEffort,
		DefaultReasoningEffort: cfg.DefaultReasoningEffort,
	}, nil
}

func (l *modelLookup) HasAlias(alias string) bool {
	if l == nil {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.configs[strings.ToLower(strings.TrimSpace(alias))]
	return ok
}

func (l *modelLookup) Upsert(cfg ModelConfig) (string, error) {
	if l == nil {
		return "", fmt.Errorf("gatewayapp: model lookup is nil")
	}
	cfg = normalizeModelConfig(cfg)
	if cfg.Provider == "" || cfg.Model == "" {
		return "", fmt.Errorf("gatewayapp: provider and model are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.configs == nil {
		l.configs = map[string]ModelConfig{}
	}
	l.configs[strings.ToLower(cfg.Alias)] = cfg
	l.defaultAlias = cfg.Alias
	if cfg.ContextWindowTokens > 0 {
		l.contextWindow = cfg.ContextWindowTokens
	}
	return cfg.Alias, nil
}

func (l *modelLookup) Delete(alias string) error {
	if l == nil {
		return fmt.Errorf("gatewayapp: model lookup is nil")
	}
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.configs[key]; !ok {
		return fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	delete(l.configs, key)
	if strings.EqualFold(l.defaultAlias, alias) {
		l.defaultAlias = ""
		aliases := make([]string, 0, len(l.configs))
		for one := range l.configs {
			aliases = append(aliases, one)
		}
		sort.Strings(aliases)
		if len(aliases) > 0 {
			l.defaultAlias = aliases[0]
		}
	}
	return nil
}

func (l *modelLookup) SetDefault(alias string) {
	if l == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if cfg, ok := l.configs[key]; ok {
		l.defaultAlias = cfg.Alias
	}
}

func (s *Stack) SessionUsageSnapshot(ctx context.Context, ref sdksession.SessionRef, modelAlias string) (sdkcompact.UsageSnapshot, error) {
	if s == nil || s.Sessions == nil {
		return sdkcompact.UsageSnapshot{}, fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	if strings.TrimSpace(ref.SessionID) == "" {
		return sdkcompact.UsageSnapshot{}, nil
	}
	events, err := s.Sessions.Events(ctx, sdksession.EventsRequest{SessionRef: ref})
	if err != nil {
		return sdkcompact.UsageSnapshot{}, err
	}
	alias := strings.TrimSpace(modelAlias)
	if alias == "" && s.lookup != nil {
		alias = strings.TrimSpace(s.lookup.DefaultAlias())
	}
	contextWindow := s.currentContextWindowTokensForAlias(alias)
	return localruntime.ComputeUsageSnapshot(events, nil, contextWindow, localruntime.CompactionConfig{
		DefaultContextWindowTokens: contextWindow,
	}), nil
}

func (s *Stack) currentContextWindowTokensForAlias(alias string) int {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		if cfg, ok := s.modelConfigForAlias(alias); ok && cfg.ContextWindowTokens > 0 {
			return cfg.ContextWindowTokens
		}
	}
	if s != nil && s.lookup != nil {
		s.lookup.mu.RLock()
		defer s.lookup.mu.RUnlock()
		if s.lookup.contextWindow > 0 {
			return s.lookup.contextWindow
		}
	}
	if s != nil && s.runtime.ContextWindow > 0 {
		return s.runtime.ContextWindow
	}
	return 0
}

func (l *modelLookup) Snapshot() persistedModelConfig {
	if l == nil {
		return persistedModelConfig{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	configs := make([]ModelConfig, 0, len(l.configs))
	for _, cfg := range l.configs {
		configs = append(configs, sanitizePersistedModelConfig(cfg))
	}
	sort.Slice(configs, func(i, j int) bool {
		return strings.ToLower(strings.TrimSpace(configs[i].Alias)) < strings.ToLower(strings.TrimSpace(configs[j].Alias))
	})
	return persistedModelConfig{
		DefaultAlias: l.defaultAlias,
		Configs:      configs,
	}
}

func (l *modelLookup) Config(alias string) (ModelConfig, bool) {
	if l == nil {
		return ModelConfig{}, false
	}
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return ModelConfig{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	cfg, ok := l.configs[key]
	if !ok {
		return ModelConfig{}, false
	}
	return cfg, true
}

func (s *Stack) saveModelConfigs() error {
	if s == nil || s.store == nil || s.lookup == nil {
		return nil
	}
	doc, err := s.store.Load()
	if err != nil {
		return err
	}
	doc.Models = s.lookup.Snapshot()
	return s.store.Save(doc)
}

func (s *Stack) saveSandboxConfig() error {
	if s == nil || s.store == nil {
		return nil
	}
	doc, err := s.store.Load()
	if err != nil {
		return err
	}
	s.mu.RLock()
	doc.Sandbox = s.sandbox
	s.mu.RUnlock()
	return s.store.Save(doc)
}

func (s *Stack) rebuildGateway() error {
	if s == nil {
		return fmt.Errorf("gatewayapp: stack is unavailable")
	}
	s.mu.RLock()
	oldGateway := s.Gateway
	sandboxCfg := s.sandbox
	runtimeCfg := s.runtime
	s.mu.RUnlock()
	if err := rejectReconfigureWithActiveTurns(oldGateway, "rebuild gateway"); err != nil {
		return err
	}
	sandboxRuntime, err := sdksandbox.New(sdksandbox.Config{
		CWD:              s.Workspace.CWD,
		RequestedBackend: sdksandbox.Backend(sandboxCfg.RequestedType),
		HelperPath:       sandboxCfg.HelperPath,
		ReadableRoots:    append([]string(nil), sandboxCfg.ReadableRoots...),
		WritableRoots:    append([]string(nil), sandboxCfg.WritableRoots...),
		ReadOnlySubpaths: append([]string(nil), sandboxCfg.ReadOnlySubpaths...),
	})
	if err != nil {
		return err
	}
	tools, err := sdkbuiltin.BuildCoreTools(sdkbuiltin.CoreToolsConfig{Runtime: sandboxRuntime})
	if err != nil {
		_ = sandboxRuntime.Close()
		return err
	}
	rt, err := localruntime.New(localruntime.Config{
		Sessions:          s.Sessions,
		AgentFactory:      chat.Factory{},
		DefaultPolicyMode: policyMode(runtimeCfg.PermissionMode),
		Assembly:          runtimeCfg.Assembly,
		TaskStore:         s.taskStore,
	})
	if err != nil {
		_ = sandboxRuntime.Close()
		return err
	}
	resolver, err := appgateway.NewAssemblyResolver(appgateway.AssemblyResolverConfig{
		Sessions:          s.Sessions,
		Assembly:          runtimeCfg.Assembly,
		DefaultModelAlias: s.lookup.DefaultAlias(),
		ContextWindow:     runtimeCfg.ContextWindow,
		ModelLookup:       s.lookup,
		Tools:             tools,
		BaseMetadata:      cloneMap(runtimeCfg.BaseMetadata),
		ToolAugmenter: func(ctx context.Context, req appgateway.ToolAugmentContext) (appgateway.ToolAugmentation, error) {
			var participants []sdksession.ParticipantBinding
			if strings.TrimSpace(req.SessionRef.SessionID) != "" {
				session, err := s.Sessions.Session(ctx, req.SessionRef)
				if err != nil {
					return appgateway.ToolAugmentation{}, err
				}
				participants = session.Participants
			}
			agents := delegationAgentsForSpawn(runtimeCfg.Assembly, participants)
			if len(agents) == 0 {
				return appgateway.ToolAugmentation{}, nil
			}
			metadata := map[string]any{}
			if systemPrompt := stringFromMap(runtimeCfg.BaseMetadata, "system_prompt"); systemPrompt != "" {
				metadata["system_prompt"] = systemPromptWithDelegationGuidance(systemPrompt)
			}
			return appgateway.ToolAugmentation{
				Tools:    []sdktool.Tool{spawntool.New(agents)},
				Metadata: metadata,
			}, nil
		},
	})
	if err != nil {
		_ = sandboxRuntime.Close()
		return err
	}
	gw, err := appgateway.New(appgateway.Config{
		Sessions: s.Sessions,
		Runtime:  rt,
		Resolver: resolver,
	})
	if err != nil {
		_ = sandboxRuntime.Close()
		return err
	}
	if err := rejectReconfigureWithActiveTurns(oldGateway, "rebuild gateway"); err != nil {
		_ = sandboxRuntime.Close()
		return err
	}
	s.mu.Lock()
	oldExec := s.exec
	s.Gateway = gw
	s.exec = sandboxRuntime
	s.engine = rt
	s.mu.Unlock()
	if oldExec != nil {
		_ = oldExec.Close()
	}
	return nil
}

func normalizeModelConfig(cfg ModelConfig) ModelConfig {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Alias = strings.ToLower(strings.TrimSpace(cfg.Alias))
	if cfg.Alias == "" {
		cfg.Alias = buildAlias(cfg.Provider, cfg.Model)
	}
	if cfg.API == "" {
		cfg.API = defaultModelAPIForProvider(cfg.Provider)
	}
	if cfg.AuthType == "" {
		if cfg.Provider == "ollama" || cfg.Provider == "codefree" {
			cfg.AuthType = sdkproviders.AuthNone
		} else {
			cfg.AuthType = sdkproviders.AuthAPIKey
		}
	}
	if cfg.MaxOutputTok <= 0 {
		cfg.MaxOutputTok = 4096
	}
	if cfg.ContextWindowTokens < 0 {
		cfg.ContextWindowTokens = 0
	}
	cfg.ReasoningLevels = dedupeNonEmptyStrings(cfg.ReasoningLevels)
	if cfg.Token == "" && strings.TrimSpace(cfg.TokenEnv) != "" {
		cfg.Token = strings.TrimSpace(os.Getenv(strings.TrimSpace(cfg.TokenEnv)))
	}
	return cfg
}

func defaultModelAPIForProvider(provider string) sdkproviders.APIType {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return sdkproviders.APIOpenAI
	case "openai-compatible":
		return sdkproviders.APIOpenAICompatible
	case "openrouter":
		return sdkproviders.APIOpenRouter
	case "codefree":
		return sdkproviders.APICodeFree
	case "gemini":
		return sdkproviders.APIGemini
	case "anthropic":
		return sdkproviders.APIAnthropic
	case "anthropic-compatible":
		return sdkproviders.APIAnthropicCompatible
	case "deepseek":
		return sdkproviders.APIDeepSeek
	case "xiaomi", "mimo":
		return sdkproviders.APIMimo
	case "volcengine":
		return sdkproviders.APIVolcengine
	case "volcengine-coding-plan", "volcengine_coding_plan":
		return sdkproviders.APIVolcengineCoding
	case "ollama":
		return sdkproviders.APIOllama
	default:
		return ""
	}
}

func sanitizePersistedModelConfig(cfg ModelConfig) ModelConfig {
	cfg = normalizeModelConfig(cfg)
	if !cfg.PersistToken {
		cfg.Token = ""
	}
	return cfg
}

func (s *Stack) rejectReconfigureWhileActive(action string) error {
	if s == nil {
		return fmt.Errorf("gatewayapp: stack is unavailable")
	}
	return rejectReconfigureWithActiveTurns(s.Gateway, action)
}

func rejectReconfigureWithActiveTurns(gw *appgateway.Gateway, action string) error {
	if gw == nil {
		return nil
	}
	active := gw.ActiveTurns()
	if len(active) == 0 {
		return nil
	}
	sessions := make([]string, 0, len(active))
	for _, item := range active {
		if sessionID := strings.TrimSpace(item.SessionRef.SessionID); sessionID != "" {
			sessions = append(sessions, sessionID)
		}
	}
	label := strings.TrimSpace(action)
	if label == "" {
		label = "reconfigure runtime"
	}
	if len(sessions) > 0 {
		return fmt.Errorf(
			"gatewayapp: cannot %s while %d turn(s) are active (session(s): %s); wait for completion or interrupt the running turn first",
			label,
			len(active),
			strings.Join(dedupeNonEmptyStrings(sessions), ", "),
		)
	}
	return fmt.Errorf(
		"gatewayapp: cannot %s while %d turn(s) are active; wait for completion or interrupt the running turn first",
		label,
		len(active),
	)
}

func buildAlias(provider string, modelName string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(modelName)
	if provider == "" {
		return strings.ToLower(modelName)
	}
	if modelName == "" {
		return provider
	}
	return strings.ToLower(provider + "/" + modelName)
}

func normalizeSessionMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto", "default":
		return "default", nil
	case "plan":
		return "plan", nil
	case "full_control", "full_access":
		return "full_access", nil
	default:
		return "", fmt.Errorf("gatewayapp: unknown session mode %q", mode)
	}
}

func normalizeSessionModeOrDefault(mode string) string {
	normalized, err := normalizeSessionMode(mode)
	if err != nil {
		return "default"
	}
	return normalized
}

func nextSessionMode(mode string) string {
	switch normalizeSessionModeOrDefault(mode) {
	case "plan":
		return "full_access"
	case "full_access":
		return "default"
	default:
		return "plan"
	}
}

func normalizeSandboxBackend(backend string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "auto":
		return "auto", nil
	case "seatbelt":
		return "seatbelt", nil
	case "bwrap":
		return "bwrap", nil
	case "landlock":
		return "landlock", nil
	default:
		return "", fmt.Errorf("gatewayapp: unknown sandbox backend %q", backend)
	}
}

func mergeSandboxConfig(stored SandboxConfig, override SandboxConfig) SandboxConfig {
	stored = normalizeSandboxConfig(stored)
	override = normalizeSandboxConfig(override)
	if override.RequestedType != "" {
		stored.RequestedType = override.RequestedType
	}
	if override.HelperPath != "" {
		stored.HelperPath = override.HelperPath
	}
	if len(override.ReadableRoots) > 0 {
		stored.ReadableRoots = append([]string(nil), override.ReadableRoots...)
	}
	if len(override.WritableRoots) > 0 {
		stored.WritableRoots = append([]string(nil), override.WritableRoots...)
	}
	if len(override.ReadOnlySubpaths) > 0 {
		stored.ReadOnlySubpaths = append([]string(nil), override.ReadOnlySubpaths...)
	}
	if stored.RequestedType == "" {
		stored.RequestedType = "auto"
	}
	return stored
}

func dedupeNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func policyMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "full_control") || strings.EqualFold(strings.TrimSpace(raw), "full_access") {
		return sdkpolicy.ModeFullAccess
	}
	return sdkpolicy.ModeDefault
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func stringFromMap(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
