package runtime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

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
	if mode != SubmissionConversation && mode != SubmissionOverlay {
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

	if initial.Mode == SubmissionOverlay {
		if !h.handleOverlaySubmission(nil, initial) {
			return
		}
		_ = h.appendOutputLifecycle(RunLifecycleStatusCompleted, "run", nil)
		return
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
				if sub.Mode == SubmissionOverlay {
					if !h.handleOverlaySubmission(inv, sub) {
						return false, false
					}
					return false, true
				}
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
				if sub.Mode == SubmissionOverlay {
					if !h.handleOverlaySubmission(inv, sub) {
						_ = pump.respond(false)
						return false, false
					}
					if !pump.respond(true) {
						if restartAfterClose {
							return true, true
						}
						return false, true
					}
					continue
				}
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
