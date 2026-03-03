package tuikit

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	AppBg          lipgloss.Color
	PanelBorder    lipgloss.Color
	PanelTitle     lipgloss.Color
	TextPrimary    lipgloss.Color
	TextSecondary  lipgloss.Color
	Info           lipgloss.Color
	Success        lipgloss.Color
	Warning        lipgloss.Color
	Error          lipgloss.Color
	Accent         lipgloss.Color
	Focus          lipgloss.Color
	ModalBg        lipgloss.Color
	StatusBg       lipgloss.Color
	StatusText     lipgloss.Color
	CommandBg      lipgloss.Color
	CommandActive  lipgloss.Color
	CommandText    lipgloss.Color
	CommandSubText lipgloss.Color

	// Line-level semantic colors (conversation / tool / diff).
	AssistantFg  lipgloss.Color
	ReasoningFg  lipgloss.Color
	UserFg       lipgloss.Color
	ToolFg       lipgloss.Color
	DiffAddFg    lipgloss.Color
	DiffRemoveFg lipgloss.Color
	DiffHeaderFg lipgloss.Color
	SectionFg    lipgloss.Color
	KeyLabelFg   lipgloss.Color
	NoteFg       lipgloss.Color

	// Input area
	PromptFg     lipgloss.Color
	CursorFg     lipgloss.Color
	ScrollHintFg lipgloss.Color

	// Inline layout
	InputBarBg   lipgloss.Color
	InputBarFg   lipgloss.Color
	HelpHintFg   lipgloss.Color
	SpinnerFg    lipgloss.Color
	SeparatorFg  lipgloss.Color
	RoleBorderFg lipgloss.Color // left border for role sections
	NewMsgBg     lipgloss.Color // "new messages" indicator
}

func DefaultTheme() Theme {
	return Theme{
		AppBg:          lipgloss.Color("#101216"),
		PanelBorder:    lipgloss.Color("#2f3f5f"),
		PanelTitle:     lipgloss.Color("#4da3ff"),
		TextPrimary:    lipgloss.Color("#d9dce3"),
		TextSecondary:  lipgloss.Color("#8d96a5"),
		Info:           lipgloss.Color("#4da3ff"),
		Success:        lipgloss.Color("#56d364"),
		Warning:        lipgloss.Color("#f5c451"),
		Error:          lipgloss.Color("#ff7b72"),
		Accent:         lipgloss.Color("#22d3ee"),
		Focus:          lipgloss.Color("#4da3ff"),
		ModalBg:        lipgloss.Color("#111827"),
		StatusBg:       lipgloss.Color("#151b26"),
		StatusText:     lipgloss.Color("#9fb0c9"),
		CommandBg:      lipgloss.Color("#0f1728"),
		CommandActive:  lipgloss.Color("#1f2a44"),
		CommandText:    lipgloss.Color("#d4d8e0"),
		CommandSubText: lipgloss.Color("#8d96a5"),

		AssistantFg:  lipgloss.Color("#56d364"),
		ReasoningFg:  lipgloss.Color("#8d96a5"),
		UserFg:       lipgloss.Color("#d9dce3"),
		ToolFg:       lipgloss.Color("#22d3ee"),
		DiffAddFg:    lipgloss.Color("#56d364"),
		DiffRemoveFg: lipgloss.Color("#ff7b72"),
		DiffHeaderFg: lipgloss.Color("#8d96a5"),
		SectionFg:    lipgloss.Color("#d9dce3"),
		KeyLabelFg:   lipgloss.Color("#4da3ff"),
		NoteFg:       lipgloss.Color("#8d96a5"),
		PromptFg:     lipgloss.Color("#4da3ff"),
		CursorFg:     lipgloss.Color("#ffffff"),
		ScrollHintFg: lipgloss.Color("#f5c451"),

		InputBarBg:   lipgloss.Color("#151b26"),
		InputBarFg:   lipgloss.Color("#d9dce3"),
		HelpHintFg:   lipgloss.Color("#5e6a7e"),
		SpinnerFg:    lipgloss.Color("#4da3ff"),
		SeparatorFg:  lipgloss.Color("#2f3f5f"),
		RoleBorderFg: lipgloss.Color("#2f3f5f"),
		NewMsgBg:     lipgloss.Color("#1f2a44"),
	}
}

func (t Theme) FrameStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(t.PanelBorder).
		Foreground(t.TextPrimary).
		Padding(0, 1)
}

func (t Theme) StatusStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(t.StatusBg).
		Foreground(t.StatusText).
		Padding(0, 1)
}

func (t Theme) HintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Accent)
}

func (t Theme) TitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(t.PanelTitle)
}

func (t Theme) ModalStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(t.ModalBg).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(t.Focus).
		Padding(1, 2)
}

func (t Theme) CommandActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(t.CommandActive).
		Foreground(t.CommandText).
		Padding(0, 1)
}

func (t Theme) CommandStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(t.CommandText).
		Padding(0, 1)
}

// ---------------------------------------------------------------------------
// Line-style rendering helpers
// ---------------------------------------------------------------------------

// AssistantStyle renders assistant text (green prefix).
func (t Theme) AssistantStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.AssistantFg)
}

// ReasoningStyle renders reasoning/thinking text (dimmed).
func (t Theme) ReasoningStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ReasoningFg)
}

// ToolStyle renders tool call/result prefixes (cyan).
func (t Theme) ToolStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ToolFg)
}

// ToolNameStyle renders tool names (bold + cyan).
func (t Theme) ToolNameStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ToolFg).Bold(true)
}

// DiffAddStyle renders added lines in diffs (green).
func (t Theme) DiffAddStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffAddFg)
}

// DiffRemoveStyle renders removed lines in diffs (red).
func (t Theme) DiffRemoveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffRemoveFg)
}

// DiffHeaderStyle renders diff headers (dimmed + bold).
func (t Theme) DiffHeaderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffHeaderFg).Bold(true)
}

// WarnStyle renders warning text (yellow).
func (t Theme) WarnStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Warning)
}

// ErrorStyle renders error text (red).
func (t Theme) ErrorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Error)
}

// NoteStyle renders note text (dimmed).
func (t Theme) NoteStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.NoteFg)
}

// SectionStyle renders section headers (bold).
func (t Theme) SectionStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.SectionFg).Bold(true)
}

// KeyLabelStyle renders key labels in key-value pairs.
func (t Theme) KeyLabelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.KeyLabelFg)
}

// PromptStyle renders the input prompt marker.
func (t Theme) PromptStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.PromptFg).Bold(true)
}

// ScrollHintIndicator renders scroll hint text.
func (t Theme) ScrollHintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ScrollHintFg)
}

func ComposeFooter(width int, left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if width <= 0 {
		return ""
	}
	if left == "" && right == "" {
		return strings.Repeat(" ", width)
	}
	if left == "" {
		if len(right) >= width {
			return right[len(right)-width:]
		}
		return strings.Repeat(" ", width-len(right)) + right
	}
	if right == "" {
		if len(left) >= width {
			return left[:width]
		}
		return left + strings.Repeat(" ", width-len(left))
	}
	if len(left)+len(right)+1 <= width {
		return left + strings.Repeat(" ", width-len(left)-len(right)) + right
	}
	maxLeft := width - len(right) - 1
	if maxLeft < 0 {
		maxLeft = 0
	}
	if len(left) > maxLeft {
		left = left[:maxLeft]
	}
	gap := width - len(left) - len(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// ---------------------------------------------------------------------------
// Inline layout styles
// ---------------------------------------------------------------------------

// InputBarStyle renders the input bar background.
func (t Theme) InputBarStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(t.InputBarBg).
		Foreground(t.InputBarFg).
		Padding(0, 1)
}

// HelpHintTextStyle renders help hint text (dimmed shortcut labels).
func (t Theme) HelpHintTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.HelpHintFg)
}

// SpinnerStyle renders the spinner indicator.
func (t Theme) SpinnerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.SpinnerFg)
}

// SeparatorStyle renders horizontal separators.
func (t Theme) SeparatorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.SeparatorFg)
}

// NewMsgIndicatorStyle renders the "new messages" indicator.
func (t Theme) NewMsgIndicatorStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(t.NewMsgBg).
		Foreground(t.Warning).
		Padding(0, 1)
}
