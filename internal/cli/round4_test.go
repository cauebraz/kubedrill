package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
)

func TestAuditPolicyDriftNoticeOncePerJournal(t *testing.T) {
	f := newFixture(t)
	f.cluster.stale = true
	_, _, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if got := strings.Count(errOut, auditPolicyOld+"\n"); got != 1 {
		t.Errorf("first start: notice printed %d times:\n%s", got, errOut)
	}
	mustNotContain(t, errOut, telemetryOff)
	if _, err := os.Stat(filepath.Join(f.journal.dir, auditPolicyNoticeFile)); err != nil {
		t.Errorf("marker: %v", err)
	}
	_, _, errOut = f.run(t, "", "start", "001", "--seed", "2", "--yes")
	mustNotContain(t, errOut, auditPolicyOld)

	g := newFixture(t)
	g.cluster.stale = true
	g.cluster.staleErr = errors.New("docker unreachable")
	_, _, errOut = g.run(t, "", "start", "001", "--seed", "1")
	mustNotContain(t, errOut, auditPolicyOld)

	h := newFixture(t)
	h.cluster.audit = false
	h.cluster.stale = true
	_, _, errOut = h.run(t, "", "start", "001", "--seed", "1")
	mustContain(t, errOut, telemetryOff+"\n"+auditPolicyOld+"\n")
}

func TestUpReportsAuditPolicyDriftEveryTime(t *testing.T) {
	f := newFixture(t)
	f.cluster.stale = true
	for i := 0; i < 2; i++ {
		code, out, errOut := f.run(t, "", "up")
		if code != 0 {
			t.Fatalf("up exit %d: %s", code, errOut)
		}
		mustContain(t, out, "cluster kubedrill: present (profile base)")
		if got := strings.Count(errOut, auditPolicyOld+"\n"); got != 1 {
			t.Errorf("up %d: notice printed %d times:\n%s", i+1, got, errOut)
		}
	}
	f.cluster.stale = false
	_, _, errOut := f.run(t, "", "up")
	mustNotContain(t, errOut, auditPolicyOld)
	f.cluster.exists = false
	f.cluster.stale = true
	_, out, errOut := f.run(t, "", "up")
	mustContain(t, out, "cluster kubedrill is ready (profile base)")
	mustNotContain(t, errOut, auditPolicyOld)
	if f.cluster.staleAsks != 3 {
		t.Errorf("stale asked %d times, want 3 (never on a creation)", f.cluster.staleAsks)
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
		if got := minutesCell(tc.d); got != tc.want {
			t.Errorf("minutesCell(%s): got %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestJournalDurationInMinutes(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	short := solvedEpisode("s1", "001-hello-down", 1, start)
	end := start.Add(3 * time.Second)
	short.EndedAt = &end
	long := solvedEpisode("s2", "001-hello-down", 2, start.Add(time.Hour))
	end2 := long.StartedAt.Add(12*time.Minute + 30*time.Second)
	long.EndedAt = &end2
	f.journal.episodes = []journal.Episode{short, long}
	_, out, _ := f.run(t, "", "journal")
	mustContain(t, out, "DURATION", "12.5 min", "0.1 min")
	mustNotContain(t, out, "12m30s", "3s")
}

func TestSolutionAfterSolvedExamAsks(t *testing.T) {
	f := newFixture(t)
	ep := solvedEpisode("x1", "001-hello-down", 1, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	ep.Mode = journal.ModeExam
	f.journal.episodes = []journal.Episode{ep}
	code, out, _ := f.run(t, "n\n", "solution", "001")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "Your latest episode of 001-hello-down was an exam (solved); show the solution? [y/N] ", "kept the solution of 001-hello-down hidden")
	mustNotContain(t, out, "## Symptom", "ended solved")
	_, out, _ = f.run(t, "y\n", "solution", "001")
	mustContain(t, out, "## Symptom")
	failed := ep
	failed.Outcome = journal.OutcomeFailed
	f.journal.episodes = []journal.Episode{failed}
	_, out, _ = f.run(t, "n\n", "solution", "001")
	mustContain(t, out, "Your latest episode of 001-hello-down ended failed; show the solution anyway? [y/N] ")
}

func TestPathModuleLineWraps(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY = true
	f.env = map[string]string{"COLUMNS": "50", "NO_COLOR": "1"}
	f.curr.Levels[0].Modules[0].Title = "Workloads, their controllers and the rollout machinery behind them"
	code, out, errOut := f.run(t, "", "path")
	if code != 0 {
		t.Fatalf("path exit %d: %s", code, errOut)
	}
	lines := strings.Split(out, "\n")
	found := false
	for i, l := range lines {
		if lipgloss.Width(l) > 50 {
			t.Errorf("line of %d columns: %q", lipgloss.Width(l), l)
		}
		if strings.HasPrefix(l, "    workloads-basics Workloads, their") {
			found = true
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], pathConceptsIndent) || strings.HasPrefix(lines[i+1], pathConceptsIndent+" ") {
				t.Errorf("module continuation lacks the concepts indent: %q", lines[i+1])
			}
		}
	}
	if !found {
		t.Errorf("module line missing:\n%s", out)
	}
	mustContain(t, strings.Join(strings.Fields(out), " "), "behind them: 2 planned, 1 existing, 0 solved concepts: pods new")
}

func TestListNarrowDropsTypeAndCutsTitle(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY = true
	f.env = map[string]string{"COLUMNS": "70", "NO_COLOR": "1"}
	_, out, _ := f.run(t, "", "list")
	header := strings.SplitN(out, "\n", 2)[0]
	mustNotContain(t, header, "TYPE")
	mustContain(t, header, "ID", "TITLE", "LEVEL", "MIN", "YOURS", "LAST4", "STATUS")
	mustContain(t, out, "Hello app is down", "Service name doe...")
	mustNotContain(t, out, "break-fix", "Service name does not resolve")
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 70 {
			t.Errorf("line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
	f.env["COLUMNS"] = "100"
	_, out, _ = f.run(t, "", "list")
	mustContain(t, strings.SplitN(out, "\n", 2)[0], "TYPE")
	mustContain(t, out, "break-fix", "Service name does not resolve")
	f.stdoutTTY = false
	f.env["COLUMNS"] = "50"
	_, out, _ = f.run(t, "", "list")
	mustContain(t, out, "TYPE", "Service name does not resolve")
}

func TestFitListTableKeepsMinimumTitle(t *testing.T) {
	header := []string{"ID", "TITLE", "LEVEL", "TYPE", "MIN", "YOURS", "LAST4", "STATUS"}
	rows := [][]string{{"001-hello-down", "A title that is rather long for a narrow pane", "L0", "break-fix", "15", "-", "-", "todo"}}
	h, r := fitListTable(header, rows, 40)
	if strings.Join(h, " ") != "ID TITLE LEVEL MIN YOURS LAST4 STATUS" {
		t.Errorf("header: %v", h)
	}
	if got := r[0][1]; got != "A title t..." || len(got) != listTitleMin {
		t.Errorf("title: got %q, want %d columns ending in ...", got, listTitleMin)
	}
	if rows[0][1] == r[0][1] || len(rows[0]) != 8 {
		t.Error("fitListTable changed the input rows")
	}
	h, r = fitListTable(header, rows, 100)
	if len(h) != 8 || r[0][1] != rows[0][1] {
		t.Errorf("at 100 columns: header %v title %q", h, r[0][1])
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
	a := newApp(Options{Stdout: &out, IsTerminal: true, Getenv: func(k string) string { return map[string]string{"NO_COLOR": "1", "COLUMNS": "60"}[k] }})
	plain := a.markdown("- first item line\n  continues here with more words and keeps going past the width\n- second\n")
	mustContain(t, plain, "- first item line continues here with more words and keeps\n  going past the width\n- second\n")
	b := newApp(Options{Stdout: &out, IsTerminal: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "60"}[k] }})
	colored := stripANSI(b.markdown("- first item line\n  continues here with more words and keeps going past the width\n- second\n"))
	mustContain(t, colored, "first item line continues here with more words and keeps")
	mustNotContain(t, colored, "line\n  continues")
	for _, l := range strings.Split(colored, "\n") {
		if lipgloss.Width(l) > 60 {
			t.Errorf("colored line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
}

func TestWatchNarrowPaneWrapsStatusBannerAndNext(t *testing.T) {
	m := newTestModel(&fakeWatcher{observe: failGrade()})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 30})
	next, _ = next.(watchModel).Update(pollMsg{grade: failGrade()})
	m = next.(watchModel)
	view := m.View().Content
	mustContain(t, view, "elapsed 0s   hints 0/2\n  checked 0s ago\n")
	end := m.started.Add(4 * time.Minute)
	m.solved = true
	m.solvedEp = &journal.Episode{StartedAt: m.started, EndedAt: &end, HintsUsed: 1, Variant: "image"}
	m.pick = &nextPick{ref: "002-service-dns", reason: "you have not tried it yet; it is the next one at L1"}
	view = m.View().Content
	mustContain(t, view, "Solved: Hello app is down in\n  4.0 min with 1 hint (variant\n  image)\n", "Next: kubedrill solution\n  001-hello-down (compare with\n  the reference) | kubedrill\n  list\n", "n: debrief, then start\n  002-service-dns (you have\n")
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, "1 of 2 checks") || strings.HasPrefix(l, "c check") {
			continue
		}
		if lipgloss.Width(l) > 30 {
			t.Errorf("solved line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
	m.title = strings.Repeat("x", 50)
	view = m.View().Content
	mustContain(t, view, "Solved:\n  "+strings.Repeat("x", 25)+"...\n  in 4.0 min")
	m.confirmReset, m.solved = true, false
	view = m.View().Content
	mustContain(t, view, "press x again to reset\n  001-hello-down with a new\n  seed; any other key cancels\n")
}

func TestWatchHintRevealsSpoilers(t *testing.T) {
	m := newTestModel(&fakeWatcher{observe: spoilerGrade(), hints: []string{"Look at the image."}})
	m.width = 120
	next, _ := m.Update(pollMsg{grade: spoilerGrade()})
	m = next.(watchModel)
	mustContain(t, m.View().Content, "actual: "+spoilerHidden)
	next, cmd := m.Update(key("h"))
	next, _ = next.(watchModel).Update(runCmd(t, cmd))
	m = next.(watchModel)
	if m.hints != 1 {
		t.Fatalf("hints: got %d, want 1", m.hints)
	}
	view := m.View().Content
	mustContain(t, view, "actual: nginx:1.2x", "hint 1 of 1 (where to look): Look at the image.")
	mustNotContain(t, view, spoilerHidden)
}

func TestWatchWidthBoundaryTruncatesOrWraps(t *testing.T) {
	g := grader.Grade{Results: []grader.Result{{ID: "a", Description: strings.Repeat("word ", 30), Status: grader.StatusFail, Expected: "1", Actual: "0"}}}
	for _, tc := range []struct {
		width int
		cut   bool
	}{{80, true}, {79, false}} {
		m := newTestModel(&fakeWatcher{observe: g})
		next, _ := m.Update(tea.WindowSizeMsg{Width: tc.width})
		next, _ = next.(watchModel).Update(pollMsg{grade: g})
		view := next.(watchModel).View().Content
		var first, second string
		for i, l := range strings.Split(view, "\n") {
			if strings.HasPrefix(l, "[fail] ") {
				first = l
				second = strings.Split(view, "\n")[i+1]
				break
			}
		}
		if tc.cut {
			if lipgloss.Width(first) != tc.width || !strings.HasSuffix(first, "...") || !strings.HasPrefix(second, detailIndent+"expected") {
				t.Errorf("width %d: first %q second %q, want a cut line", tc.width, first, second)
			}
			continue
		}
		if strings.HasSuffix(first, "...") || !strings.HasPrefix(second, detailIndent+"word") || lipgloss.Width(first) > tc.width {
			t.Errorf("width %d: first %q second %q, want a wrapped line", tc.width, first, second)
		}
	}
}

func TestWatchProgressOutsideStartKeepsNotice(t *testing.T) {
	m := newTestModel(&fakeWatcher{observe: failGrade()})
	m.notice = "surrender cancelled"
	next, cmd := m.Update(progressMsg("creating namespace x"))
	if cmd != nil {
		t.Error("progress issued a command")
	}
	if got := next.(watchModel).notice; got != "surrender cancelled" {
		t.Errorf("notice: got %q, want it untouched outside a start", got)
	}
}

func TestHangingWrapMeasuresVisibleColumnsAndKeepsLead(t *testing.T) {
	styled := "\x1b[1mbold words\x1b[0m here and some more text"
	got := hangingWrap(styled, 20, "  ")
	if len(got) != 2 || got[0] != "\x1b[1mbold words\x1b[0m here and" || got[1] != "  some more text" {
		t.Errorf("styled wrap: %q", got)
	}
	got = hangingWrap("  abc def ghi", 9, "    ")
	if len(got) != 2 || got[0] != "  abc def" || got[1] != "    ghi" {
		t.Errorf("leading spaces: %q", got)
	}
	if got := hangingWrap("short", 20, "  "); len(got) != 1 || got[0] != "short" {
		t.Errorf("fits: %q", got)
	}
}

func TestPaneWidthWinsOverColumns(t *testing.T) {
	var out strings.Builder
	a := newApp(Options{Stdout: &out, IsTerminal: true, Getenv: func(k string) string { return map[string]string{"COLUMNS": "120", "NO_COLOR": "1"}[k] }})
	if got := a.width(); got != 120 {
		t.Fatalf("width before watch: %d", got)
	}
	a.paneWidth = 50
	if got := a.width(); got != 50 {
		t.Errorf("width with a pane: got %d, want 50", got)
	}
	a.paneWidth = 20
	if got := a.width(); got != markdownMinWidth {
		t.Errorf("width with a tiny pane: got %d, want the floor %d", got, markdownMinWidth)
	}
	a.paneWidth = 50
	for _, l := range strings.Split(a.wrapLine("  your diagnosis: "+strings.Repeat("cause ", 20), "    "), "\n") {
		if lipgloss.Width(l) > 50 {
			t.Errorf("debrief line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
}

func TestFaultHelpNamesTheStateDirectory(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run(t, "", "fault", "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "Faults are recorded in\nthe cluster state directory; kubedrill heal and every kubedrill start undo them.")
	mustNotContain(t, out, "journal directory")
}

type elsewhereJournal struct {
	*memJournal
}

func (j *elsewhereJournal) Modify(string, func(*journal.Episode) error) (*journal.Episode, error) {
	if j.current != nil {
		if _, err := j.memJournal.Finish(journal.OutcomeAbandoned); err != nil {
			return nil, err
		}
	}
	return nil, journal.ErrNotCurrent
}

func TestCheckReportsEpisodeEndedElsewhere(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grade grader.Grade
		code  int
	}{{"failing", failGrade(), ExitChecksFailed}, {"passing", passGrade(), ExitOK}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.run(t, "", "start", "001", "--seed", "1")
			f.grader.grades = []grader.Grade{tc.grade}
			fs := f.factories()
			fs.Journal = func(string) (Journal, error) { return &elsewhereJournal{f.journal}, nil }
			var out, errb strings.Builder
			code := Main(context.Background(), []string{"--root", f.root, "check"}, Options{Factories: fs, Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Getenv: func(string) string { return "" }, Now: f.nowFunc()})
			if code != tc.code {
				t.Errorf("exit %d, want %d: %s", code, tc.code, errb.String())
			}
			mustContain(t, out.String(), "deployment hello has 2 ready replicas", "the episode of 001-hello-down already ended as abandoned in another terminal; kubedrill debrief shows it\n")
			mustNotContain(t, out.String(), "Solved:", "Debrief:")
			if f.journal.current != nil {
				t.Error("episode still active")
			}
		})
	}
}

func TestProfileRenderNeverExpiresAnExam(t *testing.T) {
	f := newFixture(t)
	deadline := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
	ep := examEpisode(deadline)
	f.journal.current = ep
	f.journal.episodes = []journal.Episode{*ep}
	f.grader.grades = []grader.Grade{failGrade()}
	writeProfile(t, f.root, "kind: Cluster\n")
	out := filepath.Join(t.TempDir(), "kind.yaml")
	code, stdout, errOut := f.run(t, "", "profile", "render", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	mustNotContain(t, stdout+errOut, "exam time box")
	if f.journal.current == nil || f.journal.current.Outcome != journal.OutcomeActive {
		t.Errorf("profile render ended the exam: %+v", f.journal.current)
	}
	_, stdout, _ = f.run(t, "", "status")
	mustContain(t, stdout, "exam time box of 001-hello-down expired at")
}
