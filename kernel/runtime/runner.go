package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

type SubmissionMode string

const (
	SubmissionConversation SubmissionMode = "conversation"
	SubmissionOverlay      SubmissionMode = "overlay"
)

type Submission struct {
	Text         string
	ContentParts []model.ContentPart
	Mode         SubmissionMode
}

type Runner interface {
	RunID() string
	Events() iter.Seq2[*session.Event, error]
	Submit(Submission) error
	Cancel() bool
	Close() error
}

var (
	// ErrRunnerClosed is returned when an operation is attempted on a closed runner.
	ErrRunnerClosed               = errors.New("runtime: runner is closed")
	errRunnerEventsAlreadyClaimed = errors.New("runtime: runner events already claimed")
	errUnsupportedSubmissionMode  = errors.New("runtime: unsupported submission mode")
)

const replayBufferCapacity = 512
const replayFetchLimit = 256

type replayItem struct {
	seq     uint64
	event   *session.Event
	err     error
	durable bool
}

type replaySnapshot struct {
	items                 []replayItem
	startSeq              uint64
	nextSeq               uint64
	lastDroppedDurableSeq uint64
	closed                bool
	terminalErr           error
}

type replayBuffer struct {
	mu                    sync.Mutex
	capacity              int
	startSeq              uint64
	nextSeq               uint64
	lastDroppedDurableSeq uint64
	items                 []replayItem
	closed                bool
	terminalErr           error
}

type agentRunItem struct {
	event *session.Event
	err   error
}

type agentPanicError struct {
	value any
}

func (e *agentPanicError) Error() string {
	if e == nil {
		return "runtime: agent panic"
	}
	return fmt.Sprintf("runtime: agent panic: %v", e.value)
}

type agentRunPump struct {
	ctx    context.Context
	items  chan agentRunItem
	resume chan bool
	done   chan struct{}
}

func startAgentRunPump(ctx context.Context, ag agent.Agent, inv *invocationContext) *agentRunPump {
	pump := &agentRunPump{
		ctx:    ctx,
		items:  make(chan agentRunItem),
		resume: make(chan bool),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(pump.done)
		defer close(pump.items)
		defer func() {
			if p := recover(); p != nil {
				select {
				case pump.items <- agentRunItem{err: &agentPanicError{value: p}}:
				case <-ctx.Done():
				}
			}
		}()
		for ev, err := range ag.Run(inv) {
			select {
			case pump.items <- agentRunItem{event: ev, err: err}:
			case <-ctx.Done():
				return
			}
			select {
			case cont := <-pump.resume:
				if !cont {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return pump
}

func (p *agentRunPump) next() (agentRunItem, bool) {
	if p == nil {
		return agentRunItem{}, false
	}
	select {
	case item, ok := <-p.items:
		return item, ok
	case <-p.ctx.Done():
		return agentRunItem{}, false
	}
}

func (p *agentRunPump) respond(cont bool) bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	case p.resume <- cont:
		return true
	case <-p.ctx.Done():
		return false
	}
}

type stepBoundaryTracker struct {
	pendingToolCalls map[string]int
	pendingUnnamed   int
}

func (t *stepBoundaryTracker) observe(ev *session.Event) (boundary bool, terminal bool) {
	if ev == nil || isEventPartial(ev) {
		return false, false
	}
	msg := ev.Message
	if msg.Role == model.RoleAssistant {
		if len(msg.ToolCalls) == 0 {
			t.reset()
			return true, true
		}
		t.reset()
		t.pendingToolCalls = make(map[string]int, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if id := strings.TrimSpace(call.ID); id != "" {
				t.pendingToolCalls[id]++
				continue
			}
			t.pendingUnnamed++
		}
		return false, false
	}
	if msg.Role != model.RoleTool || msg.ToolResponse == nil {
		return false, false
	}
	if len(t.pendingToolCalls) == 0 && t.pendingUnnamed == 0 {
		return false, false
	}
	if id := strings.TrimSpace(msg.ToolResponse.ID); id != "" {
		if count := t.pendingToolCalls[id]; count > 0 {
			if count == 1 {
				delete(t.pendingToolCalls, id)
			} else {
				t.pendingToolCalls[id] = count - 1
			}
		}
	} else if t.pendingUnnamed > 0 {
		t.pendingUnnamed--
	}
	if len(t.pendingToolCalls) == 0 && t.pendingUnnamed == 0 {
		t.reset()
		return true, false
	}
	return false, false
}

func (t *stepBoundaryTracker) reset() {
	t.pendingToolCalls = nil
	t.pendingUnnamed = 0
}

// loopDetector detects infinite loops where the model produces identical
// outputs across consecutive agent turns. It computes a signature from the
// full turn output (text, reasoning, tool call names AND arguments) so that
// legitimate repeated tool calls with different parameters (e.g. reading
// different lines of the same file) are never flagged.
type loopDetector struct {
	lastSig        string
	consecutiveCnt int
}

const loopDetectorThreshold = 3

// observeTurn records one complete agent turn and returns true when the model
// appears stuck in an infinite loop (loopDetectorThreshold consecutive
// identical turns).
func (d *loopDetector) observeTurn(events []*session.Event) bool {
	if isStableTaskPollingTurn(events) {
		d.reset()
		return false
	}
	sig := turnSignature(events)
	if sig == "" {
		d.reset()
		return false
	}
	if sig == d.lastSig {
		d.consecutiveCnt++
	} else {
		d.lastSig = sig
		d.consecutiveCnt = 1
	}
	return d.consecutiveCnt >= loopDetectorThreshold
}

func (d *loopDetector) reset() {
	d.lastSig = ""
	d.consecutiveCnt = 0
}

func isStableTaskPollingTurn(events []*session.Event) bool {
	if len(events) != 2 {
		return false
	}
	assistant := events[0]
	toolResult := events[1]
	if assistant == nil || toolResult == nil {
		return false
	}
	if assistant.Message.Role != model.RoleAssistant || len(assistant.Message.ToolCalls) != 1 {
		return false
	}
	call := assistant.Message.ToolCalls[0]
	if !strings.EqualFold(strings.TrimSpace(call.Name), "TASK") {
		return false
	}
	args, err := model.ParseToolCallArgs(call.Args)
	if err != nil {
		return false
	}
	action, _ := args["action"].(string)
	if !strings.EqualFold(strings.TrimSpace(action), "wait") {
		return false
	}
	if toolResult.Message.Role != model.RoleTool || toolResult.Message.ToolResponse == nil {
		return false
	}
	resp := toolResult.Message.ToolResponse
	if !strings.EqualFold(strings.TrimSpace(resp.Name), "TASK") {
		return false
	}
	running, _ := resp.Result["running"].(bool)
	return running
}

// turnSignature produces a content-addressable hash of a complete agent turn.
// It includes assistant text, reasoning, and every tool call name + normalized
// arguments, plus tool responses. This ensures that two turns that call the
// same tool with different arguments produce different signatures.
func turnSignature(events []*session.Event) string {
	if len(events) == 0 {
		return ""
	}
	h := sha256.New()
	for _, ev := range events {
		if ev == nil {
			continue
		}
		msg := ev.Message
		if msg.Role == model.RoleAssistant {
			h.Write([]byte("A:"))
			h.Write([]byte(strings.TrimSpace(msg.Text)))
			h.Write([]byte("\x00"))
			h.Write([]byte(strings.TrimSpace(msg.Reasoning)))
			h.Write([]byte("\x00"))
			for _, tc := range msg.ToolCalls {
				h.Write([]byte("TC:"))
				h.Write([]byte(strings.TrimSpace(tc.Name)))
				h.Write([]byte(":"))
				h.Write([]byte(normalizeArgs(tc.Args)))
				h.Write([]byte("\x00"))
			}
		} else if msg.Role == model.RoleTool && msg.ToolResponse != nil {
			h.Write([]byte("TR:"))
			h.Write([]byte(strings.TrimSpace(msg.ToolResponse.Name)))
			h.Write([]byte(":"))
			h.Write([]byte(normalizeResultMap(msg.ToolResponse.Result)))
			h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeArgs canonicalizes JSON tool-call argument text so that key
// ordering does not affect the signature.
func normalizeArgs(raw string) string {
	args, err := model.ParseToolCallArgs(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return normalizeMap(args)
}

func normalizeMap(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte(':')
		sb.WriteString(fmt.Sprintf("%v", m[k]))
	}
	sb.WriteByte('}')
	return sb.String()
}

func normalizeResultMap(m map[string]any) string {
	return normalizeMap(m)
}

var errLoopDetected = errors.New("runtime: agent appears stuck in an infinite loop — the same output was produced multiple consecutive times")

func newReplayBuffer(capacity int) *replayBuffer {
	if capacity <= 0 {
		capacity = replayBufferCapacity
	}
	return &replayBuffer{
		capacity: capacity,
		startSeq: 1,
		nextSeq:  1,
	}
}

func (b *replayBuffer) append(ev *session.Event, err error, durable bool) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := b.nextSeq
	b.nextSeq++
	var cp *session.Event
	if ev != nil {
		copied := *ev
		cp = &copied
	}
	b.items = append(b.items, replayItem{
		seq:     seq,
		event:   cp,
		err:     err,
		durable: durable,
	})
	for len(b.items) > b.capacity {
		dropped := b.items[0]
		b.items = b.items[1:]
		b.startSeq = dropped.seq + 1
		if dropped.durable && dropped.seq > b.lastDroppedDurableSeq {
			b.lastDroppedDurableSeq = dropped.seq
		}
	}
	if len(b.items) == 0 && b.startSeq < b.nextSeq {
		b.startSeq = b.nextSeq
	}
	return seq
}

func (b *replayBuffer) close(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.terminalErr = err
}

func (b *replayBuffer) snapshotFrom(next uint64) replaySnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snap := replaySnapshot{
		startSeq:              b.startSeq,
		nextSeq:               b.nextSeq,
		lastDroppedDurableSeq: b.lastDroppedDurableSeq,
		closed:                b.closed,
		terminalErr:           b.terminalErr,
	}
	if next < b.startSeq {
		next = b.startSeq
	}
	if len(b.items) == 0 {
		return snap
	}
	startIdx := 0
	for startIdx < len(b.items) && b.items[startIdx].seq < next {
		startIdx++
	}
	if startIdx >= len(b.items) {
		return snap
	}
	snap.items = append([]replayItem(nil), b.items[startIdx:]...)
	return snap
}

type runHandle struct {
	runtime *Runtime
	req     RunRequest
	runID   string
	sess    *session.Session
	ctx     context.Context
	cancel  context.CancelFunc

	replay         *replayBuffer
	eventNotifyCh  chan struct{}
	submitNotifyCh chan struct{}
	doneCh         chan struct{}

	eventsClaimed atomic.Bool
	closed        atomic.Bool
	closeOnce     sync.Once

	submitSlot atomic.Pointer[Submission]
}

func (h *runHandle) RunID() string { return h.runID }

func (h *runHandle) Submit(sub Submission) error {
	if h == nil || h.closed.Load() {
		return ErrRunnerClosed
	}
	mode := sub.Mode
	if mode == "" {
		mode = SubmissionConversation
	}
	if mode != SubmissionConversation {
		return errUnsupportedSubmissionMode
	}
	cp := &Submission{
		Text:         strings.TrimSpace(sub.Text),
		ContentParts: append([]model.ContentPart(nil), sub.ContentParts...),
		Mode:         mode,
	}
	h.submitSlot.Store(cp)
	select {
	case h.submitNotifyCh <- struct{}{}:
	default:
	}
	return nil
}

func (h *runHandle) Cancel() bool {
	if h == nil || h.closed.Load() {
		return false
	}
	h.cancel()
	return true
}

func (h *runHandle) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.closed.Store(true)
		h.cancel()
		<-h.doneCh
	})
	return nil
}

func (h *runHandle) Events() iter.Seq2[*session.Event, error] {
	if h == nil {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, ErrRunnerClosed)
		}
	}
	if !h.eventsClaimed.CompareAndSwap(false, true) {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, errRunnerEventsAlreadyClaimed)
		}
	}
	return func(yield func(*session.Event, error) bool) {
		var (
			nextSeq           uint64 = 1
			lastDurableCursor string
			pendingResync     bool
		)
		for {
			snap := h.replay.snapshotFrom(nextSeq)
			if nextSeq < snap.startSeq {
				if nextSeq <= snap.lastDroppedDurableSeq {
					if pendingResync {
						if !yield(streamResyncEvent(), nil) {
							return
						}
						pendingResync = false
					}
					cursor := lastDurableCursor
					for {
						events, nextCursor, err := h.fetchDurableAfter(cursor)
						if err != nil {
							if !yield(nil, err) {
								return
							}
							return
						}
						for _, ev := range events {
							if ev == nil {
								continue
							}
							if isDurableReplayEvent(ev, h.req.PersistPartialEvents) {
								lastDurableCursor = ev.ID
							}
							if !yield(ev, nil) {
								return
							}
						}
						if nextCursor != "" {
							lastDurableCursor = nextCursor
						}
						if len(events) == 0 || nextCursor == "" || nextCursor == cursor {
							break
						}
						cursor = nextCursor
					}
					nextSeq = snap.nextSeq
					pendingResync = true
					continue
				}
				nextSeq = snap.startSeq
				pendingResync = true
			}
			if pendingResync && len(snap.items) > 0 {
				if !yield(streamResyncEvent(), nil) {
					return
				}
				pendingResync = false
			}
			if len(snap.items) > 0 {
				for _, item := range snap.items {
					if item.durable && item.event != nil {
						lastDurableCursor = item.event.ID
					}
					if !yield(item.event, item.err) {
						return
					}
					nextSeq = item.seq + 1
					if item.err != nil {
						return
					}
				}
				continue
			}
			if snap.closed {
				if snap.terminalErr != nil {
					yield(nil, snap.terminalErr)
				}
				return
			}
			select {
			case <-h.eventNotifyCh:
			case <-h.doneCh:
			}
		}
	}
}

func (h *runHandle) fetchDurableAfter(cursor string) ([]*session.Event, string, error) {
	if h == nil || h.runtime == nil || h.runtime.store == nil {
		return nil, "", nil
	}
	if withCursor, ok := h.runtime.store.(session.CursorStore); ok {
		events, nextCursor, err := withCursor.ListEventsAfter(h.ctx, h.sess, cursor, replayFetchLimit)
		return durableReplaySlice(events, h.req.PersistPartialEvents), lastCursor(events, nextCursor), err
	}
	events, err := h.runtime.store.ListEvents(h.ctx, h.sess)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if cursor != "" {
		start = len(events)
		for i, ev := range events {
			if ev != nil && ev.ID == cursor {
				start = i + 1
				break
			}
		}
	}
	if start > len(events) {
		start = len(events)
	}
	events = durableReplaySlice(events[start:], h.req.PersistPartialEvents)
	return events, lastCursor(events, cursor), nil
}

func durableReplaySlice(events []*session.Event, persistPartial bool) []*session.Event {
	out := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if isDurableReplayEvent(ev, persistPartial) {
			out = append(out, ev)
		}
	}
	return out
}

func lastCursor(events []*session.Event, fallback string) string {
	if len(events) == 0 {
		return fallback
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && strings.TrimSpace(events[i].ID) != "" {
			return events[i].ID
		}
	}
	return fallback
}

func streamResyncEvent() *session.Event {
	return session.MarkUIOnly(&session.Event{
		ID:   eventID(),
		Time: now(),
		Message: model.Message{
			Role: model.RoleSystem,
			Text: "",
		},
		Meta: map[string]any{
			"kind": "stream_resync",
		},
	})
}

func isDurableReplayEvent(ev *session.Event, persistPartial bool) bool {
	if ev == nil {
		return false
	}
	if !shouldPersistEvent(ev, persistPartial) {
		return false
	}
	if isEventPartial(ev) {
		return false
	}
	return true
}

func isEventPartial(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	value, _ := ev.Meta["partial"].(bool)
	return value
}

func now() time.Time {
	return time.Now()
}

func (r *Runtime) newRunner(ctx context.Context, req RunRequest) (*runHandle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRunRequest(req); err != nil {
		return nil, err
	}
	leaseKey := runLeaseKey(req.AppName, req.UserID, req.SessionID)
	if !r.acquireRunLease(leaseKey) {
		return nil, &SessionBusyError{AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID}
	}
	if _, err := r.ReconcileSession(ctx, ReconcileSessionRequest{
		AppName:     req.AppName,
		UserID:      req.UserID,
		SessionID:   req.SessionID,
		ExecRuntime: req.CoreTools.Runtime,
	}); err != nil {
		r.releaseRunLease(leaseKey)
		return nil, err
	}
	sess, err := r.store.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
	if err != nil {
		r.releaseRunLease(leaseKey)
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	handle := &runHandle{
		runtime:        r,
		req:            req,
		runID:          idutil.NewRunID(),
		sess:           sess,
		ctx:            runCtx,
		cancel:         cancel,
		replay:         newReplayBuffer(replayBufferCapacity),
		eventNotifyCh:  make(chan struct{}, 1),
		submitNotifyCh: make(chan struct{}, 1),
		doneCh:         make(chan struct{}),
	}
	go handle.runWorker(leaseKey)
	return handle, nil
}

func (h *runHandle) runWorker(leaseKey string) {
	defer close(h.doneCh)
	defer h.runtime.releaseRunLease(leaseKey)
	defer h.closed.Store(true)
	defer func() {
		if p := recover(); p != nil {
			cause := fmt.Errorf("runtime: agent panic: %v", p)
			phase := "run"
			if _, delegated := delegationLineageFromContext(h.ctx); delegated {
				cause = fmt.Errorf("subagent panic: %v", p)
				phase = "delegate_panic"
			}
			if h.appendOutputLifecycle(RunLifecycleStatusFailed, phase, cause) {
				_ = h.appendOutput(nil, cause, false)
			}
		}
	}()
	defer func() {
		h.replay.close(nil)
		select {
		case h.eventNotifyCh <- struct{}{}:
		default:
		}
	}()

	if !h.appendOutputLifecycle(RunLifecycleStatusRunning, "run", nil) {
		return
	}

	var initial *Submission
	if strings.TrimSpace(h.req.Input) != "" || len(h.req.ContentParts) > 0 {
		initial = &Submission{
			Text:         h.req.Input,
			ContentParts: append([]model.ContentPart(nil), h.req.ContentParts...),
			Mode:         SubmissionConversation,
		}
	}
	if initial == nil {
		for initial == nil {
			select {
			case <-h.ctx.Done():
				_ = h.appendOutputLifecycle(RunLifecycleStatusInterrupted, "run", h.ctx.Err())
				return
			default:
			}
			if sub := h.takeSubmission(); sub != nil {
				initial = sub
				break
			}
			select {
			case <-h.submitNotifyCh:
			case <-h.ctx.Done():
				_ = h.appendOutputLifecycle(RunLifecycleStatusInterrupted, "run", h.ctx.Err())
				return
			}
		}
	}

	allEvents, ok := h.applySubmission(initial)
	if !ok {
		return
	}
	inv, err := h.runtime.buildInvocationContext(h.ctx, h.sess, h.req, allEvents)
	if err != nil {
		h.emitTerminalError(err)
		return
	}
	defer func() {
		if inv == nil || inv.tasks == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		inv.tasks.cleanupTurn(cleanupCtx)
	}()

	for {
		restart, ok := h.driveAgentRun(inv)
		if !ok {
			return
		}
		if restart {
			continue
		}
		if h.appendOutputLifecycle(RunLifecycleStatusCompleted, "run", nil) {
			return
		}
		return
	}
}

func (h *runHandle) driveAgentRun(inv *invocationContext) (restart bool, ok bool) {
	pump := startAgentRunPump(h.ctx, h.req.Agent, inv)
	var tracker stepBoundaryTracker
	var loop loopDetector
	var turnEvents []*session.Event
	restartAfterClose := false
	for {
		item, open := pump.next()
		if !open {
			if err := h.ctx.Err(); err != nil {
				_ = h.appendOutputLifecycle(RunLifecycleStatusInterrupted, "run", err)
				return false, false
			}
			if restartAfterClose {
				return true, true
			}
			if sub := h.takeSubmission(); sub != nil {
				if _, applied := h.applySubmission(sub); !applied {
					return false, false
				}
				if err := h.runtime.refreshInvocationState(h.ctx, h.sess, inv); err != nil {
					h.emitTerminalError(err)
					return false, false
				}
				return true, true
			}
			return false, true
		}
		if item.err != nil {
			var panicErr *agentPanicError
			if errors.As(item.err, &panicErr) {
				cause := fmt.Errorf("runtime: agent panic: %v", panicErr.value)
				phase := "run"
				if _, delegated := delegationLineageFromContext(h.ctx); delegated {
					cause = fmt.Errorf("subagent panic: %v", panicErr.value)
					phase = "delegate_panic"
				}
				if h.appendOutputLifecycle(RunLifecycleStatusFailed, phase, cause) {
					_ = h.appendOutput(nil, cause, false)
				}
				return false, false
			}
			if isContextOverflowError(item.err) {
				_ = pump.respond(false)
				restarted, handled := h.handleContextOverflow(inv)
				if !handled {
					return false, false
				}
				if restarted {
					return true, true
				}
				continue
			}
			_ = pump.respond(false)
			if !h.emitRunError(item.err) {
				return false, false
			}
			return false, false
		}
		ev := item.event
		persist := shouldPersistEvent(ev, h.req.PersistPartialEvents)
		if ev != nil {
			if !h.appendOutput(ev, nil, persist) {
				_ = pump.respond(false)
				return false, false
			}
			if persist && !isLifecycleEvent(ev) {
				if err := h.runtime.refreshInvocationState(h.ctx, h.sess, inv); err != nil {
					_ = pump.respond(false)
					h.emitTerminalError(err)
					return false, false
				}
			}
			if !isEventPartial(ev) {
				turnEvents = append(turnEvents, ev)
			}
		}
		boundary, terminal := tracker.observe(ev)
		if boundary {
			if loop.observeTurn(turnEvents) {
				_ = pump.respond(false)
				h.emitTerminalError(errLoopDetected)
				return false, false
			}
			turnEvents = turnEvents[:0]
			if sub := h.takeSubmission(); sub != nil {
				loop.reset()
				if _, applied := h.applySubmission(sub); !applied {
					_ = pump.respond(false)
					return false, false
				}
				if err := h.runtime.refreshInvocationState(h.ctx, h.sess, inv); err != nil {
					_ = pump.respond(false)
					h.emitTerminalError(err)
					return false, false
				}
				if terminal {
					restartAfterClose = true
				}
			}
		}
		if !pump.respond(true) {
			if restartAfterClose {
				return true, true
			}
			return false, true
		}
	}
}

func (h *runHandle) handleContextOverflow(inv *invocationContext) (restart bool, ok bool) {
	allEvents, listErr := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if listErr != nil {
		h.emitTerminalError(listErr)
		return false, false
	}
	compactionEvent, compactErr := h.runtime.compactIfNeededWithNotify(h.ctx, compactInput{
		Session:             h.sess,
		Model:               h.req.Model,
		Events:              allEvents,
		ContextWindowTokens: h.req.ContextWindowTokens,
		Trigger:             triggerOverflowRecovery,
		Force:               true,
	}, func(ev *session.Event) bool {
		if ev == nil {
			return true
		}
		return h.appendOutput(ev, nil, shouldPersistEvent(ev, h.req.PersistPartialEvents))
	})
	if compactErr != nil {
		h.emitTerminalError(compactErr)
		return false, false
	}
	if compactionEvent != nil {
		if !h.appendOutput(compactionEvent, nil, shouldPersistEvent(compactionEvent, h.req.PersistPartialEvents)) {
			return false, false
		}
	}
	if err := h.runtime.refreshInvocationState(h.ctx, h.sess, inv); err != nil {
		h.emitTerminalError(err)
		return false, false
	}
	return true, true
}

func (h *runHandle) appendOutputLifecycle(status RunLifecycleStatus, phase string, cause error) bool {
	return h.runtime.appendAndYieldLifecycle(h.ctx, h.sess, status, phase, cause, func(ev *session.Event, err error) bool {
		return h.appendOutput(ev, err, false)
	})
}

func (h *runHandle) emitRunError(err error) bool {
	if err == nil {
		return true
	}
	status := lifecycleStatusForError(err)
	if !h.appendOutputLifecycle(status, "run", err) {
		return false
	}
	return h.appendOutput(nil, err, false)
}

func (h *runHandle) emitTerminalError(err error) {
	if err == nil {
		return
	}
	_ = h.emitRunError(err)
}

func (h *runHandle) appendOutput(ev *session.Event, err error, persist bool) bool {
	if ev != nil {
		prepareEvent(h.ctx, h.sess, ev)
		if persist {
			if appendErr := h.runtime.store.AppendEvent(h.ctx, h.sess, ev); appendErr != nil {
				h.replay.append(nil, appendErr, false)
				return false
			}
		}
		sessionstream.Emit(h.ctx, ev.SessionID, ev)
	}
	durable := ev != nil && isDurableReplayEvent(ev, h.req.PersistPartialEvents)
	h.replay.append(ev, err, durable)
	select {
	case h.eventNotifyCh <- struct{}{}:
	default:
	}
	return err == nil
}

func (h *runHandle) takeSubmission() *Submission {
	return h.submitSlot.Swap(nil)
}

func (h *runHandle) applySubmission(sub *Submission) ([]*session.Event, bool) {
	if sub == nil {
		allEvents, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
		if err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
		return allEvents, true
	}
	existing, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	recoveryEvents := buildRecoveryEvents(existing)
	for _, recoveryEvent := range recoveryEvents {
		if recoveryEvent == nil {
			continue
		}
		prepareEvent(h.ctx, h.sess, recoveryEvent)
		if err := h.runtime.store.AppendEvent(h.ctx, h.sess, recoveryEvent); err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
		if !h.appendOutput(recoveryEvent, nil, false) {
			return nil, false
		}
	}
	userMsg := model.Message{Role: model.RoleUser, Text: sub.Text}
	if len(sub.ContentParts) > 0 {
		userMsg.ContentParts = prepareUserContentParts(sub.Text, sub.ContentParts)
	}
	userEvent := &session.Event{Message: userMsg}
	prepareEvent(h.ctx, h.sess, userEvent)
	if err := h.runtime.store.AppendEvent(h.ctx, h.sess, userEvent); err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	if !h.appendOutput(userEvent, nil, false) {
		return nil, false
	}
	allEvents, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	compactionEvent, compactErr := h.runtime.compactIfNeededWithNotify(h.ctx, compactInput{
		Session:             h.sess,
		Model:               h.req.Model,
		Events:              allEvents,
		ContextWindowTokens: h.req.ContextWindowTokens,
		Trigger:             triggerAuto,
		Force:               false,
	}, func(ev *session.Event) bool {
		if ev == nil {
			return true
		}
		return h.appendOutput(ev, nil, shouldPersistEvent(ev, h.req.PersistPartialEvents))
	})
	if compactErr != nil {
		h.emitTerminalError(compactErr)
		return nil, false
	}
	if compactionEvent != nil {
		if !h.appendOutput(compactionEvent, nil, shouldPersistEvent(compactionEvent, h.req.PersistPartialEvents)) {
			return nil, false
		}
		allEvents, err = h.runtime.listContextWindowEvents(h.ctx, h.sess)
		if err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
	}
	return allEvents, true
}
