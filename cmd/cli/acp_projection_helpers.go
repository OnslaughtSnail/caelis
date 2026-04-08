package main

import (
	"strings"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

func acpPlanEntriesToTUI(entries []internalacp.PlanEntry) []tuievents.PlanEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]tuievents.PlanEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, tuievents.PlanEntry{
			Content: strings.TrimSpace(entry.Content),
			Status:  strings.TrimSpace(entry.Status),
		})
	}
	return out
}

func projectionNarrativeSnapshot(events []acpProjectionPersistedEvent) (assistant string, reasoning string) {
	for _, ev := range events {
		if !strings.EqualFold(strings.TrimSpace(ev.Kind), "projection") {
			continue
		}
		stream := strings.ToLower(strings.TrimSpace(ev.Stream))
		switch stream {
		case "assistant", "answer":
			switch {
			case strings.TrimSpace(ev.FullText) != "":
				assistant = ev.FullText
			case strings.TrimSpace(ev.DeltaText) != "":
				next, _, _ := acpprojector.MergeNarrativeChunk(assistant, ev.DeltaText)
				assistant = next
			}
		case "reasoning":
			switch {
			case strings.TrimSpace(ev.FullText) != "":
				reasoning = ev.FullText
			case strings.TrimSpace(ev.DeltaText) != "":
				next, _, _ := acpprojector.MergeNarrativeChunk(reasoning, ev.DeltaText)
				reasoning = next
			}
		}
	}
	return strings.TrimSpace(assistant), strings.TrimSpace(reasoning)
}
