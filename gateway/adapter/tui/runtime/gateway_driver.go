package runtime

import (
	"context"
	"fmt"
	"path/filepath"
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
	defaultSandboxMode string
	sandboxMode        string
}

func NewGatewayDriver(ctx context.Context, stack *gatewayapp.Stack, sessionID string, bindingKey string, modelText string) (*GatewayDriver, error) {
	if stack == nil {
		return nil, fmt.Errorf("tui/runtime: stack is required")
	}
	key := firstNonEmpty(strings.TrimSpace(bindingKey), "cli-tui")
	session, err := stack.StartSession(ctx, strings.TrimSpace(sessionID), key)
	if err != nil {
		return nil, err
	}
	return &GatewayDriver{
		stack:              stack,
		session:            session,
		hasSession:         true,
		bindingKey:         key,
		defaultModelText:   strings.TrimSpace(modelText),
		modelText:          strings.TrimSpace(modelText),
		defaultSandboxMode: "default",
		sandboxMode:        "default",
	}, nil
}

func (d *GatewayDriver) WorkspaceDir() string {
	if d == nil || d.stack == nil {
		return ""
	}
	return strings.TrimSpace(d.stack.Workspace.CWD)
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
	session, ok := d.currentSession()
	if !ok {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: no active session")
	}
	modelText, sandboxMode := d.defaultDisplays()
	if d.stack != nil {
		if alias := strings.TrimSpace(d.stack.DefaultModelAlias()); alias != "" {
			modelText = alias
		}
	}
	if d.stack != nil {
		if state, err := d.stack.SessionRuntimeState(context.Background(), session.SessionRef); err == nil {
			if strings.TrimSpace(state.ModelAlias) != "" {
				modelText = strings.TrimSpace(state.ModelAlias)
			}
			if strings.TrimSpace(state.SandboxMode) != "" {
				sandboxMode = strings.TrimSpace(state.SandboxMode)
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return StatusSnapshot{
		SessionID: session.SessionID,
		Workspace: strings.TrimSpace(d.stack.Workspace.CWD),
		Model:     firstNonEmpty(modelText, d.modelText),
		ModeLabel: firstNonEmpty(sandboxMode, d.sandboxMode),
		Surface:   d.bindingKey,
	}, nil
}

func (d *GatewayDriver) Submit(ctx context.Context, submission Submission) (Turn, error) {
	session, ok := d.currentSession()
	if !ok {
		return nil, fmt.Errorf("tui/runtime: no active session")
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
	session, ok := d.currentSession()
	if !ok {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: no active session")
	}
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: model alias is required")
	}
	if err := d.stack.UseModel(ctx, session.SessionRef, trimmed); err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.modelText = trimmed
	d.mu.Unlock()
	return d.Status(ctx)
}

func (d *GatewayDriver) DeleteModel(ctx context.Context, alias string) error {
	session, ok := d.currentSession()
	if !ok {
		return fmt.Errorf("tui/runtime: no active session")
	}
	if err := d.stack.DeleteModel(ctx, session.SessionRef, alias); err != nil {
		return err
	}
	d.mu.Lock()
	d.defaultModelText = strings.TrimSpace(d.stack.DefaultModelAlias())
	d.mu.Unlock()
	d.refreshSessionDisplay(ctx, session)
	return nil
}

func (d *GatewayDriver) SetSandboxMode(ctx context.Context, mode string) (StatusSnapshot, error) {
	session, ok := d.currentSession()
	if !ok {
		return StatusSnapshot{}, fmt.Errorf("tui/runtime: no active session")
	}
	normalized, err := d.stack.SetSandboxMode(ctx, session.SessionRef, mode)
	if err != nil {
		return StatusSnapshot{}, err
	}
	d.mu.Lock()
	d.sandboxMode = normalized
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
		return completeConnectArgs(ctx, "connect", query, limit)
	}
	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(command)), "connect-") {
		return completeConnectArgs(ctx, strings.TrimSpace(strings.ToLower(command)), query, limit)
	}
	candidates := defaultSlashArgCandidates(strings.TrimSpace(strings.ToLower(command)))
	out := make([]SlashArgCandidate, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if query != "" && !strings.Contains(strings.ToLower(candidate.Value+" "+candidate.Display+" "+candidate.Detail), query) {
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
	session, ok := d.currentSession()
	if !ok {
		return nil, nil
	}
	aliases, err := d.stack.ListModelAliases(ctx, session.SessionRef)
	if err != nil {
		return nil, err
	}
	out := make([]SlashArgCandidate, 0, min(limit, len(aliases)))
	for _, alias := range aliases {
		display := strings.TrimSpace(alias)
		if display == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(display), query) {
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
		return []SlashArgCandidate{
			{Value: "auto", Display: "auto", Detail: "Follow runtime default"},
			{Value: "full_control", Display: "full_control", Detail: "No sandbox restriction"},
			{Value: "default", Display: "default", Detail: "Project default sandbox mode"},
		}
	case "model":
		return []SlashArgCandidate{
			{Value: "use", Display: "use", Detail: "Switch current model alias"},
			{Value: "del", Display: "del", Detail: "Delete stored model alias"},
		}
	default:
		return nil
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

func (d *GatewayDriver) defaultDisplays() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.defaultModelText, d.defaultSandboxMode
}

func (d *GatewayDriver) refreshSessionDisplay(ctx context.Context, session sdksession.Session) {
	if d == nil || d.stack == nil {
		return
	}
	modelText, sandboxMode := d.defaultDisplays()
	if alias := strings.TrimSpace(d.stack.DefaultModelAlias()); alias != "" {
		modelText = alias
	}
	if state, err := d.stack.SessionRuntimeState(ctx, session.SessionRef); err == nil {
		if strings.TrimSpace(state.ModelAlias) != "" {
			modelText = strings.TrimSpace(state.ModelAlias)
		}
		if strings.TrimSpace(state.SandboxMode) != "" {
			sandboxMode = strings.TrimSpace(state.SandboxMode)
		}
	}
	d.mu.Lock()
	d.modelText = modelText
	d.sandboxMode = sandboxMode
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
