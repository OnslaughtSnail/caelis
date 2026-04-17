package main

import (
	"fmt"
	"strings"
)

type interactiveUIMode string

const (
	uiModeAuto interactiveUIMode = "auto"
	uiModeTUI  interactiveUIMode = "tui"
)

func resolveInteractiveUIMode(requested string, stdinTTY bool, stdoutTTY bool) (interactiveUIMode, error) {
	mode := interactiveUIMode(strings.ToLower(strings.TrimSpace(requested)))
	switch mode {
	case "", uiModeAuto:
		if stdinTTY && stdoutTTY {
			return uiModeTUI, nil
		}
		return "", fmt.Errorf("interactive mode requires a TTY terminal; use -p or piped stdin for headless mode")
	case uiModeTUI:
		if !stdinTTY || !stdoutTTY {
			return "", fmt.Errorf("ui mode %q requires an interactive terminal", uiModeTUI)
		}
		return uiModeTUI, nil
	default:
		return "", fmt.Errorf("invalid ui mode %q, expected auto|tui", strings.TrimSpace(requested))
	}
}
