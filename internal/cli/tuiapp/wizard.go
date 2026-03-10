package tuiapp

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Wizard — declarative multi-step inline command framework
//
// A WizardDef describes a sequence of named steps that the user walks through
// when invoking a slash command (e.g. /connect, /model).  Each step collects
// one value — either by selecting from a completion list or by free-form text
// input — and stores it in a string map keyed by [WizardStepDef.Key].
//
// The wizard engine lives entirely inside the TUI model. The CLI layer only
// provides the definitions (through [Config.Wizards]) and the completion
// candidates (through [Config.SlashArgComplete]).
// ---------------------------------------------------------------------------

// WizardStepDef describes one step in a wizard flow.
type WizardStepDef struct {
	// Key is the storage key for this step's value in the state map.
	Key string

	// HintLabel is the text shown in the hint bar, e.g. "/connect provider".
	HintLabel string

	// FreeformHint is shown when no candidates are available, e.g.
	// "/connect model: type model name and press enter".
	// When empty a default "<HintLabel>: ↑/↓ select │ enter: apply │ tab: fill"
	// hint is used if candidates exist, or nothing otherwise.
	FreeformHint string

	// HideInput masks the typed text in the input bar (e.g. for API keys).
	HideInput bool

	// NoCompletion suppresses candidate listing for this step. The user must
	// type a value and press enter. If HideInput is true, NoCompletion is
	// implicitly true.
	NoCompletion bool

	// CompletionCommand returns the command string passed to
	// Config.SlashArgComplete for this step. It receives the accumulated
	// state from previous steps. If it returns "", no completion is requested.
	CompletionCommand func(state map[string]string) string

	// ShouldSkip returns true to skip this step. If nil, the step is never
	// skipped. It receives the accumulated state from previous steps.
	ShouldSkip func(state map[string]string) bool

	// Validate checks the entered value. Return a non-nil error to reject the
	// value and stay on the current step. If nil, any non-empty string is
	// accepted.
	Validate func(value string) error
}

// WizardDef describes a complete multi-step wizard flow bound to a slash
// command.
type WizardDef struct {
	// Command is the slash command that triggers this wizard (e.g. "connect").
	Command string

	// Steps is the ordered list of wizard steps.
	Steps []WizardStepDef

	// DisplayLine is shown in the input history instead of the full exec
	// line. When empty the exec line itself is displayed.
	DisplayLine string

	// BuildExecLine constructs the final command line from the accumulated
	// state map. It is called after the last step is confirmed.
	BuildExecLine func(state map[string]string) string

	// OnStepConfirm is called after a step value is accepted, before
	// advancing. It may mutate the state map (e.g. to set flags like
	// "_noauth"). The candidate pointer is non-nil only when the user picked
	// from the completion list (as opposed to typing free-form).
	// stepKey is the Key of the just-confirmed step.
	OnStepConfirm func(stepKey string, value string, candidate *SlashArgCandidate, state map[string]string)
}

// ---------------------------------------------------------------------------
// Runtime state
// ---------------------------------------------------------------------------

// wizardRuntime holds the mutable state of an active wizard session.
type wizardRuntime struct {
	def       *WizardDef
	stepIndex int
	state     map[string]string
}

// currentStep returns the current step definition, or nil if out of range.
func (w *wizardRuntime) currentStep() *WizardStepDef {
	if w == nil || w.stepIndex < 0 || w.stepIndex >= len(w.def.Steps) {
		return nil
	}
	return &w.def.Steps[w.stepIndex]
}

// completionCommand returns the command string for this step.
// For no-completion / hidden-input steps the string is still useful
// for state introspection (e.g. tests); the caller decides whether
// to actually request candidates.
func (w *wizardRuntime) completionCommand() string {
	step := w.currentStep()
	if step == nil || step.CompletionCommand == nil {
		return ""
	}
	return step.CompletionCommand(w.state)
}

// hideInput indicates whether the current step should mask input.
func (w *wizardRuntime) hideInput() bool {
	step := w.currentStep()
	return step != nil && step.HideInput
}

// noCompletion returns true if the current step suppresses candidate listing.
func (w *wizardRuntime) noCompletion() bool {
	step := w.currentStep()
	if step == nil {
		return true
	}
	return step.NoCompletion || step.HideInput
}

// ---------------------------------------------------------------------------
// Model integration
// ---------------------------------------------------------------------------

// findWizard looks up a registered wizard definition by command name.
func (m *Model) findWizard(command string) *WizardDef {
	cmd := strings.ToLower(strings.TrimSpace(command))
	for i := range m.cfg.Wizards {
		if strings.ToLower(strings.TrimSpace(m.cfg.Wizards[i].Command)) == cmd {
			return &m.cfg.Wizards[i]
		}
	}
	return nil
}

// startWizard initialises a new wizard session and opens the first eligible
// step. It clears any existing slash-arg / wizard state.
func (m *Model) startWizard(def *WizardDef) {
	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashCompletion()

	m.wizard = &wizardRuntime{
		def:       def,
		stepIndex: -1, // will be advanced below
		state:     make(map[string]string),
	}

	// Open the first non-skipped step.
	m.advanceWizardStep("")
}

func (m *Model) advanceWizardCursor() bool {
	w := m.wizard
	if w == nil {
		return false
	}
	for {
		w.stepIndex++
		if w.stepIndex >= len(w.def.Steps) {
			return false
		}
		step := &w.def.Steps[w.stepIndex]
		if step.ShouldSkip != nil && step.ShouldSkip(w.state) {
			continue
		}
		return true
	}
}

func (m *Model) activateWizardFromInput(line string) bool {
	raw := strings.TrimSpace(line)
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return false
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return false
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), "/")
	def := m.findWizard(command)
	if def == nil {
		return false
	}
	hasTrailingDelimiter := strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t")
	if len(fields) == 1 && !hasTrailingDelimiter {
		return false
	}

	args := fields[1:]
	committedArgs := args
	pendingQuery := ""
	if !hasTrailingDelimiter && len(args) > 0 {
		committedArgs = args[:len(args)-1]
		pendingQuery = strings.TrimSpace(args[len(args)-1])
	}

	m.clearMention()
	m.clearSkill()
	m.clearResume()
	m.clearSlashCompletion()
	m.wizard = &wizardRuntime{
		def:       def,
		stepIndex: -1,
		state:     make(map[string]string),
	}

	if !m.advanceWizardCursor() {
		m.clearWizard()
		return false
	}
	for _, arg := range committedArgs {
		step := m.wizard.currentStep()
		if step == nil {
			m.clearWizard()
			return false
		}
		value := strings.TrimSpace(arg)
		if value == "" {
			m.clearWizard()
			return false
		}
		m.wizard.state[step.Key] = value
		if m.wizard.def.OnStepConfirm != nil {
			m.wizard.def.OnStepConfirm(step.Key, value, nil, m.wizard.state)
		}
		if !m.advanceWizardCursor() {
			m.clearWizard()
			return false
		}
	}

	m.slashArgActive = true
	m.slashArgCommand = m.wizard.completionCommand()
	m.slashArgQuery = ""
	m.slashArgIndex = 0
	m.slashArgCandidates = nil
	m.setInputText("/" + def.Command + " " + pendingQuery)
	m.syncTextareaFromInput()
	m.updateSlashArgCandidates()
	return true
}

func (m *Model) tryAdvanceWizardOnTrailingDelimiter() (bool, tea.Cmd) {
	if !m.isWizardActive() || !m.slashArgActive {
		return false, nil
	}
	text := m.textarea.Value()
	if !strings.HasSuffix(text, " ") && !strings.HasSuffix(text, "\t") {
		return false, nil
	}
	step := m.wizard.currentStep()
	if step == nil {
		return false, nil
	}
	query, ok := wizardQueryAtCursor(m.wizard.def.Command, m.input, m.cursor)
	if !ok {
		return false, nil
	}
	value := strings.TrimSpace(query)
	if value == "" {
		return false, nil
	}
	var candidate *SlashArgCandidate
	for i := range m.slashArgCandidates {
		if strings.EqualFold(strings.TrimSpace(m.slashArgCandidates[i].Value), value) {
			c := m.slashArgCandidates[i]
			candidate = &c
			value = strings.TrimSpace(c.Value)
			break
		}
	}
	if step.Validate != nil {
		if err := step.Validate(value); err != nil {
			return false, nil
		}
	}
	return true, m.advanceWizardStep(value, candidate)
}

// advanceWizardStep stores the given value for the current step (if any),
// invokes OnStepConfirm, and moves to the next non-skipped step. When all
// steps are exhausted it builds the exec line and submits it.
//
// The candidate pointer is non-nil when the value came from a list selection.
func (m *Model) advanceWizardStep(value string, candidateOpt ...*SlashArgCandidate) tea.Cmd {
	w := m.wizard
	if w == nil {
		return nil
	}

	// Store current step value.
	if step := w.currentStep(); step != nil {
		w.state[step.Key] = value
		var cand *SlashArgCandidate
		if len(candidateOpt) > 0 {
			cand = candidateOpt[0]
		}
		if w.def.OnStepConfirm != nil {
			w.def.OnStepConfirm(step.Key, value, cand, w.state)
		}
	}

	// Advance to next non-skipped step.
	if !m.advanceWizardCursor() {
		return m.wizardSubmit()
	}

	// Open the new step.
	m.slashArgActive = true
	m.slashArgCommand = w.completionCommand()
	m.slashArgQuery = ""
	m.slashArgIndex = 0
	m.slashArgCandidates = nil
	m.setInputText("/" + w.def.Command + " ")
	m.syncTextareaFromInput()
	m.updateSlashArgCandidates()
	return nil
}

// wizardSubmit builds the exec line and submits it.
func (m *Model) wizardSubmit() tea.Cmd {
	w := m.wizard
	if w == nil || w.def.BuildExecLine == nil {
		m.clearWizard()
		return nil
	}
	execLine := w.def.BuildExecLine(w.state)
	displayLine := w.def.DisplayLine
	if displayLine == "" {
		displayLine = execLine
	}
	m.clearWizard()
	_, cmd := m.submitLineWithDisplay(execLine, displayLine)
	return cmd
}

// clearWizard resets all wizard and slash-arg state.
func (m *Model) clearWizard() {
	m.wizard = nil
	m.slashArgActive = false
	m.slashArgCommand = ""
	m.slashArgQuery = ""
	m.slashArgCandidates = nil
	m.slashArgIndex = 0
}

// isWizardActive returns true when a multi-step wizard is in progress.
func (m *Model) isWizardActive() bool {
	return m.wizard != nil
}

// wizardHintText returns the hint text for the current wizard step.
func (m *Model) wizardHintText() string {
	w := m.wizard
	if w == nil {
		return ""
	}
	step := w.currentStep()
	if step == nil {
		return ""
	}
	if len(m.slashArgCandidates) == 0 {
		if step.FreeformHint != "" {
			return step.FreeformHint
		}
		if step.HideInput || step.NoCompletion {
			label := step.HintLabel
			if label == "" {
				label = "/" + w.def.Command + " " + step.Key
			}
			return label + ": type and press enter"
		}
		return ""
	}
	label := step.HintLabel
	if label == "" {
		label = "/" + w.def.Command + " " + step.Key
	}
	return label + ": ↑/↓ select │ enter: apply │ tab: fill"
}

// handleWizardEnter processes the enter key when a wizard is active.
// Returns (handled bool, cmd tea.Cmd).
func (m *Model) handleWizardEnter() (bool, tea.Cmd) {
	w := m.wizard
	if w == nil {
		return false, nil
	}
	step := w.currentStep()
	if step == nil {
		return false, nil
	}

	// Determine the entered value.
	value := ""
	var candidate *SlashArgCandidate
	if len(m.slashArgCandidates) > 0 && m.slashArgIndex >= 0 && m.slashArgIndex < len(m.slashArgCandidates) {
		c := m.slashArgCandidates[m.slashArgIndex]
		value = strings.TrimSpace(c.Value)
		candidate = &c
	}
	if value == "" {
		value = strings.TrimSpace(m.slashArgQuery)
	}

	// Validate.
	if value == "" {
		return true, nil // ignore empty enter
	}
	if step.Validate != nil {
		if err := step.Validate(value); err != nil {
			return true, nil // validation failed — stay
		}
	}

	cmd := m.advanceWizardStep(value, candidate)
	return true, cmd
}

// wizardQueryAtCursor extracts the query text after the wizard command prefix.
func wizardQueryAtCursor(command string, input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	raw := string(input[:cursor])
	prefix := "/" + strings.ToLower(strings.TrimSpace(command))
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(strings.ToLower(trimmed), prefix) {
		return "", false
	}
	if strings.EqualFold(trimmed, prefix) {
		return "", true
	}
	if !strings.HasPrefix(raw, prefix+" ") {
		return "", false
	}
	return strings.TrimSpace(raw[len(prefix)+1:]), true
}

// ValidateInt returns a validator that accepts valid integer strings.
func ValidateInt(value string) error {
	_, err := strconv.Atoi(strings.TrimSpace(value))
	return err
}
