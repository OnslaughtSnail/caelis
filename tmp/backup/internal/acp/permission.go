package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/internal/approvalqueue"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
)

type permissionBridge struct {
	conn      *Conn
	sessionID string
	mode      func() string

	mu       sync.Mutex
	allowed  map[string]bool
	rejected map[string]bool
	callAuth map[string]bool
	toolAuth map[string]int
	queue    *approvalqueue.Queue
}

func newPermissionBridge(conn *Conn, sessionID string, modeResolver func() string) *permissionBridge {
	return &permissionBridge{
		conn:      conn,
		sessionID: strings.TrimSpace(sessionID),
		mode:      modeResolver,
		allowed:   map[string]bool{},
		rejected:  map[string]bool{},
		callAuth:  map[string]bool{},
		toolAuth:  map[string]int{},
		queue:     approvalqueue.New(),
	}
}

func (p *permissionBridge) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	if sessionmode.IsFullAccess(p.currentMode()) {
		if sessionmode.IsDangerousCommand(req.Command) {
			return false, &toolexec.ApprovalAbortedError{Reason: "dangerous command blocked in full_access mode"}
		}
		return true, nil
	}
	scope := strings.TrimSpace(req.Command)
	if allowed, decided := p.cached(scope); decided {
		return allowed, nil
	}
	info, _ := toolexec.ToolCallInfoFromContext(ctx)
	callID := strings.TrimSpace(info.ID)
	if callID == "" {
		callID = "approval-" + strings.ToLower(strings.TrimSpace(req.ToolName))
		if callID == "approval-" {
			callID = "approval"
		}
	}
	if allowed, ok := p.callDecision(callID); ok {
		return allowed, nil
	}
	if allowed, ok := p.consumeToolDecision(req.ToolName); ok {
		p.rememberCallDecision(callID, allowed)
		return allowed, nil
	}
	toolCall := commandApprovalToolCall(callID, req)
	var allowed bool
	err := p.queue.Do(ctx, func(ctx context.Context) error {
		if cachedAllowed, decided := p.cached(scope); decided {
			allowed = cachedAllowed
			return nil
		}
		if callAllowed, ok := p.callDecision(callID); ok {
			allowed = callAllowed
			return nil
		}
		if toolAllowed, ok := p.consumeToolDecision(req.ToolName); ok {
			p.rememberCallDecision(callID, toolAllowed)
			allowed = toolAllowed
			return nil
		}
		outcome, err := p.request(ctx, scope, toolCall)
		if err != nil {
			return err
		}
		var applyErr error
		allowed, applyErr = p.applyOutcome(scope, outcome)
		return applyErr
	})
	return allowed, err
}

func (p *permissionBridge) AuthorizeTool(ctx context.Context, req policy.ToolAuthorizationRequest) (bool, error) {
	if sessionmode.IsFullAccess(p.currentMode()) {
		return true, nil
	}
	scope := strings.TrimSpace(req.ScopeKey)
	if scope == "" {
		scope = strings.TrimSpace(req.Target)
	}
	if scope == "" {
		scope = strings.TrimSpace(req.Path)
	}
	if allowed, decided := p.cached(scope); decided {
		return allowed, nil
	}
	info, _ := toolexec.ToolCallInfoFromContext(ctx)
	callID := strings.TrimSpace(info.ID)
	if callID == "" {
		callID = "tool-" + strings.ToLower(strings.TrimSpace(req.ToolName))
	}
	toolCall := authorizationToolCall(callID, req)
	var allowed bool
	err := p.queue.Do(ctx, func(ctx context.Context) error {
		if cachedAllowed, decided := p.cached(scope); decided {
			allowed = cachedAllowed
			return nil
		}
		outcome, err := p.request(ctx, scope, toolCall)
		if err != nil {
			return err
		}
		var applyErr error
		allowed, applyErr = p.applyOutcome(scope, outcome)
		if applyErr == nil {
			p.rememberCallDecision(callID, allowed)
			p.rememberToolDecision(req.ToolName, allowed)
		}
		return applyErr
	})
	return allowed, err
}

func (p *permissionBridge) request(ctx context.Context, scope string, toolCall ToolCallUpdate) (RequestPermissionResponse, error) {
	options := []PermissionOption{
		{OptionID: "allow_once", Name: "Allow once", Kind: PermAllowOnce},
		{OptionID: "reject_once", Name: "Reject once", Kind: PermRejectOnce},
	}
	if strings.TrimSpace(scope) != "" {
		options = append([]PermissionOption{
			{OptionID: "allow_always", Name: "Always allow", Kind: PermAllowAlways},
			{OptionID: "reject_always", Name: "Always reject", Kind: PermRejectAlways},
		}, options...)
	}
	var resp RequestPermissionResponse
	err := p.conn.Call(ctx, MethodSessionReqPermission, RequestPermissionRequest{
		SessionID: p.sessionID,
		ToolCall:  toolCall,
		Options:   options,
	}, &resp)
	return resp, err
}

func (p *permissionBridge) applyOutcome(scope string, resp RequestPermissionResponse) (bool, error) {
	var kind struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(resp.Outcome, &kind); err != nil {
		return false, err
	}
	switch strings.TrimSpace(kind.Outcome) {
	case "cancelled":
		return false, &toolexec.ApprovalAbortedError{Reason: "permission request cancelled"}
	case "selected":
		var selected SelectedPermissionOutcome
		if err := json.Unmarshal(resp.Outcome, &selected); err != nil {
			return false, err
		}
		switch selected.OptionID {
		case "allow_once":
			return true, nil
		case "allow_always":
			p.cache(scope, true)
			return true, nil
		case "reject_always":
			p.cache(scope, false)
			return false, &toolexec.ApprovalAbortedError{Reason: "permission rejected"}
		case "reject_once":
			return false, &toolexec.ApprovalAbortedError{Reason: "permission rejected"}
		default:
			return false, fmt.Errorf("unknown permission option %q", selected.OptionID)
		}
	default:
		return false, fmt.Errorf("unknown permission outcome %q", kind.Outcome)
	}
}

func commandApprovalToolCall(callID string, req toolexec.ApprovalRequest) ToolCallUpdate {
	args := map[string]any{}
	if command := strings.TrimSpace(req.Command); command != "" {
		args["command"] = command
	}
	title := summarizeToolCallTitle(req.ToolName, args)
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSpace(req.ToolName)
	}
	kind := toolKindForName(req.ToolName)
	status := ToolStatusPending
	return ToolCallUpdate{
		ToolCallID: callID,
		Title:      ptr(title),
		Kind:       ptr(kind),
		Status:     ptr(status),
		RawInput:   args,
		Locations:  toolLocations(args, nil),
	}
}

func authorizationToolCall(callID string, req policy.ToolAuthorizationRequest) ToolCallUpdate {
	args := map[string]any{}
	if path := strings.TrimSpace(req.Path); path != "" {
		args["path"] = path
	}
	if target := strings.TrimSpace(req.Target); target != "" {
		args["target"] = target
	}
	title := summarizeToolCallTitle(req.ToolName, args)
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSpace(req.ToolName)
	}
	kind := toolKindForName(req.ToolName)
	status := ToolStatusPending
	return ToolCallUpdate{
		ToolCallID: callID,
		Title:      ptr(title),
		Kind:       ptr(kind),
		Status:     ptr(status),
		RawInput:   args,
		Locations:  toolLocations(args, nil),
	}
}

func (p *permissionBridge) cache(scope string, allowed bool) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if allowed {
		p.allowed[scope] = true
		delete(p.rejected, scope)
		return
	}
	p.rejected[scope] = true
	delete(p.allowed, scope)
}

func (p *permissionBridge) cached(scope string) (bool, bool) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowed[scope] {
		return true, true
	}
	if p.rejected[scope] {
		return false, true
	}
	return false, false
}

func (p *permissionBridge) callDecision(callID string) (bool, bool) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	allowed, ok := p.callAuth[callID]
	return allowed, ok
}

func (p *permissionBridge) rememberCallDecision(callID string, allowed bool) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callAuth[callID] = allowed
}

func (p *permissionBridge) rememberToolDecision(toolName string, allowed bool) {
	if !allowed {
		return
	}
	toolName = strings.ToUpper(strings.TrimSpace(toolName))
	if toolName == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolAuth[toolName]++
}

func (p *permissionBridge) consumeToolDecision(toolName string) (bool, bool) {
	toolName = strings.ToUpper(strings.TrimSpace(toolName))
	if toolName == "" {
		return false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	count := p.toolAuth[toolName]
	if count <= 0 {
		return false, false
	}
	if count == 1 {
		delete(p.toolAuth, toolName)
	} else {
		p.toolAuth[toolName] = count - 1
	}
	return true, true
}

func (p *permissionBridge) currentMode() string {
	if p == nil || p.mode == nil {
		return sessionmode.DefaultMode
	}
	return sessionmode.Normalize(p.mode())
}
