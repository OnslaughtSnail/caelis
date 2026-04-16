package terminal

import (
	"context"
	"fmt"
	"strings"

	schema "github.com/OnslaughtSnail/caelis/acp/schema"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdkterminal "github.com/OnslaughtSnail/caelis/sdk/terminal"
)

type TerminalOutputRequest = schema.TerminalOutputRequest
type TerminalExitStatus = schema.TerminalExitStatus
type TerminalOutputResponse = schema.TerminalOutputResponse
type TerminalWaitForExitRequest = schema.TerminalWaitForExitRequest
type TerminalWaitForExitResponse = schema.TerminalWaitForExitResponse
type TerminalKillRequest = schema.TerminalKillRequest
type TerminalReleaseRequest = schema.TerminalReleaseRequest
type ToolCallContent = schema.ToolCallContent

// TerminalAdapter projects one sdk/terminal service into ACP-compatible
// terminal method payloads.
type TerminalAdapter interface {
	Output(context.Context, TerminalOutputRequest) (TerminalOutputResponse, error)
	WaitForExit(context.Context, TerminalWaitForExitRequest) (TerminalWaitForExitResponse, error)
	Kill(context.Context, TerminalKillRequest) error
	Release(context.Context, TerminalReleaseRequest) error
}

type LocalTerminalAdapter struct {
	Terminals sdkterminal.Service
}

func (a LocalTerminalAdapter) Output(ctx context.Context, req TerminalOutputRequest) (TerminalOutputResponse, error) {
	if a.Terminals == nil {
		return TerminalOutputResponse{}, fmt.Errorf("acpbridge/terminal: terminal service is required")
	}
	snap, err := a.Terminals.Read(ctx, sdkterminal.ReadRequest{
		Ref: sdkterminal.Ref{
			SessionID:  strings.TrimSpace(req.SessionID),
			TerminalID: strings.TrimSpace(req.TerminalID),
		},
	})
	if err != nil {
		return TerminalOutputResponse{}, err
	}
	resp := TerminalOutputResponse{
		Output:    terminalSnapshotOutput(snap),
		Truncated: false,
	}
	if snap.ExitCode != nil {
		code := *snap.ExitCode
		resp.ExitStatus = &TerminalExitStatus{ExitCode: &code}
	}
	return resp, nil
}

func (a LocalTerminalAdapter) WaitForExit(ctx context.Context, req TerminalWaitForExitRequest) (TerminalWaitForExitResponse, error) {
	controller, ok := a.Terminals.(sdkterminal.Controller)
	if !ok || controller == nil {
		return TerminalWaitForExitResponse{}, fmt.Errorf("acpbridge/terminal: terminal wait is unsupported")
	}
	snap, err := controller.Wait(ctx, sdkterminal.Ref{
		SessionID:  strings.TrimSpace(req.SessionID),
		TerminalID: strings.TrimSpace(req.TerminalID),
	})
	if err != nil {
		return TerminalWaitForExitResponse{}, err
	}
	resp := TerminalWaitForExitResponse{}
	if snap.ExitCode != nil {
		code := *snap.ExitCode
		resp.ExitCode = &code
	}
	return resp, nil
}

func (a LocalTerminalAdapter) Kill(ctx context.Context, req TerminalKillRequest) error {
	controller, ok := a.Terminals.(sdkterminal.Controller)
	if !ok || controller == nil {
		return fmt.Errorf("acpbridge/terminal: terminal kill is unsupported")
	}
	return controller.Kill(ctx, sdkterminal.Ref{
		SessionID:  strings.TrimSpace(req.SessionID),
		TerminalID: strings.TrimSpace(req.TerminalID),
	})
}

func (a LocalTerminalAdapter) Release(ctx context.Context, req TerminalReleaseRequest) error {
	controller, ok := a.Terminals.(sdkterminal.Controller)
	if !ok || controller == nil {
		return fmt.Errorf("acpbridge/terminal: terminal release is unsupported")
	}
	return controller.Release(ctx, sdkterminal.Ref{
		SessionID:  strings.TrimSpace(req.SessionID),
		TerminalID: strings.TrimSpace(req.TerminalID),
	})
}

func terminalSnapshotOutput(snap sdkterminal.Snapshot) string {
	var out strings.Builder
	for _, frame := range snap.Frames {
		out.WriteString(frame.Text)
	}
	return out.String()
}

func RefFromEvent(event *sdksession.Event) (sdkterminal.Ref, bool) {
	if event == nil {
		return sdkterminal.Ref{}, false
	}
	ref := sdkterminal.Ref{
		SessionID: strings.TrimSpace(event.SessionID),
	}
	if event.Meta != nil {
		if taskID, _ := event.Meta["task_id"].(string); strings.TrimSpace(taskID) != "" {
			ref.TaskID = strings.TrimSpace(taskID)
		}
		if terminalID, _ := event.Meta["terminal_id"].(string); strings.TrimSpace(terminalID) != "" {
			ref.TerminalID = strings.TrimSpace(terminalID)
		}
	}
	if ref.TerminalID == "" && event.Protocol != nil && event.Protocol.ToolCall != nil && event.Protocol.ToolCall.RawOutput != nil {
		if terminalID, _ := event.Protocol.ToolCall.RawOutput["terminal_id"].(string); strings.TrimSpace(terminalID) != "" {
			ref.TerminalID = strings.TrimSpace(terminalID)
		}
	}
	if ref.TerminalID == "" {
		return sdkterminal.Ref{}, false
	}
	return ref, true
}

func ContentFromEvent(event *sdksession.Event) []ToolCallContent {
	ref, ok := RefFromEvent(event)
	if !ok {
		return nil
	}
	return []ToolCallContent{{
		Type:       "terminal",
		TerminalID: ref.TerminalID,
	}}
}

var _ TerminalAdapter = LocalTerminalAdapter{}
