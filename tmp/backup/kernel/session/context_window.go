package session

import "strings"

// ContextWindowEvents rebuilds the model-visible context window from full
// session history. When a compaction event exists, it returns the compaction
// checkpoint followed by any unsummarized events (the "tail") that appear
// between the last summarized event and the compaction, plus any events
// appended after the compaction.
//
// This reconstruction is read-only — the original event list is never
// mutated, and no events are duplicated in the store.
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

	compEv := events[lastCompaction]
	if tailIDs := compactionTailEventIDs(compEv); len(tailIDs) > 0 {
		out := make([]*Event, 0, 1+len(tailIDs)+(len(events)-lastCompaction-1))
		out = append(out, compEv)
		for _, tailID := range tailIDs {
			if tailID == "" {
				continue
			}
			for i := 0; i < lastCompaction; i++ {
				if events[i] != nil && events[i].ID == tailID {
					out = append(out, events[i])
					break
				}
			}
		}
		if lastCompaction+1 < len(events) {
			out = append(out, events[lastCompaction+1:]...)
		}
		return out
	}

	// Find the tail boundary: events between the last summarized event and
	// the compaction event. These are the events that were preserved (not
	// summarized) during compaction.
	tailStart := lastCompaction // default: no tail
	if summarizedToID := compactionSummarizedToID(compEv); summarizedToID != "" {
		for i := 0; i < lastCompaction; i++ {
			if events[i] != nil && events[i].ID == summarizedToID {
				tailStart = i + 1
				break
			}
		}
	}

	// Build: [compaction] + [tail events before compaction] + [events after compaction]
	out := make([]*Event, 0, 1+(lastCompaction-tailStart)+(len(events)-lastCompaction-1))
	out = append(out, compEv)
	out = append(out, events[tailStart:lastCompaction]...)
	if lastCompaction+1 < len(events) {
		out = append(out, events[lastCompaction+1:]...)
	}
	return out
}

// compactionSummarizedToID extracts the "summarized_to_event_id" from a
// compaction event's metadata. Returns empty string if not found.
func compactionSummarizedToID(ev *Event) string {
	if ev == nil || ev.Meta == nil {
		return ""
	}
	raw, ok := ev.Meta["compaction"]
	if !ok {
		return ""
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m["summarized_to_event_id"].(string)
	return id
}

func compactionTailEventIDs(ev *Event) []string {
	if ev == nil || ev.Meta == nil {
		return nil
	}
	raw, ok := ev.Meta["compaction"]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	rawIDs, ok := m["tail_event_ids"]
	if !ok {
		return nil
	}
	idsAny, ok := rawIDs.([]any)
	if ok {
		out := make([]string, 0, len(idsAny))
		for _, item := range idsAny {
			id, _ := item.(string)
			id = strings.TrimSpace(id)
			if id != "" {
				out = append(out, id)
			}
		}
		return out
	}
	ids, ok := rawIDs.([]string)
	if ok {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id != "" {
				out = append(out, id)
			}
		}
		return out
	}
	return nil
}

func isCompactionEvent(ev *Event) bool {
	return EventTypeOf(ev) == EventTypeCompaction
}
