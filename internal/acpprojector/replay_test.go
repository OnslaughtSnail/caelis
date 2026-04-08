package acpprojector

import (
	"encoding/json"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
)

func TestReplayProjector_LiveOnlySuppressesLoadingHistory(t *testing.T) {
	projector := NewReplayProjector(ReplayProjectorLiveOnly)

	if out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText("hello"),
		},
	}); len(out) != 0 {
		t.Fatalf("expected loading text replay to be suppressed, got %#v", out)
	}

	if out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.PlanUpdate{
			Entries: []acpclient.PlanEntry{{Content: "step", Status: "in_progress"}},
		},
	}); len(out) != 0 {
		t.Fatalf("expected loading plan replay to be suppressed, got %#v", out)
	}

	projector.MarkLoaded()

	out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(" world"),
		},
	})
	if len(out) != 1 || out[0].DeltaText != " world" {
		t.Fatalf("expected post-load delta to be emitted, got %#v", out)
	}
}

func TestReplayProjector_LiveOnlyDeduplicatesLoadedNarrativeSnapshot(t *testing.T) {
	projector := NewReplayProjector(ReplayProjectorLiveOnly)
	prefix := "先列出仓库结构，然后继续说明。"
	full := prefix + "最后给出总结。"

	if out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(prefix),
		},
	}); len(out) != 0 {
		t.Fatalf("expected loading snapshot to stay suppressed, got %#v", out)
	}

	projector.MarkLoaded()

	out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	})
	if len(out) != 1 || out[0].DeltaText != "最后给出总结。" || out[0].FullText != full {
		t.Fatalf("expected cumulative replay after attach to emit only suffix, got %#v", out)
	}

	if out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	}); len(out) != 0 {
		t.Fatalf("expected identical replay after attach to be suppressed, got %#v", out)
	}
}

func TestReplayProjector_SeedSnapshotDeduplicatesFirstLiveCumulativeUpdate(t *testing.T) {
	projector := NewReplayProjector(ReplayProjectorLiveOnly)
	projector.SeedSnapshot("先列出仓库结构，然后继续说明。", "")
	projector.MarkLoaded()

	out := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText("先列出仓库结构，然后继续说明。最后给出总结。"),
		},
	})
	if len(out) != 1 || out[0].DeltaText != "最后给出总结。" {
		t.Fatalf("expected seeded projector to emit only new suffix, got %#v", out)
	}
}

func mustMarshalReplayText(text string) json.RawMessage {
	data, err := json.Marshal(acpclient.TextContent{Type: "text", Text: text})
	if err != nil {
		panic(err)
	}
	return data
}
