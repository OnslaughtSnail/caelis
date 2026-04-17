package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type tuiDiagnostics struct {
	mu sync.RWMutex

	Frames             uint64
	IncrementalFrames  uint64
	FullRepaints       uint64
	SlowFrames         uint64
	LastFrameDuration  time.Duration
	AvgFrameDuration   time.Duration
	MaxFrameDuration   time.Duration
	LogBytes           uint64
	PeakFrameBytes     uint64
	RedrawMode         string
	LastRenderAt       time.Time
	LastInputAt        time.Time
	LastInputLatency   time.Duration
	AvgInputLatency    time.Duration
	P95InputLatency    time.Duration
	LastMentionLatency time.Duration
}

func newTUIDiagnostics() *tuiDiagnostics {
	return &tuiDiagnostics{
		RedrawMode: "incremental",
	}
}

func (d *tuiDiagnostics) SetRedrawMode(mode string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if mode == "" {
		mode = "unknown"
	}
	d.RedrawMode = mode
}

func (d *tuiDiagnostics) ObserveRender(duration time.Duration, bytes int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Frames++
	if strings.TrimSpace(d.RedrawMode) == "full" {
		d.FullRepaints++
	} else {
		d.IncrementalFrames++
	}
	if duration >= 40*time.Millisecond {
		d.SlowFrames++
	}
	d.LastFrameDuration = duration
	if duration > d.MaxFrameDuration {
		d.MaxFrameDuration = duration
	}
	if d.Frames == 1 {
		d.AvgFrameDuration = duration
	} else {
		total := time.Duration(int64(d.AvgFrameDuration)*(int64(d.Frames)-1) + int64(duration))
		d.AvgFrameDuration = total / time.Duration(d.Frames)
	}
	if bytes > 0 {
		d.LogBytes += uint64(bytes)
		if uint64(bytes) > d.PeakFrameBytes {
			d.PeakFrameBytes = uint64(bytes)
		}
	}
	d.LastRenderAt = time.Now()
}

func (d *tuiDiagnostics) ObserveLogBytes(bytes int) {
	if d == nil || bytes <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.LogBytes += uint64(bytes)
}

func (d *tuiDiagnostics) ObserveInput() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.LastInputAt = time.Now()
}

func (d *tuiDiagnostics) ObserveMentionLatency(latency time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.LastMentionLatency = latency
}

type tuiDiagnosticsSnapshot struct {
	Frames             uint64
	IncrementalFrames  uint64
	FullRepaints       uint64
	SlowFrames         uint64
	LastFrameDuration  time.Duration
	AvgFrameDuration   time.Duration
	MaxFrameDuration   time.Duration
	LogBytes           uint64
	PeakFrameBytes     uint64
	RedrawMode         string
	LastRenderAt       time.Time
	LastInputAt        time.Time
	LastInputLatency   time.Duration
	AvgInputLatency    time.Duration
	P95InputLatency    time.Duration
	LastMentionLatency time.Duration
}

func (d *tuiDiagnostics) UpdateFromModel(
	frames uint64,
	incrementalFrames uint64,
	fullRepaints uint64,
	slowFrames uint64,
	last time.Duration,
	avg time.Duration,
	maxFrame time.Duration,
	renderBytes uint64,
	peakFrameBytes uint64,
	lastRenderAt time.Time,
	lastInputAt time.Time,
	lastInputLatency time.Duration,
	avgInputLatency time.Duration,
	p95InputLatency time.Duration,
	mentionLatency time.Duration,
	redrawMode string,
) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Frames = frames
	d.IncrementalFrames = incrementalFrames
	d.FullRepaints = fullRepaints
	d.SlowFrames = slowFrames
	d.LastFrameDuration = last
	d.AvgFrameDuration = avg
	d.MaxFrameDuration = maxFrame
	if renderBytes > d.LogBytes {
		d.LogBytes = renderBytes
	}
	if peakFrameBytes > d.PeakFrameBytes {
		d.PeakFrameBytes = peakFrameBytes
	}
	d.LastRenderAt = lastRenderAt
	d.LastInputAt = lastInputAt
	d.LastInputLatency = lastInputLatency
	d.AvgInputLatency = avgInputLatency
	d.P95InputLatency = p95InputLatency
	d.LastMentionLatency = mentionLatency
	if strings.TrimSpace(redrawMode) != "" {
		d.RedrawMode = redrawMode
	}
}

func (d *tuiDiagnostics) Snapshot() tuiDiagnosticsSnapshot {
	if d == nil {
		return tuiDiagnosticsSnapshot{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return tuiDiagnosticsSnapshot{
		Frames:             d.Frames,
		IncrementalFrames:  d.IncrementalFrames,
		FullRepaints:       d.FullRepaints,
		SlowFrames:         d.SlowFrames,
		LastFrameDuration:  d.LastFrameDuration,
		AvgFrameDuration:   d.AvgFrameDuration,
		MaxFrameDuration:   d.MaxFrameDuration,
		LogBytes:           d.LogBytes,
		PeakFrameBytes:     d.PeakFrameBytes,
		RedrawMode:         d.RedrawMode,
		LastRenderAt:       d.LastRenderAt,
		LastInputAt:        d.LastInputAt,
		LastInputLatency:   d.LastInputLatency,
		AvgInputLatency:    d.AvgInputLatency,
		P95InputLatency:    d.P95InputLatency,
		LastMentionLatency: d.LastMentionLatency,
	}
}

func (s tuiDiagnosticsSnapshot) Summary() string {
	return fmt.Sprintf("redraw=%s frames=%d incr=%d full=%d slow=%d avg=%s p95_input=%s mention_latency=%s",
		s.RedrawMode,
		s.Frames,
		s.IncrementalFrames,
		s.FullRepaints,
		s.SlowFrames,
		s.AvgFrameDuration,
		s.P95InputLatency,
		s.LastMentionLatency,
	)
}
