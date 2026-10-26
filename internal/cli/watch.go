package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	defaultWatchInterval = 5 * time.Second
	watchClockTick       = time.Second
	perCheckTimeout      = 30 * time.Second
	narrowWidth          = 80
)

// watcher is what the watch model needs from the learner session. Start
// begins a practice episode in place (x resets the current scenario with a
// new seed, n starts the recommended one); seed 0 draws one.
type watcher interface {
	Observe(ctx context.Context) (grader.Grade, error)
	Grade(ctx context.Context) (grader.Grade, *journal.Episode, error)
	Hint() (hint string, index, total int, err error)
	Surrender(ctx context.Context) (solution string, err error)
	Start(ctx context.Context, ref string, seed int64) (*journal.Episode, *scenario.Rendered, error)
}

// nextPick is the scenario the n key starts after a solve.
type nextPick struct {
	ref    string
	seed   int64
	reason string
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
		text         string
		index, total int
		err          error
	}
	surrenderMsg struct {
		solution string
		err      error
	}
	startedMsg struct {
		ep  *journal.Episode
		rd  *scenario.Rendered
		err error
	}
	progressMsg string
)

// watchModel is the Bubble Tea model of kubedrill watch. Observe runs as a
// command with its own timeout, so Update never blocks on the cluster. The
// model starts with polling set because Init issues the first poll. gen
// grows with every episode the model shows, so a result of a poll issued
// for an earlier episode is dropped. revealed reports, after each poll,
// whether the episode has a hint or a diagnosis, which shows the actual
// value of spoiler checks.
type watchModel struct {
	ctx         context.Context
	w           watcher
	next        func() (nextPick, bool)
	examActive  func() bool
	revealed    func() bool
	flight      *inflight
	gen         int
	title       string
	id          string
	seed        int64
	started     time.Time
	hints       int
	totalHints  int
	checks      int
	interval    time.Duration
	pollTimeout time.Duration
	clock       time.Duration
	color       bool
	styler      func(tag grader.Status) string

	now          time.Time
	frozen       time.Time
	nextPoll     time.Time
	polling      bool
	lastPoll     time.Time
	grade        *grader.Grade
	pollErr      error
	hint         string
	notice       string
	confirm      bool
	confirmReset bool
	starting     bool
	hinting      bool
	recording    bool
	reveal       bool
	solved       bool
	solvedEp     *journal.Episode
	pick         *nextPick
	startNext    *nextPick
	solution     string
	surrender    bool
	allWhy       bool
	showHelp     bool
	quitting     bool
	fatal        error
	width        int
}

// inflight counts the commands running against the session so runWatch can
// wait for them after the program stops. Once closed, a command that has not
// started yet returns nil without running.
type inflight struct {
	mu     sync.Mutex
	cond   *sync.Cond
	n      int
	closed bool
}

func newInflight() *inflight {
	f := &inflight{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// enter registers one running command; it reports false when the tracker is
// closed.
func (f *inflight) enter() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.n++
	return true
}

func (f *inflight) leave() {
	f.mu.Lock()
	f.n--
	f.cond.Broadcast()
	f.mu.Unlock()
}

// state reports the running command count and whether the tracker is closed.
func (f *inflight) state() (n int, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n, f.closed
}

// close refuses new commands and blocks until the running ones return.
func (f *inflight) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for f.n > 0 {
		f.cond.Wait()
	}
}

// pollTimeoutFor gives every check 30s plus a 30s margin, and never less than
// the interval.
func pollTimeoutFor(interval time.Duration, checks int) time.Duration {
	t := time.Duration(checks)*perCheckTimeout + perCheckTimeout
	if t < interval {
		return interval
	}
	return t
}

func newWatchModel(ctx context.Context, w watcher, ep *journal.Episode, title string, totalHints, checks int, interval time.Duration, now time.Time) watchModel {
	return watchModel{
		ctx:         ctx,
		w:           w,
		next:        func() (nextPick, bool) { return nextPick{}, false },
		examActive:  func() bool { return false },
		revealed:    func() bool { return false },
		flight:      newInflight(),
		title:       title,
		id:          ep.ScenarioID,
		seed:        ep.Seed,
		started:     ep.StartedAt,
		hints:       ep.HintsUsed,
		totalHints:  totalHints,
		checks:      checks,
		interval:    interval,
		pollTimeout: pollTimeoutFor(interval, checks),
		clock:       watchClockTick,
		now:         now,
		nextPoll:    now.Add(interval),
		polling:     true,
		width:       markdownWidth,
		styler:      func(s grader.Status) string { return statusTag(s) },
	}
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(m.startPoll(), m.tick())
}

func (m watchModel) tick() tea.Cmd {
	return tea.Tick(m.clock, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// track wraps a command so runWatch can wait for it after cancelling the
// context. The counter is raised inside the command because Bubble Tea may
// start a command after the program stops; such a command returns nil.
func (m watchModel) track(cmd func() tea.Msg) tea.Cmd {
	f := m.flight
	return func() tea.Msg {
		if !f.enter() {
			return nil
		}
		defer f.leave()
		return cmd()
	}
}

func (m watchModel) startPoll() tea.Cmd {
	ctx, w, timeout, gen, revealed := m.ctx, m.w, m.pollTimeout, m.gen, m.revealed
	return m.track(func() tea.Msg {
		pctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		g, err := w.Observe(pctx)
		return pollMsg{gen: gen, grade: g, err: err, reveal: revealed()}
	})
}

// record grades once more and records the solve. It runs detached from the
// program context, with its own timeout, so quitting the screen while it
// runs does not lose the solve; runWatch waits for it before returning.
func (m watchModel) record() tea.Cmd {
	ctx, w, timeout, gen := context.WithoutCancel(m.ctx), m.w, m.pollTimeout, m.gen
	return m.track(func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		g, ep, err := w.Grade(rctx)
		return recordedMsg{gen: gen, grade: g, ep: ep, err: err}
	})
}

func (m watchModel) takeHint() tea.Cmd {
	w := m.w
	return m.track(func() tea.Msg {
		text, index, total, err := w.Hint()
		return hintMsg{text: text, index: index, total: total, err: err}
	})
}

func (m watchModel) doSurrender() tea.Cmd {
	ctx, w, timeout := m.ctx, m.w, m.pollTimeout
	return m.track(func() tea.Msg {
		sctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		sol, err := w.Surrender(sctx)
		return surrenderMsg{solution: sol, err: err}
	})
}

func (m watchModel) doStart(ref string, seed int64) tea.Cmd {
	ctx, w := m.ctx, m.w
	return m.track(func() tea.Msg {
		ep, rd, err := w.Start(ctx, ref, seed)
		return startedMsg{ep: ep, rd: rd, err: err}
	})
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tickMsg:
		m.now = time.Time(msg)
		if m.quitting {
			return m, nil
		}
		if !m.polling && !m.solved && !m.surrender && !m.starting && !m.now.Before(m.nextPoll) {
			m.polling = true
			return m, tea.Batch(m.startPoll(), m.tick())
		}
		return m, m.tick()
	case pollMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m.onPoll(msg)
	case recordedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m.onRecorded(msg)
	case hintMsg:
		m.hinting = false
		if msg.err != nil {
			switch {
			case errors.Is(msg.err, env.ErrNoMoreHints):
				m.notice = fmt.Sprintf("all %d hints shown; s twice surrenders and shows the solution", msg.total)
			case errors.Is(msg.err, env.ErrDiagnosisRequired):
				m.notice = diagnosisNeeded("the last hint").Error()
			default:
				m.notice = msg.err.Error()
			}
			return m, nil
		}
		m.hints = msg.index
		m.totalHints = msg.total
		m.hint = hintLabel(msg.index, msg.total) + ": " + strings.TrimSpace(msg.text)
		m.notice = ""
		return m, nil
	case surrenderMsg:
		if msg.err != nil {
			m.surrender = false
			if errors.Is(msg.err, env.ErrDiagnosisRequired) {
				m.notice = diagnosisNeeded("the solution").Error()
				return m, nil
			}
			m.notice = "surrender failed: " + msg.err.Error()
			return m, nil
		}
		m.solution = msg.solution
		m.quitting = true
		return m, tea.Quit
	case progressMsg:
		if m.starting {
			m.notice = string(msg) + "..."
		}
		return m, nil
	case startedMsg:
		m.starting = false
		if msg.err != nil {
			if m.solved {
				m.notice = "starting the next scenario failed: " + msg.err.Error()
			} else {
				m.notice = "reset failed: " + msg.err.Error()
			}
			return m, nil
		}
		return m.reload(msg.ep, msg.rd)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m watchModel) onPoll(msg pollMsg) (tea.Model, tea.Cmd) {
	m.polling = false
	m.lastPoll = m.now
	m.nextPoll = m.now.Add(m.interval)
	if msg.err != nil {
		if isNamespaceError(msg.err) {
			m.fatal = msg.err
			m.quitting = true
			return m, tea.Quit
		}
		m.pollErr = msg.err
		return m, nil
	}
	m.pollErr = nil
	m.reveal = msg.reveal
	g := msg.grade
	m.grade = &g
	if g.Passed && !m.solved {
		m.solved = true
		m.recording = true
		m.frozen = m.now
		return m, m.record()
	}
	return m, nil
}

func (m watchModel) onRecorded(msg recordedMsg) (tea.Model, tea.Cmd) {
	m.recording = false
	if msg.err != nil {
		if isNamespaceError(msg.err) {
			m.fatal = msg.err
			m.quitting = true
			return m, tea.Quit
		}
		m.notice = "recording the solve failed: " + msg.err.Error()
		return m, nil
	}
	if !msg.grade.Passed {
		m.solved = false
		m.frozen = time.Time{}
		g := msg.grade
		m.grade = &g
		m.notice = "re-grade did not pass; the cluster changed between polls"
		m.nextPoll = m.now
		return m, nil
	}
	m.solvedEp = msg.ep
	if pick, ok := m.next(); ok {
		m.pick = &pick
	}
	return m, nil
}

// reload shows a freshly started episode in place: the counters, the grade
// and the solve state start over and a poll is issued for the new episode.
func (m watchModel) reload(ep *journal.Episode, rd *scenario.Rendered) (tea.Model, tea.Cmd) {
	m.gen++
	m.id = ep.ScenarioID
	m.seed = ep.Seed
	m.started = ep.StartedAt
	m.hints = ep.HintsUsed
	if rd != nil {
		m.title = rd.Scenario.Meta.Title
		m.totalHints = len(rd.Hints)
		m.checks = len(rd.Checks)
	}
	m.pollTimeout = pollTimeoutFor(m.interval, m.checks)
	m.grade = nil
	m.pollErr = nil
	m.reveal = false
	m.hint = ""
	m.solved = false
	m.solvedEp = nil
	m.pick = nil
	m.frozen = time.Time{}
	m.confirm = false
	m.confirmReset = false
	m.notice = fmt.Sprintf("started %s (seed %d); kubedrill ticket prints the ticket", m.id, m.seed)
	m.polling = true
	m.nextPoll = m.now.Add(m.interval)
	return m, m.startPoll()
}

// handleKey dispatches a key press by screen state: help and quit first,
// then the solved screen, the two confirmations and the action keys.
func (m watchModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.recording && key != "ctrl+c" {
		m.notice = "recording the solve..."
		return m, nil
	}
	if key == "q" || key == "ctrl+c" || key == "esc" {
		m.quitting = true
		return m, tea.Quit
	}
	if key == "?" {
		m.showHelp = !m.showHelp
		return m, nil
	}
	switch {
	case m.starting || m.surrender:
		return m, nil
	case m.solved:
		return m.solvedKey(key)
	case m.confirm:
		return m.confirmSurrenderKey(key)
	case m.confirmReset:
		return m.confirmResetKey(key)
	}
	return m.actionKey(key)
}

// solvedKey handles a key on the solved screen: n leaves to start the
// recommended scenario, any other key quits.
func (m watchModel) solvedKey(key string) (tea.Model, tea.Cmd) {
	if key == "n" {
		if m.pick == nil {
			m.notice = "no recommended scenario; kubedrill list shows every scenario"
			return m, nil
		}
		pick := *m.pick
		m.startNext = &pick
	}
	m.quitting = true
	return m, tea.Quit
}

// confirmSurrenderKey handles the key after a first s: s surrenders, any
// other key cancels.
func (m watchModel) confirmSurrenderKey(key string) (tea.Model, tea.Cmd) {
	m.confirm = false
	if key != "s" {
		m.notice = "surrender cancelled"
		return m, nil
	}
	m.surrender = true
	m.notice = "surrendering..."
	return m, m.doSurrender()
}

// confirmResetKey handles the key after a first x: x starts the scenario
// again with a new seed, any other key cancels.
func (m watchModel) confirmResetKey(key string) (tea.Model, tea.Cmd) {
	m.confirmReset = false
	if key != "x" {
		m.notice = "reset cancelled"
		return m, nil
	}
	if m.examActive() {
		m.notice = errResetInExam.Error()
		return m, nil
	}
	m.starting = true
	m.notice = "resetting " + m.id + " with a new seed..."
	return m, m.doStart(m.id, 0)
}

// actionKey handles the keys of the live view before a solve.
func (m watchModel) actionKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "h":
		if m.hinting {
			m.notice = "a hint is already on its way"
			return m, nil
		}
		m.hinting = true
		return m, m.takeHint()
	case "s":
		m.confirm = true
	case "x":
		if m.examActive() {
			m.notice = errResetInExam.Error()
			return m, nil
		}
		m.confirmReset = true
	case "c":
		if m.polling {
			m.notice = "a check is already running"
			return m, nil
		}
		m.polling = true
		m.notice = ""
		return m, m.startPoll()
	case "n":
		m.notice = "n starts the next recommended scenario once this one is solved"
	case "w":
		m.allWhy = !m.allWhy
	}
	return m, nil
}

// whyWidth is the column the why lines wrap at: the terminal width when
// known, whyWidth otherwise.
func (m watchModel) whyWidth() int {
	if m.width <= 0 {
		return whyWidth
	}
	return m.width
}

// isNamespaceError reports whether err says the scenario namespace is gone
// or belongs to something else, which ends watch with the learner-facing
// text instead of a retry loop.
func isNamespaceError(err error) bool {
	return errors.Is(err, env.ErrNamespaceMissing) || errors.Is(err, env.ErrNamespaceForeign)
}

func (m watchModel) View() tea.View {
	return tea.NewView(m.render())
}

// displayNow is the clock the header uses: frozen at the solve time once the
// checks passed.
func (m watchModel) displayNow() time.Time {
	if m.solved && !m.frozen.IsZero() {
		return m.frozen
	}
	return m.now
}

// truncate cuts s to width visible columns with a trailing "..." when it
// is wider. Escape sequences do not count and a rune is never split; a cut
// through styled text ends with a reset.
func truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	keep, tail := width-3, "..."
	if width <= 3 {
		keep, tail = width, ""
	}
	head, _ := splitVisible(s, keep)
	if strings.Contains(head, "\x1b[") {
		head += "\x1b[0m"
	}
	return head + tail
}

// fit makes one check line fit the pane: below narrowWidth it wraps with a
// hanging indent, wider panes cut it.
func (m watchModel) fit(line string) []string {
	if m.width <= 0 {
		return []string{line}
	}
	if m.width < narrowWidth {
		cont := detailIndent
		if strings.HasPrefix(line, detailIndent) {
			cont = detailIndent + "  "
		}
		return m.wrapTo(line, cont)
	}
	return []string{truncate(line, m.width)}
}

// wrapTo wraps line at the pane width with cont as the hanging indent, then
// cuts any line still wider than the pane (a single token wider than the
// pane) with "...". Without a known width the line is returned whole.
func (m watchModel) wrapTo(line, cont string) []string {
	if m.width <= 0 {
		return []string{line}
	}
	out := hangingWrap(line, m.width, cont)
	for i, l := range out {
		out[i] = truncate(l, m.width)
	}
	return out
}

// writeWrapped writes line wrapped by wrapTo, one output line each.
func (m watchModel) writeWrapped(b *strings.Builder, line, cont string) {
	for _, l := range m.wrapTo(line, cont) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
}

// passedLine collapses the passed checks into one line naming them, cut to
// the terminal width (wrapped in a narrow pane).
func (m watchModel) passedLine(passed []grader.Result) string {
	names := make([]string, 0, len(passed))
	for _, r := range passed {
		names = append(names, r.ID)
	}
	line := fmt.Sprintf("%s %s: %s", m.styler(grader.StatusPass), plural(len(passed), "check"), strings.Join(names, ", "))
	return strings.Join(m.fit(line), "\n")
}

func (m watchModel) render() string {
	var b strings.Builder
	now := m.displayNow()
	b.WriteString(truncate(fmt.Sprintf("%s  [%s]  seed %d", m.title, m.id, m.seed), m.width))
	b.WriteByte('\n')
	status := fmt.Sprintf("elapsed %s   hints %d/%d", fmtDuration(now.Sub(m.started)), m.hints, m.totalHints)
	switch {
	case m.polling && !m.solved:
		status += fmt.Sprintf("   checking (%s)...", plural(m.checks, "check"))
	case !m.lastPoll.IsZero():
		status += fmt.Sprintf("   checked %s ago", fmtDuration(now.Sub(m.lastPoll)))
	}
	m.writeWrapped(&b, status, "  ")
	b.WriteByte('\n')
	switch {
	case m.pollErr != nil:
		fmt.Fprintf(&b, "[err ] grading failed: %s\n", m.pollErr)
	case m.grade == nil:
		b.WriteString("waiting for the first grading run...\n")
	default:
		shown := false
		hide := !m.reveal && m.hints == 0
		var passed []grader.Result
		for _, r := range m.grade.Results {
			if r.Status == grader.StatusPass {
				passed = append(passed, r)
				continue
			}
			withWhy := m.allWhy || !shown
			if withWhy && needsWhy(r) {
				shown = true
			}
			for _, line := range resultLinesAt(r, m.styler(r.Status), withWhy, hide, m.whyWidth()) {
				for _, l := range m.fit(line) {
					b.WriteString(l)
					b.WriteByte('\n')
				}
			}
		}
		if len(passed) > 0 {
			b.WriteString(m.passedLine(passed))
			b.WriteByte('\n')
		}
		b.WriteString(summaryLine(*m.grade))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if m.hint != "" {
		b.WriteString(wrap(m.hint, m.width))
		b.WriteString("\n\n")
	}
	if m.notice != "" {
		b.WriteString(wrap(m.notice, m.width))
		b.WriteString("\n\n")
	}
	switch {
	case m.showHelp:
		b.WriteString(m.keys().full(m.wrapTo))
	case m.solved:
		ep := m.solvedEp
		if ep == nil {
			ep = &journal.Episode{StartedAt: m.started, HintsUsed: m.hints}
		}
		m.writeWrapped(&b, solvedBanner(m.title, ep), "  ")
		m.writeWrapped(&b, fmt.Sprintf("Next: kubedrill solution %s (compare with the reference) | kubedrill list", m.id), "  ")
		keys := "?: help  any other key quits"
		if m.pick != nil {
			keys = fmt.Sprintf("n: debrief, then start %s (%s)  ", m.pick.ref, m.pick.reason) + keys
		}
		m.writeWrapped(&b, keys, "  ")
	case m.confirm:
		m.writeWrapped(&b, "press s again to surrender and show the solution; any other key cancels", "  ")
	case m.confirmReset:
		m.writeWrapped(&b, "press x again to reset "+m.id+" with a new seed; any other key cancels", "  ")
	default:
		b.WriteString(footer)
	}
	return b.String()
}

// keyBinding is one entry of the watch help: the key, what it does and the
// group the full help lists it under.
type keyBinding struct {
	key, desc, group string
}

type keyMap []keyBinding

// footer is the one-line key reminder under the live view; it fits a 100
// column pane and leaves s and n to the full help.
const footer = "c check  h hint  w why  x reset  ? help  q quit\n"

// keys lists the bindings for the current state; the w entry names what the
// toggle switches to and the ? entry what the next press does.
func (m watchModel) keys() keyMap {
	why := "why for every failing check"
	if m.allWhy {
		why = "why for the first failing check only"
	}
	help := "full help"
	if m.showHelp {
		help = "close help"
	}
	return keyMap{
		{key: "c", desc: "check now", group: "Checks"},
		{key: "w", desc: why, group: "Checks"},
		{key: "h", desc: "hint", group: "Assist"},
		{key: "s", desc: "solution (surrender, press twice)", group: "Assist"},
		{key: "x", desc: "reset with a new seed (press twice)", group: "Episode"},
		{key: "n", desc: "debrief, then the next recommended scenario (after a solve)", group: "Episode"},
		{key: "?", desc: help, group: "Episode"},
		{key: "q", desc: "quit (also esc, ctrl+c)", group: "Episode"},
	}
}

// helpIndent is the indent of a help entry's description, where a wrapped
// entry continues.
const helpIndent = "      "

// full lists every binding under its group, groups in first-seen order, as
// the bubbles help model does with ShowAll; every entry has the same two
// space indent and key column. wrap fits each entry to the pane with
// helpIndent as the hanging indent.
func (k keyMap) full(wrap func(line, cont string) []string) string {
	var order []string
	groups := map[string][]keyBinding{}
	for _, b := range k {
		if _, ok := groups[b.group]; !ok {
			order = append(order, b.group)
		}
		groups[b.group] = append(groups[b.group], b)
	}
	var sb strings.Builder
	for i, g := range order {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(g + "\n")
		for _, b := range groups[g] {
			for _, l := range wrap(fmt.Sprintf("  %-3s %s", b.key, b.desc), helpIndent) {
				sb.WriteString(l)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

// wrap breaks s at spaces so no line exceeds width visible columns.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapWords(strings.Join(strings.Fields(para), " "), width)...)
	}
	return strings.Join(out, "\n")
}

func (a *App) watchCommand() *cobra.Command {
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
			if !a.isTerminal || !a.isInputTerminal {
				return fmt.Errorf("watch needs a terminal on stdin and stdout; use kubedrill check in scripts and pipes")
			}
			s, err := a.learnerSession()
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
	cmd.Flags().DurationVar(&interval, "interval", defaultWatchInterval, "time between grading runs")
	return cmd
}

// sessionWatcher adapts the learner session to the watcher interface.
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
func (a *App) runWatch(parent context.Context, w watcher, ep *journal.Episode, title string, totalHints, checks int, interval time.Duration) error {
	for {
		fm, err := a.watchOnce(parent, w, ep, title, totalHints, checks, interval)
		if err != nil || fm == nil {
			return err
		}
		switch {
		case fm.fatal != nil:
			return fm.fatal
		case fm.solution != "":
			a.printf("episode of %s recorded as abandoned\n\n", fm.id)
			a.printf("%s", a.markdown(fm.solution))
			return nil
		case !fm.solved:
			a.println("Next: kubedrill check | kubedrill hint")
			return nil
		}
		solved := fm.solvedEp
		if solved == nil {
			solved = a.lastEndedOf(fm.id, fm.started)
		}
		if fm.width > 0 {
			a.paneWidth = fm.width
		}
		if _, err := a.afterSolve(parent, solved); err != nil {
			return err
		}
		if fm.startNext == nil {
			return nil
		}
		pick := *fm.startNext
		a.printf("starting %s: %s\n", pick.ref, pick.reason)
		next, rd, err := w.Start(parent, pick.ref, pick.seed)
		a.finishProgress()
		if err != nil {
			return fmt.Errorf("start %s: %w", pick.ref, err)
		}
		a.printf("started %s (seed %d) in namespace %s; kubedrill ticket prints the ticket\n", next.ScenarioID, next.Seed, rd.Namespace)
		ep, title, totalHints, checks = next, rd.Scenario.Meta.Title, len(rd.Hints), len(rd.Checks)
	}
}

// lastEndedOf returns the most recent ended episode when it belongs to
// scenario id and started at or after since: the solve a quit raced with,
// or nil.
func (a *App) lastEndedOf(id string, since time.Time) *journal.Episode {
	j, err := a.getJournal()
	if err != nil {
		return nil
	}
	last, err := lastEndedEpisode(j)
	if err != nil || last == nil || last.ScenarioID != id || last.StartedAt.Before(since) {
		return nil
	}
	return last
}

// examActive reports whether the journal's active episode is an exam
// before its deadline.
func (a *App) examActive() bool {
	j, err := a.getJournal()
	if err != nil {
		return false
	}
	cur, err := j.Current()
	return err == nil && a.examRunning(cur)
}

// spoilersRevealed reports whether the active episode has a hint or a
// diagnosis, after which a spoiler check shows its actual value.
func (a *App) spoilersRevealed() bool {
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

// watchOnce runs one watch program and returns its final model; while it
// runs, start progress lines go to the watch status line. teaOptions, set by
// tests, follow the context, output and input options.
func (a *App) watchOnce(parent context.Context, w watcher, ep *journal.Episode, title string, totalHints, checks int, interval time.Duration) (*watchModel, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m := newWatchModel(ctx, w, ep, title, totalHints, checks, interval, a.now())
	m.color = a.color
	m.styler = a.coloredTag
	m.next = a.nextPick
	m.examActive = a.examActive
	m.revealed = a.spoilersRevealed
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(a.stdout), tea.WithInput(a.rawStdin)}, a.teaOptions...)
	p := tea.NewProgram(m, opts...)
	a.progressSink = func(msg string) { p.Send(progressMsg(msg)) }
	final, err := p.Run()
	cancel()
	m.flight.close()
	a.progressSink = nil
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, tea.ErrInterrupted) {
		return nil, fmt.Errorf("watch: %w", err)
	}
	fm, ok := final.(watchModel)
	if !ok {
		return nil, nil
	}
	return &fm, nil
}
