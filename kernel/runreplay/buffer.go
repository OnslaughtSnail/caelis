package runreplay

import (
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const DefaultBufferCapacity = 512

type Item struct {
	Seq     uint64
	Event   *session.Event
	Err     error
	Durable bool
}

type Snapshot struct {
	Items                 []Item
	StartSeq              uint64
	NextSeq               uint64
	LastDroppedDurableSeq uint64
	Closed                bool
	TerminalErr           error
}

type Buffer struct {
	mu                    sync.Mutex
	capacity              int
	startSeq              uint64
	nextSeq               uint64
	lastDroppedDurableSeq uint64
	items                 []Item
	closed                bool
	terminalErr           error
}

func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	return &Buffer{
		capacity: capacity,
		startSeq: 1,
		nextSeq:  1,
	}
}

func (b *Buffer) Append(ev *session.Event, err error, durable bool) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := b.nextSeq
	b.nextSeq++
	b.items = append(b.items, Item{
		Seq:     seq,
		Event:   cloneEvent(ev),
		Err:     err,
		Durable: durable,
	})
	for len(b.items) > b.capacity {
		dropped := b.items[0]
		b.items = b.items[1:]
		b.startSeq = dropped.Seq + 1
		if dropped.Durable && dropped.Seq > b.lastDroppedDurableSeq {
			b.lastDroppedDurableSeq = dropped.Seq
		}
	}
	if len(b.items) == 0 && b.startSeq < b.nextSeq {
		b.startSeq = b.nextSeq
	}
	return seq
}

func (b *Buffer) Close(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.terminalErr = err
}

func (b *Buffer) SnapshotFrom(next uint64) Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := Snapshot{
		StartSeq:              b.startSeq,
		NextSeq:               b.nextSeq,
		LastDroppedDurableSeq: b.lastDroppedDurableSeq,
		Closed:                b.closed,
		TerminalErr:           b.terminalErr,
	}
	if next < b.startSeq {
		next = b.startSeq
	}
	if len(b.items) == 0 {
		return snap
	}
	startIdx := 0
	for startIdx < len(b.items) && b.items[startIdx].Seq < next {
		startIdx++
	}
	if startIdx >= len(b.items) {
		return snap
	}
	snap.Items = append([]Item(nil), b.items[startIdx:]...)
	return snap
}

func cloneEvent(ev *session.Event) *session.Event {
	if ev == nil {
		return nil
	}
	copied := *ev
	return &copied
}
