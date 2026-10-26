package render

import (
	"regexp"
	"strings"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// asciiStyle is the glamour dark style with every glyph replaced by ASCII so
// the rendered text pastes back into a terminal unchanged: "| " quotes, "- "
// bullets, "# " and "## " headings, and inline code without the non-breaking
// space padding. Colors are kept.
func asciiStyle() ansi.StyleConfig {
	st := styles.DarkStyleConfig
	st.BlockQuote.IndentToken = stringPtr("| ")
	st.Item.BlockPrefix = "- "
	st.Task.Ticked = "[x] "
	st.Task.Unticked = "[ ] "
	st.ImageText.Format = "Image: {{.text}} ->"
	st.DefinitionDescription.BlockPrefix = "\n-> "
	st.H1.Prefix = "# "
	st.H1.Suffix = ""
	st.H1.BackgroundColor = nil
	st.H2.Prefix = "## "
	st.Code.Prefix = ""
	st.Code.Suffix = ""
	st.Code.BlockPrefix = ""
	st.Code.BlockSuffix = ""
	st.CodeBlock.BlockPrefix = ""
	st.CodeBlock.BlockSuffix = ""
	return st
}

func stringPtr(s string) *string { return &s }

// markdownStyle is the fixed style markdown renders with. The CLI never
// queries the terminal for its background color, so nothing typed ahead is
// consumed by a reply; KUBEDRILL_NO_TERM_QUERY=1, which the workstation
// sets, keeps it that way should a query ever be added.
func markdownStyle() ansi.StyleConfig {
	return asciiStyle()
}

// codeSpace stands for a space inside an inline code span while the
// rendered text is wrapped, so a span never breaks across lines.
const codeSpace = '\uE000'

// listMarker matches the start of a list item: a bullet or an ordered
// marker after optional indentation.
var listMarker = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)

// joinListContinuations joins the soft line breaks inside list items: an
// indented line that follows a list item (or its continuation) without a
// blank line between, and is not itself a list item, a heading or a fence,
// is appended to the previous line after one space, so the item reflows as
// one paragraph when wrapped. Fenced code blocks are left alone.
func joinListContinuations(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	fence := ""
	inItem := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			out = append(out, l)
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			inItem = false
			out = append(out, l)
			continue
		}
		switch {
		case t == "":
			inItem = false
		case listMarker.MatchString(l):
			inItem = true
		case inItem && l != t && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "|"):
			out[len(out)-1] += " " + t
			continue
		default:
			inItem = false
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// protectCode replaces the spaces of inline code spans outside fenced
// blocks with codeSpace and returns the trimmed lines of the fenced blocks,
// which wrapLines leaves alone.
func protectCode(md string) (string, map[string]bool) {
	code := map[string]bool{}
	lines := strings.Split(md, "\n")
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
				continue
			}
			if t != "" {
				code[t] = true
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		lines[i] = protectSpans(l)
	}
	return strings.Join(lines, "\n"), code
}

// protectSpans replaces the spaces inside every backtick span of line.
func protectSpans(line string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			b.WriteString(line)
			return b.String()
		}
		n := start
		for n < len(line) && line[n] == '`' {
			n++
		}
		ticks := line[start:n]
		end := strings.Index(line[n:], ticks)
		if end < 0 {
			b.WriteString(line)
			return b.String()
		}
		b.WriteString(line[:n])
		b.WriteString(strings.ReplaceAll(line[n:n+end], " ", string(codeSpace)))
		b.WriteString(ticks)
		line = line[n+end+len(ticks):]
	}
}

// linePrefix matches the structure glamour puts before the text of a
// rendered line: the margin, block quote bars, then a list marker.
var linePrefix = regexp.MustCompile(`^( *)((?:\| )*)((?:- |\d+\. )?(?:\[[x ]\] )?)`)

// wrapLines wraps every rendered line wider than width at spaces. Lines of
// fenced code blocks (code) are kept whole; a continuation line repeats the
// margin and quote bars of its line and indents past its list marker.
func wrapLines(rendered string, width int, code map[string]bool) string {
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		plain := StripANSI(l)
		if width <= 0 || lipgloss.Width(l) <= width || code[strings.TrimSpace(plain)] {
			out = append(out, l)
			continue
		}
		m := linePrefix.FindStringSubmatch(plain)
		head, body := splitVisible(l, len([]rune(m[0])))
		cont := m[1] + m[2] + strings.Repeat(" ", len(m[3]))
		wrapped := wrapWords(body, width-len([]rune(m[0])))
		for i, w := range wrapped {
			if i == 0 {
				out = append(out, head+w)
				continue
			}
			out = append(out, cont+w)
		}
	}
	return strings.Join(out, "\n")
}
