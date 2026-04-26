package projector

import (
	"testing"

	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestEventProjectorNormalizesRuntimeToolStatus(t *testing.T) {
	updates, err := (EventProjector{}).ProjectEvent(&sdksession.Event{
		SessionID: "session-1",
		Type:      sdksession.EventTypeToolCall,
		Protocol: &sdksession.EventProtocol{
			UpdateType: UpdateToolCallInfo,
			ToolCall: &sdksession.ProtocolToolCall{
				ID:     "call-1",
				Name:   "SPAWN",
				Kind:   ToolKindOther,
				Status: "running",
				RawInput: map[string]any{
					"prompt": "child work",
				},
				RawOutput: map[string]any{
					"task_id": "task-1",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ProjectEvent() error = %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("ProjectEvent() produced %d updates, want 1", len(updates))
	}
	update, ok := updates[0].(ToolCallUpdate)
	if !ok {
		t.Fatalf("update = %T, want ToolCallUpdate", updates[0])
	}
	if update.Status == nil || *update.Status != ToolStatusInProgress {
		t.Fatalf("status = %v, want %q", update.Status, ToolStatusInProgress)
	}
}
