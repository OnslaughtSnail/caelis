package tuikit

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

type Theme struct {
	AppBg          color.Color
	PanelBorder    color.Color
	PanelTitle     color.Color
	TextPrimary    color.Color
	TextSecondary  color.Color
	Info           color.Color
	Success        color.Color
	Warning        color.Color
	Error          color.Color
	Accent         color.Color
	Focus          color.Color
	ModalBg        color.Color
	StatusBg       color.Color
	StatusText     color.Color
	CommandBg      color.Color
	CommandActive  color.Color
	CommandText    color.Color
	CommandSubText color.Color

	// Line-level semantic colors (conversation / tool / diff).
	AssistantFg        color.Color
	ReasoningFg        color.Color
	UserFg             color.Color
	UserBg             color.Color
	UserPrefixFg       color.Color
	UserMentionFg      color.Color
	ToolFg             color.Color
	DiffAddFg          color.Color
	DiffRemoveFg       color.Color
	DiffHeaderFg       color.Color
	DiffHunkFg         color.Color
	DiffAddBg          color.Color
	DiffAddStrongBg    color.Color
	DiffRemoveBg       color.Color
	DiffRemoveStrongBg color.Color
	DiffLineNoFg       color.Color
	DiffGutterFg       color.Color
	DiffPanelBorder    color.Color
	SectionFg          color.Color
	KeyLabelFg         color.Color
	NoteFg             color.Color

	// Input area
	PromptFg     color.Color
	CursorFg     color.Color
	ScrollHintFg color.Color

	// Inline layout
	InputBarBg          color.Color
	InputBarFg          color.Color
	ToolOutputBg        color.Color
	HelpHintFg          color.Color
	SpinnerFg           color.Color
	SeparatorFg         color.Color
	RoleBorderFg        color.Color // left border for role sections
	NewMsgBg            color.Color // "new messages" indicator
	ComposerBorder      color.Color
	ComposerBorderFocus color.Color
	ScrollbarTrack      color.Color
	ScrollbarThumb      color.Color
	LinkFg              color.Color
	CodeFg              color.Color
	CodeBg              color.Color
	CodeBlockFg         color.Color
	CodeBlockBg         color.Color
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

func themeColor(trueColor bool, rich string, fallback string) color.Color {
	if trueColor || fallback == "" {
		return lipgloss.Color(rich)
	}
	return lipgloss.Color(fallback)
}

func defaultThemeVariant(trueColor bool) Theme {
	return Theme{
		AppBg:          themeColor(trueColor, "#111315", "233"),
		PanelBorder:    themeColor(trueColor, "#3f4652", "240"),
		PanelTitle:     themeColor(trueColor, "#f3f4f6", "255"),
		TextPrimary:    themeColor(trueColor, "#f5f5f5", "255"),
		TextSecondary:  themeColor(trueColor, "#a1a1aa", "248"),
		Info:           themeColor(trueColor, "#d4d4d8", "252"),
		Success:        themeColor(trueColor, "#56d364", "77"),
		Warning:        themeColor(trueColor, "#f5c451", "221"),
		Error:          themeColor(trueColor, "#ff7b72", "210"),
		Accent:         themeColor(trueColor, "#e5e7eb", "254"),
		Focus:          themeColor(trueColor, "#f3f4f6", "255"),
		ModalBg:        themeColor(trueColor, "#15181d", "234"),
		StatusBg:       themeColor(trueColor, "#111315", "233"),
		StatusText:     themeColor(trueColor, "#d4d4d8", "252"),
		CommandBg:      themeColor(trueColor, "#111315", "233"),
		CommandActive:  themeColor(trueColor, "#111315", "233"),
		CommandText:    themeColor(trueColor, "#f5f5f5", "255"),
		CommandSubText: themeColor(trueColor, "#a1a1aa", "248"),

		AssistantFg:        themeColor(trueColor, "#56d364", "77"),
		ReasoningFg:        themeColor(trueColor, "#8d96a5", "246"),
		UserFg:             themeColor(trueColor, "#f5f5f5", "255"),
		UserBg:             themeColor(trueColor, "#111315", "233"),
		UserPrefixFg:       themeColor(trueColor, "#ffffff", "255"),
		UserMentionFg:      themeColor(trueColor, "#f5f5f5", "255"),
		ToolFg:             themeColor(trueColor, "#e5e7eb", "254"),
		DiffAddFg:          themeColor(trueColor, "#56d364", "77"),
		DiffRemoveFg:       themeColor(trueColor, "#ff7b72", "210"),
		DiffHeaderFg:       themeColor(trueColor, "#8d96a5", "246"),
		DiffHunkFg:         themeColor(trueColor, "#d4d4d8", "252"),
		DiffAddBg:          themeColor(trueColor, "#1d3328", "22"),
		DiffAddStrongBg:    themeColor(trueColor, "#285f3a", "29"),
		DiffRemoveBg:       themeColor(trueColor, "#3a2329", "52"),
		DiffRemoveStrongBg: themeColor(trueColor, "#6e2b34", "88"),
		DiffLineNoFg:       themeColor(trueColor, "#758195", "245"),
		DiffGutterFg:       themeColor(trueColor, "#8d96a5", "246"),
		DiffPanelBorder:    themeColor(trueColor, "#3f4652", "240"),
		SectionFg:          themeColor(trueColor, "#f5f5f5", "255"),
		KeyLabelFg:         themeColor(trueColor, "#e5e7eb", "254"),
		NoteFg:             themeColor(trueColor, "#a1a1aa", "248"),
		PromptFg:           themeColor(trueColor, "#f5f5f5", "255"),
		CursorFg:           themeColor(trueColor, "#ffffff", "255"),
		ScrollHintFg:       themeColor(trueColor, "#f5c451", "221"),

		InputBarBg:          themeColor(trueColor, "#111315", "233"),
		InputBarFg:          themeColor(trueColor, "#f5f5f5", "255"),
		ToolOutputBg:        themeColor(trueColor, "#111315", "233"),
		HelpHintFg:          themeColor(trueColor, "#a1a1aa", "248"),
		SpinnerFg:           themeColor(trueColor, "#e5e7eb", "254"),
		SeparatorFg:         themeColor(trueColor, "#3f4652", "240"),
		RoleBorderFg:        themeColor(trueColor, "#3f4652", "240"),
		NewMsgBg:            themeColor(trueColor, "#111315", "233"),
		ComposerBorder:      themeColor(trueColor, "#3f4652", "240"),
		ComposerBorderFocus: themeColor(trueColor, "#f3f4f6", "255"),
		ScrollbarTrack:      themeColor(trueColor, "#1d2128", "234"),
		ScrollbarThumb:      themeColor(trueColor, "#8d96a5", "246"),
		LinkFg:              themeColor(trueColor, "#8ab4f8", "117"),
		CodeFg:              themeColor(trueColor, "#f5c451", "221"),
		CodeBg:              themeColor(trueColor, "#1b1f27", "234"),
		CodeBlockFg:         themeColor(trueColor, "#d4d4d8", "252"),
		CodeBlockBg:         themeColor(trueColor, "#171a20", "234"),
	}
}

func nordTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = themeColor(trueColor, "#2e3440", "236")
	theme.PanelBorder = themeColor(trueColor, "#4c566a", "240")
	theme.PanelTitle = themeColor(trueColor, "#eceff4", "255")
	theme.TextPrimary = themeColor(trueColor, "#eceff4", "255")
	theme.TextSecondary = themeColor(trueColor, "#d8dee9", "252")
	theme.Info = themeColor(trueColor, "#d8dee9", "252")
	theme.Success = themeColor(trueColor, "#a3be8c", "108")
	theme.Warning = themeColor(trueColor, "#ebcb8b", "223")
	theme.Error = themeColor(trueColor, "#bf616a", "131")
	theme.Accent = themeColor(trueColor, "#88c0d0", "110")
	theme.Focus = themeColor(trueColor, "#81a1c1", "110")
	theme.ModalBg = themeColor(trueColor, "#3b4252", "237")
	theme.StatusBg = themeColor(trueColor, "#2e3440", "236")
	theme.StatusText = themeColor(trueColor, "#d8dee9", "252")
	theme.AssistantFg = themeColor(trueColor, "#a3be8c", "108")
	theme.ReasoningFg = themeColor(trueColor, "#81a1c1", "110")
	theme.ToolFg = themeColor(trueColor, "#88c0d0", "110")
	theme.DiffAddBg = themeColor(trueColor, "#314236", "23")
	theme.DiffAddStrongBg = themeColor(trueColor, "#45604e", "59")
	theme.DiffRemoveBg = themeColor(trueColor, "#4a3037", "52")
	theme.DiffRemoveStrongBg = themeColor(trueColor, "#6a3f4a", "95")
	theme.ComposerBorder = themeColor(trueColor, "#4c566a", "240")
	theme.ComposerBorderFocus = themeColor(trueColor, "#81a1c1", "110")
	theme.ScrollbarTrack = themeColor(trueColor, "#3b4252", "237")
	theme.ScrollbarThumb = themeColor(trueColor, "#81a1c1", "110")
	theme.LinkFg = themeColor(trueColor, "#88c0d0", "110")
	theme.CodeBg = themeColor(trueColor, "#3b4252", "237")
	theme.CodeBlockBg = themeColor(trueColor, "#2b303b", "236")
	return theme
}

func solarizedTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = themeColor(trueColor, "#002b36", "235")
	theme.PanelBorder = themeColor(trueColor, "#586e75", "242")
	theme.PanelTitle = themeColor(trueColor, "#fdf6e3", "230")
	theme.TextPrimary = themeColor(trueColor, "#eee8d5", "254")
	theme.TextSecondary = themeColor(trueColor, "#93a1a1", "245")
	theme.Info = themeColor(trueColor, "#93a1a1", "245")
	theme.Success = themeColor(trueColor, "#859900", "100")
	theme.Warning = themeColor(trueColor, "#b58900", "136")
	theme.Error = themeColor(trueColor, "#dc322f", "160")
	theme.Accent = themeColor(trueColor, "#2aa198", "36")
	theme.Focus = themeColor(trueColor, "#268bd2", "32")
	theme.ModalBg = themeColor(trueColor, "#073642", "236")
	theme.StatusBg = themeColor(trueColor, "#002b36", "235")
	theme.StatusText = themeColor(trueColor, "#93a1a1", "245")
	theme.AssistantFg = themeColor(trueColor, "#859900", "100")
	theme.ReasoningFg = themeColor(trueColor, "#6c71c4", "61")
	theme.ToolFg = themeColor(trueColor, "#2aa198", "36")
	theme.DiffAddBg = themeColor(trueColor, "#173d1c", "22")
	theme.DiffAddStrongBg = themeColor(trueColor, "#2f5f2f", "29")
	theme.DiffRemoveBg = themeColor(trueColor, "#4a1f1c", "52")
	theme.DiffRemoveStrongBg = themeColor(trueColor, "#7a2d24", "88")
	theme.ComposerBorder = themeColor(trueColor, "#586e75", "242")
	theme.ComposerBorderFocus = themeColor(trueColor, "#268bd2", "32")
	theme.ScrollbarTrack = themeColor(trueColor, "#073642", "236")
	theme.ScrollbarThumb = themeColor(trueColor, "#586e75", "242")
	theme.LinkFg = themeColor(trueColor, "#268bd2", "32")
	theme.CodeFg = themeColor(trueColor, "#cb4b16", "166")
	theme.CodeBg = themeColor(trueColor, "#073642", "236")
	theme.CodeBlockBg = themeColor(trueColor, "#062f3a", "236")
	return theme
}

func draculaTheme(trueColor bool) Theme {
	theme := defaultThemeVariant(trueColor)
	theme.AppBg = themeColor(trueColor, "#282a36", "236")
	theme.PanelBorder = themeColor(trueColor, "#6272a4", "61")
	theme.PanelTitle = themeColor(trueColor, "#f8f8f2", "255")
	theme.TextPrimary = themeColor(trueColor, "#f8f8f2", "255")
	theme.TextSecondary = themeColor(trueColor, "#bd93f9", "141")
	theme.Info = themeColor(trueColor, "#8be9fd", "123")
	theme.Success = themeColor(trueColor, "#50fa7b", "84")
	theme.Warning = themeColor(trueColor, "#ffb86c", "215")
	theme.Error = themeColor(trueColor, "#ff5555", "203")
	theme.Accent = themeColor(trueColor, "#ff79c6", "212")
	theme.Focus = themeColor(trueColor, "#8be9fd", "123")
	theme.ModalBg = themeColor(trueColor, "#1f2130", "235")
	theme.StatusBg = themeColor(trueColor, "#282a36", "236")
	theme.StatusText = themeColor(trueColor, "#f8f8f2", "255")
	theme.AssistantFg = themeColor(trueColor, "#50fa7b", "84")
	theme.ReasoningFg = themeColor(trueColor, "#bd93f9", "141")
	theme.ToolFg = themeColor(trueColor, "#8be9fd", "123")
	theme.DiffAddBg = themeColor(trueColor, "#21392a", "22")
	theme.DiffAddStrongBg = themeColor(trueColor, "#2f5f43", "29")
	theme.DiffRemoveBg = themeColor(trueColor, "#4a232d", "52")
	theme.DiffRemoveStrongBg = themeColor(trueColor, "#7d3243", "89")
	theme.ComposerBorder = themeColor(trueColor, "#6272a4", "61")
	theme.ComposerBorderFocus = themeColor(trueColor, "#ff79c6", "212")
	theme.ScrollbarTrack = themeColor(trueColor, "#1f2130", "235")
	theme.ScrollbarThumb = themeColor(trueColor, "#6272a4", "61")
	theme.LinkFg = themeColor(trueColor, "#8be9fd", "123")
	theme.CodeBg = themeColor(trueColor, "#343746", "237")
	theme.CodeBlockBg = themeColor(trueColor, "#21222c", "235")
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
