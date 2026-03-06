package session

import (
	"iter"
	"maps"
)

// Events provides readonly access to a sequence of session events.
type Events interface {
	All() iter.Seq[*Event]
	Len() int
	At(i int) *Event
}

// ReadonlyState provides readonly access to session state.
type ReadonlyState interface {
	Get(string) (any, bool)
	All() iter.Seq2[string, any]
}

type eventSlice struct {
	events []*Event
}

// NewEvents wraps one event slice as a readonly view.
func NewEvents(events []*Event) Events {
	if len(events) == 0 {
		return eventSlice{}
	}
	out := make([]*Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		cp := *ev
		out = append(out, &cp)
	}
	return eventSlice{events: out}
}

func (e eventSlice) All() iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for i := 0; i < len(e.events); i++ {
			if !yield(e.At(i)) {
				return
			}
		}
	}
}

func (e eventSlice) Len() int {
	return len(e.events)
}

func (e eventSlice) At(i int) *Event {
	if i < 0 || i >= len(e.events) || e.events[i] == nil {
		return nil
	}
	cp := *e.events[i]
	return &cp
}

type readonlyStateSnapshot struct {
	values map[string]any
}

// NewReadonlyState wraps one map snapshot as readonly state.
func NewReadonlyState(values map[string]any) ReadonlyState {
	out := map[string]any{}
	maps.Copy(out, values)
	return readonlyStateSnapshot{values: out}
}

func (s readonlyStateSnapshot) Get(key string) (any, bool) {
	value, ok := s.values[key]
	return value, ok
}

func (s readonlyStateSnapshot) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for key, value := range s.values {
			if !yield(key, value) {
				return
			}
		}
	}
}
