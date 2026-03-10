package acp

import "time"

type partialChunkingMode int

const (
	partialChunkingSmooth partialChunkingMode = iota
	partialChunkingCatchUp
)

type partialQueueSnapshot struct {
	queuedParts int
	oldestAge   time.Duration
}

type partialFlushThresholds struct {
	interval          time.Duration
	softLimit         int
	hardLimit         int
	minTimedFlushPart int
}

const (
	partialFlushInterval  = 70 * time.Millisecond
	partialFlushSoftLimit = 192
	partialFlushHardLimit = 768

	partialCatchUpFlushInterval  = 140 * time.Millisecond
	partialCatchUpFlushSoftLimit = 640
	partialCatchUpFlushHardLimit = 2560

	partialEnterCatchUpParts  = 8
	partialEnterCatchUpAge    = 120 * time.Millisecond
	partialExitCatchUpParts   = 2
	partialExitCatchUpAge     = 40 * time.Millisecond
	partialExitCatchUpHold    = 250 * time.Millisecond
	partialReenterHold        = 250 * time.Millisecond
	partialSevereBacklogAge   = 300 * time.Millisecond
	partialSevereBacklogParts = 32
)

type adaptivePartialChunkingPolicy struct {
	mode                 partialChunkingMode
	belowExitThresholdAt time.Time
	lastCatchUpExitAt    time.Time
}

func (p *adaptivePartialChunkingPolicy) reset() {
	*p = adaptivePartialChunkingPolicy{}
}

func (p *adaptivePartialChunkingPolicy) thresholds(snapshot partialQueueSnapshot, now time.Time) partialFlushThresholds {
	switch p.decide(snapshot, now) {
	case partialChunkingCatchUp:
		return partialFlushThresholds{
			interval:          partialCatchUpFlushInterval,
			softLimit:         partialCatchUpFlushSoftLimit,
			hardLimit:         partialCatchUpFlushHardLimit,
			minTimedFlushPart: 4,
		}
	default:
		return partialFlushThresholds{
			interval:          partialFlushInterval,
			softLimit:         partialFlushSoftLimit,
			hardLimit:         partialFlushHardLimit,
			minTimedFlushPart: 2,
		}
	}
}

func (p *adaptivePartialChunkingPolicy) decide(snapshot partialQueueSnapshot, now time.Time) partialChunkingMode {
	if snapshot.queuedParts == 0 {
		if p.mode == partialChunkingCatchUp {
			p.lastCatchUpExitAt = now
		}
		p.mode = partialChunkingSmooth
		p.belowExitThresholdAt = time.Time{}
		return p.mode
	}

	switch p.mode {
	case partialChunkingSmooth:
		if shouldEnterPartialCatchUp(snapshot) && (!p.reentryHoldActive(now) || isSeverePartialBacklog(snapshot)) {
			p.mode = partialChunkingCatchUp
			p.belowExitThresholdAt = time.Time{}
			p.lastCatchUpExitAt = time.Time{}
		}
	case partialChunkingCatchUp:
		if shouldExitPartialCatchUp(snapshot) {
			if p.belowExitThresholdAt.IsZero() {
				p.belowExitThresholdAt = now
			} else if now.Sub(p.belowExitThresholdAt) >= partialExitCatchUpHold {
				p.mode = partialChunkingSmooth
				p.belowExitThresholdAt = time.Time{}
				p.lastCatchUpExitAt = now
			}
		} else {
			p.belowExitThresholdAt = time.Time{}
		}
	}
	return p.mode
}

func (p *adaptivePartialChunkingPolicy) reentryHoldActive(now time.Time) bool {
	return !p.lastCatchUpExitAt.IsZero() && now.Sub(p.lastCatchUpExitAt) < partialReenterHold
}

func shouldEnterPartialCatchUp(snapshot partialQueueSnapshot) bool {
	return snapshot.queuedParts >= partialEnterCatchUpParts || snapshot.oldestAge >= partialEnterCatchUpAge
}

func shouldExitPartialCatchUp(snapshot partialQueueSnapshot) bool {
	return snapshot.queuedParts <= partialExitCatchUpParts && snapshot.oldestAge <= partialExitCatchUpAge
}

func isSeverePartialBacklog(snapshot partialQueueSnapshot) bool {
	return snapshot.queuedParts >= partialSevereBacklogParts || snapshot.oldestAge >= partialSevereBacklogAge
}
