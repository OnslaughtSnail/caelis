package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkacpclient "github.com/OnslaughtSnail/caelis/acp/client"
	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdksubagent "github.com/OnslaughtSnail/caelis/sdk/subagent"
)

type PermissionHandler func(context.Context, sdkacpclient.RequestPermissionRequest) (sdkacpclient.RequestPermissionResponse, error)

type PermissionBridge interface {
	RequestPermission(context.Context, PermissionRequest) (sdkacpclient.RequestPermissionResponse, error)
}

type PermissionRequest struct {
	Spawn   sdksubagent.SpawnContext
	Agent   sdkdelegation.Agent
	AgentID string
	Request sdkacpclient.RequestPermissionRequest
}

type RunnerConfig struct {
	Registry          *Registry
	ClientInfo        *sdkacpclient.Implementation
	Clock             func() time.Time
	PermissionHandler PermissionHandler
	PermissionBridge  PermissionBridge
}

type Runner struct {
	registry          *Registry
	clientInfo        *sdkacpclient.Implementation
	clock             func() time.Time
	permissionHandler PermissionHandler
	permissionBridge  PermissionBridge

	counter atomic.Uint64
	mu      sync.RWMutex
	runs    map[string]*childRun
}

type childRun struct {
	anchor sdkdelegation.Anchor
	client *sdkacpclient.Client

	mu            sync.RWMutex
	state         sdkdelegation.State
	outputPreview string
	result        string
	updatedAt     time.Time
	running       bool
	done          chan struct{}
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("sdk/subagent/acp: registry is required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Runner{
		registry:          cfg.Registry,
		clientInfo:        cfg.ClientInfo,
		clock:             clock,
		permissionHandler: cfg.PermissionHandler,
		permissionBridge:  cfg.PermissionBridge,
		runs:              map[string]*childRun{},
	}, nil
}

func (r *Runner) Spawn(ctx context.Context, spawn sdksubagent.SpawnContext, req sdkdelegation.Request) (sdkdelegation.Anchor, sdkdelegation.Result, error) {
	cfg, err := r.registry.Resolve(req.Agent)
	if err != nil {
		return sdkdelegation.Anchor{}, sdkdelegation.Result{}, err
	}
	run := &childRun{
		state:     sdkdelegation.StateRunning,
		running:   true,
		updatedAt: r.clock(),
		done:      make(chan struct{}),
	}
	agentID := r.nextAgentID(cfg.Name)
	launchEnv := maps.Clone(cfg.Env)
	if strings.EqualFold(strings.TrimSpace(cfg.Name), "self") {
		if launchEnv == nil {
			launchEnv = map[string]string{}
		}
		launchEnv["SDK_ACP_ENABLE_SPAWN"] = "0"
		launchEnv["SDK_ACP_CHILD_NO_SPAWN"] = "1"
	}
	client, err := sdkacpclient.Start(ctx, sdkacpclient.Config{
		Command:    cfg.Command,
		Args:       append([]string(nil), cfg.Args...),
		Env:        launchEnv,
		WorkDir:    pickWorkDir(cfg.WorkDir, spawn.CWD),
		ClientInfo: r.clientInfo,
		OnUpdate:   func(env sdkacpclient.UpdateEnvelope) { r.handleUpdate(run, env) },
		OnPermissionRequest: func(ctx context.Context, req sdkacpclient.RequestPermissionRequest) (sdkacpclient.RequestPermissionResponse, error) {
			return r.permissionCallback(spawn, cfg, agentID)(ctx, req)
		},
	})
	if err != nil {
		return sdkdelegation.Anchor{}, sdkdelegation.Result{}, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return sdkdelegation.Anchor{}, sdkdelegation.Result{}, err
	}
	sessionResp, err := client.NewSession(ctx, strings.TrimSpace(spawn.CWD), nil)
	if err != nil {
		_ = client.Close()
		return sdkdelegation.Anchor{}, sdkdelegation.Result{}, err
	}
	anchor := sdkdelegation.Anchor{
		SessionID: strings.TrimSpace(sessionResp.SessionID),
		Agent:     cfg.Name,
		AgentID:   agentID,
	}
	run.anchor = anchor
	run.client = client
	r.mu.Lock()
	r.runs[anchor.SessionID] = run
	r.mu.Unlock()
	go r.drivePrompt(run, strings.TrimSpace(req.Prompt))
	return anchor, r.waitRun(ctx, run, req.YieldTimeMS), nil
}

func (r *Runner) Wait(ctx context.Context, anchor sdkdelegation.Anchor, yieldTimeMS int) (sdkdelegation.Result, error) {
	run, err := r.lookup(anchor)
	if err != nil {
		return sdkdelegation.Result{}, err
	}
	return r.waitRun(ctx, run, yieldTimeMS), nil
}

func (r *Runner) Cancel(ctx context.Context, anchor sdkdelegation.Anchor) error {
	run, err := r.lookup(anchor)
	if err != nil {
		return err
	}
	run.mu.RLock()
	client := run.client
	sessionID := run.anchor.SessionID
	run.mu.RUnlock()
	if client != nil {
		_ = client.Cancel(ctx, sessionID)
	}
	run.mu.Lock()
	run.running = false
	run.state = sdkdelegation.StateCancelled
	run.outputPreview = "cancelled"
	run.updatedAt = r.clock()
	run.mu.Unlock()
	return nil
}

func (r *Runner) drivePrompt(run *childRun, prompt string) {
	ctx := context.Background()
	resp, err := run.client.Prompt(ctx, run.anchor.SessionID, prompt, nil)
	run.mu.Lock()
	defer run.mu.Unlock()
	defer close(run.done)
	run.running = false
	run.updatedAt = r.clock()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			run.state = sdkdelegation.StateInterrupted
			run.outputPreview = "interrupted"
			run.result = ""
			_ = run.client.Close()
			return
		}
		run.state = sdkdelegation.StateFailed
		run.outputPreview = compactPreview(err.Error())
		run.result = ""
		_ = run.client.Close()
		return
	}
	if strings.EqualFold(strings.TrimSpace(resp.StopReason), "cancelled") {
		run.state = sdkdelegation.StateCancelled
		run.outputPreview = "cancelled"
		_ = run.client.Close()
		return
	}
	if strings.TrimSpace(run.result) == "" {
		run.result = strings.TrimSpace(run.outputPreview)
	}
	run.state = sdkdelegation.StateCompleted
	run.outputPreview = compactPreview(run.outputPreview)
	_ = run.client.Close()
}

func (r *Runner) waitRun(ctx context.Context, run *childRun, yieldTimeMS int) sdkdelegation.Result {
	if run == nil {
		return sdkdelegation.Result{}
	}
	wait := time.Duration(yieldTimeMS) * time.Millisecond
	if wait < 0 {
		wait = 0
	}
	if wait > 0 {
		select {
		case <-ctx.Done():
		case <-run.done:
		case <-time.After(wait):
		}
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	out := sdkdelegation.Result{
		State:         run.state,
		Running:       run.running,
		Yielded:       run.running,
		OutputPreview: strings.TrimSpace(run.outputPreview),
		Result:        "",
		UpdatedAt:     run.updatedAt,
	}
	if !run.running {
		out.Result = strings.TrimSpace(run.result)
	}
	return sdkdelegation.CloneResult(out)
}

func (r *Runner) lookup(anchor sdkdelegation.Anchor) (*childRun, error) {
	sessionID := strings.TrimSpace(anchor.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sdk/subagent/acp: session_id is required")
	}
	r.mu.RLock()
	run := r.runs[sessionID]
	r.mu.RUnlock()
	if run == nil {
		return nil, fmt.Errorf("sdk/subagent/acp: child session %q not found", sessionID)
	}
	return run, nil
}

func (r *Runner) nextAgentID(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("%s-%03d", name, r.counter.Add(1))
}

func (r *Runner) permissionCallback(spawn sdksubagent.SpawnContext, cfg AgentConfig, agentID string) PermissionHandler {
	if r.permissionBridge != nil {
		return func(ctx context.Context, req sdkacpclient.RequestPermissionRequest) (sdkacpclient.RequestPermissionResponse, error) {
			resp, err := r.permissionBridge.RequestPermission(ctx, PermissionRequest{
				Spawn: spawn,
				Agent: sdkdelegation.Agent{
					Name:        strings.TrimSpace(cfg.Name),
					Description: strings.TrimSpace(cfg.Description),
				},
				AgentID: strings.TrimSpace(agentID),
				Request: req,
			})
			if err != nil {
				return sdkacpclient.RequestPermissionResponse{}, err
			}
			return resp, nil
		}
	}
	if r.permissionHandler != nil {
		return r.permissionHandler
	}
	return func(ctx context.Context, req sdkacpclient.RequestPermissionRequest) (sdkacpclient.RequestPermissionResponse, error) {
		if !strings.EqualFold(strings.TrimSpace(cfg.Name), "self") {
			resolution := sdkacpclient.ResolveApproveAllOnce(spawn.Mode, cfg.Name, req)
			if auto, ok := resolution.AutoResponse(); ok {
				return auto, nil
			}
		}
		if spawn.ApprovalRequester != nil {
			resp, err := spawn.ApprovalRequester.RequestSubagentApproval(ctx, translateApprovalRequest(spawn, cfg, agentID, req))
			if err != nil {
				return sdkacpclient.RequestPermissionResponse{}, err
			}
			if strings.EqualFold(strings.TrimSpace(resp.Outcome), "selected") && strings.TrimSpace(resp.OptionID) != "" {
				return sdkacpclient.PermissionSelectedOutcome(resp.OptionID), nil
			}
		}
		return sdkacpclient.PermissionSelectedOutcome("reject_once"), nil
	}
}

func translateApprovalRequest(
	spawn sdksubagent.SpawnContext,
	cfg AgentConfig,
	agentID string,
	req sdkacpclient.RequestPermissionRequest,
) sdksubagent.ApprovalRequest {
	options := make([]sdksubagent.ApprovalOption, 0, len(req.Options))
	for _, item := range req.Options {
		options = append(options, sdksubagent.ApprovalOption{
			ID:   strings.TrimSpace(item.OptionID),
			Name: strings.TrimSpace(item.Name),
			Kind: strings.TrimSpace(item.Kind),
		})
	}
	return sdksubagent.ApprovalRequest{
		SessionRef: sdksession.NormalizeSessionRef(spawn.SessionRef),
		Session:    sdksession.CloneSession(spawn.Session),
		TaskID:     strings.TrimSpace(spawn.TaskID),
		Agent:      firstNonEmpty(strings.TrimSpace(cfg.Name), strings.TrimSpace(agentID)),
		Mode:       strings.TrimSpace(spawn.Mode),
		ToolCall: sdksubagent.ApprovalToolCall{
			ID:     strings.TrimSpace(req.ToolCall.ToolCallID),
			Name:   toolCallName(req.ToolCall),
			Kind:   trimStringPtr(req.ToolCall.Kind),
			Title:  trimStringPtr(req.ToolCall.Title),
			Status: trimStringPtr(req.ToolCall.Status),
		},
		Options: options,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func toolCallName(update sdkacpclient.ToolCallUpdate) string {
	if output, ok := update.RawOutput.(map[string]any); ok {
		if name, _ := output["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	if input, ok := update.RawInput.(map[string]any); ok {
		if name, _ := input["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return "UNKNOWN"
}

func compactPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(lines) == 0 {
		return ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		last = strings.TrimSpace(text)
	}
	if len(last) <= 160 {
		return last
	}
	return strings.TrimSpace(last[:80]) + " ...[truncated]... " + strings.TrimSpace(last[len(last)-48:])
}

func pickWorkDir(preferred string, fallback string) string {
	if text := strings.TrimSpace(preferred); text != "" {
		return text
	}
	return strings.TrimSpace(fallback)
}

func (r *Runner) handleUpdate(run *childRun, env sdkacpclient.UpdateEnvelope) {
	if run == nil {
		return
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	run.updatedAt = r.clock()
	switch update := env.Update.(type) {
	case sdkacpclient.ContentChunk:
		if text := chunkText(update); text != "" {
			run.outputPreview = compactPreview(text)
			if strings.TrimSpace(update.SessionUpdate) == sdkacpclient.UpdateAgentMessage {
				if run.result == "" {
					run.result = text
				} else {
					run.result = strings.TrimSpace(run.result + text)
				}
			}
		}
	case sdkacpclient.ToolCall:
		run.outputPreview = compactPreview(toolActivity(update.Title, update.Kind, update.Status))
	case sdkacpclient.ToolCallUpdate:
		run.outputPreview = compactPreview(toolActivity(derefString(update.Title), derefString(update.Kind), derefString(update.Status)))
	case sdkacpclient.PlanUpdate:
		run.outputPreview = "updating plan"
	}
}

func chunkText(chunk sdkacpclient.ContentChunk) string {
	var text sdkacpclient.TextChunk
	if err := json.Unmarshal(chunk.Content, &text); err == nil {
		return strings.TrimSpace(text.Text)
	}
	return ""
}

func toolActivity(title string, kind string, status string) string {
	title = strings.TrimSpace(title)
	kind = strings.TrimSpace(strings.ToLower(kind))
	status = strings.TrimSpace(strings.ToLower(status))
	switch {
	case title != "":
		return strings.ToLower(title)
	case kind != "" && status != "":
		return kind + " " + status
	case kind != "":
		return kind
	default:
		return "working"
	}
}

func derefString(in *string) string {
	if in == nil {
		return ""
	}
	return strings.TrimSpace(*in)
}
