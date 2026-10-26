// Package watch is the Bubble Tea model of kubedrill watch: the live view of
// the checks of the active episode, re-graded every interval, with keys for
// a hint, a surrender, a reset and the next recommended scenario. It knows
// the learner session only through Session and the command line only
// through Options.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// DefaultInterval is the time between grading runs when none is given.
const DefaultInterval = 5 * time.Second

const (
	watchClockTick  = time.Second
	perCheckTimeout = 30 * time.Second
	narrowWidth     = 80
)

// Session is what the watch model needs from the learner session. Start
// begins a practice episode in place (x resets the current scenario with a
// new seed, n starts the recommended one); seed 0 draws one.
type Session interface {
	Observe(ctx context.Context) (grader.Grade, error)
	Grade(ctx context.Context) (grader.Grade, *journal.Episode, error)
	Hint() (hint string, index, total int, err error)
	Surrender(ctx context.Context) (solution string, err error)
	Start(ctx context.Context, ref string, seed int64) (*journal.Episode, *scenario.Rendered, error)
}

// Pick is the scenario the n key starts after a solve: its reference, the
// seed and the sentence that says why it was picked.
type Pick struct {
	Ref    string
	Seed   int64
	Reason string
}

// Options configure a watch model. Interval is the time between grading
// runs and Now the clock reading the model starts at. Tag renders the tag of
// a check status (default render.StatusTag). Next returns the recommended
// scenario after a solve, ExamActive reports an exam before its deadline
// (which refuses x) and Revealed whether the episode has a hint or a
// diagnosis (which shows spoiler values); each defaults to none. HintLabel
// names a hint by its rung, Markdown renders the hint text at a width (the
// same rendering kubedrill hint prints; default: the source wrapped as
// markdown without color), DiagnosisNeeded is the refusal when what (the
// last hint, the solution) waits for a diagnosis, and ResetInExam is the
// refusal of x during an exam.
type Options struct {
	Interval        time.Duration
	Now             time.Time
	Tag             func(grader.Status) string
	Next            func() (Pick, bool)
	ExamActive      func() bool
	Revealed        func() bool
	HintLabel       func(index, total int) string
	Markdown        func(md string, width int) string
	DiagnosisNeeded func(what string) string
	ResetInExam     string
}

// Program configures the Bubble Tea program Run starts. Out and In are the
// terminal streams. Divert, when set, sends the progress lines of a start to
// sink until the restore it returns runs. Extra, set by tests, follows the
// context, output and input options.
type Program struct {
	Out    io.Writer
	In     io.Reader
	Divert func(sink func(string)) (restore func())
	Extra  []tea.ProgramOption
}

// Result is how a watch program ended: the scenario and start time of the
// episode shown last, the pane width, and either a fatal namespace error, a
// solution (the learner surrendered), or the solve state with the recorded
// episode and the scenario n asked to start next.
type Result struct {
	ID            string
	Started       time.Time
	Width         int
	Fatal         error
	Solution      string
	Solved        bool
	SolvedEpisode *journal.Episode
	StartNext     *Pick
}

// Model is the Bubble Tea model of kubedrill watch. Observe runs as a
// command with its own timeout, so Update never blocks on the cluster. The
// model starts with polling set because Init issues the first poll. gen
// grows with every episode the model shows, so a poll, record, hint,
// surrender or start result issued for an earlier episode is dropped.
// revealed reports, after each poll, whether the episode has a hint or a
// diagnosis, which shows the actual value of spoiler checks.
type Model struct {
	ctx             context.Context
	w               Session
	next            func() (Pick, bool)
	examActive      func() bool
	revealed        func() bool
	hintLabel       func(index, total int) string
	markdown        func(md string, width int) string
	diagnosisNeeded func(what string) string
	resetInExam     string
	flight          *inflight
	gen             int
	title           string
	id              string
	seed            int64
	started         time.Time
	hints           int
	totalHints      int
	checks          int
	interval        time.Duration
	pollTimeout     time.Duration
	clock           time.Duration
	styler          func(tag grader.Status) string

	now          time.Time
	frozen       time.Time
	nextPoll     time.Time
	polling      bool
	lastPoll     time.Time
	grade        *grader.Grade
	pollErr      error
	hint         string
	hintText     string
	notice       string
	confirm      bool
	confirmReset bool
	starting     bool
	hinting      bool
	recording    bool
	reveal       bool
	solved       bool
	solvedEp     *journal.Episode
	pick         *Pick
	startNext    *Pick
	solution     string
	surrender    bool
	allWhy       bool
	showHelp     bool
	quitting     bool
	fatal        error
	width        int
}

type (
	tickMsg time.Time
	pollMsg struct {
		gen    int
		grade  grader.Grade
		err    error
		reveal bool
	}
	recordedMsg struct {
		gen   int
		grade grader.Grade
		ep    *journal.Episode
		err   error
	}
	hintMsg struct {
		gen          int
		text         string
		index, total int
		err          error
	}
	surrenderMsg struct {
		gen      int
		solution string
		err      error
	}
	startedMsg struct {
		gen int
		ep  *journal.Episode
		rd  *scenario.Rendered
		err error
	}
	progressMsg string
)

// pollTimeoutFor gives every check 30s plus a 30s margin, and never less than
// the interval.
func pollTimeoutFor(interval time.Duration, checks int) time.Duration {
	t := time.Duration(checks)*perCheckTimeout + perCheckTimeout
	if t < interval {
		return interval
	}
	return t
}

// New is the watch model of episode ep of the scenario titled title, with
// totalHints hints and checks checks; its commands run under ctx.
func New(ctx context.Context, s Session, ep *journal.Episode, title string, totalHints, checks int, o Options) Model {
	m := Model{
		ctx:             ctx,
		w:               s,
		next:            o.Next,
		examActive:      o.ExamActive,
		revealed:        o.Revealed,
		hintLabel:       o.HintLabel,
		markdown:        o.Markdown,
		diagnosisNeeded: o.DiagnosisNeeded,
		resetInExam:     o.ResetInExam,
		flight:          newInflight(),
		title:           title,
		id:              ep.ScenarioID,
		seed:            ep.Seed,
		started:         ep.StartedAt,
		hints:           ep.HintsUsed,
		totalHints:      totalHints,
		checks:          checks,
		interval:        o.Interval,
		pollTimeout:     pollTimeoutFor(o.Interval, checks),
		clock:           watchClockTick,
		now:             o.Now,
		nextPoll:        o.Now.Add(o.Interval),
		polling:         true,
		width:           render.DefaultWidth,
		styler:          o.Tag,
	}
	if m.next == nil {
		m.next = func() (Pick, bool) { return Pick{}, false }
	}
	if m.examActive == nil {
		m.examActive = func() bool { return false }
	}
	if m.revealed == nil {
		m.revealed = func() bool { return false }
	}
	if m.hintLabel == nil {
		m.hintLabel = func(index, total int) string { return fmt.Sprintf("hint %d of %d", index, total) }
	}
	if m.markdown == nil {
		m.markdown = func(md string, width int) string {
			return render.Printer{IsTerminal: true, Width: width}.Markdown(md)
		}
	}
	if m.diagnosisNeeded == nil {
		m.diagnosisNeeded = func(what string) string { return what + " needs your diagnosis first" }
	}
	if m.resetInExam == "" {
		m.resetInExam = "reset is not available in exam mode"
	}
	if m.styler == nil {
		m.styler = render.StatusTag
	}
	return m
}

// Run runs one watch program on episode ep until the learner quits and
// returns how it ended; nil without an error means the program ended
// without a final model. The commands still running when the program stops
// are waited for, so a solve being recorded is never lost.
func Run(parent context.Context, s Session, ep *journal.Episode, title string, totalHints, checks int, o Options, p Program) (*Result, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m := New(ctx, s, ep, title, totalHints, checks, o)
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(p.Out), tea.WithInput(p.In)}, p.Extra...)
	prog := tea.NewProgram(m, opts...)
	restore := func() {}
	if p.Divert != nil {
		restore = p.Divert(func(msg string) { prog.Send(progressMsg(msg)) })
	}
	final, err := prog.Run()
	cancel()
	m.flight.close()
	restore()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, tea.ErrInterrupted) {
		return nil, fmt.Errorf("watch: %w", err)
	}
	fm, ok := final.(Model)
	if !ok {
		return nil, nil
	}
	return &Result{
		ID:            fm.id,
		Started:       fm.started,
		Width:         fm.width,
		Fatal:         fm.fatal,
		Solution:      fm.solution,
		Solved:        fm.solved,
		SolvedEpisode: fm.solvedEp,
		StartNext:     fm.startNext,
	}, nil
}

// isNamespaceError reports whether err says the scenario namespace is gone
// or belongs to something else, which ends watch with the learner-facing
// text instead of a retry loop.
func isNamespaceError(err error) bool {
	return errors.Is(err, env.ErrNamespaceMissing) || errors.Is(err, env.ErrNamespaceForeign)
}
