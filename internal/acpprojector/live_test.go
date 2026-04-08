package acpprojector

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
)

func TestLiveProjector_DeduplicatesCumulativeNarrativeReplay(t *testing.T) {
	projector := NewLiveProjector()
	prefix := "先列出仓库结构，然后继续说明。"
	full := prefix + "最后给出总结。"

	first := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(prefix),
		},
	})
	if len(first) != 1 || first[0].DeltaText != prefix || first[0].FullText != prefix {
		t.Fatalf("expected first delta to pass through, got %#v", first)
	}

	second := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	})
	if len(second) != 1 || second[0].DeltaText != "最后给出总结。" || second[0].FullText != full {
		t.Fatalf("expected cumulative replay to emit only incremental suffix, got %#v", second)
	}

	third := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	})
	if len(third) != 0 {
		t.Fatalf("expected identical replay to be suppressed, got %#v", third)
	}
}
