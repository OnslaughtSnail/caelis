package delegation

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestWithLineageRoundTrip(t *testing.T) {
	lineage := Lineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-1",
		DelegationID:    "dlg-1",
	}
	ctx := WithLineage(context.Background(), lineage)

	got, ok := LineageFromContext(ctx)
	if !ok {
		t.Fatal("expected lineage in context")
	}
	if got != lineage {
		t.Fatalf("expected lineage %+v, got %+v", lineage, got)
	}
}

func TestAnnotateEventAndMetadataFromEvent(t *testing.T) {
	ev := &session.Event{Message: model.NewTextMessage(model.RoleAssistant, "done")}
	lineage := Lineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-1",
		ParentToolName:  "TASK WRITE",
		DelegationID:    "dlg-1",
	}

	AnnotateEvent(ev, "child", lineage)
	meta, ok := MetadataFromEvent(ev)
	if !ok {
		t.Fatal("expected metadata from annotated event")
	}
	if meta.ParentSessionID != "parent" || meta.ChildSessionID != "child" || meta.DelegationID != "dlg-1" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}
