package delegation

import (
	"context"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// RunRequest describes one delegated child run.
type RunRequest struct {
	SessionID    string
	Input        string
	ContentParts []model.ContentPart
}

// RunResult captures the final delegated child run summary.
type RunResult struct {
	SessionID    string
	DelegationID string
	Assistant    string
	State        string
	Running      bool
}

// Runner starts delegated child runs from the current invocation.
type Runner interface {
	RunSubagent(context.Context, RunRequest) (RunResult, error)
}

type AsyncRunner interface {
	Runner
	StartSubagent(context.Context, RunRequest) (RunResult, error)
	StatusSubagent(context.Context, StatusRequest) (RunResult, error)
	WaitSubagent(context.Context, WaitRequest) (RunResult, error)
}

type StatusRequest struct {
	SessionID string
}

type WaitRequest struct {
	SessionID string
	Timeout   time.Duration
}
