package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// Init issues the first poll and starts the clock.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.startPoll(), m.tick())
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.clock, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// track wraps a command so Run can wait for it after cancelling the
// context. The counter is raised inside the command because Bubble Tea may
// start a command after the program stops; such a command returns nil.
func (m Model) track(cmd func() tea.Msg) tea.Cmd {
	f := m.flight
	return func() tea.Msg {
		if !f.enter() {
			return nil
		}
		defer f.leave()
		return cmd()
	}
}

func (m Model) startPoll() tea.Cmd {
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
// runs does not lose the solve; Run waits for it before returning.
func (m Model) record() tea.Cmd {
	ctx, w, timeout, gen := context.WithoutCancel(m.ctx), m.w, m.pollTimeout, m.gen
	return m.track(func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		g, ep, err := w.Grade(rctx)
		return recordedMsg{gen: gen, grade: g, ep: ep, err: err}
	})
}

func (m Model) takeHint() tea.Cmd {
	w, gen := m.w, m.gen
	return m.track(func() tea.Msg {
		text, index, total, err := w.Hint()
		return hintMsg{gen: gen, text: text, index: index, total: total, err: err}
	})
}

func (m Model) doSurrender() tea.Cmd {
	ctx, w, timeout, gen := m.ctx, m.w, m.pollTimeout, m.gen
	return m.track(func() tea.Msg {
		sctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		sol, err := w.Surrender(sctx)
		return surrenderMsg{gen: gen, solution: sol, err: err}
	})
}

func (m Model) doStart(ref string, seed int64) tea.Cmd {
	ctx, w, gen := m.ctx, m.w, m.gen
	return m.track(func() tea.Msg {
		ep, rd, err := w.Start(ctx, ref, seed)
		return startedMsg{gen: gen, ep: ep, rd: rd, err: err}
	})
}

// Update applies a message: a key, a tick, a window size, or the result of a
// poll, record, hint, surrender or start command.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if msg.gen != m.gen {
			return m, nil
		}
		m.hinting = false
		if msg.err != nil {
			switch {
			case errors.Is(msg.err, env.ErrNoMoreHints):
				m.notice = fmt.Sprintf("all %d hints shown; s twice surrenders and shows the solution", msg.total)
			case errors.Is(msg.err, env.ErrDiagnosisRequired):
				m.notice = m.diagnosisNeeded("the last hint")
			default:
				m.notice = msg.err.Error()
			}
			return m, nil
		}
		m.hints = msg.index
		m.totalHints = msg.total
		m.hint = m.hintLabel(msg.index, msg.total)
		m.hintText = strings.TrimSpace(msg.text)
		m.notice = ""
		return m, nil
	case surrenderMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.err != nil {
			m.surrender = false
			if errors.Is(msg.err, env.ErrDiagnosisRequired) {
				m.notice = m.diagnosisNeeded("the solution")
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
		if msg.gen != m.gen {
			return m, nil
		}
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

func (m Model) onPoll(msg pollMsg) (tea.Model, tea.Cmd) {
	m.polling = false
	m.lastPoll = m.now
	m.nextPoll = m.now.Add(m.interval)
	if msg.err != nil {
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

func (m Model) onRecorded(msg recordedMsg) (tea.Model, tea.Cmd) {
	m.recording = false
	if msg.err != nil {
		if isNamespaceError(msg.err) {
			m.solved = false
			m.frozen = time.Time{}
			m.pollErr = msg.err
			m.nextPoll = m.now
			return m, nil
		}
		m.solved = false
		m.frozen = time.Time{}
		m.notice = "recording the solve failed: " + msg.err.Error() + "; the next passing check records it"
		m.nextPoll = m.now
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
func (m Model) reload(ep *journal.Episode, rd *scenario.Rendered) (tea.Model, tea.Cmd) {
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
	m.hintText = ""
	m.hinting = false
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
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
func (m Model) solvedKey(key string) (tea.Model, tea.Cmd) {
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
func (m Model) confirmSurrenderKey(key string) (tea.Model, tea.Cmd) {
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
func (m Model) confirmResetKey(key string) (tea.Model, tea.Cmd) {
	m.confirmReset = false
	if key != "x" {
		m.notice = "reset cancelled"
		return m, nil
	}
	if m.hinting {
		m.notice = noticeResetWhileHinting
		return m, nil
	}
	if m.examActive() {
		m.notice = m.resetInExam
		return m, nil
	}
	m.starting = true
	m.notice = "resetting " + m.id + " with a new seed..."
	return m, m.doStart(m.id, 0)
}

// actionKey handles the keys of the live view before a solve.
func (m Model) actionKey(key string) (tea.Model, tea.Cmd) {
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
			m.notice = m.resetInExam
			return m, nil
		}
		if m.hinting {
			m.notice = noticeResetWhileHinting
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
// known, render.WhyWidth otherwise.
func (m Model) whyWidth() int {
	if m.width <= 0 {
		return render.WhyWidth
	}
	return m.width
}

// View draws the header, the checks, the hint and notice lines and the keys.
func (m Model) View() tea.View {
	return tea.NewView(m.render())
}

// displayNow is the clock the header uses: frozen at the solve time once the
// checks passed.
func (m Model) displayNow() time.Time {
	if m.solved && !m.frozen.IsZero() {
		return m.frozen
	}
	return m.now
}

// fit makes one check line fit the pane: below narrowWidth it wraps with a
// hanging indent, wider panes cut it.
func (m Model) fit(line string) []string {
	if m.width <= 0 {
		return []string{line}
	}
	if m.width < narrowWidth {
		cont := render.DetailIndent
		if strings.HasPrefix(line, render.DetailIndent) {
			cont = render.DetailIndent + "  "
		}
		return m.wrapTo(line, cont)
	}
	return []string{render.Truncate(line, m.width)}
}

// wrapTo wraps line at the pane width with cont as the hanging indent, then
// cuts any line still wider than the pane (a single token wider than the
// pane) with "...". Without a known width the line is returned whole.
func (m Model) wrapTo(line, cont string) []string {
	if m.width <= 0 {
		return []string{line}
	}
	out := render.HangingWrap(line, m.width, cont)
	for i, l := range out {
		out[i] = render.Truncate(l, m.width)
	}
	return out
}

// writeWrapped writes line wrapped by wrapTo, one output line each.
func (m Model) writeWrapped(b *strings.Builder, line, cont string) {
	for _, l := range m.wrapTo(line, cont) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
}

// passedLine collapses the passed checks into one line naming them, cut to
// the terminal width (wrapped in a narrow pane).
func (m Model) passedLine(passed []grader.Result) string {
	names := make([]string, 0, len(passed))
	for _, r := range passed {
		names = append(names, r.ID)
	}
	line := fmt.Sprintf("%s %s: %s", m.styler(grader.StatusPass), render.Plural(len(passed), "check"), strings.Join(names, ", "))
	return strings.Join(m.fit(line), "\n")
}

func (m Model) render() string {
	var b strings.Builder
	now := m.displayNow()
	b.WriteString(render.Truncate(fmt.Sprintf("%s  [%s]  seed %d", m.title, m.id, m.seed), m.width))
	b.WriteByte('\n')
	status := fmt.Sprintf("elapsed %s   hints %d/%d", render.Duration(now.Sub(m.started)), m.hints, m.totalHints)
	switch {
	case m.polling && !m.solved:
		status += fmt.Sprintf("   checking (%s)...", render.Plural(m.checks, "check"))
	case !m.lastPoll.IsZero():
		status += fmt.Sprintf("   checked %s ago", render.Duration(now.Sub(m.lastPoll)))
	}
	m.writeWrapped(&b, status, "  ")
	b.WriteByte('\n')
	switch {
	case m.pollErr != nil && isNamespaceError(m.pollErr):
		m.writeWrapped(&b, "[err ] "+namespaceErrorText(m.pollErr), "       ")
		b.WriteByte('\n')
		m.writeWrapped(&b, "       x twice resets the episode here; this pane follows a kubedrill reset run in the shell", "       ")
		b.WriteByte('\n')
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
			if withWhy && render.NeedsWhy(r) {
				shown = true
			}
			for _, line := range render.ResultLinesAt(r, m.styler(r.Status), withWhy, hide, m.whyWidth()) {
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
		b.WriteString(render.SummaryLine(*m.grade))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if m.hint != "" {
		b.WriteString(render.Wrap(m.hint+":", m.width))
		b.WriteByte('\n')
		b.WriteString(strings.Trim(m.markdown(m.hintText, m.width), "\n"))
		b.WriteString("\n\n")
	}
	if m.notice != "" {
		b.WriteString(render.Wrap(m.notice, m.width))
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
		m.writeWrapped(&b, render.SolvedBanner(m.title, ep), "  ")
		m.writeWrapped(&b, fmt.Sprintf("Next: kubedrill solution %s (compare with the reference) | kubedrill list", m.id), "  ")
		keys := "?: help  any other key quits"
		if m.pick != nil {
			keys = fmt.Sprintf("n: debrief, then start %s (%s)  ", m.pick.Ref, m.pick.Reason) + keys
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

const noticeResetWhileHinting = "a hint is on its way; press x again once it shows"

// footer is the one-line key reminder under the live view; it fits a 100
// column pane and leaves s and n to the full help.
const footer = "c check  h hint  w why  x reset  ? help  q quit\n"

// keys lists the bindings for the current state; the w entry names what the
// toggle switches to and the ? entry what the next press does.
func (m Model) keys() keyMap {
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
