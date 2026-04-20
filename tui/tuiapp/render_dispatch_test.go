package tuiapp

import (
	"testing"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
)

func TestRenderEventPolicyForGatewayEnvelopeUsesStructuredToolLane(t *testing.T) {
	policy, ok := renderEventPolicyFor(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind: appgateway.EventKindToolCall,
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "READ",
				Status:   "running",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	if !ok {
		t.Fatal("renderEventPolicyFor() = not ok, want ok")
	}
	if policy.lane != renderLaneToolStream {
		t.Fatalf("renderEventPolicyFor() lane = %q, want %q", policy.lane, renderLaneToolStream)
	}
	if !policy.flushLogChunks {
		t.Fatal("renderEventPolicyFor() did not flush pending log chunks before structured tool events")
	}
}
