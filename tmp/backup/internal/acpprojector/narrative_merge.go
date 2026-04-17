package acpprojector

import "strings"

func MergeNarrativeChunk(existing string, incoming string) (next string, delta string, changed bool) {
	incoming = normalizeNarrativeChunkBoundary(existing, incoming)
	if incoming == "" {
		return existing, "", false
	}
	if existing == "" {
		return incoming, incoming, true
	}
	if incoming == existing {
		return existing, "", false
	}

	const stableReplayThreshold = 12
	if runeCount(existing) >= stableReplayThreshold && strings.HasPrefix(incoming, existing) {
		suffix := incoming[len(existing):]
		if suffix == "" {
			return existing, "", false
		}
		return incoming, suffix, true
	}
	if runeCount(incoming) >= stableReplayThreshold && strings.HasPrefix(existing, incoming) {
		return existing, "", false
	}
	if suffix := overlappingNarrativeSuffix(existing, incoming, 6); suffix != incoming {
		if suffix == "" {
			return existing, "", false
		}
		return existing + suffix, suffix, true
	}
	return existing + incoming, incoming, true
}

func normalizeNarrativeChunkBoundary(existing string, incoming string) string {
	if incoming == "" {
		return ""
	}
	if existing == "" {
		return strings.TrimLeft(incoming, "\uFEFF")
	}
	return strings.TrimLeft(incoming, "\uFFFD\uFEFF")
}

func overlappingNarrativeSuffix(existing string, incoming string, minOverlap int) string {
	existingRunes := []rune(existing)
	incomingRunes := []rune(incoming)
	limit := minInt(len(existingRunes), len(incomingRunes))
	for overlap := limit; overlap >= minOverlap; overlap-- {
		if string(existingRunes[len(existingRunes)-overlap:]) == string(incomingRunes[:overlap]) {
			return string(incomingRunes[overlap:])
		}
	}
	return incoming
}

func runeCount(text string) int {
	return len([]rune(text))
}

func minInt(a int, b int) int {
	if a <= b {
		return a
	}
	return b
}
