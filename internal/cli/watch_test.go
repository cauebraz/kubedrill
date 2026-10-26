package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

type fakeWatcher struct {
	observe      grader.Grade
	entered      chan struct{}
	block        chan struct{}
	observed     int
	graded       int
	hints        []string
	hintIndex    int
	solution     string
	surrender    int
	surrenderErr error
	starts       []string
}

func (w *fakeWatcher) Observe(context.Context) (grader.Grade, error) {
	w.observed++
	if w.block != nil {
		w.entered <- struct{}{}
		<-w.block
	}
	return w.observe, nil
}

func (w *fakeWatcher) Grade(context.Context) (grader.Grade, *journal.Episode, error) {
	w.graded++
	end := time.Date(2026, 10, 25, 12, 5, 0, 0, time.UTC)
	return w.observe, &journal.Episode{ScenarioID: "001", StartedAt: end.Add(-5 * time.Minute), EndedAt: &end, HintsUsed: w.hintIndex, Outcome: journal.OutcomeSolved}, nil
}

func (w *fakeWatcher) Hint() (string, int, int, error) {
	if w.hintIndex >= len(w.hints) {
		return "", w.hintIndex, len(w.hints), env.ErrNoMoreHints
	}
	w.hintIndex++
	return w.hints[w.hintIndex-1], w.hintIndex, len(w.hints), nil
}

func (w *fakeWatcher) Surrender(context.Context) (string, error) {
	w.surrender++
	return w.solution, w.surrenderErr
}

func (w *fakeWatcher) Start(_ context.Context, ref string, seed int64) (*journal.Episode, *scenario.Rendered, error) {
	w.starts = append(w.starts, fmt.Sprintf("%s %d", ref, seed))
	start := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	sc := &scenario.Scenario{Meta: scenario.Meta{ID: ref, Title: "Title of " + ref}}
	rd := &scenario.Rendered{Scenario: sc, Hints: []string{"a", "b", "c"}, Checks: []scenario.Check{{ID: "one"}, {ID: "two"}, {ID: "three"}}}
	return &journal.Episode{ScenarioID: ref, Seed: 77, StartedAt: start}, rd, nil
}

func newTestModel(w watcher) watchModel {
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ep := &journal.Episode{ScenarioID: "001-hello-down", Seed: 3, Variant: "image", StartedAt: start}
	m := newWatchModel(context.Background(), w, ep, "Hello app is down", 2, 2, 5*time.Second, start)
	m.clock = time.Millisecond
	return m
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("got nil command, want one")
	}
	return cmd()
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestWatchPollRendersResults(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init: got nil cmd, want poll and tick")
	}
	next, _ := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	view := m.View().Content
	for _, want := range []string{"Hello app is down  [001-hello-down]  seed 3\n", "[fail] deployment hello has 2 ready replicas", "expected: 2  actual: 0", "1 of 2 checks passed (weighted score 0.50)", "       why: Ready replicas are the pods", "\nc check  h hint  w why  x reset  ? help  q quit\n"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	for _, width := range []int{100, 120} {
		wide := m
		wide.width = width
		view := wide.View().Content
		mustContain(t, view, "\nc check  h hint  w why  x reset  ? help  q quit\n")
		for _, l := range strings.Split(view, "\n") {
			if len(l) > width {
				t.Errorf("width %d: line of %d columns: %q", width, len(l), l)
			}
		}
	}
	if strings.Contains(view, "image") {
		t.Errorf("view leaks the variant name:\n%s", view)
	}
	if m.polling {
		t.Errorf("polling flag still set after pollMsg")
	}
}

func TestWatchTickPollsOnInterval(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	start := m.now
	next, _ := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, cmd := m.Update(tickMsg(start.Add(2 * time.Second)))
	m = next.(watchModel)
	if m.polling {
		t.Errorf("polled before the interval elapsed")
	}
	if cmd == nil {
		t.Fatal("tick: got nil cmd, want re-armed tick")
	}
	next, _ = m.Update(tickMsg(start.Add(6 * time.Second)))
	m = next.(watchModel)
	if !m.polling {
		t.Errorf("did not start a poll after the interval")
	}
	if !strings.Contains(m.View().Content, "checking (2 checks)...") {
		t.Errorf("view lacks the polling marker")
	}
	next, _ = m.Update(tickMsg(start.Add(7 * time.Second)))
	m = next.(watchModel)
	if !strings.Contains(m.View().Content, "elapsed 7s") {
		t.Errorf("elapsed not updated: %s", m.View().Content)
	}
}

func TestWatchSolvedRecordsAndQuitsOnKey(t *testing.T) {
	w := &fakeWatcher{observe: passGrade()}
	m := newTestModel(w)
	next, cmd := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	if !m.solved {
		t.Fatal("solved flag not set on a passed grade")
	}
	msg := runCmd(t, cmd)
	rec, ok := msg.(recordedMsg)
	if !ok || w.graded != 1 {
		t.Fatalf("record cmd: got %T, graded %d; want recordedMsg and 1", msg, w.graded)
	}
	next, _ = m.Update(rec)
	m = next.(watchModel)
	view := m.View().Content
	if !strings.Contains(view, "Solved: Hello app is down in 5.0 min with 0 hints") || !strings.Contains(view, "?: help  any other key quits") || !strings.Contains(view, "Next: kubedrill solution 001-hello-down (compare with the reference) | kubedrill\n  list\n") {
		t.Errorf("solved view:\n%s", view)
	}
	m.width = 120
	if wide := m.View().Content; !strings.Contains(wide, "Next: kubedrill solution 001-hello-down (compare with the reference) | kubedrill list\n") {
		t.Errorf("solved view at 120 columns:\n%s", wide)
	}
	m.width = 80
	next, cmd = m.Update(tickMsg(m.now.Add(10 * time.Second)))
	m = next.(watchModel)
	if m.polling {
		t.Errorf("kept polling after solve")
	}
	if frozen := m.View().Content; !strings.Contains(frozen, "elapsed 0s") || !strings.Contains(frozen, "checked 0s ago") {
		t.Errorf("clock not frozen after solve:\n%s", frozen)
	}
	_, cmd = m.Update(key("x"))
	if !isQuit(cmd) {
		t.Errorf("key after solve: got no quit")
	}
}

func TestWatchHintKeys(t *testing.T) {
	w := &fakeWatcher{observe: failGrade(), hints: []string{"Look at events."}}
	m := newTestModel(w)
	_, cmd := m.Update(key("h"))
	msg := runCmd(t, cmd)
	next, _ := m.Update(msg)
	m = next.(watchModel)
	if !strings.Contains(m.View().Content, "hint 1 of 1 (where to look): Look at events.") || m.hints != 1 {
		t.Errorf("hint view:\n%s", m.View().Content)
	}
	_, cmd = m.Update(key("h"))
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(watchModel)
	if !strings.Contains(m.View().Content, "all 1 hints shown; s twice surrenders") {
		t.Errorf("exhausted hint notice missing:\n%s", m.View().Content)
	}
}

func TestWatchSurrenderNeedsConfirmation(t *testing.T) {
	w := &fakeWatcher{observe: failGrade(), solution: "## Root cause"}
	m := newTestModel(w)
	next, cmd := m.Update(key("s"))
	m = next.(watchModel)
	if cmd != nil || !m.confirm || !strings.Contains(m.View().Content, "press s again") {
		t.Fatalf("first s: cmd %v confirm %v", cmd, m.confirm)
	}
	next, _ = m.Update(key("x"))
	m = next.(watchModel)
	if m.confirm || w.surrender != 0 {
		t.Errorf("cancel: confirm %v surrender %d", m.confirm, w.surrender)
	}
	next, _ = m.Update(key("s"))
	m = next.(watchModel)
	next, cmd = m.Update(key("s"))
	m = next.(watchModel)
	msg := runCmd(t, cmd)
	if w.surrender != 1 {
		t.Fatalf("surrender calls: got %d, want 1", w.surrender)
	}
	next, cmd = m.Update(msg)
	m = next.(watchModel)
	if m.solution != "## Root cause" || !isQuit(cmd) {
		t.Errorf("after surrender: solution %q quit %v", m.solution, isQuit(cmd))
	}
	next, cmd = m.Update(tickMsg(m.now.Add(time.Second)))
	if cmd != nil {
		t.Errorf("tick after quit re-armed the ticker")
	}
	_ = next
}

func TestWatchQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newTestModel(&fakeWatcher{})
		_, cmd := m.Update(key(k))
		if !isQuit(cmd) {
			t.Errorf("%s: got no quit", k)
		}
	}
}

func TestWatchPollError(t *testing.T) {
	m := newTestModel(&fakeWatcher{})
	next, _ := m.Update(pollMsg{err: errors.New("connection refused")})
	m = next.(watchModel)
	if !strings.Contains(m.View().Content, "[err ] grading failed: connection refused") {
		t.Errorf("error view:\n%s", m.View().Content)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("one two three four", 9)
	want := "one two\nthree\nfour"
	if got != want {
		t.Errorf("wrap: got %q, want %q", got, want)
	}
}

func TestWatchInitDoesNotDoublePoll(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	if !m.polling {
		t.Fatal("model does not start in polling state although Init issues a poll")
	}
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init: got %T len %d, want a batch of poll and tick", batch, len(batch))
	}
	next, cmd := m.Update(tickMsg(m.now.Add(6 * time.Second)))
	m = next.(watchModel)
	if _, isTick := runCmd(t, cmd).(tickMsg); !isTick {
		t.Errorf("first tick after Init issued a second poll")
	}
	if w.observed != 0 {
		t.Errorf("observe calls before the Init poll returned: %d", w.observed)
	}
	next, _ = m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, cmd = m.Update(tickMsg(m.now.Add(6 * time.Second)))
	m = next.(watchModel)
	if _, isBatch := cmd().(tea.BatchMsg); !isBatch || !m.polling {
		t.Errorf("poll not re-armed once the interval elapsed after the first result")
	}
}

func TestWatchRegradeFailureKeepsWatching(t *testing.T) {
	w := &fakeWatcher{observe: passGrade()}
	m := newTestModel(w)
	next, _ := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, cmd := m.Update(recordedMsg{grade: failGrade()})
	m = next.(watchModel)
	if m.solved || cmd != nil {
		t.Errorf("solved still %v after a failed re-grade", m.solved)
	}
	view := m.View().Content
	if !strings.Contains(view, "re-grade did not pass; the cluster changed between polls") || strings.Contains(view, "Solved:") || !strings.Contains(view, "h hint") {
		t.Errorf("view after failed re-grade:\n%s", view)
	}
	next, _ = m.Update(tickMsg(m.now.Add(time.Second)))
	m = next.(watchModel)
	if !m.polling {
		t.Errorf("did not resume polling after the failed re-grade")
	}
}

func TestWatchPollTimeoutFollowsChecks(t *testing.T) {
	tests := []struct {
		interval time.Duration
		checks   int
		want     time.Duration
	}{
		{5 * time.Second, 0, 30 * time.Second},
		{5 * time.Second, 3, 120 * time.Second},
		{10 * time.Minute, 1, 10 * time.Minute},
	}
	for _, tc := range tests {
		if got := pollTimeoutFor(tc.interval, tc.checks); got != tc.want {
			t.Errorf("pollTimeoutFor(%s, %d): got %s, want %s", tc.interval, tc.checks, got, tc.want)
		}
	}
}

func TestWatchHeaderTruncates(t *testing.T) {
	m := newTestModel(&fakeWatcher{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 10})
	m = next.(watchModel)
	first := strings.SplitN(m.View().Content, "\n", 2)[0]
	if len(first) != 20 || !strings.HasSuffix(first, "...") {
		t.Errorf("header: got %q (len %d), want 20 chars ending in ...", first, len(first))
	}
}

func TestWatchWaitsForInFlightCommands(t *testing.T) {
	w := &fakeWatcher{observe: failGrade(), entered: make(chan struct{}), block: make(chan struct{})}
	m := newTestModel(w)
	cmd := m.startPoll()
	go cmd()
	<-w.entered
	if n, closed := m.flight.state(); n != 1 || closed {
		t.Fatalf("inflight before close: n %d closed %v, want 1 false", n, closed)
	}
	done := make(chan struct{})
	go func() {
		m.flight.close()
		close(done)
	}()
	for {
		if _, closed := m.flight.state(); closed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("close returned while Observe was still running")
	default:
	}
	if n, _ := m.flight.state(); n != 1 {
		t.Errorf("inflight while close waits: n %d, want 1", n)
	}
	close(w.block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("close did not return after the command finished")
	}
	if n, closed := m.flight.state(); n != 0 || !closed {
		t.Errorf("inflight after close: n %d closed %v, want 0 true", n, closed)
	}
}

func TestWatchWhyFirstFailingAndToggle(t *testing.T) {
	g := grader.Grade{Score: 0, Results: []grader.Result{
		{ID: "a", Description: "first", Status: grader.StatusFail, Expected: "1", Actual: "0", Why: "why of the first check"},
		{ID: "b", Description: "second", Status: grader.StatusError, Message: "boom", Why: "why of the second check"},
		{ID: "c", Description: "third", Status: grader.StatusPass, Why: "why of a passing check"},
	}}
	w := &fakeWatcher{observe: g}
	m := newTestModel(w)
	m.width = 120
	next, _ := m.Update(pollMsg{grade: g})
	m = next.(watchModel)
	view := m.View().Content
	mustContain(t, view, "       why: why of the first check")
	help, _ := m.Update(key("?"))
	mustContain(t, help.(watchModel).View().Content, "w   why for every failing check")
	for _, absent := range []string{"why of the second check", "why of a passing check"} {
		if strings.Contains(view, absent) {
			t.Errorf("view shows %q before the toggle:\n%s", absent, view)
		}
	}
	next, cmd := m.Update(key("w"))
	m = next.(watchModel)
	if cmd != nil {
		t.Errorf("w issued a command")
	}
	view = m.View().Content
	mustContain(t, view, "why of the first check", "why of the second check")
	help, _ = m.Update(key("?"))
	mustContain(t, help.(watchModel).View().Content, "w   why for the first failing check only")
	if strings.Contains(view, "why of a passing check") {
		t.Errorf("passing check shows why:\n%s", view)
	}
	next, _ = m.Update(key("w"))
	m = next.(watchModel)
	if view = m.View().Content; strings.Contains(view, "why of the second check") {
		t.Errorf("second toggle did not hide the other why lines:\n%s", view)
	}
}

func TestWatchCommandAfterCloseDoesNotRun(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	cmd := m.startPoll()
	m.flight.close()
	if msg := cmd(); msg != nil {
		t.Errorf("command after close returned %T, want nil", msg)
	}
	if w.observed != 0 {
		t.Errorf("Observe ran %d times after close, want 0", w.observed)
	}
}

func TestWatchWhyWrapsToWindowWidth(t *testing.T) {
	g := grader.Grade{Results: []grader.Result{{ID: "a", Description: "first", Status: grader.StatusFail, Expected: "1", Actual: "0", Why: strings.Repeat("word ", 40)}}}
	m := newTestModel(&fakeWatcher{observe: g})
	next, _ := m.Update(pollMsg{grade: g})
	m = next.(watchModel)
	m.width = 0
	if w := widestWhyLine(m.View().Content); w > whyWidth || w <= whyWidth-10 {
		t.Errorf("width 0: widest why line %d, want within (%d, %d]", w, whyWidth-10, whyWidth)
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(watchModel)
	if w := widestWhyLine(m.View().Content); w <= whyWidth || w > 120 {
		t.Errorf("width 120: widest why line %d, want within (%d, 120]", w, whyWidth)
	}
}

func widestWhyLine(view string) int {
	widest := 0
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, "       why: ") || strings.HasPrefix(l, "            word") {
			widest = max(widest, len(l))
		}
	}
	return widest
}

func TestWatchNamespaceErrorQuits(t *testing.T) {
	for _, sentinel := range []error{env.ErrNamespaceMissing, env.ErrNamespaceForeign} {
		m := newTestModel(&fakeWatcher{})
		err := fmt.Errorf("namespace task-hello: %w; run kubedrill reset", sentinel)
		next, cmd := m.Update(pollMsg{err: err})
		m = next.(watchModel)
		if m.fatal == nil || !isQuit(cmd) {
			t.Errorf("%v: fatal %v quit %v, want the error and a quit", sentinel, m.fatal, isQuit(cmd))
		}
		next, cmd = m.Update(tickMsg(m.now.Add(time.Second)))
		if cmd != nil {
			t.Errorf("%v: tick after the fatal error re-armed the ticker", sentinel)
		}
		_ = next
	}
	m := newTestModel(&fakeWatcher{})
	next, cmd := m.Update(pollMsg{err: errors.New("connection refused")})
	m = next.(watchModel)
	if m.fatal != nil || cmd != nil {
		t.Errorf("transient error ended watch: fatal %v", m.fatal)
	}
}

func TestWatchRecheckKey(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	next, cmd := m.Update(key("c"))
	m = next.(watchModel)
	if cmd != nil || !strings.Contains(m.View().Content, "a check is already running") {
		t.Errorf("c during the Init poll: cmd %v view:\n%s", cmd != nil, m.View().Content)
	}
	next, _ = m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, cmd = m.Update(key("c"))
	m = next.(watchModel)
	if !m.polling || !strings.Contains(m.View().Content, "checking (2 checks)...") {
		t.Fatalf("c: polling %v view:\n%s", m.polling, m.View().Content)
	}
	if _, ok := runCmd(t, cmd).(pollMsg); !ok || w.observed != 1 {
		t.Errorf("c did not poll: observed %d", w.observed)
	}
}

func TestWatchResetNeedsTwoPresses(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	next, _ := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, cmd := m.Update(key("x"))
	m = next.(watchModel)
	if cmd != nil || !strings.Contains(m.View().Content, "press x again to reset 001-hello-down with a new seed") {
		t.Fatalf("first x: cmd %v view:\n%s", cmd != nil, m.View().Content)
	}
	next, _ = m.Update(key("h"))
	m = next.(watchModel)
	if m.confirmReset || len(w.starts) != 0 || !strings.Contains(m.View().Content, "reset cancelled") {
		t.Errorf("cancel: confirm %v starts %v", m.confirmReset, w.starts)
	}
	next, _ = m.Update(key("x"))
	m = next.(watchModel)
	next, cmd = m.Update(key("x"))
	m = next.(watchModel)
	msg := runCmd(t, cmd)
	if strings.Join(w.starts, ",") != "001-hello-down 0" {
		t.Fatalf("starts: got %v, want a new seed of the same scenario", w.starts)
	}
	stale := pollMsg{gen: m.gen, grade: passGrade()}
	next, cmd = m.Update(msg)
	m = next.(watchModel)
	view := m.View().Content
	mustContain(t, view, "Title of 001-hello-down  [001-hello-down]  seed 77", "hints 0/3   checking (3 checks)...", "started 001-hello-down (seed 77)", "waiting for the first grading run")
	if cmd == nil {
		t.Fatal("reload issued no poll")
	}
	next, _ = m.Update(stale)
	if m = next.(watchModel); m.solved || m.grade != nil {
		t.Errorf("a poll of the previous episode changed the view: solved %v", m.solved)
	}
}

func TestWatchNextOnlyAfterSolve(t *testing.T) {
	w := &fakeWatcher{observe: passGrade()}
	m := newTestModel(w)
	m.next = func() (nextPick, bool) {
		return nextPick{ref: "002-service-dns", seed: 41, reason: "practice: level L1"}, true
	}
	next, cmd := m.Update(key("n"))
	m = next.(watchModel)
	if cmd != nil || len(w.starts) != 0 || !strings.Contains(m.View().Content, "n starts the next recommended scenario once this one is solved") {
		t.Fatalf("n before solve: starts %v view:\n%s", w.starts, m.View().Content)
	}
	next, cmd = m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(watchModel)
	view := m.View().Content
	mustContain(t, view, "Solved: Hello app is down", "n: debrief, then start 002-service-dns (practice: level L1)")
	if m.solved && len(w.starts) != 0 {
		t.Fatalf("solve advanced on its own: %v", w.starts)
	}
	next, cmd = m.Update(key("n"))
	m = next.(watchModel)
	if !isQuit(cmd) || m.startNext == nil || m.startNext.ref != "002-service-dns" || m.startNext.seed != 41 {
		t.Fatalf("n after solve: quit %v startNext %+v", isQuit(cmd), m.startNext)
	}
	if len(w.starts) != 0 {
		t.Errorf("n started %v inside the program; the debrief runs first", w.starts)
	}
}

func TestWatchNextWithoutRecommendation(t *testing.T) {
	w := &fakeWatcher{observe: passGrade()}
	m := newTestModel(w)
	next, cmd := m.Update(pollMsg{grade: w.observe})
	m = next.(watchModel)
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(watchModel)
	next, cmd = m.Update(key("n"))
	m = next.(watchModel)
	if cmd != nil || !strings.Contains(m.View().Content, "no recommended scenario") {
		t.Errorf("n without recommendation: view\n%s", m.View().Content)
	}
}

func TestWatchFullHelp(t *testing.T) {
	m := newTestModel(&fakeWatcher{})
	next, cmd := m.Update(key("?"))
	m = next.(watchModel)
	if cmd != nil {
		t.Errorf("? issued a command")
	}
	mustContain(t, m.View().Content, "Checks\n  c   check now\n  w   why for every failing check\n\nAssist\n  h   hint\n  s   solution (surrender, press twice)\n\nEpisode\n  x   reset with a new seed (press twice)\n  n   debrief, then the next recommended scenario (after a solve)\n  ?   close help\n  q   quit (also esc, ctrl+c)\n")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 40})
	mustContain(t, next.(watchModel).View().Content, "  n   debrief, then the next recommended\n      scenario (after a solve)\n")
	next, _ = m.Update(key("?"))
	m = next.(watchModel)
	mustNotContain(t, m.View().Content, "close help", "Assist")
}

func TestWatchCollapsesPassedChecks(t *testing.T) {
	g := grader.Grade{Results: []grader.Result{
		{ID: "ready", Status: grader.StatusPass},
		{ID: "endpoints", Status: grader.StatusPass},
		{ID: "image", Description: "image tag exists", Status: grader.StatusFail, Expected: "nginx:1.27", Actual: "nginx:1.2x", Why: "A missing tag keeps pods in ImagePullBackOff."},
		{ID: "dns-resolves-from-a-client-pod", Status: grader.StatusPass},
	}}
	m := newTestModel(&fakeWatcher{observe: g})
	next, _ := m.Update(pollMsg{grade: g})
	m = next.(watchModel)
	view := m.View().Content
	mustContain(t, view, "[fail] image tag exists\n       expected: nginx:1.27  actual: nginx:1.2x\n       why: A missing tag", "[pass] 3 checks: ready, endpoints, dns-resolves-from-a-client-pod\n")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 30})
	m = next.(watchModel)
	mustContain(t, m.View().Content, "[pass] 3 checks: ready,\n       endpoints,\n", "       expected: nginx:1.27\n       actual: nginx:1.2x\n")
	next, _ = m.Update(tea.WindowSizeMsg{Width: 90})
	m = next.(watchModel)
	long := grader.Grade{Results: []grader.Result{{ID: "p", Status: grader.StatusPass, Description: strings.Repeat("x", 120)}}}
	next, _ = m.Update(pollMsg{grade: long})
	mustContain(t, next.(watchModel).View().Content, "[pass] 1 check: p\n")
}

func TestWatchSurrenderNeedsDiagnosis(t *testing.T) {
	w := &fakeWatcher{observe: failGrade(), surrenderErr: fmt.Errorf("x: %w", env.ErrDiagnosisRequired)}
	m := newTestModel(w)
	m.width = 200
	next, _ := m.Update(key("s"))
	m = next.(watchModel)
	next, cmd := m.Update(key("s"))
	m = next.(watchModel)
	next, cmd = m.Update(runCmd(t, cmd))
	m = next.(watchModel)
	if isQuit(cmd) || m.surrender {
		t.Fatalf("surrender without diagnosis quit")
	}
	mustContain(t, m.View().Content, `the solution needs your diagnosis first: kubedrill diagnose "<what you think is wrong>"`)
}
