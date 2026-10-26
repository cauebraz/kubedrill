package render

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// HangingWrap breaks line at space runs into lines at most width columns
// wide, measured without escape sequences; the first line keeps its own
// leading spaces and every other line starts with cont. The spacing between
// words that stay on one line is kept; a word wider than the room stays
// whole.
func HangingWrap(line string, width int, cont string) []string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return []string{line}
	}
	trimmed := strings.TrimLeft(line, " ")
	lead := line[:len(line)-len(trimmed)]
	words := strings.Split(trimmed, " ")
	var out []string
	cur, w := "", 0
	limit := max(width-len(lead), 1)
	flush := func() {
		if len(out) == 0 {
			out = append(out, lead+cur)
		} else {
			out = append(out, cont+cur)
		}
		cur, w = "", 0
		limit = max(width-len(cont), 1)
	}
	sep := ""
	for _, word := range words {
		if word == "" {
			sep += " "
			continue
		}
		ww := lipgloss.Width(word)
		switch {
		case w == 0:
			cur, w = word, ww
		case w+len(sep)+1+ww > limit:
			flush()
			cur, w = word, ww
		default:
			cur += sep + " " + word
			w += len(sep) + 1 + ww
		}
		sep = ""
	}
	flush()
	return out
}

// wrapWords breaks s at spaces into lines at most width columns wide,
// measured without escape sequences; a word wider than width stays whole.
func wrapWords(s string, width int) []string {
	words := strings.Split(s, " ")
	var lines []string
	line, w := "", 0
	for _, word := range words {
		ww := lipgloss.Width(word)
		if ww == 0 {
			line += word
			continue
		}
		switch {
		case w == 0:
			line += word
			w = ww
		case w+1+ww > width:
			lines = append(lines, line)
			line, w = word, ww
		default:
			line += " " + word
			w += 1 + ww
		}
	}
	return append(lines, strings.TrimRight(line, " "))
}

// splitVisible cuts s after n visible runes; escape sequences before the
// cut stay in head.
func splitVisible(s string, n int) (head, tail string) {
	i, seen := 0, 0
	for i < len(s) && seen < n {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		seen++
	}
	return s[:i], s[i:]
}

// skipEscape returns the index after the escape sequence starting at i: a
// CSI sequence (ESC [ ... final byte) or an OSC sequence ended by BEL or
// ESC \.
func skipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		return min(j+1, len(s))
	case ']':
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(s)
	}
	return i + 2
}

// StripANSI removes escape sequences from s.
func StripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Truncate cuts s to width visible columns with a trailing "..." when it
// is wider. Escape sequences do not count and a rune is never split; a cut
// through styled text ends with a reset.
func Truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	keep, tail := width-3, "..."
	if width <= 3 {
		keep, tail = width, ""
	}
	head, _ := splitVisible(s, keep)
	if strings.Contains(head, "\x1b[") {
		head += "\x1b[0m"
	}
	return head + tail
}

// Wrap breaks s at spaces so no line exceeds width visible columns.
func Wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapWords(strings.Join(strings.Fields(para), " "), width)...)
	}
	return strings.Join(out, "\n")
}
