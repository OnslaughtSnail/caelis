package tuikit

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	AppBg          lipgloss.TerminalColor
	PanelBorder    lipgloss.TerminalColor
	PanelTitle     lipgloss.TerminalColor
	TextPrimary    lipgloss.TerminalColor
	TextSecondary  lipgloss.TerminalColor
	Info           lipgloss.TerminalColor
	Success        lipgloss.TerminalColor
	Warning        lipgloss.TerminalColor
	Error          lipgloss.TerminalColor
	Accent         lipgloss.TerminalColor
	Focus          lipgloss.TerminalColor
	ModalBg        lipgloss.TerminalColor
	StatusBg       lipgloss.TerminalColor
	StatusText     lipgloss.TerminalColor
	CommandBg      lipgloss.TerminalColor
	CommandActive  lipgloss.TerminalColor
	CommandText    lipgloss.TerminalColor
	CommandSubText lipgloss.TerminalColor

	// Line-level semantic colors (conversation / tool / diff).
	AssistantFg        lipgloss.TerminalColor
	ReasoningFg        lipgloss.TerminalColor
	UserFg             lipgloss.TerminalColor
	UserBg             lipgloss.TerminalColor
	UserPrefixFg       lipgloss.TerminalColor
	UserMentionFg      lipgloss.TerminalColor
	ToolFg             lipgloss.TerminalColor
	DiffAddFg          lipgloss.TerminalColor
	DiffRemoveFg       lipgloss.TerminalColor
	DiffHeaderFg       lipgloss.TerminalColor
	DiffHunkFg         lipgloss.TerminalColor
	DiffAddBg          lipgloss.TerminalColor
	DiffAddStrongBg    lipgloss.TerminalColor
	DiffRemoveBg       lipgloss.TerminalColor
	DiffRemoveStrongBg lipgloss.TerminalColor
	DiffLineNoFg       lipgloss.TerminalColor
	DiffGutterFg       lipgloss.TerminalColor
	DiffPanelBorder    lipgloss.TerminalColor
	SectionFg          lipgloss.TerminalColor
	KeyLabelFg         lipgloss.TerminalColor
	NoteFg             lipgloss.TerminalColor

	// Input area
	PromptFg     lipgloss.TerminalColor
	CursorFg     lipgloss.TerminalColor
	ScrollHintFg lipgloss.TerminalColor

	// Inline layout
	InputBarBg          lipgloss.TerminalColor
	InputBarFg          lipgloss.TerminalColor
	ToolOutputBg        lipgloss.TerminalColor
	HelpHintFg          lipgloss.TerminalColor
	SpinnerFg           lipgloss.TerminalColor
	SeparatorFg         lipgloss.TerminalColor
	RoleBorderFg        lipgloss.TerminalColor // left border for role sections
	NewMsgBg            lipgloss.TerminalColor // "new messages" indicator
	ComposerBorder      lipgloss.TerminalColor
	ComposerBorderFocus lipgloss.TerminalColor
	ScrollbarTrack      lipgloss.TerminalColor
	ScrollbarThumb      lipgloss.TerminalColor
	LinkFg              lipgloss.TerminalColor
	CodeFg              lipgloss.TerminalColor
	CodeBg              lipgloss.TerminalColor
	CodeBlockFg         lipgloss.TerminalColor
	CodeBlockBg         lipgloss.TerminalColor
}

func DefaultTheme() Theme {
	return ResolveThemeFromEnv()
}

func ResolveThemeFromEnv() Theme {
	useTrueColor := supportsTrueColor()
	name := strings.ToLower(strings.TrimSpace(os.Getenv("CAELIS_THEME")))
	theme := namedTheme(name, useTrueColor)
	if accent := strings.TrimSpace(os.Getenv("CAELIS_ACCENT")); accent != "" {
		theme.Accent = lipgloss.Color(accent)
		theme.Focus = lipgloss.Color(accent)
		theme.ComposerBorderFocus = lipgloss.Color(accent)
		theme.LinkFg = lipgloss.Color(accent)
	}
	return theme
}

func supportsTrueColor() bool {
	colorterm := strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM")))
	if strings.Contains(colorterm, "truecolor") || strings.Contains(colorterm, "24bit") {
		return true
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	return strings.Contains(term, "truecolor") || strings.Contains(term, "24bit") || strings.Contains(term, "direct")
}

func namedTheme(name string, trueColor bool) Theme {
	switch name {
	case "nord":
		return nordTheme(trueColor)
	case "solarized":
		return solarizedTheme(trueColor)
	case "dracula":
		return draculaTheme(trueColor)
	default:
		return defaultThemeVariant(trueColor)
	}
}

func color(trueColor bool, rich string, fallback string) lipgloss.TerminalColor {
	if trueColor || fallback == "" {
		return lipgloss.Color(rich)
	}
	return lipgloss.Color(fallback)
}

func defaultThemeVariant(trueColor bool) Theme {
	return Theme{
		AppBg:          color(trueColor, "#111315", "233"),
		PanelBorder:    color(trueColor, "#3f4652", "240"),
		PanelTitle:     color(trueColor, "#f3f4f6", "255"),
		TextPrimary:    color(trueColor, "#f5f5f5", "255"),
		TextSecondary:  color(trueColor, "#a1a1aa", "248"),
		Info:           color(trueColor, "#d4d4d8", "252"),
		Success:        color(trueColor, "#56d364", "77"),
		Warning:        color(trueColor, "#f5c451", "221"),
		Error:          color(trueColor, "#ff7b72", "210"),
		Accent:         color(trueColor, "#e5e7eb", "254"),
		Focus:          color(trueColor, "#f3f4f6", "255"),
		ModalBg:        color(trueColor, "#15181d", "234"),
		StatusBg:       color(trueColor, "#111315", "233"),
		StatusText:     color(trueColor, "#d4d4d8", "252"),
		CommandBg:      color(trueColor, "#111315", "233"),
		CommandActive:  color(trueColor, "#111315", "233"),
		CommandText:    color(trueColor, "#f5f5f5", "255"),
		CommandSubText: color(trueColor, "#a1a1aa", "248"),

		AssistantFg:        color(trueColor, "#56d364", "77"),
		ReasoningFg:        color(trueColor, "#8d96a5", "246"),
		UserFg:             color(trueColor, "#f5f5f5", "255"),
		UserBg:             color(trueColor, "#111315", "233"),
		UserPrefixFg:       color(trueColor, "#ffffff", "255"),
		UserMentionFg:      color(trueColor, "#f5f5f5", "255"),
		ToolFg:             color(trueColor, "#e5e7eb", "254"),
		DiffAddFg:          color(trueColor, "#56d364", "77"),
		DiffRemoveFg:       color(trueColor, "#ff7b72", "210"),
		DiffHeaderFg:       color(trueColor, "#8d96a5", "246"),
		DiffHunkFg:         color(trueColor, "#d4d4d8", "252"),
		DiffAddBg:          color(trueColor, "#1d3328", "22"),
		DiffAddStrongBg:    color(trueColor, "#285f3a", "29"),
		DiffRemoveBg:       color(trueColor, "#3a2329", "52"),
		DiffRemoveStrongBg: color(trueColor, "#6e2b34", "88"),
		DiffLineNoFg:       color(trueColor, "#758195", "245"),
		DiffGutterFg:       color(trueColor, "#8d96a5", "246"),
		DiffPanelBorder:    color(trueColor, "#3f4652", "240"),
		SectionFg:          color(trueColor, "#f5f5f5", "255"),
		KeyLabelFg:         color(trueColor, "#e5e7eb", "254"),
		NoteFg:             color(trueColor, "#a1a1aa", "248"),
		PromptFg:           color(trueColor, "#f5f5f5", "255"),
		CursorFg:           color(trueColor, "#ffffff", "255"),
		ScrollHintFg:       color(trueColor, "#f5c451", "221"),

		InputBarBg:          color(trueColor, "#111315", "233"),
		InputBarFg:          color(trueColor, "#f5f5f5", "255"),
		ToolOutputBg:        color(trueColor, "#111315", "233"),
		HelpHintFg:          color(trueColor, "#a1a1aa", "248"),
		SpinnerFg:           color(trueColor, "#e5e7eb", "254"),
		SeparatorFg:         color(trueColor, "#3f4652", "240"),
		RoleBorderFg:        color(trueColor, "#3f4652", "240"),
		NewMsgBg:            color(trueColor, "#111315", "233"),
		ComposerBorder:      color(trueColor, "#3f4652", "240"),
		ComposerBorderFocus: color(trueColor, "#f3f4f6", "255"),
		ScrollbarTrack:      color(trueColor, "#1d2128", "234"),
		ScrollbarThumb:      color(trueColor, "#8d96a5", "246"),
		LinkFg:              color(trueColor, "#8ab4f8", "117"),
		CodeFg:              color(trueColor, "#f5c451", "221"),
		CodeBg:              color(trueColor, "#1b1f27", "234"),
		CodeBlockFg:         color(trueColor, "#d4d4d8", "252"),
		CodeBlockBg:         color(trueColor, "#171a20", "234"),
	}
}

func nordTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = color(trueColor, "#2e3440", "236")
	theme.PanelBorder = color(trueColor, "#4c566a", "240")
	theme.PanelTitle = color(trueColor, "#eceff4", "255")
	theme.TextPrimary = color(trueColor, "#eceff4", "255")
	theme.TextSecondary = color(trueColor, "#d8dee9", "252")
	theme.Info = color(trueColor, "#d8dee9", "252")
	theme.Success = color(trueColor, "#a3be8c", "108")
	theme.Warning = color(trueColor, "#ebcb8b", "223")
	theme.Error = color(trueColor, "#bf616a", "131")
	theme.Accent = color(trueColor, "#88c0d0", "110")
	theme.Focus = color(trueColor, "#81a1c1", "110")
	theme.ModalBg = color(trueColor, "#3b4252", "237")
	theme.StatusBg = color(trueColor, "#2e3440", "236")
	theme.StatusText = color(trueColor, "#d8dee9", "252")
	theme.AssistantFg = color(trueColor, "#a3be8c", "108")
	theme.ReasoningFg = color(trueColor, "#81a1c1", "110")
	theme.ToolFg = color(trueColor, "#88c0d0", "110")
	theme.DiffAddBg = color(trueColor, "#314236", "23")
	theme.DiffAddStrongBg = color(trueColor, "#45604e", "59")
	theme.DiffRemoveBg = color(trueColor, "#4a3037", "52")
	theme.DiffRemoveStrongBg = color(trueColor, "#6a3f4a", "95")
	theme.ComposerBorder = color(trueColor, "#4c566a", "240")
	theme.ComposerBorderFocus = color(trueColor, "#81a1c1", "110")
	theme.ScrollbarTrack = color(trueColor, "#3b4252", "237")
	theme.ScrollbarThumb = color(trueColor, "#81a1c1", "110")
	theme.LinkFg = color(trueColor, "#88c0d0", "110")
	theme.CodeBg = color(trueColor, "#3b4252", "237")
	theme.CodeBlockBg = color(trueColor, "#2b303b", "236")
	return theme
}

func solarizedTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = color(trueColor, "#002b36", "235")
	theme.PanelBorder = color(trueColor, "#586e75", "242")
	theme.PanelTitle = color(trueColor, "#fdf6e3", "230")
	theme.TextPrimary = color(trueColor, "#eee8d5", "254")
	theme.TextSecondary = color(trueColor, "#93a1a1", "245")
	theme.Info = color(trueColor, "#93a1a1", "245")
	theme.Success = color(trueColor, "#859900", "100")
	theme.Warning = color(trueColor, "#b58900", "136")
	theme.Error = color(trueColor, "#dc322f", "160")
	theme.Accent = color(trueColor, "#2aa198", "36")
	theme.Focus = color(trueColor, "#268bd2", "32")
	theme.ModalBg = color(trueColor, "#073642", "236")
	theme.StatusBg = color(trueColor, "#002b36", "235")
	theme.StatusText = color(trueColor, "#93a1a1", "245")
	theme.AssistantFg = color(trueColor, "#859900", "100")
	theme.ReasoningFg = color(trueColor, "#6c71c4", "61")
	theme.ToolFg = color(trueColor, "#2aa198", "36")
	theme.DiffAddBg = color(trueColor, "#173d1c", "22")
	theme.DiffAddStrongBg = color(trueColor, "#2f5f2f", "29")
	theme.DiffRemoveBg = color(trueColor, "#4a1f1c", "52")
	theme.DiffRemoveStrongBg = color(trueColor, "#7a2d24", "88")
	theme.ComposerBorder = color(trueColor, "#586e75", "242")
	theme.ComposerBorderFocus = color(trueColor, "#268bd2", "32")
	theme.ScrollbarTrack = color(trueColor, "#073642", "236")
	theme.ScrollbarThumb = color(trueColor, "#586e75", "242")
	theme.LinkFg = color(trueColor, "#268bd2", "32")
	theme.CodeFg = color(trueColor, "#cb4b16", "166")
	theme.CodeBg = color(trueColor, "#073642", "236")
	theme.CodeBlockBg = color(trueColor, "#062f3a", "236")
	return theme
}

func draculaTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = color(trueColor, "#282a36", "236")
	theme.PanelBorder = color(trueColor, "#6272a4", "61")
	theme.PanelTitle = color(trueColor, "#f8f8f2", "255")
	theme.TextPrimary = color(trueColor, "#f8f8f2", "255")
	theme.TextSecondary = color(trueColor, "#bd93f9", "141")
	theme.Info = color(trueColor, "#8be9fd", "123")
	theme.Success = color(trueColor, "#50fa7b", "84")
	theme.Warning = color(trueColor, "#ffb86c", "215")
	theme.Error = color(trueColor, "#ff5555", "203")
	theme.Accent = color(trueColor, "#ff79c6", "212")
	theme.Focus = color(trueColor, "#8be9fd", "123")
	theme.ModalBg = color(trueColor, "#1f2130", "235")
	theme.StatusBg = color(trueColor, "#282a36", "236")
	theme.StatusText = color(trueColor, "#f8f8f2", "255")
	theme.AssistantFg = color(trueColor, "#50fa7b", "84")
	theme.ReasoningFg = color(trueColor, "#bd93f9", "141")
	theme.ToolFg = color(trueColor, "#8be9fd", "123")
	theme.DiffAddBg = color(trueColor, "#21392a", "22")
	theme.DiffAddStrongBg = color(trueColor, "#2f5f43", "29")
	theme.DiffRemoveBg = color(trueColor, "#4a232d", "52")
	theme.DiffRemoveStrongBg = color(trueColor, "#7d3243", "89")
	theme.ComposerBorder = color(trueColor, "#6272a4", "61")
	theme.ComposerBorderFocus = color(trueColor, "#ff79c6", "212")
	theme.ScrollbarTrack = color(trueColor, "#1f2130", "235")
	theme.ScrollbarThumb = color(trueColor, "#6272a4", "61")
	theme.LinkFg = color(trueColor, "#8be9fd", "123")
	theme.CodeBg = color(trueColor, "#343746", "237")
	theme.CodeBlockBg = color(trueColor, "#21222c", "235")
	return theme
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
		Foreground(t.StatusText).
		Padding(0, StatusInset)
}

func (t Theme) HintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.TextSecondary)
}

func (t Theme) HintRowStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(t.TextSecondary).
		Padding(0, StatusInset)
}

func (t Theme) TextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.TextPrimary)
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
		Foreground(t.CommandText).
		Bold(true).
		Underline(true).
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

// ReasoningStyle renders reasoning/thinking text (dimmed + italic).
func (t Theme) ReasoningStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ReasoningFg).Italic(true)
}

// ToolStyle renders tool call/result prefixes (cyan).
func (t Theme) ToolStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ToolFg)
}

// ToolNameStyle renders tool names (bold + cyan).
func (t Theme) ToolNameStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ToolFg).Bold(true)
}

// UserStyle renders user messages in a subtle chat bubble-like background.
func (t Theme) UserStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.UserFg).Bold(true)
}

// UserPrefixStyle renders the leading "> " marker for user messages.
func (t Theme) UserPrefixStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.UserPrefixFg).Bold(true)
}

// UserMentionStyle renders @path mentions inside user messages.
func (t Theme) UserMentionStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.UserMentionFg).Bold(true)
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

// DiffHunkStyle renders diff hunk headers (@@ ... @@) in blue.
func (t Theme) DiffHunkStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffHunkFg).Bold(true)
}

// DiffLineNoStyle renders diff line numbers.
func (t Theme) DiffLineNoStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffLineNoFg)
}

// DiffGutterStyle renders diff markers/gutters.
func (t Theme) DiffGutterStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffGutterFg)
}

// DiffPanelBorderStyle renders split-view separator lines.
func (t Theme) DiffPanelBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.DiffPanelBorder)
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

// LogBlockStyle renders log/tool output lines with a subtle left border
// to visually separate them from narrative assistant text.
func (t Theme) LogBlockStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(t.TextSecondary).
		PaddingLeft(1)
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
		Foreground(t.InputBarFg).
		Padding(0, 1)
}

func (t Theme) ComposerStyle(focused bool) lipgloss.Style {
	style := lipgloss.NewStyle().
		Foreground(t.InputBarFg).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(t.ComposerBorder)
	if focused {
		return style.BorderForeground(t.ComposerBorderFocus).PaddingLeft(1)
	}
	return style.PaddingLeft(0)
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
		Foreground(t.Warning).
		Bold(true).
		Padding(0, 1)
}

func (t Theme) ScrollbarTrackStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ScrollbarTrack)
}

func (t Theme) ScrollbarThumbStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.ScrollbarThumb).Bold(true)
}

func (t Theme) LinkStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.LinkFg).Underline(true)
}
