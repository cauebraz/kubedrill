package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func TestTruncateCountsVisibleColumns(t *testing.T) {
	s := "\x1b[1mh\u00e9llo w\u00f6rld\x1b[0m"
	got := Truncate(s, 8)
	if !utf8.ValidString(got) || lipgloss.Width(got) != 8 || !strings.HasSuffix(got, "\x1b[0m...") || !strings.HasPrefix(StripANSI(got), "h\u00e9llo") {
		t.Errorf("truncate: got %q (width %d)", got, lipgloss.Width(got))
	}
	if got := Truncate(s, 20); got != s {
		t.Errorf("fitting text changed: %q", got)
	}
	if got := Truncate("abcdef", 3); got != "abc" {
		t.Errorf("width 3: got %q", got)
	}
}

func TestHangingWrapMeasuresVisibleColumnsAndKeepsLead(t *testing.T) {
	styled := "\x1b[1mbold words\x1b[0m here and some more text"
	got := HangingWrap(styled, 20, "  ")
	if len(got) != 2 || got[0] != "\x1b[1mbold words\x1b[0m here and" || got[1] != "  some more text" {
		t.Errorf("styled wrap: %q", got)
	}
	got = HangingWrap("  abc def ghi", 9, "    ")
	if len(got) != 2 || got[0] != "  abc def" || got[1] != "    ghi" {
		t.Errorf("leading spaces: %q", got)
	}
	if got := HangingWrap("short", 20, "  "); len(got) != 1 || got[0] != "short" {
		t.Errorf("fits: %q", got)
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("one two three four", 9)
	want := "one two\nthree\nfour"
	if got != want {
		t.Errorf("wrap: got %q, want %q", got, want)
	}
}
