package runtime

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// FinalAssistantText reconstructs the latest canonical assistant output from a
// session event stream. If the final assistant turn was split across multiple
// consecutive assistant events, it joins that trailing run back together.
func FinalAssistantText(events []*session.Event) string {
	if len(events) == 0 {
		return ""
	}

	var trailing []string
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || !session.IsCanonicalHistoryEvent(ev) {
			continue
		}
		if ev.Message.Role != model.RoleAssistant {
			if len(trailing) > 0 {
				break
			}
			continue
		}
		text := strings.TrimSpace(ev.Message.TextContent())
		if text == "" {
			if len(trailing) > 0 {
				break
			}
			continue
		}
		trailing = append(trailing, text)
	}
	if len(trailing) > 0 {
		for left, right := 0, len(trailing)-1; left < right; left, right = left+1, right-1 {
			trailing[left], trailing[right] = trailing[right], trailing[left]
		}
		return joinAssistantSegments(trailing)
	}
	return ""
}

func joinAssistantSegments(parts []string) string {
	var b strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if b.Len() > 0 {
			current := b.String()
			if !strings.HasSuffix(current, "\n") && !strings.HasPrefix(part, "\n") {
				b.WriteByte('\n')
			}
		}
		b.WriteString(part)
	}
	return strings.TrimSpace(b.String())
}
