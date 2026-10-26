package env

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/kube"
)

func TestDeleteNamespaceKeepsForeignNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.calls, h.progress = nil, nil
	h.kube.deleteErr = fmt.Errorf("refuse to delete namespace %s: it was %w", testNS, kube.ErrNotManaged)
	h.now = t0.Add(time.Minute)
	h.suffix = "fghij"
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 2}); err != nil {
		t.Fatalf("Start over a foreign namespace: got %v, want nil", err)
	}
	if !slices.Contains(h.calls, "delete:"+testNS) {
		t.Errorf("calls %v lack the delete attempt", h.calls)
	}
	var kept bool
	for _, p := range h.progress {
		if strings.HasPrefix(p, "kept namespace "+testNS+": ") && strings.Contains(p, "not created by kubedrill") {
			kept = true
		}
	}
	if !kept {
		t.Errorf("progress %v lacks the kept-namespace line", h.progress)
	}
	if h.out.Len() != 0 {
		t.Errorf("output: got %q, want no warning for a foreign namespace", h.out.String())
	}
	if h.journal.current == nil || h.journal.current.Seed != 2 {
		t.Errorf("current after Start: got %+v, want the new episode with seed 2", h.journal.current)
	}
}

func TestExpireCancelledFinishesNothing(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep := h.start(t, journal.ModeExam, 1)
	h.now = t0.Add(12 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.s.d.Grader = &cancelGrader{cancel: cancel}
	got, err := h.s.Expire(ctx)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("Expire with the context cancelled mid-grade: got (%+v, %v), want (nil, context.Canceled)", got, err)
	}
	if h.journal.current == nil || h.journal.current.ID != ep.ID || h.journal.current.Outcome != journal.OutcomeActive || len(h.journal.current.Grades) != 0 {
		t.Errorf("episode after a cancelled Expire: got %+v, want it still active with no grade", h.journal.current)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "telemetry:") || strings.HasPrefix(c, "delete:") || strings.HasPrefix(c, "finish:") {
			t.Errorf("call %q after a cancelled Expire, want no telemetry, finish or namespace deletion (calls %v)", c, h.calls)
		}
	}
}

func TestSubmitReturnsEpisodeEndedElsewhere(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep := h.start(t, journal.ModeExam, 1)
	h.grader.grade = grader.Grade{Passed: true}
	h.grader.during = func() {
		e := *h.journal.current
		e.Outcome = journal.OutcomeTimedOut
		end := t0.Add(time.Minute)
		e.EndedAt = &end
		h.journal.ended = append(h.journal.ended, e)
		h.journal.current = nil
	}
	g, got, err := h.s.Submit(context.Background())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !g.Passed {
		t.Errorf("grade: got %+v, want the pass the grader returned", g)
	}
	if got == nil || got.ID != ep.ID || got.Outcome != journal.OutcomeTimedOut {
		t.Errorf("Submit after another process ended the exam: got %+v, want it timed out as recorded", got)
	}
	if len(h.journal.finished) != 0 {
		t.Errorf("finished: got %v, want nothing written by this Submit", h.journal.finished)
	}

	h.journal.ended = nil
	h.calls = nil
	h.start(t, journal.ModeExam, 1)
	h.grader.during = func() { h.journal.current = nil }
	if _, _, err := h.s.Submit(context.Background()); !errors.Is(err, journal.ErrNoCurrent) {
		t.Errorf("Submit with the episode gone and not recorded: got %v, want ErrNoCurrent", err)
	}
	if len(h.journal.finished) != 0 || len(h.journal.ended) != 0 {
		t.Errorf("journal after the vanished episode: finished %v ended %v, want nothing written", h.journal.finished, h.journal.ended)
	}
}

func TestFinishActiveToleratesEpisodeEndedElsewhere(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	first := h.start(t, journal.ModePractice, 1)
	h.journal.modifyErr = fmt.Errorf("modify episode %s: %w (current is other)", first.ID, journal.ErrNotCurrent)
	h.now = t0.Add(time.Minute)
	h.suffix = "fghij"
	ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 2})
	if err != nil {
		t.Fatalf("Start while another process finished the previous episode: got %v, want nil", err)
	}
	if ep.ID == first.ID || ep.Seed != 2 || ep.Outcome != journal.OutcomeActive {
		t.Errorf("new episode: got %+v, want a fresh active episode with seed 2", ep)
	}
	if h.journal.current == nil || h.journal.current.ID != ep.ID {
		t.Errorf("current: got %+v, want the new episode %s", h.journal.current, ep.ID)
	}
	if !slices.Contains(h.calls, "delete:"+first.Namespace) {
		t.Errorf("calls %v lack the deletion of the previous namespace %s", h.calls, first.Namespace)
	}
}

func TestGradeFailingKeepsNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep := h.start(t, journal.ModePractice, 1)
	h.grader.grade = grader.Grade{Passed: false}
	g, got, err := h.s.Grade(context.Background())
	if err != nil || g.Passed {
		t.Fatalf("Grade: got (%+v, %v), want a failing grade and no error", g, err)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "delete:") {
			t.Errorf("failing grade deleted a namespace: %q (calls %v)", c, h.calls)
		}
	}
	if got == nil || got.ID != ep.ID || got.EndedAt != nil || got.Outcome != journal.OutcomeActive || len(got.Grades) != 1 {
		t.Errorf("episode after a failing grade: got %+v, want it active with one grade and no end time", got)
	}
	if h.journal.current == nil || h.journal.current.EndedAt != nil {
		t.Errorf("current after a failing grade: got %+v, want it active with EndedAt nil", h.journal.current)
	}
}
