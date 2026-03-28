package main

import (
	"context"
	"strings"
)

func (c *cliConsole) ensureSessionPrompt(_ context.Context) (string, error) {
	if c == nil {
		return "", nil
	}
	sessionID := strings.TrimSpace(c.sessionID)
	if sessionID == "" {
		return "", nil
	}
	c.promptMu.Lock()
	if frozen := strings.TrimSpace(c.promptSnapshots[sessionID]); frozen != "" {
		c.promptMu.Unlock()
		return frozen, nil
	}
	c.promptMu.Unlock()
	promptText, err := resolveSystemPrompt(buildAgentInput{
		AppName:                     c.appName,
		PromptRole:                  promptRoleMainSession,
		WorkspaceDir:                c.workspace.CWD,
		EnableExperimentalLSPPrompt: c.enableExperimentalLSP,
		BasePrompt:                  c.systemPrompt,
		SkillDirs:                   c.skillDirs,
		DefaultAgent:                c.configStore.DefaultAgent(),
		AgentDescriptors:            c.configStore.AgentDescriptors(),
	})
	if err != nil {
		return "", err
	}
	promptText = strings.TrimSpace(promptText)
	c.promptMu.Lock()
	if c.promptSnapshots == nil {
		c.promptSnapshots = map[string]string{}
	}
	if frozen := strings.TrimSpace(c.promptSnapshots[sessionID]); frozen != "" {
		c.promptMu.Unlock()
		return frozen, nil
	}
	c.promptSnapshots[sessionID] = promptText
	c.promptMu.Unlock()
	return promptText, nil
}
