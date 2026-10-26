package render

import (
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/grader"
)

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{12 * time.Second, "12s"},
		{4*time.Minute + 3*time.Second, "4m03s"},
		{time.Hour + 2*time.Minute, "1h02m"},
		{-time.Second, "0s"},
	}
	for _, tc := range tests {
		if got := Duration(tc.d); got != tc.want {
			t.Errorf("Duration(%s): got %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestResultLines(t *testing.T) {
	r := grader.Result{ID: "probe", Description: "", Status: grader.StatusError, Message: "pod did not start"}
	got := ResultLines(r, "[err ]", true, false)
	want := []string{"[err ] probe", "       pod did not start"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResultLinesSplitLongPair(t *testing.T) {
	r := grader.Result{ID: "x", Status: grader.StatusFail, Expected: strings.Repeat("e", 40), Actual: strings.Repeat("a", 40)}
	got := strings.Join(ResultLinesAt(r, "[fail]", false, false, 78), "\n")
	want := "[fail] x\n" + DetailIndent + "expected: " + strings.Repeat("e", 40) + "\n" + DetailIndent + "actual: " + strings.Repeat("a", 40)
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestWhyLines(t *testing.T) {
	r := grader.Result{ID: "x", Status: grader.StatusError, Message: "boom", Why: strings.Repeat("word ", 30)}
	got := ResultLines(r, "[err ]", true, false)
	if len(got) < 4 {
		t.Fatalf("lines: got %v, want tag, message and at least two why lines", got)
	}
	if !strings.HasPrefix(got[2], "       why: word") || !strings.HasPrefix(got[3], "            word") {
		t.Errorf("why lines: %q", got[2:])
	}
	for _, l := range got {
		if len(l) > WhyWidth {
			t.Errorf("line wider than %d: %q", WhyWidth, l)
		}
	}
	skipped := grader.Result{ID: "s", Status: grader.StatusSkipped, Message: "dependency failed", Why: "not shown"}
	if lines := ResultLines(skipped, "[skip]", true, false); strings.Contains(strings.Join(lines, "|"), "why:") {
		t.Errorf("skipped check printed why: %v", lines)
	}
	if lines := ResultLines(r, "[err ]", false, false); strings.Contains(strings.Join(lines, "|"), "why:") {
		t.Errorf("why printed with withWhy=false: %v", lines)
	}
}

func TestStatusTagDefault(t *testing.T) {
	tests := []struct {
		status grader.Status
		want   string
	}{
		{status: grader.StatusPass, want: "[pass]"},
		{status: grader.StatusFail, want: "[fail]"},
		{status: grader.StatusSkipped, want: "[skip]"},
		{status: grader.StatusError, want: "[err ]"},
		{status: grader.Status("weird"), want: "[err ]"},
		{status: grader.Status(""), want: "[err ]"},
	}
	for _, tc := range tests {
		if got := StatusTag(tc.status); got != tc.want {
			t.Errorf("StatusTag(%q): got %q, want %q", tc.status, got, tc.want)
		}
		if len(tc.want) != 6 {
			t.Errorf("tag %q: got %d columns, want 6", tc.want, len(tc.want))
		}
	}
}

func TestPluralOne(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: 0, want: "0 hints"},
		{n: 1, want: "1 hint"},
		{n: 2, want: "2 hints"},
	}
	for _, tc := range tests {
		if got := Plural(tc.n, "hint"); got != tc.want {
			t.Errorf("Plural(%d, hint): got %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestCheckWhyWidthExact(t *testing.T) {
	first := strings.Repeat("aaaaaaaaaa ", 5) + "bbbbbbbbbbb"
	if len(first) != WhyWidth-len(DetailIndent)-len("why: ") {
		t.Fatalf("test setup: first line text is %d columns, want %d", len(first), WhyWidth-len(DetailIndent)-len("why: "))
	}
	r := grader.Result{ID: "x", Status: grader.StatusFail, Expected: "2", Actual: "0", Why: first + " cc dd"}
	got := ResultLines(r, "[fail]", true, false)
	want := []string{
		"[fail] x",
		"       expected: 2  actual: 0",
		"       why: " + first,
		"            cc dd",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ResultLines:\n got %q\nwant %q", got, want)
	}
	if len(got[2]) != WhyWidth {
		t.Errorf("first why line: got %d columns, want exactly %d", len(got[2]), WhyWidth)
	}
	one := strings.Repeat("aaaaaaaaaa ", 5) + "bbbbbbbbbbbb"
	lines := WhyLines(one, WhyWidth)
	if len(lines) != 2 || lines[0] != DetailIndent+"why: "+strings.TrimSpace(strings.Repeat("aaaaaaaaaa ", 5)) || lines[1] != DetailIndent+"     bbbbbbbbbbbb" {
		t.Errorf("WhyLines one over the width: got %q, want the last word on a second line", lines)
	}
}

func TestMinutesCellFloorsAtOneTenth(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "-"},
		{3 * time.Second, "0.1 min"},
		{30 * time.Second, "0.5 min"},
		{4*time.Minute + 6*time.Second, "4.1 min"},
		{90 * time.Minute, "90.0 min"},
	} {
		if got := Minutes(tc.d); got != tc.want {
			t.Errorf("Minutes(%s): got %q, want %q", tc.d, got, tc.want)
		}
	}
}
