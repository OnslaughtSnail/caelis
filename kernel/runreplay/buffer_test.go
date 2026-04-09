package runreplay

import (
	"errors"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestBufferSnapshotFromTracksDroppedDurableItems(t *testing.T) {
	buf := NewBuffer(2)
	ev1 := &session.Event{ID: "ev-1", Message: model.NewTextMessage(model.RoleUser, "one")}
	ev2 := &session.Event{ID: "ev-2", Message: model.NewTextMessage(model.RoleAssistant, "two")}
	ev3 := &session.Event{ID: "ev-3", Message: model.NewTextMessage(model.RoleAssistant, "three")}

	buf.Append(ev1, nil, true)
	buf.Append(ev2, nil, false)
	buf.Append(ev3, nil, true)

	snap := buf.SnapshotFrom(1)
	if snap.StartSeq != 2 {
		t.Fatalf("expected start seq 2, got %d", snap.StartSeq)
	}
	if snap.LastDroppedDurableSeq != 1 {
		t.Fatalf("expected last dropped durable seq 1, got %d", snap.LastDroppedDurableSeq)
	}
	if len(snap.Items) != 2 {
		t.Fatalf("expected 2 retained items, got %d", len(snap.Items))
	}
	if snap.Items[0].Event.ID != "ev-2" || snap.Items[1].Event.ID != "ev-3" {
		t.Fatalf("unexpected retained events: %#v", snap.Items)
	}
}

func TestBufferCloseMarksTerminalState(t *testing.T) {
	buf := NewBuffer(1)
	want := errors.New("terminal")

	buf.Close(want)
	snap := buf.SnapshotFrom(1)
	if !snap.Closed {
		t.Fatal("expected buffer snapshot to be closed")
	}
	if !errors.Is(snap.TerminalErr, want) {
		t.Fatalf("expected terminal error %v, got %v", want, snap.TerminalErr)
	}
}
