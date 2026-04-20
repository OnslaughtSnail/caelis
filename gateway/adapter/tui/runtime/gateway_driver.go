package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/app/gatewayapp"
	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type GatewayDriver struct {
	mu                 sync.Mutex
	stack              *gatewayapp.Stack
	session            sdksession.Session
	hasSession         bool
	bindingKey         string
	defaultModelText   string
	modelText          string
	defaultSessionMode string
	sessionMode        string
	defaultSandboxType string
	sandboxType        string
}

func NewGatewayDriver(ctx context.Context, stack *gatewayapp.Stack, preferredSessionID string, bindingKey string, modelText string) (*GatewayDriver, error) {
	if stack == nil {
		return nil, fmt.Errorf("tui/runtime: stack is required")
	}
	key := firstNonEmpty(strings.TrimSpace(bindingKey), "cli-tui")
	if ctx == nil {
		ctx = context.Background()
	}
	driver := &GatewayDriver{
		stack:              stack,
		bindingKey:         key,
		defaultModelText:   strings.TrimSpace(modelText),
		modelText:          strings.TrimSpace(modelText),
		defaultSessionMode: "default",
		sessionMode:        "default",
		defaultSandboxType: firstNonEmpty(stack.SandboxStatus().ResolvedBackend, stack.SandboxStatus().RequestedBackend, "auto"),
		sandboxType:        firstNonEmpty(stack.SandboxStatus().ResolvedBackend, stack.SandboxStatus().RequestedBackend, "auto"),
	}
	session, err := driver.stack.StartSession(ctx, strings.TrimSpace(preferredSessionID), driver.bindingKey)
	if err != nil {
		return nil, err
	}
	driver.session = session
	driver.hasSession = true
	driver.refreshSessionDisplay(ctx, session)
	return driver, nil
}

func (d *GatewayDriver) WorkspaceDir() string {
	if d == nil || d.stack == nil {
		return ""
	}
	return strings.TrimSpace(d.stack.Workspace.CWD)
}

func (d *GatewayDriver) ensureSession(ctx context.Context) (sdksession.Session, error) {
	if session, ok := d.currentSession(); ok {
		return session, nil
	}
	if d == nil || d.stack == nil {
		return sdksession.Session{}, fmt.Errorf("tui/runtime: stack is unavailable")
	}
	session, err := d.stack.StartSession(ctx, "", d.bindingKey)
	if err != nil {
		return sdksession.Session{}, err
	}
	d.mu.Lock()
	d.session = session
	d.hasSession = true
	d.mu.Unlock()
	d.refreshSessionDisplay(ctx, session)
	return session, nil
}

func (d *GatewayDriver) currentSession() (sdksession.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.hasSession {
		return sdksession.Session{}, false
	}
	return d.session, true
}

func (d *GatewayDriver) Status(_ context.Context) (StatusSnapshot, error) {
	modelText, sessionMode, sandboxType := d.defaultDisplays()
	if d.stack != nil {
		if alias := strings.TrimSpace(d.stack.DefaultModelAlias()); alias != "" {
			modelText = alias
		}
	}
	sandboxStatus := gatewayapp.SandboxStatus{}
	if d.stack != nil {
		sandboxStatus = d.stack.SandboxStatus()
	}
	session, ok := d.currentSession()
	if ok && d.stack != nil {
		if state, err := d.stack.SessionRuntimeState(context.Background(), session.SessionRef); err == nil {
			if strings.TrimSpace(state.ModelAlias) != "" {
				modelText = strings.TrimSpace(state.ModelAlias)
			}
			if strings.TrimSpace(state.SessionMode) != "" {
				sessionMode = strings.TrimSpace(state.SessionMode)
			}
		}
	}
	sandboxType = firstNonEmpty(sandboxStatus.ResolvedBackend, sandboxStatus.RequestedBackend, sandboxType)
	route := sandboxStatus.Route
	securitySummary := sandboxStatus.SecuritySummary
	if strings.EqualFold(strings.TrimSpace(sessionMode), "full_access") {
		route = "host"
		securitySummary = "full access"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	sessionID := ""
	if ok {
		sessionID = session.SessionID
	}
	return StatusSnapshot{
		SessionID:       sessionID,
		Workspace:       strings.TrimSpace(d.stack.Workspace.CWD),
		Model:           firstNonEmpty(modelText, d.modelText),
		ModeLabel:       firstNonEmpty(sessionMode, d.sessionMode),
		SessionMode:     firstNonEmpty(sessionMode, d.sessionMode),
		SandboxType:     firstNonEmpty(sandboxType, d.sandboxType),
		Route:           route,
		FallbackReason:  sandboxStatus.FallbackReason,
		SecuritySummary: securitySummary,
		Surface:         d.bindingKey,
	}, nil
}

func (d *GatewayDriver) Submit(ctx context.Context, submission Submission) (Turn, error) {
	session, err := d.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	result, err := d.stack.Gateway.BeginTurn(ctx, appgateway.BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      strings.TrimSpace(submission.Text),
		Surface:    d.bindingKey,
		Metadata: map[string]any{
			"submission_mode": string(submission.Mode),
			"display_text":    strings.TrimSpace(submission.DisplayText),
		},
	})
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.session = result.Session
	d.hasSession = true
	d.mu.Unlock()
	if result.Handle == nil {
		return nil, nil
	}
	return gatewayTurn{handle: result.Handle}, nil
}

func (d *GatewayDriver) Interrupt(ctx context.Context) error {
	session, ok := d.currentSession()
	if !ok {
		return fmt.Errorf("tui/runtime: no active session")
	}
	return d.stack.Gateway.Interrupt(ctx, appgateway.InterruptRequest{
		SessionRef: session.SessionRef,
		BindingKey: d.bindingKey,
		Reason:     "tui interrupt",
	})
}

func (d *GatewayDriver) NewSession(ctx context.Context) (sdksession.Session, error) {
	session, err := d.stack.StartSession(ctx, "", d.bindingKey)
	if err != nil {
		return sdksession.Session{}, err
	}
	d.mu.Lock()
	d.session = session
	d.hasSession = true
	d.mu.Unlock()
	d.refreshSessionDisplay(ctx, session)
	return session, nil
}

func (d *GatewayDriver) ResumeSession(ctx context.Context, sessionID string) (sdksession.Session, error) {
	result, err := d.stack.Gateway.ResumeSession(ctx, appgateway.ResumeSessionRequest{
		AppName:    d.stack.AppName,
		UserID:     d.stack.UserID,
		Workspace:  d.stack.Workspace,
		SessionID:  strings.TrimSpace(sessionID),
		BindingKey: d.bindingKey,
		Binding: appgateway.BindingDescriptor{
			Surface: d.bindingKey,
			Owner:   d.stack.AppName,
		},
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	d.mu.Lock()
	d.session = result.Session
	d.hasSession = true
	d.mu.Unlock()
	d.refreshSessionDisplay(ctx, result.Session)
	return result.Session, nil
}

func (d *GatewayDriver) ListSessions(ctx context.Context, limit int) ([]ResumeCandidate, error) {
	if limit <= 0 {
		limit = 20
	}
	result, err := d.stack.Gateway.ListSessions(ctx, appgateway.ListSessionsRequest{
		AppName:      d.stack.AppName,
		UserID:       d.stack.UserID,
		WorkspaceKey: d.stack.Workspace.Key,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ResumeCandidate, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		out = append(out, ResumeCandidate{
			SessionID: session.SessionID,
			Prompt:    strings.TrimSpace(session.Title),
			Age:       humanAge(session.UpdatedAt),
		})
	}
	return out, nil
}

func (d *GatewayDriver) ReplayEvents(ctx context.Context) ([]appgateway.EventEnvelope, error) {
	session, ok := d.currentSession()
	if !ok {
		return nil, fmt.Errorf("tui/runtime: no active session")
	}
	result, err := d.stack.Gateway.ReplayEvents(ctx, appgateway.ReplayEventsRequest{
		SessionRef: session.SessionRef,
		BindingKey: d.bindingKey,
	})
	if err != nil {
		return nil, err
	}
	return result.Events, nil
}

func (d *GatewayDriver) Compact(ctx context.Context, note string) error {
	session, ok := d.currentSession()
	if !ok {
		return fmt.Errorf("tui/runtime: no active session")
	}
	return d.stack.CompactSession(ctx, session.SessionRef, note)
}

func (d *GatewayDriver) Connect(ctx context.Context, cfg ConnectConfig) (StatusSnapshot, error) {
	tpl, ok := findProviderTemplate(cfg.Provider)
	if !ok {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: unknown provider %q", cfg.Provider)
	}
	if defaults, err := connectDefaultsForConfig(ctx, cfg); err == nil {
		if cfg.ContextWindowTokens <= 0 {
			cfg.ContextWindowTokens = defaults.ContextWindow
		}
		if cfg.MaxOutputTokens <= 0 {
			cfg.MaxOutputTokens = defaults.MaxOutput
		}
		if len(cfg.ReasoningLevels) == 0 {
			cfg.ReasoningLevels = defaults.ReasoningLevels
		}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = tpl.defaultBaseURL
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if cfg.TimeoutSeconds <= 0 {
		timeout = 60 * time.Second
	}
	authType := authTypeFromString(strings.TrimSpace(cfg.AuthType))
	if tpl.noAuthRequired {
		authType = sdkproviders.AuthNone
	}
	reasoningLevels := normalizeReasoningLevels(cfg.ReasoningLevels)
	defaultReasoningEffort := ""
	if len(reasoningLevels) > 0 {
		defaultReasoningEffort = reasoningLevels[0]
	}
	alias, err := d.stack.Connect(gatewayapp.ModelConfig{
		Provider:               strings.TrimSpace(tpl.provider),
		API:                    tpl.api,
		Model:                  strings.TrimSpace(cfg.Model),
		BaseURL:                baseURL,
		Token:                  strings.TrimSpace(cfg.APIKey),
		TokenEnv:               strings.TrimSpace(cfg.TokenEnv),
		AuthType:               authType,
		ContextWindowTokens:    cfg.ContextWindowTokens,
		DefaultReasoningEffort: defaultReasoningEffort,
		ReasoningLevels:        reasoningLevels,
		MaxOutputTok:           cfg.MaxOutputTokens,
		Timeout:                timeout,
	})
	if err != nil {
		return StatusSnapshot{}, err
	}
	if session, ok := d.currentSession(); ok && alias != "" {
		if err := d.stack.UseModel(ctx, session.SessionRef, alias); err != nil {
			return StatusSnapshot{}, err
		}
	}
	d.mu.Lock()
	if alias != "" {
		d.defaultModelText = alias
		d.modelText = alias
	}
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) UseModel(ctx context.Context, model string) (StatusSnapshot, error) {
	session, err := d.ensureSession(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	alias, err := d.resolveStoredModelAlias(ctx, strings.TrimSpace(model))
	if err != nil {
		return StatusSnapshot{}, err
	}
	if alias == "" {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: model alias is required")
	}
	if err := d.stack.UseModel(ctx, session.SessionRef, alias); err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.modelText = alias
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) DeleteModel(ctx context.Context, alias string) error {
	session, err := d.ensureSession(ctx)
	if err != nil {
		return err
	}
	resolved, err := d.resolveStoredModelAlias(ctx, strings.TrimSpace(alias))
	if err != nil {
		return err
	}
	if err := d.stack.DeleteModel(ctx, session.SessionRef, resolved); err != nil {
		return err
	}
	d.mu.Lock()
	d.defaultModelText = strings.TrimSpace(d.stack.DefaultModelAlias())
	d.mu.Unlock()
	d.refreshSessionDisplay(ctx, session)
	return nil
}

func (d *GatewayDriver) CycleSessionMode(ctx context.Context) (StatusSnapshot, error) {
	session, err := d.ensureSession(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	normalized, err := d.stack.CycleSessionMode(ctx, session.SessionRef)
	if err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.sessionMode = normalized
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) SetSandboxBackend(ctx context.Context, backend string) (StatusSnapshot, error) {
	status, err := d.stack.SetSandboxBackend(ctx, backend)
	if err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.sandboxType = firstNonEmpty(status.ResolvedBackend, status.RequestedBackend, d.sandboxType)
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) SetSandboxMode(ctx context.Context, mode string) (StatusSnapshot, error) {
	session, err := d.ensureSession(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	normalized, err := d.stack.SetSessionMode(ctx, session.SessionRef, mode)
	if err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.sessionMode = normalized
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) CompleteMention(context.Context, string, int) ([]string, error) {
	return nil, nil
}

func (d *GatewayDriver) CompleteFile(_ context.Context, query string, limit int) ([]string, error) {
	query = strings.TrimSpace(query)
	root := d.WorkspaceDir()
	if root == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	matches, err := filepath.Glob(filepath.Join(root, query+"*"))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, min(limit, len(matches)))
	for _, match := range matches {
		out = append(out, match)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *GatewayDriver) CompleteSkill(context.Context, string, int) ([]string, error) {
	return nil, nil
}

func (d *GatewayDriver) CompleteResume(ctx context.Context, query string, limit int) ([]ResumeCandidate, error) {
	all, err := d.ListSessions(ctx, limit)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return all, nil
	}
	out := make([]ResumeCandidate, 0, len(all))
	for _, item := range all {
		if strings.Contains(strings.ToLower(item.SessionID), query) || strings.Contains(strings.ToLower(item.Prompt), query) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *GatewayDriver) CompleteSlashArg(ctx context.Context, command string, query string, limit int) ([]SlashArgCandidate, error) {
	if limit <= 0 {
		limit = 8
	}
	query = strings.TrimSpace(strings.ToLower(query))
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "model use", "model del":
		return d.completeModelAliases(ctx, query, limit)
	case "connect":
		return completeConnectArgs(ctx, d, "connect", query, limit)
	}
	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(command)), "connect-") {
		return completeConnectArgs(ctx, d, strings.TrimSpace(strings.ToLower(command)), query, limit)
	}
	candidates := defaultSlashArgCandidates(strings.TrimSpace(strings.ToLower(command)))
	out := make([]SlashArgCandidate, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if query != "" && !hasSlashArgPrefix(query, candidate.Value, candidate.Display, candidate.Detail) {
			continue
		}
		out = append(out, candidate)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *GatewayDriver) completeModelAliases(ctx context.Context, query string, limit int) ([]SlashArgCandidate, error) {
	ref := sdksession.SessionRef{}
	if session, ok := d.currentSession(); ok {
		ref = session.SessionRef
	}
	aliases, err := d.stack.ListModelAliases(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := make([]SlashArgCandidate, 0, min(limit, len(aliases)))
	for _, alias := range aliases {
		display := strings.TrimSpace(alias)
		if display == "" {
			continue
		}
		if query != "" && !hasSlashArgPrefix(query, display) {
			continue
		}
		out = append(out, SlashArgCandidate{
			Value:   display,
			Display: display,
			Detail:  "configured model alias",
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *GatewayDriver) resolveStoredModelAlias(ctx context.Context, input string) (string, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return "", fmt.Errorf("tui/runtime: model alias is required")
	}
	ref := sdksession.SessionRef{}
	if session, ok := d.currentSession(); ok {
		ref = session.SessionRef
	}
	aliases, err := d.stack.ListModelAliases(ctx, ref)
	if err != nil {
		return "", err
	}
	var exact string
	prefixMatches := make([]string, 0, 2)
	for _, alias := range aliases {
		normalized := strings.ToLower(strings.TrimSpace(alias))
		if normalized == "" {
			continue
		}
		if normalized == input {
			exact = strings.TrimSpace(alias)
			break
		}
		if strings.HasPrefix(normalized, input) {
			prefixMatches = append(prefixMatches, strings.TrimSpace(alias))
		}
	}
	if exact != "" {
		return exact, nil
	}
	switch len(prefixMatches) {
	case 1:
		return prefixMatches[0], nil
	case 0:
		return "", fmt.Errorf("tui/runtime: unknown model alias %q", input)
	default:
		return "", fmt.Errorf("tui/runtime: ambiguous model alias %q", input)
	}
}

func hasSlashArgPrefix(query string, values ...string) bool {
	if query == "" {
		return true
	}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(normalized, query) {
			return true
		}
	}
	return false
}

type gatewayTurn struct {
	handle appgateway.TurnHandle
}

func (t gatewayTurn) HandleID() string                  { return t.handle.HandleID() }
func (t gatewayTurn) RunID() string                     { return t.handle.RunID() }
func (t gatewayTurn) TurnID() string                    { return t.handle.TurnID() }
func (t gatewayTurn) SessionRef() sdksession.SessionRef { return t.handle.SessionRef() }
func (t gatewayTurn) Events() <-chan appgateway.EventEnvelope {
	return t.handle.Events()
}
func (t gatewayTurn) Submit(ctx context.Context, req appgateway.SubmitRequest) error {
	return t.handle.Submit(ctx, req)
}
func (t gatewayTurn) Cancel() bool { return t.handle.Cancel() }
func (t gatewayTurn) Close() error { return t.handle.Close() }

func defaultSlashArgCandidates(command string) []SlashArgCandidate {
	switch command {
	case "sandbox":
		return sandboxCandidates()
	case "model":
		return []SlashArgCandidate{
			{Value: "use", Display: "use", Detail: "Switch current model alias"},
			{Value: "del", Display: "del", Detail: "Delete stored model alias"},
		}
	default:
		return nil
	}
}

func sandboxCandidates() []SlashArgCandidate {
	switch runtime.GOOS {
	case "darwin":
		return []SlashArgCandidate{
			{Value: "auto", Display: "auto", Detail: "Use the default macOS sandbox backend"},
			{Value: "seatbelt", Display: "seatbelt", Detail: "Use sandbox-exec seatbelt isolation"},
		}
	case "linux":
		return []SlashArgCandidate{
			{Value: "auto", Display: "auto", Detail: "Prefer bwrap, then fall back to landlock"},
			{Value: "bwrap", Display: "bwrap", Detail: "Use bubblewrap container isolation"},
			{Value: "landlock", Display: "landlock", Detail: "Use the landlock helper sandbox"},
		}
	default:
		return []SlashArgCandidate{{Value: "auto", Display: "auto", Detail: "Use the default sandbox backend"}}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	delta := time.Since(t).Round(time.Minute)
	if delta < time.Minute {
		return "just now"
	}
	return delta.String() + " ago"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (d *GatewayDriver) defaultDisplays() (string, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.defaultModelText, d.defaultSessionMode, d.defaultSandboxType
}

func (d *GatewayDriver) refreshSessionDisplay(ctx context.Context, session sdksession.Session) {
	if d == nil || d.stack == nil {
		return
	}
	modelText, sessionMode, sandboxType := d.defaultDisplays()
	if alias := strings.TrimSpace(d.stack.DefaultModelAlias()); alias != "" {
		modelText = alias
	}
	if state, err := d.stack.SessionRuntimeState(ctx, session.SessionRef); err == nil {
		if strings.TrimSpace(state.ModelAlias) != "" {
			modelText = strings.TrimSpace(state.ModelAlias)
		}
		if strings.TrimSpace(state.SessionMode) != "" {
			sessionMode = strings.TrimSpace(state.SessionMode)
		}
	}
	sandbox := d.stack.SandboxStatus()
	sandboxType = firstNonEmpty(sandbox.ResolvedBackend, sandbox.RequestedBackend, sandboxType)
	d.mu.Lock()
	d.modelText = modelText
	d.sessionMode = sessionMode
	d.sandboxType = sandboxType
	d.mu.Unlock()
}

func authTypeFromString(s string) sdkproviders.AuthType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "api_key", "apikey":
		return sdkproviders.AuthAPIKey
	case "bearer_token", "bearer":
		return sdkproviders.AuthBearerToken
	case "oauth_token", "oauth":
		return sdkproviders.AuthOAuthToken
	case "none":
		return sdkproviders.AuthNone
	default:
		return sdkproviders.AuthAPIKey
	}
}
