package acpprojector

import (
	"strings"
	"sync"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type ReplayProjector struct {
	mu          sync.Mutex
	loading     bool
	emitLoading bool
	assistant   string
	reasoning   string
	toolCalls   map[string]toolCallSnapshot
}

type ReplayProjectorMode int

const (
	ReplayProjectorReplayHistory ReplayProjectorMode = iota
	ReplayProjectorLiveOnly
)

func NewReplayProjector(mode ReplayProjectorMode) *ReplayProjector {
	emitLoading := mode != ReplayProjectorLiveOnly
	return &ReplayProjector{
		loading:     true,
		emitLoading: emitLoading,
		toolCalls:   map[string]toolCallSnapshot{},
	}
}

func (p *ReplayProjector) MarkLoaded() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loading = false
}

func (p *ReplayProjector) Snapshot() (assistant string, reasoning string) {
	if p == nil {
		return "", ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assistant, p.reasoning
}

func (p *ReplayProjector) SeedSnapshot(assistant string, reasoning string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assistant = strings.TrimSpace(assistant)
	p.reasoning = strings.TrimSpace(reasoning)
}

func (p *ReplayProjector) Project(env acpclient.UpdateEnvelope) []Projection {
	if p == nil || env.Update == nil {
		return nil
	}
	sessionID := strings.TrimSpace(env.SessionID)
	switch update := env.Update.(type) {
	case acpclient.ContentChunk:
		if out, ok := p.projectContent(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.ToolCall:
		if out, ok := p.projectToolCall(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.ToolCallUpdate:
		if out, ok := p.projectToolCallUpdate(sessionID, update); ok {
			return []Projection{out}
		}
	case acpclient.PlanUpdate:
		p.mu.Lock()
		loading := p.loading
		emitLoading := p.emitLoading
		p.mu.Unlock()
		if loading && !emitLoading {
			return nil
		}
		return []Projection{{
			SessionID:   sessionID,
			PlanEntries: append([]internalacp.PlanEntry(nil), update.Entries...),
		}}
	}
	return nil
}

func (p *ReplayProjector) projectContent(sessionID string, update acpclient.ContentChunk) (Projection, bool) {
	text := decodeTextChunk(update.Content)
	if text == "" {
		return Projection{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch strings.TrimSpace(update.SessionUpdate) {
	case acpclient.UpdateAgentMessage:
		next, delta, changed := MergeNarrativeChunk(p.assistant, text)
		if !changed {
			return Projection{}, false
		}
		p.assistant = next
		if p.loading {
			if !p.emitLoading {
				return Projection{}, false
			}
			return Projection{
				SessionID: sessionID,
				Event:     &session.Event{Message: model.NewTextMessage(model.RoleAssistant, p.assistant)},
				FullText:  p.assistant,
				Stream:    "assistant",
			}, true
		}
		return Projection{
			SessionID: sessionID,
			Event: &session.Event{
				Message: model.NewTextMessage(model.RoleAssistant, delta),
				Meta:    map[string]any{"partial": true, "channel": "answer"},
			},
			DeltaText: delta,
			FullText:  p.assistant,
			Stream:    "assistant",
		}, true
	case acpclient.UpdateAgentThought:
		next, delta, changed := MergeNarrativeChunk(p.reasoning, text)
		if !changed {
			return Projection{}, false
		}
		p.reasoning = next
		if p.loading {
			if !p.emitLoading {
				return Projection{}, false
			}
			return Projection{
				SessionID: sessionID,
				Event:     &session.Event{Message: model.NewReasoningMessage(model.RoleAssistant, p.reasoning, model.ReasoningVisibilityVisible)},
				FullText:  p.reasoning,
				Stream:    "reasoning",
			}, true
		}
		return Projection{
			SessionID: sessionID,
			Event: &session.Event{
				Message: model.NewReasoningMessage(model.RoleAssistant, delta, model.ReasoningVisibilityVisible),
				Meta:    map[string]any{"partial": true, "channel": "reasoning"},
			},
			DeltaText: delta,
			FullText:  p.reasoning,
			Stream:    "reasoning",
		}, true
	default:
		return Projection{}, false
	}
}

func (p *ReplayProjector) projectToolCall(sessionID string, update acpclient.ToolCall) (Projection, bool) {
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return Projection{}, false
	}
	name, args := toolCallShape(update.Title, update.Kind, update.RawInput)
	p.mu.Lock()
	loading := p.loading
	emitLoading := p.emitLoading
	p.toolCalls[callID] = toolCallSnapshot{Name: name, Args: cloneMap(args)}
	p.mu.Unlock()
	if loading && !emitLoading {
		return Projection{}, false
	}
	return Projection{
		SessionID:  sessionID,
		ToolCallID: callID,
		ToolName:   name,
		ToolArgs:   cloneMap(args),
		Event: &session.Event{
			Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
				ID:   callID,
				Name: name,
				Args: marshalToolInput(args),
			}}, ""),
		},
	}, true
}

func (p *ReplayProjector) projectToolCallUpdate(sessionID string, update acpclient.ToolCallUpdate) (Projection, bool) {
	status := strings.ToLower(strings.TrimSpace(derefString(update.Status)))
	if status != internalacp.ToolStatusCompleted && status != internalacp.ToolStatusFailed {
		return Projection{}, false
	}
	callID := strings.TrimSpace(update.ToolCallID)
	if callID == "" {
		return Projection{}, false
	}
	p.mu.Lock()
	loading := p.loading
	emitLoading := p.emitLoading
	snap := p.toolCalls[callID]
	delete(p.toolCalls, callID)
	p.mu.Unlock()
	name := strings.TrimSpace(snap.Name)
	args := cloneMap(snap.Args)
	if title := strings.TrimSpace(derefString(update.Title)); title != "" || strings.TrimSpace(derefString(update.Kind)) != "" || update.RawInput != nil {
		name, args = toolCallShape(derefString(update.Title), derefString(update.Kind), update.RawInput)
	}
	if name == "" {
		name = "TOOL"
	}
	result := resultMap(update.RawOutput)
	if loading && !emitLoading {
		return Projection{}, false
	}
	return Projection{
		SessionID:  sessionID,
		ToolCallID: callID,
		ToolName:   name,
		ToolArgs:   cloneMap(args),
		ToolResult: cloneMap(result),
		ToolStatus: status,
		Event: &session.Event{
			Message: model.MessageFromToolResponse(&model.ToolResponse{
				ID:     callID,
				Name:   name,
				Result: result,
			}),
		},
	}, true
}
