package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/env"
)

const (
	defaultTicketInterval = 2 * time.Second
	ticketWheelStep       = 3
	ticketChromeLines     = 2
)

// ticketDoc is one rendering of the active episode's ticket at a width.
// episode is the journal ID of the episode, so a reset or a new start
// scrolls back to the top.
type ticketDoc struct {
	episode string
	header  string
	body    string
}

// ticketLoader renders the ticket of the active episode at width columns.
type ticketLoader func(width int) (ticketDoc, error)

type (
	ticketTickMsg   time.Time
	ticketLoadedMsg struct {
		doc   ticketDoc
		width int
		err   error
	}
)

// ticketModel is the Bubble Tea model of kubedrill ticket --follow: the
// ticket in its own scrollable screen, re-read every interval so a reset or
// a new start in another pane shows up without a keystroke. Scrolling never
// depends on what happens in the shell pane.
type ticketModel struct {
	load     ticketLoader
	interval time.Duration
	width    int
	height   int
	doc      ticketDoc
	lines    []string
	offset   int
	err      error
	loading  bool
	loaded   bool
}

func newTicketModel(load ticketLoader, interval time.Duration) ticketModel {
	return ticketModel{load: load, interval: interval}
}

func (m ticketModel) Init() tea.Cmd {
	return m.tick()
}

func (m ticketModel) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return ticketTickMsg(t) })
}

func (m ticketModel) startLoad() (ticketModel, tea.Cmd) {
	if m.loading || m.width <= 0 {
		return m, nil
	}
	m.loading = true
	load, width := m.load, m.width
	return m, func() tea.Msg {
		doc, err := load(width)
		return ticketLoadedMsg{doc: doc, width: width, err: err}
	}
}

func (m ticketModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		resized := msg.Width != m.width
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		if resized {
			return m.startLoad()
		}
		return m, nil
	case ticketTickMsg:
		m, cmd := m.startLoad()
		return m, tea.Batch(cmd, m.tick())
	case ticketLoadedMsg:
		m.loading = false
		if msg.width != m.width {
			return m.startLoad()
		}
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		if msg.doc.episode != m.doc.episode {
			m.offset = 0
		}
		m.doc = msg.doc
		m.lines = strings.Split(strings.TrimRight(msg.doc.body, "\n"), "\n")
		m.loaded = true
		m.clamp()
		return m, nil
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-ticketWheelStep)
		case tea.MouseWheelDown:
			m.scroll(ticketWheelStep)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m ticketModel) handleKey(key string) (tea.Model, tea.Cmd) {
	page := max(m.bodyHeight()-1, 1)
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.scroll(1)
	case "k", "up":
		m.scroll(-1)
	case "space", "f", "pgdown", "ctrl+d":
		m.scroll(page)
	case "b", "pgup", "ctrl+u":
		m.scroll(-page)
	case "g", "home":
		m.offset = 0
	case "G", "end":
		m.offset = m.maxOffset()
	}
	return m, nil
}

// bodyHeight is the number of ticket lines on screen: the pane height less
// the header and the footer.
func (m ticketModel) bodyHeight() int {
	return max(m.height-ticketChromeLines, 1)
}

func (m ticketModel) maxOffset() int {
	return max(len(m.lines)-m.bodyHeight(), 0)
}

func (m *ticketModel) scroll(n int) {
	m.offset += n
	m.clamp()
}

func (m *ticketModel) clamp() {
	m.offset = min(max(m.offset, 0), m.maxOffset())
}

func (m ticketModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m ticketModel) render() string {
	var b strings.Builder
	switch {
	case m.err != nil && errors.Is(m.err, env.ErrNoActiveEpisode):
		b.WriteString(wrap("no active episode; kubedrill start <ref> opens one and this pane shows its ticket", m.width))
		return b.String()
	case m.err != nil && !m.loaded:
		b.WriteString(wrap("ticket: "+m.err.Error(), m.width))
		return b.String()
	case !m.loaded:
		return "loading the ticket..."
	}
	b.WriteString(truncate(styleBold.Render(m.doc.header), m.width))
	b.WriteByte('\n')
	end := min(m.offset+m.bodyHeight(), len(m.lines))
	shown := 0
	for _, l := range m.lines[m.offset:end] {
		b.WriteString(truncate(l, m.width))
		b.WriteByte('\n')
		shown++
	}
	for ; shown < m.bodyHeight(); shown++ {
		b.WriteByte('\n')
	}
	b.WriteString(truncate(styleSkip.Render(m.footer(end)), m.width))
	return b.String()
}

// footer names the visible lines and the scroll keys; the shell pane keeps
// the keyboard, so the wheel is the first way named.
func (m ticketModel) footer(end int) string {
	pos := fmt.Sprintf("lines %d-%d of %d", min(m.offset+1, len(m.lines)), end, len(m.lines))
	if m.maxOffset() == 0 {
		pos = "all " + plural(len(m.lines), "line") + " shown"
	}
	if m.err != nil {
		pos += "; reload failed: " + m.err.Error()
	}
	return pos + "  wheel scrolls; click here for j/k space b g G"
}

// ticketLoaderFor renders the ticket of the active episode of s the way
// kubedrill ticket prints it, wrapped at the pane width.
func (a *App) ticketLoaderFor(s *env.Session) ticketLoader {
	return func(width int) (ticketDoc, error) {
		ep, rd, err := s.Current()
		if err != nil {
			return ticketDoc{}, err
		}
		var buf bytes.Buffer
		r := *a
		r.stdout = &buf
		r.paneWidth = width
		r.printTicket(ep, rd)
		return ticketDoc{
			episode: ep.ID,
			header:  fmt.Sprintf("%s (seed %d), namespace %s", ep.ScenarioID, ep.Seed, rd.Namespace),
			body:    buf.String(),
		}, nil
	}
}

// runTicketFollow runs the ticket view until the learner quits or ctx ends.
func (a *App) runTicketFollow(ctx context.Context, s *env.Session, interval time.Duration) error {
	m := newTicketModel(a.ticketLoaderFor(s), interval)
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(a.stdout), tea.WithInput(a.rawStdin)}, a.teaOptions...)
	_, err := tea.NewProgram(m, opts...).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, tea.ErrInterrupted) {
		return fmt.Errorf("ticket: %w", err)
	}
	return nil
}
