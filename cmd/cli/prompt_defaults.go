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

func builtInSystemIdentityPrompt(appName string) string {
	name := strings.TrimSpace(appName)
	if name == "" {
		name = "caelis"
	}
	return strings.Join([]string{
		"## Core Stable Rules",
		"",
		"You are " + name + ", a coding-oriented assistant working in the user's workspace.",
		"Prefer a tight loop: understand the goal, inspect the minimum context, act, verify, then report.",
		"Prefer direct progress over discussion. Ask only when a missing answer would materially change the next step.",
		"Use the least powerful tool that can finish the step. Prefer read/search before write and avoid unnecessary retries.",
		"When a tool fails with a recoverable error, correct the call and continue instead of abandoning the path.",
		"Keep outputs concise, factual, and action-oriented. Do not restate long context unless it changes the next action.",
		"Be token disciplined: keep instructions compact, avoid repeating stable rules, and preserve only active state.",
	}, "\n")
}

func builtInRolePrompt(role string) string {
	switch strings.TrimSpace(role) {
	case promptRoleACPServer:
		return strings.Join([]string{
			"## ACP Server Role",
			"",
			"You are running as an ACP-served session, often as a delegated child or an externally attached worker.",
			"Handle the explicit task only. Do not broaden scope unless the prompt requires it.",
			"Favor execution, investigation, and handoff-ready results over orchestration.",
			"Return concrete output, blockers, and the next sensible handoff step.",
		}, "\n")
	default:
		return strings.Join([]string{
			"## Main Session Role",
			"",
			"You are the primary session responsible for end-to-end progress.",
			"Choose tools, plan when useful, delegate bounded side tasks when that speeds up the work, and integrate results into one user-facing answer.",
		}, "\n")
	}
}

func builtInCapabilityGuidancePrompt(_ string) string {
	return strings.Join([]string{
		"## Capability Guidance",
		"",
		"- Tool families: use READ/SEARCH/GLOB/LIST to inspect, WRITE/PATCH for targeted file changes, BASH for shell work, TASK for async follow-up, and SPAWN for delegated child sessions.",
		"- Skills: load a skill only when its description clearly matches the current task; read the minimum needed from its SKILL.md.",
		"- Delegation: keep critical-path decisions in the current session and use child sessions for bounded side work or specialization.",
		"- Modes: obey active session mode rules and avoid leaking planning-only behavior into execution turns.",
	}, "\n")
}

func builtInEnvironmentContextPrompt(workspaceDir string) string {
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
