// Package render is the terminal text toolkit of kubedrill: width
// resolution, word wrapping that ignores escape sequences, markdown for the
// terminal, the color styles, the check result lines of the command
// output, and Printer, the value that carries a writer, a width and the
// color switch.
package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// DefaultWidth is the column text wraps at when neither COLUMNS nor the
// terminal gives one.
const DefaultWidth = 80

const (
	markdownMinWidth = 40
	markdownMaxWidth = 120
)

// Width is the column markdown and long lines wrap at: COLUMNS when it is a
// positive integer, else the width tty reports, else DefaultWidth, kept
// between 40 and 120. getenv and tty may be nil.
func Width(getenv func(string) string, tty func() int) int {
	n := 0
	if getenv != nil {
		v, err := strconv.Atoi(strings.TrimSpace(getenv("COLUMNS")))
		if err == nil && v > 0 {
			n = v
		}
	}
	if n <= 0 && tty != nil {
		n = tty()
	}
	if n <= 0 {
		return DefaultWidth
	}
	return min(max(n, markdownMinWidth), markdownMaxWidth)
}

// Terminal describes the output stream of one run: Out is where text goes,
// IsTerminal says whether Out is a terminal, Color whether text is styled,
// Getenv reads COLUMNS and Columns asks the terminal for its column count
// (0 when unknown). The width is resolved on every call, so a resized
// terminal is honored.
type Terminal struct {
	Out        io.Writer
	IsTerminal bool
	Color      bool
	Getenv     func(string) string
	Columns    func() int
}

// Width is the column this run wraps text at: Width over COLUMNS and the
// terminal.
func (t Terminal) Width() int {
	return Width(t.Getenv, t.Columns)
}

// Printer is the terminal's printer at the run's width.
func (t Terminal) Printer() Printer {
	return Printer{Out: t.Out, Width: t.Width(), Color: t.Color, IsTerminal: t.IsTerminal}
}

// PrinterAt is the terminal's printer at the width of a pane of cols
// columns (kept between 40 and 120), or at the run's width when cols is not
// positive.
func (t Terminal) PrinterAt(cols int) Printer {
	p := t.Printer()
	if cols > 0 {
		p.Width = Width(nil, func() int { return cols })
	}
	return p
}

// Printer writes learner-facing text to Out, wrapped at Width columns on a
// terminal and styled when Color is set.
type Printer struct {
	Out        io.Writer
	Width      int
	Color      bool
	IsTerminal bool
}

// Printf writes the formatted text to Out.
func (p Printer) Printf(format string, args ...any) {
	fmt.Fprintf(p.Out, format, args...)
}

// Println writes args and a newline to Out.
func (p Printer) Println(args ...any) {
	fmt.Fprintln(p.Out, args...)
}

// Style applies st to text only when color output is enabled.
func (p Printer) Style(st lipgloss.Style, text string) string {
	if !p.Color {
		return text
	}
	return st.Render(text)
}

// WrapLine wraps line at the printer's width on a terminal; continuation
// lines start with cont, so a list or a labeled value keeps a hanging
// indent. A pipe gets the line whole, as markdown keeps its source there.
func (p Printer) WrapLine(line, cont string) string {
	if !p.IsTerminal {
		return line
	}
	return strings.Join(HangingWrap(line, p.Width, cont), "\n")
}

// Markdown renders md for the terminal, wrapped at the printer's width;
// without color it returns the source, wrapped the same way on a terminal
// and unchanged on a pipe so logs keep it. Glamour renders without wrapping
// and the result is wrapped afterwards: fenced code blocks and inline code
// spans never wrap (they overflow), and block quote and list item
// continuation lines keep the "| " prefix and a hanging indent. A hard line
// break (a line ending in a backslash or two spaces) stays a line break,
// which glamour alone would turn into a space.
func (p Printer) Markdown(md string) string {
	if !p.Color {
		md = strings.TrimRight(md, "\n") + "\n"
		if !p.IsTerminal {
			return md
		}
		src, code := protectCode(joinListContinuations(md))
		return strings.ReplaceAll(wrapLines(src, p.Width, code), string(codeSpace), " ")
	}
	src, code := protectCode(markHardBreaks(joinListContinuations(md)))
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
	return strings.ReplaceAll(wrapLines(splitHardBreaks(out), p.Width, code), string(codeSpace), " ")
}

// The styles of the command output and the TUIs.
var (
	StyleBold   = lipgloss.NewStyle().Bold(true)
	StylePass   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	StyleFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	StyleSkip   = lipgloss.NewStyle().Faint(true)
	StyleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	StyleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)
