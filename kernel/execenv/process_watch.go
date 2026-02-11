package execenv

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
)

var errIdleTimeout = errors.New("process idle timeout exceeded")

type activityWriter struct {
	buffer     *bytes.Buffer
	lastOutput *atomic.Int64
}

func (w *activityWriter) Write(p []byte) (int, error) {
	if w.lastOutput != nil {
		w.lastOutput.Store(time.Now().UnixNano())
	}
	if w.buffer == nil {
		return len(p), nil
	}
	return w.buffer.Write(p)
}

func waitWithIdleTimeout(ctx context.Context, cmd *exec.Cmd, idleTimeout time.Duration, lastOutput *atomic.Int64) error {
	if cmd == nil {
		return errors.New("nil command")
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	if idleTimeout <= 0 {
		select {
		case err := <-waitCh:
			return err
		case <-ctx.Done():
			_ = killProcess(cmd)
			<-waitCh
			return ctx.Err()
		}
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-waitCh:
			return err
		case <-ctx.Done():
			_ = killProcess(cmd)
			<-waitCh
			return ctx.Err()
		case <-ticker.C:
			if lastOutput == nil {
				continue
			}
			last := time.Unix(0, lastOutput.Load())
			if time.Since(last) > idleTimeout {
				_ = killProcess(cmd)
				<-waitCh
				return errIdleTimeout
			}
		}
	}
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Kill the whole process group so child processes (for example spawned by
	// "go run" / shells) do not keep stdout/stderr pipes open.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
