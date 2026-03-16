package session

// ContextWindowEvents rebuilds the model-visible context window from full
// session history. It keeps only the latest compaction checkpoint plus newer
// events.
func ContextWindowEvents(events []*Event) []*Event {
	if len(events) == 0 {
		return nil
	}
	lastCompaction := -1
	for i := len(events) - 1; i >= 0; i-- {
		if isCompactionEvent(events[i]) {
			lastCompaction = i
			break
		}
	}
	if lastCompaction < 0 {
		return append([]*Event(nil), events...)
	}
	return append([]*Event(nil), events[lastCompaction:]...)
}

func isCompactionEvent(ev *Event) bool {
	return EventTypeOf(ev) == EventTypeCompaction
}
