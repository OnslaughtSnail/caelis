package runtime

import (
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const replayBufferCapacity = 512
const replayFetchLimit = 256

type replayItem struct {
	seq     uint64
	event   *session.Event
	err     error
	durable bool
}

type replaySnapshot struct {
	items                 []replayItem
	startSeq              uint64
	nextSeq               uint64
	lastDroppedDurableSeq uint64
	closed                bool
	terminalErr           error
}

type replayBuffer struct {
	mu                    sync.Mutex
	capacity              int
	startSeq              uint64
	nextSeq               uint64
	lastDroppedDurableSeq uint64
	items                 []replayItem
	closed                bool
	terminalErr           error
}

func newReplayBuffer(capacity int) *replayBuffer {
	if capacity <= 0 {
		capacity = replayBufferCapacity
	}
	return &replayBuffer{
		capacity: capacity,
		startSeq: 1,
		nextSeq:  1,
	}
}

func (b *replayBuffer) append(ev *session.Event, err error, durable bool) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := b.nextSeq
	b.nextSeq++
	var cp *session.Event
	if ev != nil {
		copied := *ev
		cp = &copied
	}
	b.items = append(b.items, replayItem{
		seq:     seq,
		event:   cp,
		err:     err,
		durable: durable,
	})
	for len(b.items) > b.capacity {
		dropped := b.items[0]
		b.items = b.items[1:]
		b.startSeq = dropped.seq + 1
		if dropped.durable && dropped.seq > b.lastDroppedDurableSeq {
			b.lastDroppedDurableSeq = dropped.seq
		}
	}
	if len(b.items) == 0 && b.startSeq < b.nextSeq {
		b.startSeq = b.nextSeq
	}
	return seq
}

func (b *replayBuffer) close(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.terminalErr = err
}

func (b *replayBuffer) snapshotFrom(next uint64) replaySnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := replaySnapshot{
		startSeq:              b.startSeq,
		nextSeq:               b.nextSeq,
		lastDroppedDurableSeq: b.lastDroppedDurableSeq,
		closed:                b.closed,
		terminalErr:           b.terminalErr,
	}
	if next < b.startSeq {
		next = b.startSeq
	}
	if len(b.items) == 0 {
		return snap
	}
	startIdx := 0
	for startIdx < len(b.items) && b.items[startIdx].seq < next {
		startIdx++
	}
	if startIdx >= len(b.items) {
		return snap
	}
	snap.items = append([]replayItem(nil), b.items[startIdx:]...)
	return snap
}

func durableReplaySlice(events []*session.Event, persistPartial bool) []*session.Event {
	out := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if isDurableReplayEvent(ev, persistPartial) {
			out = append(out, ev)
		}
	}
	return out
}

func lastCursor(events []*session.Event, fallback string) string {
	if len(events) == 0 {
		return fallback
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && strings.TrimSpace(events[i].ID) != "" {
			return events[i].ID
		}
	}
	return fallback
}

func streamResyncEvent() *session.Event {
	return session.MarkUIOnly(&session.Event{
		ID:   eventID(),
		Time: now(),
		Message: model.Message{
			Role: model.RoleSystem,
			Text: "",
		},
		Meta: map[string]any{
			"kind": "stream_resync",
		},
	})
}

func isDurableReplayEvent(ev *session.Event, persistPartial bool) bool {
	if ev == nil {
		return false
	}
	if !shouldPersistEvent(ev, persistPartial) {
		return false
	}
	if isEventPartial(ev) {
		return false
	}
	return true
}

func isEventPartial(ev *session.Event) bool {
	return session.IsPartial(ev)
}
