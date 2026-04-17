package local

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdkterminal "github.com/OnslaughtSnail/caelis/sdk/terminal"
)

type terminalService struct {
	tasks *taskRuntime
}

func newTerminalService(tasks *taskRuntime) *terminalService {
	return &terminalService{tasks: tasks}
}

func (s *terminalService) Read(ctx context.Context, req sdkterminal.ReadRequest) (sdkterminal.Snapshot, error) {
	ref := sdkterminal.NormalizeRef(req.Ref)
	cursor := sdkterminal.CloneCursor(req.Cursor)
	task, err := s.resolveTask(ctx, ref)
	if err != nil {
		return sdkterminal.Snapshot{}, err
	}
	status, err := task.session.Status(ctx)
	if err != nil {
		return sdkterminal.Snapshot{}, err
	}
	stdout, stderr, nextStdout, nextStderr, err := task.session.ReadOutput(ctx, cursor.Stdout, cursor.Stderr)
	if err != nil {
		return sdkterminal.Snapshot{}, err
	}
	snap := sdkterminal.Snapshot{
		Ref: sdkterminal.Ref{
			SessionID:  strings.TrimSpace(task.sessionRef.SessionID),
			TaskID:     strings.TrimSpace(task.ref.TaskID),
			TerminalID: strings.TrimSpace(status.Terminal.TerminalID),
		},
		Cursor: sdkterminal.Cursor{
			Stdout: nextStdout,
			Stderr: nextStderr,
		},
		Running:       status.Running,
		SupportsInput: status.SupportsInput,
		StartedAt:     status.StartedAt,
		UpdatedAt:     status.UpdatedAt,
	}
	if !status.Running {
		exitCode := status.ExitCode
		snap.ExitCode = &exitCode
	}
	if len(stdout) > 0 {
		snap.Frames = append(snap.Frames, sdkterminal.Frame{
			Ref:       snap.Ref,
			Stream:    "stdout",
			Text:      string(stdout),
			Cursor:    snap.Cursor,
			Running:   status.Running,
			UpdatedAt: status.UpdatedAt,
		})
	}
	if len(stderr) > 0 {
		snap.Frames = append(snap.Frames, sdkterminal.Frame{
			Ref:       snap.Ref,
			Stream:    "stderr",
			Text:      string(stderr),
			Cursor:    snap.Cursor,
			Running:   status.Running,
			UpdatedAt: status.UpdatedAt,
		})
	}
	return sdkterminal.CloneSnapshot(snap), nil
}

func (s *terminalService) Subscribe(ctx context.Context, req sdkterminal.SubscribeRequest) iter.Seq2[*sdkterminal.Frame, error] {
	return func(yield func(*sdkterminal.Frame, error) bool) {
		ref := sdkterminal.NormalizeRef(req.Ref)
		cursor := sdkterminal.CloneCursor(req.Cursor)
		poll := req.PollInterval
		if poll <= 0 {
			poll = 100 * time.Millisecond
		}
		closedSent := false
		for {
			snap, err := s.Read(ctx, sdkterminal.ReadRequest{Ref: ref, Cursor: cursor})
			if err != nil {
				yield(nil, err)
				return
			}
			cursor = snap.Cursor
			for _, frame := range snap.Frames {
				cloned := sdkterminal.CloneFrame(frame)
				if !yield(&cloned, nil) {
					return
				}
			}
			if !snap.Running {
				if !closedSent {
					frame := sdkterminal.Frame{
						Ref:       snap.Ref,
						Cursor:    snap.Cursor,
						Running:   false,
						Closed:    true,
						UpdatedAt: snap.UpdatedAt,
					}
					if snap.ExitCode != nil {
						code := *snap.ExitCode
						frame.ExitCode = &code
					}
					if !yield(&frame, nil) {
						return
					}
				}
				return
			}
			timer := time.NewTimer(poll)
			select {
			case <-ctx.Done():
				timer.Stop()
				yield(nil, ctx.Err())
				return
			case <-timer.C:
			}
		}
	}
}

func (s *terminalService) Wait(ctx context.Context, ref sdkterminal.Ref) (sdkterminal.Snapshot, error) {
	ref = sdkterminal.NormalizeRef(ref)
	poll := 100 * time.Millisecond
	for {
		snap, err := s.Read(ctx, sdkterminal.ReadRequest{Ref: ref})
		if err != nil {
			return sdkterminal.Snapshot{}, err
		}
		if !snap.Running {
			return snap, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return sdkterminal.Snapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *terminalService) Kill(ctx context.Context, ref sdkterminal.Ref) error {
	task, err := s.resolveTask(ctx, sdkterminal.NormalizeRef(ref))
	if err != nil {
		return err
	}
	return task.session.Terminate(ctx)
}

func (s *terminalService) Release(ctx context.Context, ref sdkterminal.Ref) error {
	task, err := s.resolveTask(ctx, sdkterminal.NormalizeRef(ref))
	if err != nil {
		return err
	}
	status, err := task.session.Status(ctx)
	if err != nil {
		return err
	}
	if status.Running {
		return task.session.Terminate(ctx)
	}
	return nil
}

func (s *terminalService) resolveTask(ctx context.Context, ref sdkterminal.Ref) (*bashTask, error) {
	if s == nil || s.tasks == nil {
		return nil, fmt.Errorf("sdk/runtime/local: terminal service is unavailable")
	}
	if ref.SessionID == "" {
		return nil, fmt.Errorf("sdk/runtime/local: session_id is required")
	}
	sessionRef := sdksession.SessionRef{SessionID: ref.SessionID}
	if ref.TaskID != "" {
		return s.tasks.lookupBash(ctx, sessionRef, ref.TaskID)
	}
	if ref.TerminalID == "" {
		return nil, fmt.Errorf("sdk/runtime/local: task_id or terminal_id is required")
	}
	s.tasks.mu.RLock()
	for _, task := range s.tasks.tasks {
		if task == nil {
			continue
		}
		if strings.TrimSpace(task.sessionRef.SessionID) != ref.SessionID {
			continue
		}
		if strings.TrimSpace(task.ref.TerminalID) == ref.TerminalID {
			s.tasks.mu.RUnlock()
			return task, nil
		}
	}
	s.tasks.mu.RUnlock()
	if s.tasks.store == nil {
		return nil, fmt.Errorf("sdk/runtime/local: terminal %q not found", ref.TerminalID)
	}
	entries, err := s.tasks.store.ListSession(ctx, sessionRef)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Terminal.TerminalID) != ref.TerminalID {
			continue
		}
		hydrated, err := s.tasks.store.Get(ctx, strings.TrimSpace(entry.TaskID))
		if err != nil {
			return nil, err
		}
		return s.tasks.rehydrateBashTask(hydrated)
	}
	return nil, fmt.Errorf("sdk/runtime/local: terminal %q not found", ref.TerminalID)
}
