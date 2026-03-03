package runtime

import (
	"sort"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	metaKindRecovery = "recovery"
)

type pendingToolCall struct {
	EventIndex int
	ID         string
	Name       string
	Args       string
}

func buildRecoveryEvents(events []*session.Event) []*session.Event {
	window := contextWindowEvents(events)
	if len(window) == 0 {
		return nil
	}

	pending := map[string]pendingToolCall{}
	order := make([]string, 0, 8)

	for idx, ev := range window {
		if ev == nil {
			continue
		}
		if len(ev.Message.ToolCalls) > 0 {
			for _, call := range ev.Message.ToolCalls {
				if call.ID == "" || call.Name == "" {
					continue
				}
				if _, exists := pending[call.ID]; exists {
					continue
				}
				pending[call.ID] = pendingToolCall{
					EventIndex: idx,
					ID:         call.ID,
					Name:       call.Name,
					Args:       strings.TrimSpace(call.Args),
				}
				order = append(order, call.ID)
			}
		}
		if ev.Message.ToolResponse != nil && ev.Message.ToolResponse.ID != "" {
			delete(pending, ev.Message.ToolResponse.ID)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	sort.Slice(order, func(i, j int) bool {
		left, lok := pending[order[i]]
		right, rok := pending[order[j]]
		if !lok || !rok {
			return order[i] < order[j]
		}
		if left.EventIndex == right.EventIndex {
			return left.ID < right.ID
		}
		return left.EventIndex < right.EventIndex
	})

	out := make([]*session.Event, 0, len(order))
	for _, callID := range order {
		call, exists := pending[callID]
		if !exists {
			continue
		}
		out = append(out, &session.Event{
			ID:   eventID(),
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID:   call.ID,
					Name: call.Name,
					Result: map[string]any{
						"error":       "tool call interrupted before completion",
						"interrupted": true,
					},
				},
			},
			Meta: map[string]any{
				metaKind: metaKindRecovery,
				metaKindRecovery: map[string]any{
					"type":         "dangling_tool_call",
					"tool_call_id": call.ID,
					"tool_name":    call.Name,
					"tool_args":    call.Args,
				},
			},
		})
	}
	return out
}
