package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

type mainACPBootstrapState string

const (
	mainACPBootstrapNone        mainACPBootstrapState = ""
	mainACPBootstrapFresh       mainACPBootstrapState = "fresh"
	mainACPBootstrapReconnected mainACPBootstrapState = "reconnected"
)

type acpMainAvailableCommand struct {
	Name        string
	Description string
	Hint        string
}

type acpMainModelProfile struct {
	ID          string
	Name        string
	Description string
	Reasoning   []tuiapp.SlashArgCandidate
}

func cloneACPModeState(state *acpclient.SessionModeState) *acpclient.SessionModeState {
	if state == nil {
		return nil
	}
	out := &acpclient.SessionModeState{
		CurrentModeID: strings.TrimSpace(state.CurrentModeID),
	}
	if len(state.AvailableModes) > 0 {
		out.AvailableModes = append([]acpclient.SessionMode(nil), state.AvailableModes...)
	}
	return out
}

func cloneACPConfigOptions(options []acpclient.SessionConfigOption) []acpclient.SessionConfigOption {
	if len(options) == 0 {
		return nil
	}
	out := make([]acpclient.SessionConfigOption, 0, len(options))
	for _, one := range options {
		clone := one
		clone.ID = strings.TrimSpace(clone.ID)
		clone.Category = strings.TrimSpace(clone.Category)
		clone.Name = strings.TrimSpace(clone.Name)
		clone.CurrentValue = strings.TrimSpace(clone.CurrentValue)
		if len(clone.Options) > 0 {
			clone.Options = append([]acpclient.SessionConfigSelectOption(nil), clone.Options...)
		}
		out = append(out, clone)
	}
	return out
}

func decodeACPConfigOptions(raw any) []acpclient.SessionConfigOption {
	switch typed := raw.(type) {
	case nil:
		return nil
	case []acpclient.SessionConfigOption:
		return cloneACPConfigOptions(typed)
	case acpclient.ConfigOptionUpdate:
		return decodeACPConfigOptions(typed.ConfigOptions)
	default:
		data, err := json.Marshal(raw)
		if err != nil || len(data) == 0 {
			return nil
		}
		var out []acpclient.SessionConfigOption
		if err := json.Unmarshal(data, &out); err != nil {
			return nil
		}
		return cloneACPConfigOptions(out)
	}
}

func cloneACPAvailableCommands(commands []acpMainAvailableCommand) []acpMainAvailableCommand {
	if len(commands) == 0 {
		return nil
	}
	out := make([]acpMainAvailableCommand, 0, len(commands))
	for _, one := range commands {
		name := strings.ToLower(strings.TrimSpace(one.Name))
		if name == "" {
			continue
		}
		out = append(out, acpMainAvailableCommand{
			Name:        name,
			Description: strings.TrimSpace(one.Description),
			Hint:        strings.TrimSpace(one.Hint),
		})
	}
	return out
}

func cloneACPMainModelProfiles(profiles []acpMainModelProfile) []acpMainModelProfile {
	if len(profiles) == 0 {
		return nil
	}
	out := make([]acpMainModelProfile, 0, len(profiles))
	for _, one := range profiles {
		id := strings.TrimSpace(one.ID)
		if id == "" {
			continue
		}
		clone := acpMainModelProfile{
			ID:          id,
			Name:        strings.TrimSpace(one.Name),
			Description: strings.TrimSpace(one.Description),
		}
		if len(one.Reasoning) > 0 {
			clone.Reasoning = append([]tuiapp.SlashArgCandidate(nil), one.Reasoning...)
		}
		out = append(out, clone)
	}
	return out
}

func decodeACPAvailableCommands(raw any) []acpMainAvailableCommand {
	switch typed := raw.(type) {
	case nil:
		return nil
	case []acpMainAvailableCommand:
		return cloneACPAvailableCommands(typed)
	case acpclient.AvailableCommandsUpdate:
		return decodeACPAvailableCommands(typed.AvailableCommands)
	case []map[string]any:
		out := make([]acpMainAvailableCommand, 0, len(typed))
		for _, one := range typed {
			name := strings.ToLower(strings.TrimSpace(asString(one["name"])))
			if name == "" {
				continue
			}
			hint := ""
			if input, ok := one["input"].(map[string]any); ok {
				hint = strings.TrimSpace(asString(input["hint"]))
			}
			out = append(out, acpMainAvailableCommand{
				Name:        name,
				Description: strings.TrimSpace(asString(one["description"])),
				Hint:        hint,
			})
		}
		return cloneACPAvailableCommands(out)
	default:
		data, err := json.Marshal(raw)
		if err != nil || len(data) == 0 {
			return nil
		}
		var out []map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil
		}
		return decodeACPAvailableCommands(out)
	}
}

func (s *persistentMainACPState) snapshotSessionState() (string, *acpclient.SessionModeState, []acpclient.SessionConfigOption, mainACPBootstrapState) {
	if s == nil {
		return "", nil, nil, mainACPBootstrapNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.remoteSessionID), cloneACPModeState(s.modes), cloneACPConfigOptions(s.configOptions), s.bootstrapState
}

func (s *persistentMainACPState) snapshotAvailableCommands() []acpMainAvailableCommand {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneACPAvailableCommands(s.availableCmds)
}

func (s *persistentMainACPState) snapshotModelProfiles() []acpMainModelProfile {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneACPMainModelProfiles(s.modelProfiles)
}

func (s *persistentMainACPState) storeSessionState(sessionID string, modes *acpclient.SessionModeState, options []acpclient.SessionConfigOption, bootstrap mainACPBootstrapState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remoteSessionID = strings.TrimSpace(sessionID)
	s.modes = cloneACPModeState(modes)
	s.configOptions = cloneACPConfigOptions(options)
	s.bootstrapState = bootstrap
}

func (s *persistentMainACPState) storeModelProfiles(profiles []acpMainModelProfile) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelProfiles = cloneACPMainModelProfiles(profiles)
}

func (s *persistentMainACPState) consumeBootstrapState(sessionID string) mainACPBootstrapState {
	if s == nil {
		return mainACPBootstrapNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && strings.TrimSpace(s.remoteSessionID) != strings.TrimSpace(sessionID) {
		return mainACPBootstrapNone
	}
	current := s.bootstrapState
	s.bootstrapState = mainACPBootstrapNone
	return current
}

func (s *persistentMainACPState) applyCurrentModeUpdate(sessionID string, modeID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && strings.TrimSpace(s.remoteSessionID) != strings.TrimSpace(sessionID) {
		return false
	}
	modeID = strings.TrimSpace(modeID)
	if modeID == "" {
		return false
	}
	if s.modes == nil {
		s.modes = &acpclient.SessionModeState{}
	}
	if s.modes.CurrentModeID == modeID {
		return false
	}
	s.modes.CurrentModeID = modeID
	return true
}

func (s *persistentMainACPState) applyConfigOptionsUpdate(sessionID string, options []acpclient.SessionConfigOption) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && strings.TrimSpace(s.remoteSessionID) != strings.TrimSpace(sessionID) {
		return false
	}
	s.configOptions = cloneACPConfigOptions(options)
	return true
}

func (s *persistentMainACPState) applyAvailableCommandsUpdate(sessionID string, commands []acpMainAvailableCommand) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && strings.TrimSpace(s.remoteSessionID) != strings.TrimSpace(sessionID) {
		return false
	}
	next := cloneACPAvailableCommands(commands)
	if slices.EqualFunc(s.availableCmds, next, func(a, b acpMainAvailableCommand) bool {
		return a == b
	}) {
		return false
	}
	s.availableCmds = next
	return true
}

func (c *cliConsole) currentMainACPDescriptor() (appagents.Descriptor, bool, error) {
	if c == nil || c.configStore == nil {
		return appagents.Descriptor{}, false, nil
	}
	desc, usesACP, err := resolveMainSessionAgentDescriptor(buildAgentInput{
		MainAgent:        c.configStore.MainAgent(),
		AgentDescriptors: c.configStore.AgentDescriptors(),
	})
	if err != nil {
		return appagents.Descriptor{}, false, err
	}
	return desc, usesACP, nil
}

func (c *cliConsole) ensureCurrentSessionExists(ctx context.Context) error {
	if c == nil || c.sessionStore == nil {
		return nil
	}
	sess := c.currentSessionRef()
	if sess == nil || strings.TrimSpace(sess.ID) == "" {
		return nil
	}
	_, err := c.sessionStore.GetOrCreate(cliContext(ctx), sess)
	return err
}

func (c *cliConsole) currentSessionACPRemoteSession(ctx context.Context, agentID string) (string, bool, error) {
	if c == nil {
		return "", false, nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", false, nil
	}
	if c.persistentMainACP != nil {
		sessionID, _, _, _ := c.persistentMainACP.snapshotSessionState()
		c.persistentMainACP.mu.Lock()
		persistentAgentID := strings.TrimSpace(c.persistentMainACP.agentID)
		c.persistentMainACP.mu.Unlock()
		if sessionID != "" && strings.EqualFold(persistentAgentID, agentID) {
			return sessionID, true, nil
		}
	}
	if c.sessionStore == nil || c.currentSessionRef() == nil {
		return "", false, nil
	}
	stored, err := coreacpmeta.ControllerSessionFromStore(cliContext(ctx), c.sessionStore, c.currentSessionRef())
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if !strings.EqualFold(strings.TrimSpace(stored.AgentID), agentID) {
		return "", false, nil
	}
	sessionID := strings.TrimSpace(stored.SessionID)
	if sessionID == "" {
		return "", false, nil
	}
	return sessionID, true, nil
}

func (c *cliConsole) ensureMainACPControlSession(ctx context.Context) (mainACPClient, string, error) {
	desc, usesACP, err := c.currentMainACPDescriptor()
	if err != nil {
		return nil, "", err
	}
	if !usesACP {
		return nil, "", fmt.Errorf("main agent does not use ACP")
	}
	if err := c.ensureCurrentSessionExists(ctx); err != nil {
		return nil, "", err
	}
	client, freshClient, err := c.ensurePersistentMainACPClient(cliContext(ctx), desc)
	if err != nil {
		return nil, "", err
	}
	sessionMeta, err := c.mainACPSessionMeta(cliContext(ctx))
	if err != nil {
		return nil, "", err
	}
	sessionID, _, _, err := c.ensureMainACPRemoteSession(cliContext(ctx), client, strings.TrimSpace(desc.ID), sessionMeta, freshClient, true)
	if err != nil {
		return nil, "", err
	}
	return client, sessionID, nil
}

func (c *cliConsole) applyMainAgentSelectionState(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if !c.currentMainAgentUsesACP() {
		c.closePersistentMainACP()
		c.syncTUIStatus()
		return nil
	}
	client, sessionID, err := c.ensureMainACPControlSession(ctx)
	if err != nil {
		c.closePersistentMainACP()
		c.syncTUIStatus()
		return err
	}
	if err := c.restoreACPMainAgentSettings(ctx, client, sessionID); err != nil {
		c.closePersistentMainACP()
		c.syncTUIStatus()
		return err
	}
	if err := c.probeACPMainModelProfiles(ctx, client, sessionID); err != nil {
		c.closePersistentMainACP()
		c.syncTUIStatus()
		return err
	}
	c.syncTUIStatus()
	return nil
}

func (c *cliConsole) restoreACPMainAgentSettings(ctx context.Context, client mainACPClient, sessionID string) error {
	if c == nil || c.configStore == nil || client == nil {
		return nil
	}
	desc, usesACP, err := c.currentMainACPDescriptor()
	if err != nil {
		return err
	}
	if !usesACP {
		return nil
	}
	settings, ok := c.configStore.ACPAgentSettings(desc.ID)
	if !ok {
		return nil
	}
	_, options := c.acpMainSessionState()
	modelOption := findACPConfigOption(options, acpConfigModel, "model")
	if modelOption != nil && strings.TrimSpace(settings.Model) != "" {
		if value, _, ok := resolveACPSelectOption(modelOption.Options, settings.Model); ok && !strings.EqualFold(strings.TrimSpace(modelOption.CurrentValue), value) {
			resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), value)
			if err != nil {
				return err
			}
			options = cloneACPConfigOptions(resp.ConfigOptions)
			if c.persistentMainACP != nil {
				_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, options)
			}
		}
	}
	reasoningOption := findACPConfigOption(options, acpConfigReasoningEffort, "thought_level")
	if reasoningOption != nil && strings.TrimSpace(settings.ReasoningEffort) != "" {
		if value, _, ok := resolveACPSelectOption(reasoningOption.Options, settings.ReasoningEffort); ok && !strings.EqualFold(strings.TrimSpace(reasoningOption.CurrentValue), value) {
			resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(reasoningOption.ID, acpConfigReasoningEffort), value)
			if err != nil {
				return err
			}
			if c.persistentMainACP != nil {
				_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, resp.ConfigOptions)
			}
		}
	}
	return nil
}

func (c *cliConsole) showTransientHint(hint string) {
	if c == nil || c.tuiSender == nil {
		return
	}
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return
	}
	sendTUIMsg(c.tuiSender, tuievents.SetHintMsg{
		Hint:       hint,
		ClearAfter: transientHintDuration,
	})
}

func (c *cliConsole) acpMainSessionState() (*acpclient.SessionModeState, []acpclient.SessionConfigOption) {
	if c == nil || c.persistentMainACP == nil {
		return nil, nil
	}
	_, modes, options, _ := c.persistentMainACP.snapshotSessionState()
	return modes, options
}

func (c *cliConsole) acpMainAvailableCommands() []acpMainAvailableCommand {
	if c == nil || c.persistentMainACP == nil {
		return nil
	}
	return c.persistentMainACP.snapshotAvailableCommands()
}

func (c *cliConsole) acpMainModelProfiles() []acpMainModelProfile {
	if c == nil || c.persistentMainACP == nil {
		return nil
	}
	return c.persistentMainACP.snapshotModelProfiles()
}

func findACPConfigOption(options []acpclient.SessionConfigOption, id string, category string) *acpclient.SessionConfigOption {
	id = strings.ToLower(strings.TrimSpace(id))
	category = strings.ToLower(strings.TrimSpace(category))
	for i := range options {
		one := &options[i]
		if id != "" && strings.EqualFold(strings.TrimSpace(one.ID), id) {
			return one
		}
	}
	if category == "" {
		return nil
	}
	for i := range options {
		one := &options[i]
		if strings.EqualFold(strings.TrimSpace(one.Category), category) {
			return one
		}
	}
	return nil
}

func acpReasoningCandidatesFromOption(opt *acpclient.SessionConfigOption, limit int) []tuiapp.SlashArgCandidate {
	if opt == nil {
		return nil
	}
	if limit <= 0 {
		limit = len(opt.Options)
	}
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(opt.Options)))
	for _, one := range opt.Options {
		value := strings.TrimSpace(one.Value)
		if value == "" {
			continue
		}
		display := strings.TrimSpace(one.Name)
		if display == "" {
			display = value
		}
		out = append(out, tuiapp.SlashArgCandidate{
			Value:   value,
			Display: display,
			Detail:  strings.TrimSpace(one.Description),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func resolveACPSelectOption(options []acpclient.SessionConfigSelectOption, raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	for _, one := range options {
		value := strings.TrimSpace(one.Value)
		name := strings.TrimSpace(one.Name)
		switch {
		case strings.EqualFold(value, raw):
			return value, formatACPOptionDisplayName(name, value), true
		case name != "" && strings.EqualFold(name, raw):
			return value, formatACPOptionDisplayName(name, value), true
		}
	}
	return "", "", false
}

func formatACPOptionDisplayName(name string, value string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return strings.ToLower(name)
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func (c *cliConsole) acpMainCurrentModelAlias() string {
	_, options := c.acpMainSessionState()
	if opt := findACPConfigOption(options, acpConfigModel, "model"); opt != nil {
		return strings.TrimSpace(opt.CurrentValue)
	}
	return ""
}

func (c *cliConsole) acpMainModelOption() *acpclient.SessionConfigOption {
	_, options := c.acpMainSessionState()
	return findACPConfigOption(options, acpConfigModel, "model")
}

func (c *cliConsole) resolveACPMainModelValue(raw string) string {
	modelOption := c.acpMainModelOption()
	if modelOption == nil {
		return strings.TrimSpace(raw)
	}
	value, _, ok := resolveACPSelectOption(modelOption.Options, raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return value
}

func (c *cliConsole) findACPMainModelProfile(alias string) (acpMainModelProfile, bool) {
	target := c.resolveACPMainModelValue(alias)
	if target == "" {
		return acpMainModelProfile{}, false
	}
	for _, one := range c.acpMainModelProfiles() {
		if strings.EqualFold(strings.TrimSpace(one.ID), target) {
			return one, true
		}
	}
	return acpMainModelProfile{}, false
}

func (c *cliConsole) needsACPMainModelProfileRefresh() bool {
	modelOption := c.acpMainModelOption()
	if modelOption == nil {
		return false
	}
	expected := make([]string, 0, len(modelOption.Options))
	for _, one := range modelOption.Options {
		if value := strings.TrimSpace(one.Value); value != "" {
			expected = append(expected, value)
		}
	}
	cached := c.acpMainModelProfiles()
	if len(expected) == 0 {
		return len(cached) != 0
	}
	if len(cached) != len(expected) {
		return true
	}
	for i, one := range cached {
		if !strings.EqualFold(strings.TrimSpace(one.ID), expected[i]) {
			return true
		}
	}
	return false
}

func (c *cliConsole) probeACPMainModelProfiles(ctx context.Context, client mainACPClient, sessionID string) error {
	if c == nil || c.persistentMainACP == nil || client == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	modelOption := c.acpMainModelOption()
	if modelOption == nil {
		c.persistentMainACP.storeModelProfiles(nil)
		return nil
	}
	if !c.needsACPMainModelProfileRefresh() {
		return nil
	}
	_, currentOptions := c.acpMainSessionState()
	originalModel := strings.TrimSpace(modelOption.CurrentValue)
	originalReasoning := ""
	if reasoningOption := findACPConfigOption(currentOptions, acpConfigReasoningEffort, "thought_level"); reasoningOption != nil {
		originalReasoning = strings.TrimSpace(reasoningOption.CurrentValue)
	}
	currentModel := originalModel
	restoreState := func(options []acpclient.SessionConfigOption) {
		if c.persistentMainACP != nil && len(options) > 0 {
			_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, options)
		}
	}
	defer func() {
		if originalModel == "" {
			restoreState(currentOptions)
			return
		}
		if !strings.EqualFold(currentModel, originalModel) {
			resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), originalModel)
			if err == nil {
				currentOptions = cloneACPConfigOptions(resp.ConfigOptions)
				currentModel = originalModel
			}
		}
		reasoningOption := findACPConfigOption(currentOptions, acpConfigReasoningEffort, "thought_level")
		if reasoningOption != nil && originalReasoning != "" {
			if value, _, ok := resolveACPSelectOption(reasoningOption.Options, originalReasoning); ok && !strings.EqualFold(strings.TrimSpace(reasoningOption.CurrentValue), value) {
				resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(reasoningOption.ID, acpConfigReasoningEffort), value)
				if err == nil {
					currentOptions = cloneACPConfigOptions(resp.ConfigOptions)
				}
			}
		}
		restoreState(currentOptions)
	}()

	profiles := make([]acpMainModelProfile, 0, len(modelOption.Options))
	for _, one := range modelOption.Options {
		modelID := strings.TrimSpace(one.Value)
		if modelID == "" {
			continue
		}
		probeOptions := cloneACPConfigOptions(currentOptions)
		if !strings.EqualFold(modelID, currentModel) {
			resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), modelID)
			if err != nil {
				profiles = append(profiles, acpMainModelProfile{
					ID:          modelID,
					Name:        strings.TrimSpace(one.Name),
					Description: strings.TrimSpace(one.Description),
				})
				continue
			}
			probeOptions = cloneACPConfigOptions(resp.ConfigOptions)
			currentOptions = cloneACPConfigOptions(resp.ConfigOptions)
			currentModel = modelID
		}
		reasoning := acpReasoningCandidatesFromOption(findACPConfigOption(probeOptions, acpConfigReasoningEffort, "thought_level"), 20)
		profiles = append(profiles, acpMainModelProfile{
			ID:          modelID,
			Name:        strings.TrimSpace(one.Name),
			Description: strings.TrimSpace(one.Description),
			Reasoning:   reasoning,
		})
	}
	c.persistentMainACP.storeModelProfiles(profiles)
	return nil
}

func (c *cliConsole) acpMainReasoningCandidatesForAlias(alias string, limit int) []tuiapp.SlashArgCandidate {
	if c == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil
	}
	currentAlias := strings.TrimSpace(c.acpMainCurrentModelAlias())
	targetAlias := c.resolveACPMainModelValue(alias)
	if currentAlias != "" && strings.EqualFold(currentAlias, targetAlias) {
		_, options := c.acpMainSessionState()
		if opt := findACPConfigOption(options, acpConfigReasoningEffort, "thought_level"); opt != nil {
			out := acpReasoningCandidatesFromOption(opt, limit)
			if len(out) > 0 {
				return out
			}
		}
	}
	if profile, ok := c.findACPMainModelProfile(targetAlias); ok {
		out := append([]tuiapp.SlashArgCandidate(nil), profile.Reasoning...)
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	return nil
}

func (c *cliConsole) acpMainStatusReasoningLabel() string {
	_, options := c.acpMainSessionState()
	opt := findACPConfigOption(options, acpConfigReasoningEffort, "thought_level")
	if opt == nil {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(opt.CurrentValue))
	switch value {
	case "", "auto", "none", "off":
		return ""
	case "on":
		return "reasoning on"
	}
	for _, one := range opt.Options {
		if !strings.EqualFold(strings.TrimSpace(one.Value), value) {
			continue
		}
		label := formatACPOptionDisplayName(one.Name, one.Value)
		if label == "on" {
			return "reasoning on"
		}
		if label == "none" || label == "off" || label == "auto" {
			return ""
		}
		return label
	}
	return value
}

func (c *cliConsole) acpMainModelLabel() string {
	label := strings.TrimSpace(c.acpMainCurrentModelAlias())
	if label == "" {
		label = strings.TrimSpace(c.modelAlias)
	}
	if label == "" {
		label = "no model"
	}
	return label
}

func (c *cliConsole) acpMainStatusModelAlias() string {
	label := strings.TrimSpace(c.acpMainCurrentModelAlias())
	if label == "" {
		return "(unavailable)"
	}
	return label
}

func (c *cliConsole) acpMainModeLabel() string {
	modes, _ := c.acpMainSessionState()
	if modes == nil {
		return ""
	}
	current := strings.TrimSpace(modes.CurrentModeID)
	if current == "" {
		return ""
	}
	for _, one := range modes.AvailableModes {
		if !strings.EqualFold(strings.TrimSpace(one.ID), current) {
			continue
		}
		if name := strings.TrimSpace(one.Name); name != "" {
			return strings.ToLower(name)
		}
		break
	}
	return strings.ToLower(current)
}

func normalizeACPMainModeID(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "autopilot"):
		return "autopilot"
	case strings.Contains(value, "full_access"):
		return sessionmode.FullMode
	case strings.Contains(value, "plan"):
		return sessionmode.PlanMode
	default:
		return sessionmode.DefaultMode
	}
}

func (c *cliConsole) acpMainApprovalMode() string {
	if c == nil {
		return sessionmode.DefaultMode
	}
	_, options := c.acpMainSessionState()
	if opt := findACPConfigOption(options, acpConfigMode, "mode"); opt != nil {
		switch normalizeACPMainModeID(opt.CurrentValue) {
		case "autopilot", sessionmode.FullMode:
			return sessionmode.FullMode
		case sessionmode.PlanMode:
			return sessionmode.PlanMode
		}
	}
	modes, _ := c.acpMainSessionState()
	if modes != nil {
		switch normalizeACPMainModeID(modes.CurrentModeID) {
		case "autopilot", sessionmode.FullMode:
			return sessionmode.FullMode
		case sessionmode.PlanMode:
			return sessionmode.PlanMode
		}
	}
	return sessionmode.DefaultMode
}

func (c *cliConsole) acpMainModeCycleTarget() (acpclient.SessionMode, bool) {
	modes, _ := c.acpMainSessionState()
	if modes == nil || len(modes.AvailableModes) == 0 {
		return acpclient.SessionMode{}, false
	}
	current := strings.TrimSpace(modes.CurrentModeID)
	index := -1
	for i, one := range modes.AvailableModes {
		if strings.EqualFold(strings.TrimSpace(one.ID), current) {
			index = i
			break
		}
	}
	nextIndex := 0
	if index >= 0 {
		nextIndex = (index + 1) % len(modes.AvailableModes)
	}
	return modes.AvailableModes[nextIndex], true
}

func (c *cliConsole) currentSessionModelAlias() string {
	if c != nil && c.currentMainAgentUsesACP() {
		if alias := c.acpMainCurrentModelAlias(); alias != "" {
			return alias
		}
	}
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.modelAlias)
}

func (c *cliConsole) updateMainACPStateFromUpdate(env acpclient.UpdateEnvelope) bool {
	if c == nil || c.persistentMainACP == nil || env.Update == nil {
		return false
	}
	switch update := env.Update.(type) {
	case acpclient.CurrentModeUpdate:
		return c.persistentMainACP.applyCurrentModeUpdate(env.SessionID, update.CurrentModeID)
	case acpclient.ConfigOptionUpdate:
		options := decodeACPConfigOptions(update.ConfigOptions)
		if len(options) == 0 {
			return false
		}
		return c.persistentMainACP.applyConfigOptionsUpdate(env.SessionID, options)
	case acpclient.AvailableCommandsUpdate:
		commands := decodeACPAvailableCommands(update.AvailableCommands)
		return c.persistentMainACP.applyAvailableCommandsUpdate(env.SessionID, commands)
	default:
		return false
	}
}

func (c *cliConsole) syncACPMainMode(ctx context.Context, client mainACPClient, sessionID string, target acpclient.SessionMode) error {
	if err := client.SetMode(cliContext(ctx), sessionID, target.ID); err != nil {
		return err
	}
	if c.persistentMainACP != nil {
		_ = c.persistentMainACP.applyCurrentModeUpdate(sessionID, target.ID)
	}
	_, options := c.acpMainSessionState()
	modeOption := findACPConfigOption(options, acpConfigMode, "mode")
	if modeOption == nil {
		return nil
	}
	value, _, ok := resolveACPSelectOption(modeOption.Options, target.ID)
	if !ok {
		value, _, ok = resolveACPSelectOption(modeOption.Options, target.Name)
	}
	if !ok || strings.EqualFold(strings.TrimSpace(modeOption.CurrentValue), value) {
		return nil
	}
	resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(modeOption.ID, acpConfigMode), value)
	if err != nil {
		return err
	}
	if c.persistentMainACP != nil {
		_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, resp.ConfigOptions)
	}
	return nil
}

func (c *cliConsole) persistMainACPRemoteSession(ctx context.Context, agentID string, sessionID string) error {
	if c == nil {
		return nil
	}
	if err := c.ensureCurrentSessionExists(ctx); err != nil {
		return err
	}
	return coreacpmeta.UpdateControllerSession(ctx, c.sessionStore, c.currentSessionRef(), func(_ coreacpmeta.ControllerSession) coreacpmeta.ControllerSession {
		return coreacpmeta.ControllerSession{
			AgentID:   strings.TrimSpace(agentID),
			SessionID: strings.TrimSpace(sessionID),
		}
	})
}

func (c *cliConsole) handleACPMainModelUse(ctx context.Context, args []string) (bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return false, fmt.Errorf("usage: /model use <alias> [reasoning]")
	}
	client, sessionID, err := c.ensureMainACPControlSession(ctx)
	if err != nil {
		return false, err
	}
	_, options := c.acpMainSessionState()
	modelOption := findACPConfigOption(options, acpConfigModel, "model")
	if modelOption == nil {
		return false, fmt.Errorf("ACP main agent does not expose model selection")
	}
	alias := strings.TrimSpace(args[0])
	if c.configStore != nil {
		alias = c.configStore.ResolveModelAlias(alias)
	}
	targetAlias, _, ok := resolveACPSelectOption(modelOption.Options, alias)
	if !ok {
		return false, fmt.Errorf("model %q is not supported by the ACP main agent", alias)
	}
	previousAlias := strings.TrimSpace(modelOption.CurrentValue)
	var resolvedReasoningValue string
	var resolvedReasoningDisplay string
	if len(args) == 2 {
		candidates := c.acpMainReasoningCandidatesForAlias(targetAlias, 100)
		if len(candidates) > 0 {
			for _, one := range candidates {
				value := strings.TrimSpace(one.Value)
				display := strings.TrimSpace(one.Display)
				switch {
				case strings.EqualFold(value, args[1]):
					resolvedReasoningValue = value
					resolvedReasoningDisplay = firstNonEmptyString(display, value)
				case display != "" && strings.EqualFold(display, args[1]):
					resolvedReasoningValue = value
					resolvedReasoningDisplay = display
				}
				if resolvedReasoningValue != "" {
					break
				}
			}
			if resolvedReasoningValue == "" {
				return false, fmt.Errorf("reasoning option %q is not supported by the ACP main agent", args[1])
			}
		}
	}
	resp, err := client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), targetAlias)
	if err != nil {
		return false, err
	}
	if c.persistentMainACP != nil {
		_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, resp.ConfigOptions)
	}
	if len(args) == 2 {
		_, options = c.acpMainSessionState()
		reasoningOption := findACPConfigOption(options, acpConfigReasoningEffort, "thought_level")
		if reasoningOption == nil {
			if rollbackErr := c.restoreACPMainModelAlias(ctx, client, sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), previousAlias); rollbackErr != nil {
				return false, rollbackErr
			}
			return false, fmt.Errorf("ACP main agent does not expose reasoning selection")
		}
		if resolvedReasoningValue == "" {
			value, display, ok := resolveACPSelectOption(reasoningOption.Options, args[1])
			if !ok {
				if rollbackErr := c.restoreACPMainModelAlias(ctx, client, sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), previousAlias); rollbackErr != nil {
					return false, rollbackErr
				}
				return false, fmt.Errorf("reasoning option %q is not supported by the ACP main agent", args[1])
			}
			resolvedReasoningValue = value
			resolvedReasoningDisplay = display
		}
		value, display, ok := resolveACPSelectOption(reasoningOption.Options, resolvedReasoningValue)
		if !ok {
			if rollbackErr := c.restoreACPMainModelAlias(ctx, client, sessionID, firstNonEmptyString(modelOption.ID, acpConfigModel), previousAlias); rollbackErr != nil {
				return false, rollbackErr
			}
			return false, fmt.Errorf("reasoning option %q is not supported by the ACP main agent", args[1])
		}
		resp, err = client.SetConfigOption(cliContext(ctx), sessionID, firstNonEmptyString(reasoningOption.ID, acpConfigReasoningEffort), value)
		if err != nil {
			return false, err
		}
		if c.persistentMainACP != nil {
			_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, resp.ConfigOptions)
		}
		if err := c.persistACPMainAgentSettings(); err != nil {
			return false, err
		}
		c.syncTUIStatus()
		if resolvedReasoningDisplay != "" {
			display = resolvedReasoningDisplay
		}
		c.printModelSwitchMessage("model switched to %s (reasoning=%s)\n", targetAlias, display)
		return false, nil
	}
	if err := c.persistACPMainAgentSettings(); err != nil {
		return false, err
	}
	c.syncTUIStatus()
	c.printModelSwitchMessage("model switched to %s\n", targetAlias)
	return false, nil
}

func (c *cliConsole) restoreACPMainModelAlias(ctx context.Context, client mainACPClient, sessionID string, configID string, alias string) error {
	alias = strings.TrimSpace(alias)
	if c == nil || client == nil || sessionID == "" || configID == "" || alias == "" {
		return nil
	}
	resp, err := client.SetConfigOption(cliContext(ctx), sessionID, configID, alias)
	if err != nil {
		return err
	}
	if c.persistentMainACP != nil {
		_ = c.persistentMainACP.applyConfigOptionsUpdate(sessionID, resp.ConfigOptions)
	}
	return nil
}

func (c *cliConsole) persistACPMainAgentSettings() error {
	if c == nil || c.configStore == nil {
		return nil
	}
	desc, usesACP, err := c.currentMainACPDescriptor()
	if err != nil {
		return err
	}
	if !usesACP {
		return nil
	}
	_, options := c.acpMainSessionState()
	settings := agentACPRecord{}
	if modelOption := findACPConfigOption(options, acpConfigModel, "model"); modelOption != nil {
		settings.Model = strings.TrimSpace(modelOption.CurrentValue)
	}
	if reasoningOption := findACPConfigOption(options, acpConfigReasoningEffort, "thought_level"); reasoningOption != nil {
		settings.ReasoningEffort = strings.TrimSpace(reasoningOption.CurrentValue)
	}
	return c.configStore.SetACPAgentSettings(desc.ID, settings)
}
