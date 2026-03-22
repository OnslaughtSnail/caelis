package main

import (
	"fmt"
	"sort"
	"strings"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
)

func handleAgent(c *cliConsole, args []string) (bool, error) {
	if c == nil || c.configStore == nil {
		return false, fmt.Errorf("agent config is unavailable")
	}
	if len(args) == 0 {
		return false, fmt.Errorf("usage: /agent list | /agent add <preset> | /agent rm <name>")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		return false, showAgentServers(c)
	case "add":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /agent add <preset>")
		}
		presetID := strings.TrimSpace(args[1])
		preset, ok := appagents.LookupRegistryPreset(presetID)
		if !ok {
			return false, fmt.Errorf("unknown agent preset %q", presetID)
		}
		if err := c.configStore.UpsertAgentServer(preset.ID, agentRecord{
			ID:          preset.ID,
			Name:        preset.Name,
			Description: preset.Description,
			Type:        appagents.TypeRegistry,
		}); err != nil {
			return false, err
		}
		reg, err := c.configStore.AgentRegistry()
		if err != nil {
			return false, err
		}
		c.agentRegistry = reg
		c.printf("agent added: %s\n", preset.ID)
		return false, nil
	case "rm", "del", "remove":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /agent rm <name>")
		}
		name := strings.TrimSpace(args[1])
		if err := c.configStore.DeleteAgentServer(name); err != nil {
			return false, err
		}
		reg, err := c.configStore.AgentRegistry()
		if err != nil {
			return false, err
		}
		c.agentRegistry = reg
		c.printf("agent removed: %s\n", name)
		return false, nil
	default:
		return false, fmt.Errorf("usage: /agent list | /agent add <preset> | /agent rm <name>")
	}
}

func showAgentServers(c *cliConsole) error {
	if c == nil {
		return nil
	}
	presets := appagents.KnownRegistryPresets()
	c.ui.Section("Available ACP Agent Presets")
	for _, preset := range presets {
		c.ui.Plain("  %-20s %s\n", preset.ID, preset.Description)
	}
	c.ui.Section("Configured Agent Servers")
	if len(c.configStore.data.AgentServers) == 0 {
		c.ui.Plain("  (none)\n")
		return nil
	}
	keys := make([]string, 0, len(c.configStore.data.AgentServers))
	for key := range c.configStore.data.AgentServers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rec := c.configStore.data.AgentServers[key]
		kind := strings.TrimSpace(rec.Type)
		if kind == "" {
			kind = "custom"
		}
		c.ui.Plain("  %-20s type=%s\n", key, kind)
	}
	return nil
}
