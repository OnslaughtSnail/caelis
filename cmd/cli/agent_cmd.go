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
		return false, fmt.Errorf("usage: /agent list | /agent add <builtin> | /agent rm <name>")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		return false, showAgents(c)
	case "add":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /agent add <builtin>")
		}
		builtinID := strings.TrimSpace(args[1])
		preset, ok := appagents.LookupBuiltin(builtinID)
		if !ok {
			return false, fmt.Errorf("unknown builtin agent %q", builtinID)
		}
		if err := c.configStore.UpsertAgent(preset.ID, agentRecord{
			Description: preset.Description,
			Command:     preset.Command,
			Args:        append([]string(nil), preset.Args...),
			Env:         copyStringMap(preset.Env),
			WorkDir:     preset.WorkDir,
			Stability:   preset.Stability,
		}); err != nil {
			return false, err
		}
		reg, err := c.configStore.AgentRegistry()
		if err != nil {
			return false, err
		}
		c.agentRegistry = reg
		c.notifyCommandListChanged()
		c.printf("agent added: %s\n", preset.ID)
		return false, nil
	case "rm", "del", "remove":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /agent rm <name>")
		}
		name := strings.TrimSpace(args[1])
		if err := c.configStore.DeleteAgent(name); err != nil {
			return false, err
		}
		reg, err := c.configStore.AgentRegistry()
		if err != nil {
			return false, err
		}
		c.agentRegistry = reg
		c.notifyCommandListChanged()
		c.printf("agent removed: %s\n", name)
		return false, nil
	default:
		return false, fmt.Errorf("usage: /agent list | /agent add <builtin> | /agent rm <name>")
	}
}

func showAgents(c *cliConsole) error {
	if c == nil {
		return nil
	}
	builtins := appagents.KnownBuiltins()
	c.ui.Section("Config File")
	if c.configStore != nil {
		c.ui.Plain("  %s\n", c.configStore.path)
	}
	c.ui.Section("Available Builtin ACP Agents")
	for _, preset := range builtins {
		c.ui.Plain("  %-14s %-12s %s\n", preset.ID, preset.Stability, preset.Description)
	}
	c.ui.Section("Configured Agents")
	if len(c.configStore.data.Agents) == 0 {
		c.ui.Plain("  (none)\n")
	} else {
		keys := make([]string, 0, len(c.configStore.data.Agents))
		for key := range c.configStore.data.Agents {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rec := c.configStore.data.Agents[key]
			stability := strings.TrimSpace(rec.Stability)
			if stability == "" {
				stability = appagents.StabilityExperimental
			}
			c.ui.Plain("  %-14s %-12s %s\n", key, stability, strings.TrimSpace(rec.Command))
		}
	}
	c.ui.Section("Custom agents example")
	c.ui.Plain("  \"agents\": {\n")
	c.ui.Plain("    \"my-agent\": {\n")
	c.ui.Plain("      \"command\": \"./bin/my-acp-server\",\n")
	c.ui.Plain("      \"args\": [\"--stdio\"],\n")
	c.ui.Plain("      \"env\": {},\n")
	c.ui.Plain("      \"workDir\": \"/abs/path\",\n")
	c.ui.Plain("      \"stability\": \"experimental\",\n")
	c.ui.Plain("      \"description\": \"Local custom ACP server\"\n")
	c.ui.Plain("    }\n")
	c.ui.Plain("  }\n")
	return nil
}
