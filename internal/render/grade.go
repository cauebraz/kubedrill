package render

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
)

// StatusTag is the four-letter tag printed before each check.
func StatusTag(s grader.Status) string {
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

// DetailIndent indents the lines under a check result.
const DetailIndent = "       "

// WhyWidth is the column the why text wraps at when no pane width is known.
const WhyWidth = 78

// SpoilerHidden stands for the actual value of a spoiler check (one whose
// value names the root cause) until the episode has a hint or a diagnosis.
const SpoilerHidden = "hidden until your first hint or diagnosis"

// ResultLines formats one check result: the tag and description, then an
// indented expected/actual line for anything that did not pass and, when
// withWhy is set, the check's why text under a failed or errored check.
// hide replaces the actual value of a spoiler check with SpoilerHidden.
func ResultLines(r grader.Result, tag string, withWhy, hide bool) []string {
	return ResultLinesAt(r, tag, withWhy, hide, WhyWidth)
}

// ResultLinesAt is ResultLines with the why text wrapped at width columns;
// an expected/actual pair wider than width puts actual on its own line.
func ResultLinesAt(r grader.Result, tag string, withWhy, hide bool, width int) []string {
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
			actual = SpoilerHidden
		}
		expected := "expected: " + OrDash(r.Expected)
		got := "actual: " + OrDash(actual)
		if pair := expected + "  " + got; width > 0 && len(DetailIndent)+lipgloss.Width(pair) > width {
			detail = append(detail, expected, got)
		} else {
			detail = append(detail, pair)
		}
	}
	if r.Message != "" {
		detail = append(detail, r.Message)
	}
	for _, d := range detail {
		lines = append(lines, DetailIndent+d)
	}
	if withWhy && NeedsWhy(r) {
		lines = append(lines, WhyLines(r.Why, width)...)
	}
	return lines
}

// NeedsWhy reports whether r is a failed or errored check with a why text.
func NeedsWhy(r grader.Result) bool {
	return r.Why != "" && (r.Status == grader.StatusFail || r.Status == grader.StatusError)
}

// WhyLines wraps why so every line, indent included, fits in width
// columns; continuation lines align with the text after "why: ".
func WhyLines(why string, width int) []string {
	const label = "why: "
	text := Wrap(strings.TrimSpace(why), width-len(DetailIndent)-len(label))
	var out []string
	for i, l := range strings.Split(text, "\n") {
		if i == 0 {
			out = append(out, DetailIndent+label+l)
			continue
		}
		out = append(out, DetailIndent+strings.Repeat(" ", len(label))+l)
	}
	return out
}

// OrDash is s, or "-" when s is empty.
func OrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// SummaryLine is the "N of M checks passed" line under a grade.
func SummaryLine(g grader.Grade) string {
	passed, total := g.Summary()
	return fmt.Sprintf("%d of %d checks passed (weighted score %.2f)", passed, total, g.Score)
}

// SolvedBanner names the scenario, the time spent, the hints taken and the
// variant, which is safe to reveal once the episode has ended.
func SolvedBanner(title string, ep *journal.Episode) string {
	dur := "-"
	hints := 0
	variant := ""
	if ep != nil {
		dur = Minutes(ep.Duration())
		hints = ep.HintsUsed
		variant = ep.Variant
	}
	line := fmt.Sprintf("Solved: %s in %s with %s", title, dur, Plural(hints, "hint"))
	if variant != "" {
		line += " (variant " + variant + ")"
	}
	return line
}

// Plural is n followed by noun, with an s unless n is 1.
func Plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Duration prints 4m03s, 1h02m or 12s.
func Duration(d time.Duration) string {
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

// Minutes prints a duration in minutes with one decimal, "1.2 min", or
// "-"; a duration under 0.1 min prints "0.1 min". list, journal, stats, the
// debrief and the ticket footer share the unit.
func Minutes(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f min", max(d.Minutes(), 0.1))
}

// Tag is StatusTag of s, colored by status when the printer has color.
func (p Printer) Tag(s grader.Status) string {
	tag := StatusTag(s)
	switch s {
	case grader.StatusPass:
		return p.Style(StylePass, tag)
	case grader.StatusFail:
		return p.Style(StyleFail, tag)
	case grader.StatusSkipped:
		return p.Style(StyleSkip, tag)
	default:
		return p.Style(StyleErr, tag)
	}
}
