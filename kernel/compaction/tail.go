package compaction

import (
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type SplitOptions struct {
	SoftTailTokens int
	HardTailTokens int
	MinTailEvents  int
}

func SplitTarget(window []*session.Event) ([]*session.Event, []*session.Event) {
	if len(window) == 0 {
		return nil, nil
	}
	tailStart := legacyTailBoundary(window)
	if tailStart <= 0 || tailStart >= len(window) {
		return window, nil
	}
	return window[:tailStart], window[tailStart:]
}

func SplitTargetWithOptions(window []*session.Event, opts SplitOptions) ([]*session.Event, []*session.Event) {
	if len(window) == 0 {
		return nil, nil
	}
	tailStart := findTailBoundary(window, opts)
	if tailStart <= 0 || tailStart >= len(window) {
		return window, nil
	}
	return window[:tailStart], window[tailStart:]
}

func findTailBoundary(events []*session.Event, opts SplitOptions) int {
	if len(events) <= 2 {
		return 0
	}
	softTailTokens := opts.SoftTailTokens
	if softTailTokens <= 0 {
		softTailTokens = 1200
	}
	hardTailTokens := opts.HardTailTokens
	if hardTailTokens <= 0 {
		hardTailTokens = max(softTailTokens+(softTailTokens/2), 1800)
	}
	minTailEvents := opts.MinTailEvents
	if minTailEvents <= 0 {
		minTailEvents = 2
	}

	tailStart := len(events) - minTailEvents
	if tailStart < 0 {
		tailStart = 0
	}
	tailTokens := 0
	for i := len(events) - 1; i >= 0; i-- {
		tailTokens += EstimateEventTokens(events[i])
		if tailTokens > softTailTokens && i < len(events)-minTailEvents {
			break
		}
		tailStart = i
	}

	lastUserIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && events[i].Message.Role == model.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx > 0 {
		userTailTokens := EstimateEventsTokens(events[lastUserIdx:])
		if userTailTokens <= hardTailTokens {
			if tailStart == 0 {
				tailStart = lastUserIdx
			} else {
				tailStart = min(tailStart, lastUserIdx)
			}
		}
	}
	return adjustTailBoundary(events, tailStart)
}

func legacyTailBoundary(events []*session.Event) int {
	if len(events) <= 2 {
		return 0
	}
	lastUserIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && events[i].Message.Role == model.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx <= 0 {
		return 0
	}
	return lastUserIdx
}

func adjustTailBoundary(events []*session.Event, tailStart int) int {
	if tailStart <= 0 || tailStart >= len(events) {
		return tailStart
	}
	for tailStart > 0 {
		prev := events[tailStart-1]
		curr := events[tailStart]
		switch {
		case prev == nil || curr == nil:
			return tailStart
		case len(prev.Message.ToolCalls()) > 0 && curr.Message.ToolResponse() != nil:
			tailStart--
		case prev.Message.ToolResponse() != nil && curr.Message.ToolResponse() != nil:
			tailStart--
		default:
			return tailStart
		}
	}
	return tailStart
}
