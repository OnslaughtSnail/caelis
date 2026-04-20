package runnerruntime

import (
	"context"
	"testing"
	"time"

	sdksandbox "github.com/OnslaughtSnail/caelis/sdk/sandbox"
	"github.com/OnslaughtSnail/caelis/sdk/sandbox/internal/cmdsession"
)

type waitResultTestRunner struct {
	session *cmdsession.AsyncSession
}

func (r *waitResultTestRunner) Run(context.Context, Request) (sdksandbox.CommandResult, error) {
	return sdksandbox.CommandResult{}, nil
}

func (r *waitResultTestRunner) StartAsync(context.Context, Request) (string, error) {
	return r.session.ID, nil
}

func (r *waitResultTestRunner) WriteInput(string, []byte) error { return nil }

func (r *waitResultTestRunner) ReadOutput(string, int64, int64) ([]byte, []byte, int64, int64, error) {
	return nil, nil, 0, 0, nil
}

func (r *waitResultTestRunner) GetSessionStatus(string) (cmdsession.SessionStatus, error) {
	return r.session.Status(), nil
}

func (r *waitResultTestRunner) WaitSession(ctx context.Context, _ string, timeout time.Duration) (sdksandbox.CommandResult, error) {
	if timeout > 0 {
		if _, err := r.session.WaitWithTimeout(timeout); err != nil {
			return sdksandbox.CommandResult{}, err
		}
	} else if _, err := r.session.Wait(ctx); err != nil {
		return sdksandbox.CommandResult{}, err
	}
	return r.session.GetResult()
}

func (r *waitResultTestRunner) TerminateSession(string) error {
	return r.session.Terminate()
}

func (r *waitResultTestRunner) Close() error { return nil }

func (r *waitResultTestRunner) GetSession(_ string) (*cmdsession.AsyncSession, error) {
	return r.session, nil
}

func TestSessionWaitDoesNotConsumeExitForResult(t *testing.T) {
	t.Parallel()

	async := cmdsession.NewAsyncSession(cmdsession.AsyncSessionConfig{
		Command: "printf 'ok\\n'",
	})
	if err := async.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = async.Close() })

	sess := &session{
		backend:   sdksandbox.BackendHost,
		runner:    &waitResultTestRunner{session: async},
		sessionID: async.ID,
	}

	status, err := sess.Wait(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if status.Running {
		t.Fatalf("Wait() status = %+v, want exited session", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, err := sess.Result(ctx)
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if result.Stdout != "ok\n" {
		t.Fatalf("Result().Stdout = %q, want %q", result.Stdout, "ok\n")
	}
}
