package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
)

const agentCommandUsage = "usage: /agent list | /agent use <self|name> | /agent add <builtin> | /agent rm <name>"

func handleAgent(c *cliConsole, args []string) (bool, error) {
	if c == nil || c.configStore == nil {
		return false, fmt.Errorf("agent config is unavailable")
	}
	if len(args) == 0 {
		return false, errors.New(agentCommandUsage)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		return false, showAgents(c)
	case "use":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /agent use <self|name>")
		}
		if c.currentRunKind() != runOccupancyNone {
			return false, fmt.Errorf("main agent can only be switched while idle")
		}
		previous := currentMainAgentName(c)
		target, addedBuiltin, err := switchMainAgent(c, args[1])
		if err != nil {
			return false, err
		}
		if addedBuiltin {
			c.printf("agent added: %s\n", target)
		}
		if previous == target {
			c.printf("main agent unchanged: %s\n", target)
			return false, nil
		}
		c.printf("main agent switched: %s -> %s\n", previous, target)
		c.printf("applies on the next turn; current session history is preserved\n")
		return false, nil
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
		if err := c.reloadAgentRegistry(); err != nil {
			return false, err
		}
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
		if err := c.reloadAgentRegistry(); err != nil {
			return false, err
		}
		c.printf("agent removed: %s\n", name)
		return false, nil
	default:
		return false, errors.New(agentCommandUsage)
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
	c.ui.Section("Current Selection")
	c.ui.Plain("  mainAgent      %s\n", currentMainAgentName(c))
	if c.configStore != nil {
		defaultAgent := strings.TrimSpace(c.configStore.DefaultAgent())
		if defaultAgent == "" {
			defaultAgent = "(unset)"
		}
		c.ui.Plain("  defaultAgent   %s\n", defaultAgent)
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
		mainAgent := currentMainAgentName(c)
		defaultAgent := ""
		if c.configStore != nil {
			defaultAgent = strings.TrimSpace(c.configStore.DefaultAgent())
		}
		for _, key := range keys {
			rec := c.configStore.data.Agents[key]
			stability := strings.TrimSpace(rec.Stability)
			if stability == "" {
				stability = appagents.StabilityExperimental
			}
			tags := make([]string, 0, 2)
			if key == mainAgent {
				tags = append(tags, "main")
			}
			if key == defaultAgent {
				tags = append(tags, "default")
			}
			if len(tags) == 0 {
				c.ui.Plain("  %-14s %-12s %s\n", key, stability, strings.TrimSpace(rec.Command))
				continue
			}
			c.ui.Plain("  %-14s %-12s %s [%s]\n", key, stability, strings.TrimSpace(rec.Command), strings.Join(tags, ","))
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

func currentMainAgentName(c *cliConsole) string {
	if c == nil || c.configStore == nil {
		return "self"
	}
	mainAgent := strings.TrimSpace(c.configStore.MainAgent())
	if mainAgent == "" {
		return "self"
	}
	return mainAgent
}

func switchMainAgent(c *cliConsole, rawTarget string) (string, bool, error) {
	if c == nil || c.configStore == nil {
		return "", false, fmt.Errorf("agent config is unavailable")
	}
	target := strings.TrimSpace(strings.ToLower(rawTarget))
	if target == "" || target == "self" {
		if err := c.configStore.SetMainAgent("self"); err != nil {
			return "", false, err
		}
		return "self", false, nil
	}
	if _, ok := c.lookupConfiguredAgent(target); ok {
		if err := c.configStore.SetMainAgent(target); err != nil {
			return "", false, err
		}
		return target, false, nil
	}
	preset, ok := appagents.LookupBuiltin(target)
	if !ok {
		return "", false, fmt.Errorf("unknown agent %q; add it under config.agents or use /agent add <builtin> first", target)
	}
	if err := c.configStore.UpsertAgent(preset.ID, agentRecord{
		Description: preset.Description,
		Command:     preset.Command,
		Args:        append([]string(nil), preset.Args...),
		Env:         copyStringMap(preset.Env),
		WorkDir:     preset.WorkDir,
		Stability:   preset.Stability,
	}); err != nil {
		return "", false, err
	}
	if err := c.reloadAgentRegistry(); err != nil {
		return "", false, err
	}
	if err := c.configStore.SetMainAgent(target); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func (c *cliConsole) reloadAgentRegistry() error {
	if c == nil || c.configStore == nil {
		return nil
	}
	reg, err := c.configStore.AgentRegistry()
	if err != nil {
		return err
	}
	c.agentRegistry = reg
	c.notifyCommandListChanged()
	return nil
}

func (c *cliConsole) lookupConfiguredAgent(name string) (appagents.Descriptor, bool) {
	if c == nil || c.configStore == nil {
		return appagents.Descriptor{}, false
	}
	reg := c.agentRegistry
	if reg == nil {
		var err error
		reg, err = c.configStore.AgentRegistry()
		if err != nil {
			return appagents.Descriptor{}, false
		}
	}
	desc, ok := reg.Lookup(strings.TrimSpace(strings.ToLower(name)))
	if !ok || desc.Transport != appagents.TransportACP {
		return appagents.Descriptor{}, false
	}
	return desc, true
}
