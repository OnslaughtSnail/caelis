package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	headlessadapter "github.com/OnslaughtSnail/caelis/gateway/adapter/headless"
	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type headlessOutputFormat string

const (
	headlessFormatText headlessOutputFormat = "text"
	headlessFormatJSON headlessOutputFormat = "json"
)

type headlessRunResult struct {
	SessionID    string `json:"session_id"`
	Output       string `json:"output"`
	PromptTokens int    `json:"prompt_tokens,omitempty"`
}

type headlessGateway interface {
	StartSession(context.Context, appgateway.StartSessionRequest) (sdksession.Session, error)
	BeginTurn(context.Context, appgateway.BeginTurnRequest) (appgateway.BeginTurnResult, error)
}

type headlessGatewayRunRequest struct {
	AppName      string
	UserID       string
	SessionID    string
	Workspace    sdksession.WorkspaceRef
	Input        string
	ContentParts []sdkmodel.ContentPart
}

func parseHeadlessOutputFormat(raw string) (headlessOutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(headlessFormatText):
		return headlessFormatText, nil
	case string(headlessFormatJSON):
		return headlessFormatJSON, nil
	default:
		return "", fmt.Errorf("invalid format %q, expected text|json", strings.TrimSpace(raw))
	}
}

func resolveSingleShotInput(
	promptFlag string,
	stdin io.Reader,
	stdinTTY bool,
	stdoutTTY bool,
) (string, bool, error) {
	prompt := strings.TrimSpace(promptFlag)
	if prompt != "" {
		return prompt, true, nil
	}
	if !stdinTTY {
		if stdin == nil {
			return "", false, fmt.Errorf("stdin is unavailable in non-interactive mode")
		}
		buf, err := io.ReadAll(stdin)
		if err != nil {
			return "", false, fmt.Errorf("read stdin prompt failed: %w", err)
		}
		prompt = strings.TrimSpace(string(buf))
		if prompt == "" {
			return "", false, fmt.Errorf("stdin prompt is empty")
		}
		return prompt, true, nil
	}
	if !stdoutTTY {
		return "", false, fmt.Errorf("non-interactive mode requires -p or piped stdin")
	}
	return "", false, nil
}

func runHeadlessOnce(ctx context.Context, gw headlessGateway, req headlessGatewayRunRequest) (headlessRunResult, error) {
	if ctx == nil {
		return headlessRunResult{}, fmt.Errorf("cli: context is required")
	}
	if gw == nil {
		return headlessRunResult{}, fmt.Errorf("cli: headless gateway is required")
	}
	session, err := gw.StartSession(ctx, appgateway.StartSessionRequest{
		AppName:            req.AppName,
		UserID:             req.UserID,
		Workspace:          req.Workspace,
		PreferredSessionID: req.SessionID,
	})
	if err != nil {
		return headlessRunResult{}, err
	}
	result, err := headlessadapter.RunOnce(ctx, gw, appgateway.BeginTurnRequest{
		SessionRef:   session.SessionRef,
		Input:        req.Input,
		ContentParts: append([]sdkmodel.ContentPart(nil), req.ContentParts...),
		Surface:      "headless",
	}, headlessadapter.Options{})
	if err != nil {
		return headlessRunResult{}, err
	}
	return headlessRunResult{
		SessionID:    strings.TrimSpace(result.Session.SessionID),
		Output:       strings.TrimSpace(result.Output),
		PromptTokens: result.PromptTokens,
	}, nil
}

func writeHeadlessResult(w io.Writer, format headlessOutputFormat, result headlessRunResult) error {
	if w == nil {
		return fmt.Errorf("headless output writer is nil")
	}
	switch format {
	case headlessFormatJSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(result)
	default:
		if strings.TrimSpace(result.Output) == "" {
			return nil
		}
		_, err := fmt.Fprintln(w, result.Output)
		return err
	}
}
