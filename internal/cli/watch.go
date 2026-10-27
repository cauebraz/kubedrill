package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/tui/watch"
)

func (a *app) watchCommand() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Live view of the checks, re-graded every interval",
		Long: "watch re-grades the active episode every interval and redraws the checks.\n" +
			"Keys: c checks now, h takes the next hint, s twice surrenders, x twice resets\n" +
			"with a new seed, n starts the next recommended scenario after a solve, w\n" +
			"toggles why, ? shows every key, q quits. A solve never advances on its own.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < time.Second {
				return fmt.Errorf("interval must be at least 1s, got %s", interval)
			}
			if err := a.refuseInExam("watch"); err != nil {
				return err
			}
			if !a.term.IsTerminal || !a.isInputTerminal {
				return fmt.Errorf("watch needs a terminal on stdin and stdout; use kubedrill check in scripts and pipes")
			}
			s, err := a.learnerSession(cmd.Context())
			if err != nil {
				return err
			}
			ep, rd, err := s.Current()
			if err != nil {
				return err
			}
			return a.runWatch(cmd.Context(), sessionWatcher{s}, ep, rd.Scenario.Meta.Title, len(rd.Hints), len(rd.Checks), interval)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", watch.DefaultInterval, "time between grading runs")
	return cmd
}

// sessionWatcher adapts the learner session to watch.Session.
type sessionWatcher struct {
	*env.Session
}

func (s sessionWatcher) Start(ctx context.Context, ref string, seed int64) (*journal.Episode, *scenario.Rendered, error) {
	return s.Session.Start(ctx, ref, env.StartOptions{Seed: seed, Mode: journal.ModePractice})
}

// runWatch runs the watch program until the learner quits. After n on a
// solved screen it runs the debrief outside the program, starts the
// recommended scenario and watches it, so the debrief prompts are never
// skipped. The banner and the debrief printed after the program wrap to the
// width the program saw, so a narrow pane keeps them readable.
func (a *app) runWatch(parent context.Context, w watch.Session, ep *journal.Episode, title string, totalHints, checks int, interval time.Duration) error {
	for {
		fm, err := watch.Run(parent, w, ep, title, totalHints, checks, a.watchOptions(interval), a.watchProgram())
		if err != nil || fm == nil {
			return err
		}
		switch {
		case fm.Solution != "":
			a.printf("episode of %s recorded as abandoned\n\n", fm.ID)
			a.printf("%s", a.printer().Markdown(fm.Solution))
			return nil
		case !fm.Solved:
			a.println("Next: kubedrill check | kubedrill hint")
			return nil
		}
		solved := fm.SolvedEpisode
		if solved == nil {
			solved = a.lastEndedOf(fm.ID, fm.Started)
		}
		if solved == nil {
			return fmt.Errorf("the checks of %s passed but the solve was not recorded; kubedrill check grades and records it", fm.ID)
		}
		if _, err := a.afterSolve(parent, a.term.PrinterAt(fm.Width), solved); err != nil {
			return err
		}
		if fm.StartNext == nil {
			return nil
		}
		pick := *fm.StartNext
		a.printf("starting %s: %s\n", pick.Ref, pick.Reason)
		next, rd, err := w.Start(parent, pick.Ref, pick.Seed)
		a.progress.Finish()
		if err != nil {
			return fmt.Errorf("start %s: %w", pick.Ref, err)
		}
		a.printf("started %s (seed %d) in namespace %s; kubedrill ticket prints the ticket\n", next.ScenarioID, next.Seed, rd.Namespace)
		ep, title, totalHints, checks = next, rd.Scenario.Meta.Title, len(rd.Hints), len(rd.Checks)
	}
}

// watchOptions wires the watch model to this run: the clock, the colored
// tags, the recommendation, the exam and spoiler state of the journal, and
// the learner-facing refusals the commands print.
func (a *app) watchOptions(interval time.Duration) watch.Options {
	return watch.Options{
		Interval:        interval,
		Now:             a.now(),
		Tag:             a.printer().Tag,
		Next:            a.nextPick,
		ExamActive:      a.examActive,
		Revealed:        a.spoilersRevealed,
		HintLabel:       hintLabel,
		Markdown:        func(md string, width int) string { return a.term.PrinterAt(width).Markdown(md) },
		DiagnosisNeeded: func(what string) string { return diagnosisNeeded(what).Error() },
		ResetInExam:     errResetInExam.Error(),
	}
}

// watchProgram runs the watch program on the terminal; while it runs, start
// progress lines go to the watch status line. teaOptions, set by tests,
// follow the context, output and input options.
func (a *app) watchProgram() watch.Program {
	return watch.Program{Out: a.stdout, In: a.rawStdin, Divert: a.progress.Divert, Extra: a.teaOptions}
}

// lastEndedOf returns the most recent ended episode when it belongs to
// scenario id and started at or after since: the solve a quit raced with,
// or nil.
func (a *app) lastEndedOf(id string, since time.Time) *journal.Episode {
	j, err := a.getJournal()
	if err != nil {
		return nil
	}
	eps, err := allEpisodes(j)
	if err != nil {
		return nil
	}
	last := latestEnded(eps)
	if last == nil || last.ScenarioID != id || last.StartedAt.Before(since) {
		return nil
	}
	return last
}

// examActive reports whether the journal's active episode is an exam
// before its deadline.
func (a *app) examActive() bool {
	j, err := a.getJournal()
	if err != nil {
		return false
	}
	cur, err := j.Current()
	return err == nil && a.examRunning(cur)
}

// spoilersRevealed reports whether the active episode has a hint or a
// diagnosis, after which a spoiler check shows its actual value.
func (a *app) spoilersRevealed() bool {
	j, err := a.getJournal()
	if err != nil {
		return false
	}
	cur, err := j.Current()
	return err == nil && revealsSpoilers(cur)
}

// revealsSpoilers reports whether ep has taken a hint or recorded a
// diagnosis.
func revealsSpoilers(ep *journal.Episode) bool {
	return ep != nil && (ep.HintsUsed > 0 || ep.Diagnosis != nil)
}
