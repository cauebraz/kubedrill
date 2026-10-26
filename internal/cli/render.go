package cli

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
)

const (
	markdownWidth    = 80
	markdownMinWidth = 40
	markdownMaxWidth = 120
)

// renderWidth is the column markdown and long lines wrap at: COLUMNS when
// it is a positive integer, else the width tty reports, else markdownWidth,
// kept between markdownMinWidth and markdownMaxWidth.
func renderWidth(getenv func(string) string, tty func() int) int {
	n, err := strconv.Atoi(strings.TrimSpace(getenv("COLUMNS")))
	if err != nil || n <= 0 {
		n = 0
		if tty != nil {
			n = tty()
		}
	}
	if n <= 0 {
		return markdownWidth
	}
	return min(max(n, markdownMinWidth), markdownMaxWidth)
}

// width is the column this run wraps text at: the width the watch program
// last saw when it ran (paneWidth), else renderWidth.
func (a *App) width() int {
	if a.paneWidth > 0 {
		return renderWidth(func(string) string { return "" }, func() int { return a.paneWidth })
	}
	return renderWidth(a.getenv, a.termWidth)
}

// wrapLine wraps line at the run's width on a terminal; continuation lines
// start with cont, so a list or a labeled value keeps a hanging indent. A
// pipe gets the line whole, as markdown keeps its source there.
func (a *App) wrapLine(line, cont string) string {
	if !a.isTerminal {
		return line
	}
	return strings.Join(hangingWrap(line, a.width(), cont), "\n")
}

// hangingWrap breaks line at space runs into lines at most width columns
// wide, measured without escape sequences; the first line keeps its own
// leading spaces and every other line starts with cont. The spacing between
// words that stay on one line is kept; a word wider than the room stays
// whole.
func hangingWrap(line string, width int, cont string) []string {
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

// markdown renders md for the terminal, wrapped at the run's width; without
// color it returns the source, wrapped the same way on a terminal and
// unchanged on a pipe so logs keep it.
// Glamour renders without wrapping and wrapLines wraps the result: fenced
// code blocks and inline code spans never wrap (they overflow), and block
// quote and list item continuation lines keep the "| " prefix and a hanging
// indent.
func (a *App) markdown(md string) string {
	if !a.color {
		md = strings.TrimRight(md, "\n") + "\n"
		if !a.isTerminal {
			return md
		}
		src, code := protectCode(joinListContinuations(md))
		return strings.ReplaceAll(wrapLines(src, a.width(), code), string(codeSpace), " ")
	}
	src, code := protectCode(joinListContinuations(md))
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle()),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return md
	}
	out, err := r.Render(src)
	if err != nil {
		return md
	}
	return strings.ReplaceAll(wrapLines(out, a.width(), code), string(codeSpace), " ")
}

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
		plain := stripANSI(l)
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

// stripANSI removes escape sequences from s.
func stripANSI(s string) string {
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

// statusTag is the four-letter tag printed before each check.
func statusTag(s grader.Status) string {
	switch s {
	case grader.StatusPass:
		return "[pass]"
	case grader.StatusFail:
		return "[fail]"
	case grader.StatusSkipped:
		return "[skip]"
	default:
		return "[err ]"
	}
}

func (a *App) coloredTag(s grader.Status) string {
	tag := statusTag(s)
	switch s {
	case grader.StatusPass:
		return a.style(stylePass, tag)
	case grader.StatusFail:
		return a.style(styleFail, tag)
	case grader.StatusSkipped:
		return a.style(styleSkip, tag)
	default:
		return a.style(styleErr, tag)
	}
}

const (
	detailIndent = "       "
	whyWidth     = 78
)

// spoilerHidden stands for the actual value of a spoiler check (one whose
// value names the root cause) until the episode has a hint or a diagnosis.
const spoilerHidden = "hidden until your first hint or diagnosis"

// resultLines formats one check result: the tag and description, then an
// indented expected/actual line for anything that did not pass and, when
// withWhy is set, the check's why text under a failed or errored check.
// hide replaces the actual value of a spoiler check with spoilerHidden.
func resultLines(r grader.Result, tag string, withWhy, hide bool) []string {
	return resultLinesAt(r, tag, withWhy, hide, whyWidth)
}

// resultLinesAt is resultLines with the why text wrapped at width columns;
// an expected/actual pair wider than width puts actual on its own line.
func resultLinesAt(r grader.Result, tag string, withWhy, hide bool, width int) []string {
	desc := r.Description
	if desc == "" {
		desc = r.ID
	}
	lines := []string{tag + " " + desc}
	if r.Status == grader.StatusPass {
		return lines
	}
	var detail []string
	if r.Expected != "" || r.Actual != "" {
		actual := r.Actual
		if hide && r.Spoiler {
			actual = spoilerHidden
		}
		expected := "expected: " + orDash(r.Expected)
		got := "actual: " + orDash(actual)
		if pair := expected + "  " + got; width > 0 && len(detailIndent)+lipgloss.Width(pair) > width {
			detail = append(detail, expected, got)
		} else {
			detail = append(detail, pair)
		}
	}
	if r.Message != "" {
		detail = append(detail, r.Message)
	}
	for _, d := range detail {
		lines = append(lines, detailIndent+d)
	}
	if withWhy && needsWhy(r) {
		lines = append(lines, whyLines(r.Why, width)...)
	}
	return lines
}

// needsWhy reports whether r is a failed or errored check with a why text.
func needsWhy(r grader.Result) bool {
	return r.Why != "" && (r.Status == grader.StatusFail || r.Status == grader.StatusError)
}

// whyLines wraps why so every line, indent included, fits in width
// columns; continuation lines align with the text after "why: ".
func whyLines(why string, width int) []string {
	const label = "why: "
	text := wrap(strings.TrimSpace(why), width-len(detailIndent)-len(label))
	var out []string
	for i, l := range strings.Split(text, "\n") {
		if i == 0 {
			out = append(out, detailIndent+label+l)
			continue
		}
		out = append(out, detailIndent+strings.Repeat(" ", len(label))+l)
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeGrade prints every result and the summary line; hide keeps the
// actual value of spoiler checks back (see spoilerHidden).
func (a *App) writeGrade(w io.Writer, g grader.Grade, hide bool) {
	for _, r := range g.Results {
		for _, line := range resultLines(r, a.coloredTag(r.Status), true, hide) {
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w, summaryLine(g))
}

func summaryLine(g grader.Grade) string {
	passed, total := g.Summary()
	return fmt.Sprintf("%d of %d checks passed (weighted score %.2f)", passed, total, g.Score)
}

// solvedBanner names the scenario, the time spent, the hints taken and the
// variant, which is safe to reveal once the episode has ended.
func solvedBanner(title string, ep *journal.Episode) string {
	dur := "-"
	hints := 0
	variant := ""
	if ep != nil {
		dur = minutesCell(ep.Duration())
		hints = ep.HintsUsed
		variant = ep.Variant
	}
	line := fmt.Sprintf("Solved: %s in %s with %s", title, dur, plural(hints, "hint"))
	if variant != "" {
		line += " (variant " + variant + ")"
	}
	return line
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// fmtDuration prints 4m03s, 1h02m or 12s.
func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < 0 {
		d = 0
	}
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// table writes rows as aligned columns separated by two spaces.
func table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// conceptsLine lists ids as "a, b".
func conceptsLine(ids []string) string {
	return strings.Join(ids, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
