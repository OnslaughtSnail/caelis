package tuiapp

import (
	"errors"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// External prompt handling
// ---------------------------------------------------------------------------

func (m *Model) enqueuePrompt(req tuievents.PromptRequestMsg) {
	if req.Response == nil {
		return
	}
	if m.activePrompt == nil {
		m.activePrompt = newPromptState(req)
		return
	}
	m.pendingPrompt = append(m.pendingPrompt, req)
}

func (m *Model) finishPrompt(line string, err error) {
	if m.activePrompt == nil {
		return
	}
	resp := m.activePrompt.response
	if resp != nil {
		resp <- tuievents.PromptResponse{Line: line, Err: err}
	}
	if len(m.pendingPrompt) == 0 {
		m.activePrompt = nil
		return
	}
	next := m.pendingPrompt[0]
	m.pendingPrompt = m.pendingPrompt[1:]
	m.activePrompt = newPromptState(next)
}

func (m *Model) handlePromptKey(msg tea.KeyMsg) tea.Cmd {
	if m.activePrompt == nil {
		return nil
	}
	if len(m.activePrompt.choices) > 0 {
		return m.handlePromptChoiceKey(msg)
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		m.finishPrompt("", errors.New(tuievents.PromptErrInterrupt))
		return nil
	case "ctrl+d":
		if len(m.activePrompt.input) == 0 {
			m.finishPrompt("", errors.New(tuievents.PromptErrEOF))
			return nil
		}
		if m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor], m.activePrompt.input[m.activePrompt.cursor+1:]...)
		}
		return nil
	case "enter":
		m.finishPrompt(strings.TrimSpace(string(m.activePrompt.input)), nil)
		return nil
	case "left":
		if m.activePrompt.cursor > 0 {
			m.activePrompt.cursor--
		}
		return nil
	case "right":
		if m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.cursor++
		}
		return nil
	case "home", "ctrl+a":
		m.activePrompt.cursor = 0
		return nil
	case "end", "ctrl+e":
		m.activePrompt.cursor = len(m.activePrompt.input)
		return nil
	case "backspace":
		if m.activePrompt.cursor > 0 {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor-1], m.activePrompt.input[m.activePrompt.cursor:]...)
			m.activePrompt.cursor--
		}
		return nil
	case "delete":
		if m.activePrompt.cursor >= 0 && m.activePrompt.cursor < len(m.activePrompt.input) {
			m.activePrompt.input = append(m.activePrompt.input[:m.activePrompt.cursor], m.activePrompt.input[m.activePrompt.cursor+1:]...)
		}
		return nil
	case "ctrl+u":
		m.activePrompt.input = m.activePrompt.input[:0]
		m.activePrompt.cursor = 0
		return nil
	}
	if len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			head := append([]rune(nil), m.activePrompt.input[:m.activePrompt.cursor]...)
			head = append(head, r)
			m.activePrompt.input = append(head, m.activePrompt.input[m.activePrompt.cursor:]...)
			m.activePrompt.cursor++
		}
	}
	return nil
}

func newPromptState(req tuievents.PromptRequestMsg) *promptState {
	state := &promptState{
		prompt:   req.Prompt,
		secret:   req.Secret,
		response: req.Response,
	}
	if req.Secret {
		return state
	}
	if choices, idx, ok := parsePromptChoices(req.Prompt); ok {
		state.choices = choices
		state.choiceIndex = idx
	}
	return state
}

func parsePromptChoices(prompt string) ([]promptChoice, int, bool) {
	normalized := strings.ToLower(strings.Join(strings.Fields(prompt), " "))
	if strings.Contains(normalized, "[y] allow") &&
		strings.Contains(normalized, "[a] always") &&
		strings.Contains(normalized, "[n] deny") {
		return []promptChoice{
			{label: "allow", value: "y"},
			{label: "always", value: "a"},
			{label: "deny", value: "n"},
		}, 2, true
	}
	return nil, 0, false
}

func (m *Model) handlePromptChoiceKey(msg tea.KeyMsg) tea.Cmd {
	if m.activePrompt == nil || len(m.activePrompt.choices) == 0 {
		return nil
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		m.finishPrompt("", errors.New(tuievents.PromptErrInterrupt))
		return nil
	case "ctrl+d":
		m.finishPrompt("", errors.New(tuievents.PromptErrEOF))
		return nil
	case "up", "k", "shift+tab":
		if m.activePrompt.choiceIndex > 0 {
			m.activePrompt.choiceIndex--
		}
		return nil
	case "down", "j", "tab":
		if m.activePrompt.choiceIndex < len(m.activePrompt.choices)-1 {
			m.activePrompt.choiceIndex++
		}
		return nil
	case "enter":
		choice := m.activePrompt.choices[m.activePrompt.choiceIndex]
		m.finishPrompt(choice.value, nil)
		return nil
	}
	if len(msg.Runes) > 0 {
		key := strings.ToLower(strings.TrimSpace(string(msg.Runes)))
		for _, choice := range m.activePrompt.choices {
			if choice.value == key {
				m.finishPrompt(choice.value, nil)
				return nil
			}
		}
	}
	return nil
}
