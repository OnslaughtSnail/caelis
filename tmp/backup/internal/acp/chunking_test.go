package acp

import (
	"testing"
	"time"
)

func TestAdaptivePartialChunkingPolicy_UsesSmoothThresholdsByDefault(t *testing.T) {
	var policy adaptivePartialChunkingPolicy
	thresholds := policy.thresholds(partialQueueSnapshot{
		queuedParts: 1,
		oldestAge:   10 * time.Millisecond,
	}, time.Now())
	if thresholds.interval != partialFlushInterval || thresholds.softLimit != partialFlushSoftLimit || thresholds.hardLimit != partialFlushHardLimit {
		t.Fatalf("unexpected smooth thresholds: %+v", thresholds)
	}
}

func TestAdaptivePartialChunkingPolicy_EntersCatchUpOnBurstPressure(t *testing.T) {
	var policy adaptivePartialChunkingPolicy
	thresholds := policy.thresholds(partialQueueSnapshot{
		queuedParts: partialEnterCatchUpParts,
		oldestAge:   10 * time.Millisecond,
	}, time.Now())
	if thresholds.interval != partialCatchUpFlushInterval || thresholds.softLimit != partialCatchUpFlushSoftLimit || thresholds.hardLimit != partialCatchUpFlushHardLimit {
		t.Fatalf("unexpected catch-up thresholds: %+v", thresholds)
	}
}

func TestAdaptivePartialChunkingPolicy_ExitsCatchUpAfterHold(t *testing.T) {
	var policy adaptivePartialChunkingPolicy
	t0 := time.Now()
	_ = policy.thresholds(partialQueueSnapshot{
		queuedParts: partialEnterCatchUpParts,
		oldestAge:   10 * time.Millisecond,
	}, t0)
	if policy.mode != partialChunkingCatchUp {
		t.Fatalf("expected catch-up mode, got %v", policy.mode)
	}

	_ = policy.thresholds(partialQueueSnapshot{
		queuedParts: partialExitCatchUpParts,
		oldestAge:   partialExitCatchUpAge,
	}, t0.Add(partialExitCatchUpHold/2))
	if policy.mode != partialChunkingCatchUp {
		t.Fatalf("expected catch-up mode before hold elapsed, got %v", policy.mode)
	}

	thresholds := policy.thresholds(partialQueueSnapshot{
		queuedParts: partialExitCatchUpParts,
		oldestAge:   partialExitCatchUpAge,
	}, t0.Add(partialExitCatchUpHold+partialExitCatchUpHold/2+10*time.Millisecond))
	if policy.mode != partialChunkingSmooth {
		t.Fatalf("expected smooth mode after hold, got %v", policy.mode)
	}
	if thresholds.interval != partialFlushInterval {
		t.Fatalf("expected smooth thresholds after exit, got %+v", thresholds)
	}
}
