package tuikit

import (
	"strings"
	"unicode/utf8"
)

// SanitizeLogText strips ANSI escape sequences and control characters from
// text destined for the TUI viewport. Newlines are preserved; tabs are
// expanded to 4 spaces; all other control bytes and DEL are dropped.
func SanitizeLogText(input string) string {
	if input == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(input))
	for i := 0; i < len(input); {
		b := input[i]

		// ESC — skip the entire ANSI sequence.
		if b == 0x1b {
			i = skipANSISequence(input, i)
			continue
		}

		// Control characters (except newline, carriage return, and tab).
		if b < 0x20 {
			if b == '\r' {
				// Convert \r\n to single \n; convert standalone \r to \n.
				if i+1 < len(input) && input[i+1] == '\n' {
					i++ // skip \r, let the next iteration handle \n
				} else {
					out.WriteByte('\n')
				}
				i++
				continue
			}
			if b == '\n' {
				out.WriteByte('\n')
			}
			if b == '\t' {
				out.WriteString("    ")
			}
			i++
			continue
		}

		// DEL
		if b == 0x7f {
			i++
			continue
		}

		// Regular rune.
		r, size := utf8.DecodeRuneInString(input[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte — skip.
			i++
			continue
		}
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

// skipANSISequence advances past one ANSI escape sequence starting at
// input[start] (which must be 0x1b).
func skipANSISequence(input string, start int) int {
	if start+1 >= len(input) {
		return start + 1
	}
	switch input[start+1] {
	case '[':
		// CSI: ESC [ ... final-byte (0x40–0x7e)
		i := start + 2
		for i < len(input) {
			b := input[i]
			if b >= 0x40 && b <= 0x7e {
				return i + 1
			}
			i++
		}
		return len(input)
	case ']':
		// OSC: ESC ] ... BEL(0x07) or ST(ESC \)
		i := start + 2
		for i < len(input) {
			if input[i] == 0x07 {
				return i + 1
			}
			if input[i] == 0x1b && i+1 < len(input) && input[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return len(input)
	default:
		// Two-byte escape (e.g., ESC M).
		if start+2 <= len(input) {
			return start + 2
		}
		return len(input)
	}
}
