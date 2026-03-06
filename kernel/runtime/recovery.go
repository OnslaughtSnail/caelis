package runtime

import (
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/eventview"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	metaKindRecovery = "recovery"
)

func buildRecoveryEvents(events []*session.Event) []*session.Event {
	pending := eventview.PendingToolCalls(eventview.AgentVisibleView(events))
	if len(pending) == 0 {
		return nil
	}
	out := make([]*session.Event, 0, len(pending))
	for _, call := range pending {
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
