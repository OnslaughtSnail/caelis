package taskruntime

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

type stubSubagentRunner struct{}

func (stubSubagentRunner) RunSubagent(context.Context, agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	return agent.SubagentRunResult{}, nil
}

func (stubSubagentRunner) InspectSubagent(context.Context, string) (agent.SubagentRunResult, error) {
	return agent.SubagentRunResult{}, nil
}

func nilContext() context.Context {
	return nil
}

func TestSubagentTaskControllerWriteRejectsNilContextBeforeInspection(t *testing.T) {
	controller := &SubagentTaskController{
		Runner: stubSubagentRunner{},
	}
	_, err := controller.Write(nilContext(), &task.Record{}, "continue", 0)
	if err == nil || err.Error() != "task: context is required" {
		t.Fatalf("expected context required error, got %v", err)
	}
}
