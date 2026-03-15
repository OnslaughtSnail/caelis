package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultExperimentalLSPRoutingPrompt = `Use LSP_DEFINITION and LSP_REFERENCES to navigate by symbol name when the workspace language server is enabled.
Use LSP_SYMBOLS to discover exact symbol names before semantic lookups.
Use SEARCH and GLOB for text-level fallback when semantic lookup is unnecessary or unavailable.`
)

func builtInIdentityPrompt(appName string) string {
	name := strings.TrimSpace(appName)
	if name == "" {
		name = "caelis"
	}
	return "# Agent Identity\n\nYou are " + name + "."
}

func builtInWorkspaceContextPrompt(workspaceDir string) string {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return ""
	}
	shell := currentShellName()
	currentDate := time.Now().Format("2006-01-02")
	timezone := currentTimezoneLabel()
	return fmt.Sprintf(`<environment_context>
  <cwd>%s</cwd>
  <shell>%s</shell>
  <current_date>%s</current_date>
  <timezone>%s</timezone>
</environment_context>`, workspaceDir, shell, currentDate, timezone)
}

func currentShellName() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return "unknown"
	}
	base := filepath.Base(shell)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return shell
	}
	return base
}

func currentTimezoneLabel() string {
	now := time.Now()
	name, offsetSeconds := now.Zone()
	name = strings.TrimSpace(name)
	if name == "" {
		name = now.Location().String()
	}
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s %s%02d:%02d", name, sign, hours, minutes)
}
