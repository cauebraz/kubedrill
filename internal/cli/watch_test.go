package cli

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

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

func TestWatchOptionsText(t *testing.T) {
	a := newApp(Options{Stdout: io.Discard, Stderr: io.Discard, Getenv: func(string) string { return "" }})
	o := a.watchOptions(time.Hour)
	for _, tc := range []struct{ name, got, want string }{
		{"hint label", o.HintLabel(1, 1), "hint 1 of 1 (where to look)"},
		{"hint label past the ladder", o.HintLabel(5, 5), "hint 5 of 5"},
		{"solution", o.DiagnosisNeeded("the solution"), `the solution needs your diagnosis first: kubedrill diagnose "<what you think is wrong>"`},
		{"last hint", o.DiagnosisNeeded("the last hint"), `the last hint needs your diagnosis first: kubedrill diagnose "<what you think is wrong>"`},
		{"reset in exam", o.ResetInExam, "reset is not available in exam mode; kubedrill submit ends the exam"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if o.Interval != time.Hour || o.Tag == nil || o.Next == nil || o.ExamActive == nil || o.Revealed == nil {
		t.Errorf("options not wired: %+v", o)
	}
}
