package render

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderWidth(t *testing.T) {
	tests := []struct {
		columns string
		tty     int
		want    int
	}{
		{"", 0, 80}, {"abc", 0, 80}, {"39", 0, 40}, {"40", 0, 40}, {"100", 0, 100}, {"120", 0, 120}, {"121", 0, 120}, {" 72 ", 0, 72},
		{"", 77, 77}, {"", 30, 40}, {"", 200, 120}, {"90", 77, 90}, {"0", 77, 77},
	}
	for _, tc := range tests {
		got := Width(func(string) string { return tc.columns }, func() int { return tc.tty })
		if got != tc.want {
			t.Errorf("COLUMNS=%q tty %d: got %d, want %d", tc.columns, tc.tty, got, tc.want)
		}
	}
	if got := Width(func(string) string { return "" }, nil); got != 80 {
		t.Errorf("no COLUMNS, no tty: got %d, want 80", got)
	}
}

func TestMarkdownWrapsAt60WithoutBreakingCode(t *testing.T) {
	env := map[string]string{"COLUMNS": "60"}
	a := Terminal{Out: &strings.Builder{}, IsTerminal: true, Color: true, Getenv: func(k string) string { return env[k] }}
	md := "Run `kubectl -n task-hello-abcde get pods -o wide --show-labels` before you change anything in the deployment.\n\n" +
		"```\nkubectl -n task-hello-abcde set image deploy/hello app=nginx:1.27 --dry-run=server -o yaml\n```\n\n" +
		"> A quoted line that is long enough to wrap around the sixty column limit for sure, yes indeed it is.\n\n" +
		"- A list item that is long enough to wrap around the sixty column limit for sure, yes indeed.\n" +
		"- short\n\n" +
		"1. A numbered item that is long enough to wrap around the sixty column limit for sure.\n"
	plain := StripANSI(a.Printer().Markdown(md))
	lines := strings.Split(plain, "\n")
	find := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		t.Fatalf("rendered output lacks %q:\n%s", sub, plain)
		return -1
	}
	mustContain(t, plain, "kubectl -n task-hello-abcde get pods -o wide --show-labels", "kubectl -n task-hello-abcde set image deploy/hello app=nginx:1.27 --dry-run=server -o yaml")
	if strings.ContainsRune(plain, codeSpace) {
		t.Errorf("rendered output keeps the code space placeholder:\n%q", plain)
	}
	q := find("| A quoted line")
	if !strings.HasPrefix(strings.TrimLeft(lines[q+1], " "), "| ") {
		t.Errorf("quote continuation lost the bar: %q", lines[q+1])
	}
	li := find("- A list item")
	marker := strings.Index(lines[li], "- A list")
	if cont := lines[li+1]; len(cont)-len(strings.TrimLeft(cont, " ")) != marker+2 {
		t.Errorf("list continuation %q: want a hanging indent of %d", cont, marker+2)
	}
	num := find("1. A numbered")
	nmarker := strings.Index(lines[num], "1. A")
	if cont := lines[num+1]; len(cont)-len(strings.TrimLeft(cont, " ")) != nmarker+3 {
		t.Errorf("numbered continuation %q: want a hanging indent of %d", cont, nmarker+3)
	}
	for _, l := range lines {
		if strings.Contains(l, "kubectl") {
			continue
		}
		if w := len([]rune(strings.TrimRight(l, " "))); w > 60 {
			t.Errorf("line of %d columns over 60: %q", w, l)
		}
	}
}

func TestMarkdownJoinsListContinuations(t *testing.T) {
	md := "Intro line\nsecond intro line.\n\n- first item line\n  continues here with more words\n  and a third line\n- second item\n  - nested item\n    nested continuation\n\n  not a continuation after a blank line\n\n```\n- code line\n  code continuation\n```\n\n1. ordered item\n   continues too\n"
	got := joinListContinuations(md)
	want := "Intro line\nsecond intro line.\n\n- first item line continues here with more words and a third line\n- second item\n  - nested item nested continuation\n\n  not a continuation after a blank line\n\n```\n- code line\n  code continuation\n```\n\n1. ordered item continues too\n"
	if got != want {
		t.Errorf("joinListContinuations:\n%s\nwant:\n%s", got, want)
	}
	var out strings.Builder
	a := Terminal{Out: &out, IsTerminal: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "60"}[k] }}
	plain := a.Printer().Markdown("- first item line\n  continues here with more words and keeps going past the width\n- second\n")
	mustContain(t, plain, "- first item line continues here with more words and keeps\n  going past the width\n- second\n")
	b := Terminal{Out: &out, IsTerminal: true, Color: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "60"}[k] }}
	colored := StripANSI(b.Printer().Markdown("- first item line\n  continues here with more words and keeps going past the width\n- second\n"))
	mustContain(t, colored, "first item line continues here with more words and keeps")
	mustNotContain(t, colored, "line\n  continues")
	for _, l := range strings.Split(colored, "\n") {
		if lipgloss.Width(l) > 60 {
			t.Errorf("colored line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
}

func TestMarkdownKeepsHardLineBreaks(t *testing.T) {
	tests := []struct {
		name    string
		md      string
		want    []string
		notWant []string
	}{
		{name: "backslash in a block quote", md: "> **From:** Dana (storefront team)\\\n> **Priority:** high\n>\n> We moved it.\n", want: []string{"| From: Dana (storefront team)\n", "| Priority: high\n"}, notWant: []string{"\uE001", "team) Priority"}},
		{name: "two spaces in a paragraph", md: "From: Dana  \nPriority: high\n", want: []string{"From: Dana\n", "Priority: high\n"}, notWant: []string{"Dana Priority"}},
		{name: "soft break stays a space", md: "From: Dana\nPriority: high\n", want: []string{"From: Dana Priority: high"}},
		{name: "escaped backslash is no break", md: "path C:\\\\\nnext\n", want: []string{"next"}, notWant: []string{"\uE001"}},
		{name: "backslash before a blank line", md: "end\\\n\nnext paragraph\n", want: []string{"next paragraph"}, notWant: []string{"\uE001"}},
		{name: "fenced code keeps its backslash", md: "```\nline one \\\nline two\n```\n", want: []string{"line one \\\n"}, notWant: []string{"\uE001"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			term := Terminal{Out: &out, IsTerminal: true, Color: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "80"}[k] }}
			got := StripANSI(term.Printer().Markdown(tc.md))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Markdown(%q):\n%s\nwant it to contain %q", tc.md, got, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("Markdown(%q):\n%s\nwant no %q", tc.md, got, w)
				}
			}
		})
	}
}

func TestPaneWidthWinsOverColumns(t *testing.T) {
	var out strings.Builder
	a := Terminal{Out: &out, IsTerminal: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "120"}[k] }}
	if got := a.Width(); got != 120 {
		t.Fatalf("width before watch: %d", got)
	}
	if got := a.PrinterAt(50).Width; got != 50 {
		t.Errorf("width with a pane: got %d, want 50", got)
	}
	if got := a.PrinterAt(20).Width; got != markdownMinWidth {
		t.Errorf("width with a tiny pane: got %d, want the floor %d", got, markdownMinWidth)
	}
	if got := a.Width(); got != 120 {
		t.Errorf("width after a pane printer: got %d, want 120", got)
	}
	for _, l := range strings.Split(a.PrinterAt(50).WrapLine("  your diagnosis: "+strings.Repeat("cause ", 20), "    "), "\n") {
		if lipgloss.Width(l) > 50 {
			t.Errorf("debrief line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("output contains %q; got:\n%s", u, got)
		}
	}
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q; got:\n%s", w, got)
		}
	}
}

func TestInlineCodeStyleIsNeutral(t *testing.T) {
	c := markdownStyle().Code
	if c.BackgroundColor != nil {
		t.Errorf("inline code background: got %q, want none", *c.BackgroundColor)
	}
	if c.Color == nil || *c.Color != "6" {
		t.Errorf("inline code color: got %v, want the terminal's cyan, 6", c.Color)
	}
}
