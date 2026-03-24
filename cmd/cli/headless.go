package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
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
	legacyInputFlag string,
	stdin io.Reader,
	stdinTTY bool,
	stdoutTTY bool,
) (string, bool, error) {
	prompt := strings.TrimSpace(promptFlag)
	legacy := strings.TrimSpace(legacyInputFlag)
	if prompt != "" && legacy != "" && prompt != legacy {
		return "", false, fmt.Errorf("flags -p and -input conflict, provide only one")
	}
	if prompt == "" {
		prompt = legacy
	}
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

func runHeadlessOnce(ctx context.Context, svc *sessionsvc.Service, req sessionsvc.RunTurnRequest) (headlessRunResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		lastAssistant string
		answerPartial strings.Builder
		promptTokens  int
	)
	runResult, err := svc.RunTurn(ctx, req)
	if err != nil {
		return headlessRunResult{}, err
	}
	runner := runResult.Handle
	defer runner.Close() // Close always returns nil; safe to ignore.
	for ev, err := range runner.Events() {
		if err != nil {
			if toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalRequired) ||
				toolexec.IsErrorCode(err, toolexec.ErrorCodeApprovalAborted) {
				return headlessRunResult{}, fmt.Errorf("headless mode cannot handle interactive approval prompts: %w", err)
			}
			return headlessRunResult{}, err
		}
		if ev == nil {
			continue
		}
		if ev.Meta != nil {
			if usageRaw, ok := ev.Meta["usage"]; ok {
				if usageMap, ok := usageRaw.(map[string]any); ok {
					prompt := toInt(usageMap["prompt_tokens"])
					if prompt > 0 {
						promptTokens = prompt
					}
				}
			}
		}
		msg := ev.Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		if eventIsPartial(ev) {
			if eventChannel(ev) == "answer" {
				answerPartial.WriteString(msg.TextContent())
			}
			continue
		}
		text := strings.TrimSpace(msg.TextContent())
		if text == "" {
			continue
		}
		lastAssistant = text
		answerPartial.Reset()
	}
	if strings.TrimSpace(lastAssistant) == "" {
		lastAssistant = strings.TrimSpace(answerPartial.String())
	}
	return headlessRunResult{
		SessionID:    strings.TrimSpace(runResult.Session.SessionID),
		Output:       strings.TrimSpace(lastAssistant),
		PromptTokens: promptTokens,
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
