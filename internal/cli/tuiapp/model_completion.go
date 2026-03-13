package tuiapp

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// ---------------------------------------------------------------------------
// Command palette
// ---------------------------------------------------------------------------

func (m *Model) togglePalette() {
	m.showPalette = !m.showPalette
	m.paletteAnimating = true
	if m.showPalette {
		m.palette.ResetSelected()
		if m.paletteAnimLines < 0 {
			m.paletteAnimLines = 0
		}
	}
}

func (m *Model) handlePaletteKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.showPalette = false
		m.paletteAnimating = true
		return animatePaletteCmd()
	case key.Matches(msg, m.keys.Accept):
		item, ok := m.palette.SelectedItem().(commandItem)
		if ok {
			m.textarea.SetValue("/" + item.name)
			m.textarea.CursorEnd()
			m.adjustTextareaHeight()
			m.syncInputFromTextarea()
			m.refreshSlashCommands()
		}
		m.showPalette = false
		m.paletteAnimating = true
		return animatePaletteCmd()
	}
	var cmd tea.Cmd
	m.palette, cmd = m.palette.Update(msg)
	return cmd
}

// ---------------------------------------------------------------------------
// @Mention completion
// ---------------------------------------------------------------------------

func (m *Model) clearMention() {
	m.mentionQuery = ""
	m.mentionCandidates = nil
	m.mentionIndex = 0
	m.mentionStart = 0
	m.mentionEnd = 0
}

func (m *Model) refreshMention() {
	m.clearMention()
	if m.cfg.MentionComplete == nil || m.running {
		return
	}
	start, end, query, ok := mentionQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	begin := time.Now()
	candidates, err := m.cfg.MentionComplete(query, 8)
	latency := time.Since(begin)
	m.diag.LastMentionLatency = latency
	if err != nil || len(candidates) == 0 {
		return
	}
	m.mentionQuery = query
	m.mentionCandidates = append([]string(nil), candidates...)
	m.mentionStart = start
	m.mentionEnd = end
	m.mentionIndex = 0
}

func (m *Model) applyMentionCompletion() {
	if len(m.mentionCandidates) == 0 {
		m.refreshMention()
		if len(m.mentionCandidates) == 0 {
			return
		}
	}
	choice := "@" + m.mentionCandidates[m.mentionIndex]
	replaced, nextCursor := replaceRuneSpan(m.input, m.mentionStart, m.mentionEnd, choice)
	m.input = replaced
	m.cursor = nextCursor
	if m.cursor == len(m.input) {
		m.input = append(m.input, ' ')
		m.cursor++
	}
	m.clearMention()
}

func (m *Model) handleMentionKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.clearMention()
		return true, nil
	case key.Matches(msg, m.keys.ChoosePrev):
		if m.mentionIndex > 0 {
			m.mentionIndex--
		}
		return true, nil
	case key.Matches(msg, m.keys.ChooseNext):
		if m.mentionIndex < len(m.mentionCandidates)-1 {
			m.mentionIndex++
		}
		return true, nil
	case key.Matches(msg, m.keys.Accept), key.Matches(msg, m.keys.Complete):
		m.applyMentionCompletion()
		m.syncTextareaFromInput()
		return true, nil
	default:
		return false, nil
	}
}

// ---------------------------------------------------------------------------
// $skill completion
// ---------------------------------------------------------------------------

func (m *Model) clearSkill() {
	m.skillQuery = ""
	m.skillCandidates = nil
	m.skillIndex = 0
	m.skillStart = 0
	m.skillEnd = 0
}

func (m *Model) refreshSkill() {
	m.clearSkill()
	if m.cfg.SkillComplete == nil || m.running {
		return
	}
	// Don't show skill popup if mention popup is active.
	if len(m.mentionCandidates) > 0 {
		return
	}
	start, end, query, ok := skillQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	candidates, err := m.cfg.SkillComplete(query, 8)
	if err != nil || len(candidates) == 0 {
		return
	}
	m.skillQuery = query
	m.skillCandidates = append([]string(nil), candidates...)
	m.skillStart = start
	m.skillEnd = end
	m.skillIndex = 0
}

func (m *Model) applySkillCompletion() {
	if len(m.skillCandidates) == 0 {
		m.refreshSkill()
		if len(m.skillCandidates) == 0 {
			return
		}
	}
	choice := "$" + m.skillCandidates[m.skillIndex]
	replaced, nextCursor := replaceRuneSpan(m.input, m.skillStart, m.skillEnd, choice)
	m.input = replaced
	m.cursor = nextCursor
	if m.cursor == len(m.input) {
		m.input = append(m.input, ' ')
		m.cursor++
	}
	m.clearSkill()
}

func (m *Model) handleSkillKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.clearSkill()
		return true, nil
	case key.Matches(msg, m.keys.ChoosePrev):
		if m.skillIndex > 0 {
			m.skillIndex--
		}
		return true, nil
	case key.Matches(msg, m.keys.ChooseNext):
		if m.skillIndex < len(m.skillCandidates)-1 {
			m.skillIndex++
		}
		return true, nil
	case key.Matches(msg, m.keys.Accept), key.Matches(msg, m.keys.Complete):
		m.applySkillCompletion()
		m.syncTextareaFromInput()
		return true, nil
	default:
		return false, nil
	}
}

// renderSkillList renders the $skill candidates as an overlay list.
func (m *Model) renderSkillList() string {
	if len(m.skillCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.skillCandidates))
	var lines []string
	for i := 0; i < maxItems; i++ {
		prefix := "  "
		if i == m.skillIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render("$"+m.skillCandidates[i]))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render("$"+m.skillCandidates[i]))
		}
	}
	if len(m.skillCandidates) > maxItems {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.skillCandidates)-maxItems),
		))
	}
	return m.renderCompletionOverlay("Skills", lines)
}

// ---------------------------------------------------------------------------
// /resume completion
// ---------------------------------------------------------------------------

func (m *Model) clearResume() {
	m.resumeActive = false
	m.resumeQuery = ""
	m.resumeCandidates = nil
	m.resumeIndex = 0
}

func (m *Model) openResumePicker() {
	m.clearMention()
	m.clearSkill()
	m.clearSlashArg()
	m.clearSlashCompletion()
	m.resumeActive = true
	m.setInputText("/resume ")
	m.syncTextareaFromInput()
	m.updateResumeCandidates()
}

func (m *Model) activateResumePickerFromInput() {
	if m.resumeActive {
		m.updateResumeCandidates()
		return
	}
	m.clearMention()
	m.clearSkill()
	m.clearSlashArg()
	m.clearSlashCompletion()
	m.resumeActive = true
	m.updateResumeCandidates()
}

func (m *Model) updateResumeCandidates() {
	if !m.resumeActive || m.cfg.ResumeComplete == nil || m.running {
		m.resumeCandidates = nil
		m.resumeQuery = ""
		m.resumeIndex = 0
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.slashArgCandidates) > 0 {
		m.resumeCandidates = nil
		return
	}
	query, ok := resumeQueryAtCursor(m.input, m.cursor)
	if !ok {
		m.resumeCandidates = nil
		m.resumeQuery = ""
		m.resumeIndex = 0
		return
	}
	candidates, err := m.cfg.ResumeComplete(query, 200)
	if err != nil || len(candidates) == 0 {
		m.resumeCandidates = nil
		m.resumeQuery = query
		m.resumeIndex = 0
		return
	}
	m.resumeQuery = query
	m.resumeCandidates = append([]ResumeCandidate(nil), candidates...)
	if m.resumeIndex >= len(m.resumeCandidates) {
		m.resumeIndex = len(m.resumeCandidates) - 1
	}
	if m.resumeIndex < 0 {
		m.resumeIndex = 0
	}
}

func (m *Model) applyResumeCompletion() {
	if len(m.resumeCandidates) == 0 {
		m.updateResumeCandidates()
		if len(m.resumeCandidates) == 0 {
			return
		}
	}
	choice := strings.TrimSpace(m.resumeCandidates[m.resumeIndex].SessionID)
	if choice == "" {
		return
	}
	m.setInputText("/resume " + choice + " ")
	m.clearResume()
}

func (m *Model) handleResumeKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		if _, ok := resumeQueryAtCursor(m.input, m.cursor); ok {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearResume()
		return true, nil
	case key.Matches(msg, m.keys.ChoosePrev):
		if m.resumeIndex > 0 {
			m.resumeIndex--
		}
		return true, nil
	case key.Matches(msg, m.keys.ChooseNext):
		if m.resumeIndex < len(m.resumeCandidates)-1 {
			m.resumeIndex++
		}
		return true, nil
	case key.Matches(msg, m.keys.Complete):
		m.applyResumeCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case key.Matches(msg, m.keys.Accept):
		if m.running || len(m.resumeCandidates) == 0 {
			return true, nil
		}
		selected := strings.TrimSpace(m.resumeCandidates[m.resumeIndex].SessionID)
		if selected == "" {
			return true, nil
		}
		_, cmd := m.submitLine("/resume " + selected)
		return true, cmd
	default:
		return false, nil
	}
}

func (m *Model) renderResumeList() string {
	if len(m.resumeCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.resumeCandidates))
	start := 0
	if m.resumeIndex >= maxItems {
		start = m.resumeIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.resumeCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.resumeCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		item := m.resumeCandidates[i]
		prompt := strings.TrimSpace(item.Prompt)
		if prompt == "" {
			prompt = "-"
		}
		age := strings.TrimSpace(item.Age)
		if age == "" {
			age = "-"
		}
		display := fmt.Sprintf("%s  %s", age, prompt)
		prefix := "  "
		if i == m.resumeIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(display))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(display))
		}
	}
	if end < len(m.resumeCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.resumeCandidates)-end),
		))
	}
	return m.renderCompletionOverlay("Recent", lines)
}

func (m *Model) clearSlashArg() {
	m.clearWizard()
}

func (m *Model) openSlashArgPicker(command string) {
	cmd := strings.ToLower(strings.TrimSpace(command))
	if cmd == "" {
		return
	}
	// Check if this command has a registered wizard definition.
	if def := m.findWizard(cmd); def != nil {
		m.startWizard(def)
		return
	}
	// Fallback: simple single-step slash-arg (no wizard).
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashCompletion()
	m.slashArgActive = true
	m.slashArgCommand = cmd
	m.wizard = nil
	m.setInputText("/" + cmd + " ")
	m.syncTextareaFromInput()
	m.updateSlashArgCandidates()
}

func (m *Model) activateSlashArgPickerFromInput(command string) {
	cmd := strings.ToLower(strings.TrimSpace(command))
	if cmd == "" {
		return
	}
	if m.slashArgActive && strings.TrimSpace(m.slashArgCommand) == cmd && !m.isWizardActive() {
		m.updateSlashArgCandidates()
		return
	}
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashCompletion()
	m.slashArgActive = true
	m.slashArgCommand = cmd
	m.wizard = nil
	m.updateSlashArgCandidates()
}

func (m *Model) syncSlashInputOverlays() {
	if m.running {
		return
	}
	raw := string(m.input[:m.cursor])
	trimmed := strings.TrimSpace(raw)
	hasResumePrefix := strings.HasPrefix(raw, "/resume ")
	hasBareResumeTrigger := strings.EqualFold(trimmed, "/resume") && len(raw) > 0 && (raw[len(raw)-1] == ' ' || raw[len(raw)-1] == '\t')
	if hasResumePrefix || hasBareResumeTrigger {
		m.activateResumePickerFromInput()
		return
	}
	if m.resumeActive {
		m.clearResume()
	}
	if command, _, ok := slashArgQueryAtCursor(m.input, m.cursor); ok {
		m.activateSlashArgPickerFromInput(command)
		return
	}
	if m.slashArgActive && !m.isWizardActive() {
		m.clearSlashArg()
	}
}

func (m *Model) updateSlashArgCandidates() {
	if !m.slashArgActive || m.cfg.SlashArgComplete == nil || m.running {
		m.slashArgCandidates = nil
		m.slashArgQuery = ""
		m.slashArgIndex = 0
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.resumeCandidates) > 0 {
		m.slashArgCandidates = nil
		return
	}

	// Determine the command key and query.
	command := m.slashArgCommand
	query := ""
	ok := false

	if m.isWizardActive() {
		w := m.wizard
		step := w.currentStep()
		if step == nil {
			m.slashArgCandidates = nil
			m.slashArgQuery = ""
			m.slashArgIndex = 0
			return
		}
		// Wizard steps that suppress completion.
		if w.noCompletion() {
			query, _ = wizardQueryAtCursor(w.def.Command, m.input, m.cursor)
			m.slashArgCandidates = nil
			m.slashArgQuery = query
			m.slashArgIndex = 0
			return
		}
		command = w.completionCommand()
		query, ok = wizardQueryAtCursor(w.def.Command, m.input, m.cursor)
	} else {
		// Non-wizard slash arg (simple single-step commands).
		var parsedCmd string
		parsedCmd, query, ok = slashArgQueryAtCursor(m.input, m.cursor)
		if ok && parsedCmd != command {
			ok = false
		}
	}
	if !ok {
		m.slashArgCandidates = nil
		m.slashArgQuery = ""
		m.slashArgIndex = 0
		return
	}
	candidates, err := m.cfg.SlashArgComplete(command, query, 200)
	if err != nil || len(candidates) == 0 {
		m.slashArgCandidates = nil
		m.slashArgQuery = query
		m.slashArgIndex = 0
		return
	}
	filtered := make([]SlashArgCandidate, 0, len(candidates))
	for _, one := range candidates {
		value := strings.TrimSpace(one.Value)
		if value == "" {
			continue
		}
		display := strings.TrimSpace(one.Display)
		if display == "" {
			display = value
		}
		filtered = append(filtered, SlashArgCandidate{Value: value, Display: display, NoAuth: one.NoAuth})
	}
	if len(filtered) == 0 {
		m.slashArgCandidates = nil
		m.slashArgQuery = query
		m.slashArgIndex = 0
		return
	}
	m.slashArgQuery = query
	m.slashArgCandidates = filtered
	if m.slashArgIndex >= len(m.slashArgCandidates) {
		m.slashArgIndex = len(m.slashArgCandidates) - 1
	}
	if m.slashArgIndex < 0 {
		m.slashArgIndex = 0
	}
}

func (m *Model) applySlashArgCompletion() {
	if len(m.slashArgCandidates) == 0 || strings.TrimSpace(m.slashArgCommand) == "" {
		m.updateSlashArgCandidates()
		if len(m.slashArgCandidates) == 0 || strings.TrimSpace(m.slashArgCommand) == "" {
			return
		}
	}
	choice := strings.TrimSpace(m.slashArgCandidates[m.slashArgIndex].Value)
	if choice == "" {
		return
	}
	if m.isWizardActive() {
		// During a wizard, tab fills the choice into the input after the command prefix.
		m.setInputText("/" + m.wizard.def.Command + " " + choice)
		m.syncTextareaFromInput()
		m.updateSlashArgCandidates()
		return
	}
	// Non-wizard: fill and close.
	command := strings.TrimSpace(m.slashArgCommand)
	switch command {
	case "model":
		m.setInputText("/model " + choice + " ")
		switch choice {
		case "use":
			m.activateSlashArgPickerFromInput("model " + choice)
		case "del":
			m.clearSlashArg()
		default:
			m.clearSlashArg()
		}
		return
	case "model use":
		m.setInputText("/model use " + choice + " ")
		m.activateSlashArgPickerFromInput("model use " + choice)
		return
	case "model use ":
		m.setInputText("/model use " + choice + " ")
		m.clearSlashArg()
		return
	}
	if strings.HasPrefix(command, "model use ") {
		m.setInputText("/" + command + " " + choice)
		m.clearSlashArg()
		return
	}
	m.setInputText("/" + command + " " + choice + " ")
	m.clearSlashArg()
}

func (m *Model) shouldExecuteSlashArgSelection(command string, choice string) bool {
	command = strings.TrimSpace(command)
	choice = strings.TrimSpace(choice)
	if command == "" || choice == "" {
		return false
	}
	current := strings.TrimSpace(m.textarea.Value())
	if current == "" {
		return false
	}
	if current != strings.TrimSpace(m.suggestedSlashArgInput(choice)) {
		return false
	}
	switch command {
	case "model":
		return false
	case "model use":
		return false
	case "model del":
		return true
	}
	if strings.HasPrefix(command, "model use ") {
		return true
	}
	return true
}

func isExecutableSlashArgInput(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(fields[0])) {
	case "/sandbox":
		return len(fields) >= 2
	case "/model":
		action := strings.ToLower(strings.TrimSpace(fields[1]))
		switch action {
		case "use":
			return len(fields) >= 3
		case "del":
			return len(fields) >= 2
		default:
			return false
		}
	default:
		return false
	}
}

func (m *Model) handleSlashArgKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.slashArgActive && strings.TrimSpace(m.slashArgCommand) == "" && !m.isWizardActive() {
		m.clearSlashArg()
		return false, nil
	}
	switch {
	case key.Matches(msg, m.keys.Back):
		if m.slashArgActive {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearSlashArg()
		return true, nil
	case key.Matches(msg, m.keys.ChoosePrev):
		if m.slashArgIndex > 0 {
			m.slashArgIndex--
		}
		return true, nil
	case key.Matches(msg, m.keys.ChooseNext):
		if m.slashArgIndex < len(m.slashArgCandidates)-1 {
			m.slashArgIndex++
		}
		return true, nil
	case key.Matches(msg, m.keys.Complete):
		m.applySlashArgCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case key.Matches(msg, m.keys.Accept):
		if m.running || strings.TrimSpace(m.slashArgCommand) == "" {
			return true, nil
		}
		// Delegate to wizard engine if active.
		if m.isWizardActive() {
			handled, cmd := m.handleWizardEnter()
			return handled, cmd
		}
		line := strings.TrimSpace(m.textarea.Value())
		if len(m.slashArgCandidates) == 0 && isExecutableSlashArgInput(line) {
			m.clearSlashArg()
			_, cmd := m.submitLine(line)
			return true, cmd
		}
		// Non-wizard: single-step slash arg.
		selected := ""
		if len(m.slashArgCandidates) > 0 && m.slashArgIndex >= 0 && m.slashArgIndex < len(m.slashArgCandidates) {
			selected = strings.TrimSpace(m.slashArgCandidates[m.slashArgIndex].Value)
		}
		if selected == "" {
			return true, nil
		}
		command := strings.TrimSpace(m.slashArgCommand)
		if m.shouldExecuteSlashArgSelection(command, selected) {
			m.clearSlashArg()
			_, cmd := m.submitLine(line)
			return true, cmd
		}
		if command == "model" || command == "model use" || strings.HasPrefix(command, "model use ") {
			m.applySlashArgCompletion()
			m.syncTextareaFromInput()
			return true, nil
		}
		m.applySlashArgCompletion()
		m.syncTextareaFromInput()
		return true, nil
	default:
		return false, nil
	}
}

func (m *Model) renderSlashArgList() string {
	if len(m.slashArgCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.slashArgCandidates))
	start := 0
	if m.slashArgIndex >= maxItems {
		start = m.slashArgIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.slashArgCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.slashArgCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		display := strings.TrimSpace(m.slashArgCandidates[i].Display)
		if display == "" {
			display = strings.TrimSpace(m.slashArgCandidates[i].Value)
		}
		prefix := "  "
		if i == m.slashArgIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(display))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(display))
		}
	}
	if end < len(m.slashArgCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.slashArgCandidates)-end),
		))
	}
	title := "/" + strings.TrimSpace(m.slashArgCommand)
	if title == "/" {
		title = "Options"
	}
	return m.renderCompletionOverlay(title, lines)
}

// ---------------------------------------------------------------------------
// Slash command completion
// ---------------------------------------------------------------------------

func (m *Model) refreshSlashCommands() {
	m.clearSlashCompletion()
	if m.running {
		return
	}
	// Avoid overlapping popups.
	if len(m.mentionCandidates) > 0 || len(m.skillCandidates) > 0 || len(m.resumeCandidates) > 0 || len(m.slashArgCandidates) > 0 {
		return
	}
	query, ok := slashCommandQueryAtCursor(m.input, m.cursor)
	if !ok {
		return
	}
	candidates := make([]string, 0, len(m.cfg.Commands))
	for _, cmd := range m.cfg.Commands {
		full := "/" + strings.TrimSpace(cmd)
		if full == "/" {
			continue
		}
		if query == "" || strings.HasPrefix(strings.ToLower(full), "/"+strings.ToLower(query)) {
			candidates = append(candidates, full)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return
	}
	m.slashCandidates = candidates
	m.slashIndex = 0
	m.slashPrefix = "/" + query
}

func (m *Model) applySlashCommandCompletion() {
	if len(m.slashCandidates) == 0 {
		m.refreshSlashCommands()
		if len(m.slashCandidates) == 0 {
			return
		}
	}
	m.setInputText(strings.TrimSpace(m.slashCandidates[m.slashIndex]))
	m.clearSlashCompletion()
}

func (m *Model) handleSlashCommandKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		if _, ok := slashCommandQueryAtCursor(m.input, m.cursor); ok {
			m.setInputText("")
			m.syncTextareaFromInput()
		}
		m.clearSlashCompletion()
		return true, nil
	case key.Matches(msg, m.keys.ChoosePrev):
		if m.slashIndex > 0 {
			m.slashIndex--
		}
		return true, nil
	case key.Matches(msg, m.keys.ChooseNext):
		if m.slashIndex < len(m.slashCandidates)-1 {
			m.slashIndex++
		}
		return true, nil
	case key.Matches(msg, m.keys.Complete):
		m.applySlashCommandCompletion()
		m.syncTextareaFromInput()
		return true, nil
	case key.Matches(msg, m.keys.Accept):
		if m.running || len(m.slashCandidates) == 0 {
			return true, nil
		}
		selected := strings.TrimSpace(m.slashCandidates[m.slashIndex])
		if selected == "" {
			return true, nil
		}
		if selected == "/model" || selected == "/sandbox" || selected == "/resume" {
			m.setInputText(selected)
			m.syncTextareaFromInput()
			m.clearSlashCompletion()
			m.tryOpenSlashArgPicker(selected)
			return true, nil
		}
		_, cmd := m.submitLine(selected)
		return true, cmd
	default:
		return false, nil
	}
}

func (m *Model) renderSlashCommandList() string {
	if len(m.slashCandidates) == 0 {
		return ""
	}
	maxItems := minInt(8, len(m.slashCandidates))
	start := 0
	if m.slashIndex >= maxItems {
		start = m.slashIndex - maxItems + 1
	}
	maxStart := maxInt(0, len(m.slashCandidates)-maxItems)
	if start > maxStart {
		start = maxStart
	}
	end := minInt(len(m.slashCandidates), start+maxItems)
	var lines []string
	if start > 0 {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d earlier", start),
		))
	}
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.slashIndex {
			prefix = m.theme.PromptStyle().Render("▸ ")
			lines = append(lines, prefix+m.theme.CommandActiveStyle().Render(m.slashCandidates[i]))
		} else {
			lines = append(lines, prefix+m.theme.HelpHintTextStyle().Render(m.slashCandidates[i]))
		}
	}
	if end < len(m.slashCandidates) {
		lines = append(lines, m.theme.HelpHintTextStyle().Render(
			fmt.Sprintf("  … and %d more", len(m.slashCandidates)-end),
		))
	}
	return m.renderCompletionOverlay("Commands", lines)
}

func (m *Model) clearSlashCompletion() {
	m.slashCandidates = nil
	m.slashIndex = 0
	m.slashPrefix = ""
}

func (m *Model) clearInputOverlays() {
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashArg()
	m.clearSlashCompletion()
	if m.showPalette {
		m.showPalette = false
	}
}

func (m *Model) setInputText(text string) {
	m.input = []rune(text)
	m.cursor = len(m.input)
	m.clearInputAttachments()
	if m.cfg.ClearAttachments != nil {
		m.cfg.ClearAttachments()
	}
}

func (m *Model) recordHistoryEntry(value string, attachments []inputAttachment) {
	entry := strings.TrimSpace(value)
	if entry == "" {
		return
	}
	// Slash commands are control inputs and should not pollute user message history.
	if strings.HasPrefix(entry, "/") {
		return
	}
	clonedAttachments := cloneInputAttachments(attachments)
	if len(m.history) == 0 || m.history[len(m.history)-1] != entry || !inputAttachmentsEqual(m.historyAttachments[len(m.historyAttachments)-1], clonedAttachments) {
		m.history = append(m.history, entry)
		m.historyAttachments = append(m.historyAttachments, clonedAttachments)
	}
}

func inputAttachmentsEqual(left []inputAttachment, right []inputAttachment) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
