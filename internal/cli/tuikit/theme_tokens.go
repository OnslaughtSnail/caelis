package tuikit

import (
	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// Semantic Token System
//
// Tokens provide a layer of indirection between visual components and raw
// colors. Every chrome, block-shell, composer, and overlay primitive
// references tokens rather than Theme color fields directly.
//
// The token set is intentionally small: surface (elevation 0/1/2), text
// hierarchy (primary/secondary/muted), semantic signals (accent/focus/
// success/warning/danger), structural edges (border-subtle/strong), and
// purpose-specific surfaces (chrome-bg, card-bg, code-bg, overlay-bg).
//
// Each Theme resolves tokens via ResolveTokens(). Components obtain tokens
// through Theme.Tokens().
// ---------------------------------------------------------------------------

// Tokens holds the resolved semantic design tokens for the current theme.
type Tokens struct {
	// ── Surface elevation ──────────────────────────────────────────
	Surface0 lipgloss.Style // deepest background (app bg)
	Surface1 lipgloss.Style // card / panel background
	Surface2 lipgloss.Style // raised / hover / active background

	// ── Text hierarchy ─────────────────────────────────────────────
	TextPrimary   lipgloss.Style // main body text
	TextSecondary lipgloss.Style // secondary labels, meta
	TextMuted     lipgloss.Style // placeholders, disabled

	// ── Semantic signals ───────────────────────────────────────────
	Accent  lipgloss.Style // brand / interactive accent
	Focus   lipgloss.Style // focused element highlight
	Success lipgloss.Style // success / completed
	Warning lipgloss.Style // warning / caution
	Danger  lipgloss.Style // error / destructive

	// ── Structural edges ───────────────────────────────────────────
	BorderSubtle lipgloss.Style // light separators, rail lines
	BorderStrong lipgloss.Style // focused borders, active panels

	// ── Purpose-specific surfaces ──────────────────────────────────
	ChromeBg  lipgloss.Style // header / footer bar background
	CardBg    lipgloss.Style // card / panel body
	CodeBg    lipgloss.Style // code block / inline code background
	OverlayBg lipgloss.Style // modal / overlay backdrop

	// ── Chrome text ────────────────────────────────────────────────
	ChromeTitle lipgloss.Style // header/footer bold title
	ChromeMeta  lipgloss.Style // header/footer metadata
	ChromeHint  lipgloss.Style // hint row text

	// ── Composer ───────────────────────────────────────────────────
	ComposerBorder      lipgloss.Style // composer frame border
	ComposerBorderFocus lipgloss.Style // composer frame focused border
	ComposerLabel       lipgloss.Style // "compose" label
	ComposerPlaceholder lipgloss.Style // placeholder / ghost text
	ComposerCounter     lipgloss.Style // char / attachment counter

	// ── Block shell ────────────────────────────────────────────────
	BlockRail   lipgloss.Style // timeline indentation rail
	BlockHeader lipgloss.Style // block header title
	BlockMeta   lipgloss.Style // block header metadata / elapsed time
	BlockBadge  lipgloss.Style // inline status badge

	// ── Overlay / modal ────────────────────────────────────────────
	OverlayBorder lipgloss.Style // modal frame border
	OverlayTitle  lipgloss.Style // modal title text

	// ── Scrollbar ──────────────────────────────────────────────────
	ScrollTrack lipgloss.Style
	ScrollThumb lipgloss.Style

	// ── Separator ──────────────────────────────────────────────────
	Separator lipgloss.Style // horizontal rule / divider character
}

// resolveTokens derives Tokens from a fully populated Theme.
func resolveTokens(t Theme) Tokens {
	return Tokens{
		// Surfaces
		Surface0: lipgloss.NewStyle().Background(t.AppBg),
		Surface1: lipgloss.NewStyle().Background(t.ModalBg),
		Surface2: lipgloss.NewStyle().Background(t.StatusBg),

		// Text
		TextPrimary:   lipgloss.NewStyle().Foreground(t.TextPrimary),
		TextSecondary: lipgloss.NewStyle().Foreground(t.TextSecondary),
		TextMuted:     lipgloss.NewStyle().Foreground(t.MutedText),

		// Signals
		Accent:  lipgloss.NewStyle().Foreground(t.Accent),
		Focus:   lipgloss.NewStyle().Foreground(t.Focus),
		Success: lipgloss.NewStyle().Foreground(t.Success),
		Warning: lipgloss.NewStyle().Foreground(t.Warning),
		Danger:  lipgloss.NewStyle().Foreground(t.Error),

		// Edges
		BorderSubtle: lipgloss.NewStyle().Foreground(t.PanelBorder),
		BorderStrong: lipgloss.NewStyle().Foreground(t.Focus),

		// Surfaces
		ChromeBg:  lipgloss.NewStyle().Background(t.StatusBg),
		CardBg:    lipgloss.NewStyle().Background(t.ModalBg),
		CodeBg:    lipgloss.NewStyle().Background(t.CodeBlockBg),
		OverlayBg: lipgloss.NewStyle().Background(t.ModalBg),

		// Chrome text
		ChromeTitle: lipgloss.NewStyle().Foreground(t.PanelTitle).Bold(true),
		ChromeMeta:  lipgloss.NewStyle().Foreground(t.SecondaryText),
		ChromeHint:  lipgloss.NewStyle().Foreground(t.TextSecondary),

		// Composer
		ComposerBorder:      lipgloss.NewStyle().Foreground(t.ComposerBorder),
		ComposerBorderFocus: lipgloss.NewStyle().Foreground(t.ComposerBorderFocus),
		ComposerLabel:       lipgloss.NewStyle().Foreground(t.SecondaryText).Bold(true),
		ComposerPlaceholder: lipgloss.NewStyle().Foreground(t.MutedText).Italic(true),
		ComposerCounter:     lipgloss.NewStyle().Foreground(t.MutedText),

		// Block shell
		BlockRail:   lipgloss.NewStyle().Foreground(t.TranscriptRail),
		BlockHeader: lipgloss.NewStyle().Foreground(t.PanelTitle).Bold(true),
		BlockMeta:   lipgloss.NewStyle().Foreground(t.MutedText),
		BlockBadge:  lipgloss.NewStyle().Foreground(t.SecondaryText).Bold(true),

		// Overlay
		OverlayBorder: lipgloss.NewStyle().Foreground(t.Focus),
		OverlayTitle:  lipgloss.NewStyle().Foreground(t.PanelTitle).Bold(true),

		// Scrollbar
		ScrollTrack: lipgloss.NewStyle().Foreground(t.ScrollbarTrack),
		ScrollThumb: lipgloss.NewStyle().Foreground(t.ScrollbarThumb),

		// Separator
		Separator: lipgloss.NewStyle().Foreground(t.PanelBorder),
	}
}
