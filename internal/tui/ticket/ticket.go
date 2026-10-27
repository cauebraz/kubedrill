// Package ticket is the Bubble Tea model of kubedrill ticket --follow: the
// ticket of the active episode in its own scrollable screen, and with s the
// scenario's story, re-read every interval through a Loader so a reset or a
// new start in another pane shows up without a keystroke.
package ticket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/render"
)

// DefaultInterval is the time between two reads of the ticket.
const DefaultInterval = 2 * time.Second

const (
	wheelStep   = 3
	chromeLines = 2
)

// Doc is one rendering of the active episode's ticket at a width: Episode
// is the journal ID of the episode, so a reset or a new start scrolls back
// to the top; Header is the line above the scrolled Body. Story is the
// rendered story of the scenario, empty when it has none; the s key
// switches the pane between Body and Story.
type Doc struct {
	Episode string
	Header  string
	Body    string
	Story   string
}

// Loader renders the ticket of the active episode at width columns; an
// error matching env.ErrNoActiveEpisode shows the "no active episode" text.
type Loader func(width int) (Doc, error)

type (
	tickMsg   time.Time
	loadedMsg struct {
		doc   Doc
		width int
		err   error
	}
)

// Model is the Bubble Tea model of kubedrill ticket --follow. Scrolling
// never depends on what happens in the shell pane.
type Model struct {
	load     Loader
	interval time.Duration
	width    int
	height   int
	doc      Doc
	lines    []string
	offset   int
	err      error
	loading  bool
	loaded   bool
	story    bool
}

// New is the ticket model reading the ticket through load every interval.
func New(load Loader, interval time.Duration) Model {
	return Model{load: load, interval: interval}
}

// Run runs m on the terminal streams out and in until the learner quits or
// ctx ends; extra, set by tests, follows the context, output and input
// options.
func Run(ctx context.Context, m Model, out io.Writer, in io.Reader, extra ...tea.ProgramOption) error {
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(out), tea.WithInput(in)}, extra...)
	_, err := tea.NewProgram(m, opts...).Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, tea.ErrInterrupted) {
		return fmt.Errorf("ticket: %w", err)
	}
	return nil
}

// Init starts the reload clock; the first load waits for the window size.
func (m Model) Init() tea.Cmd {
	return m.tick()
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) startLoad() (Model, tea.Cmd) {
	if m.loading || m.width <= 0 {
		return m, nil
	}
	m.loading = true
	load, width := m.load, m.width
	return m, func() tea.Msg {
		doc, err := load(width)
		return loadedMsg{doc: doc, width: width, err: err}
	}
}

// Update applies a key, a wheel turn, a window size, a tick or a loaded
// ticket.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		resized := msg.Width != m.width
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		if resized {
			return m.startLoad()
		}
		return m, nil
	case tickMsg:
		m, cmd := m.startLoad()
		return m, tea.Batch(cmd, m.tick())
	case loadedMsg:
		m.loading = false
		if msg.width != m.width {
			return m.startLoad()
		}
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		if msg.doc.Episode != m.doc.Episode {
			m.offset = 0
			m.story = false
		}
		m.doc = msg.doc
		m.setLines()
		m.loaded = true
		m.clamp()
		return m, nil
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-wheelStep)
		case tea.MouseWheelDown:
			m.scroll(wheelStep)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m Model) handleKey(key string) (tea.Model, tea.Cmd) {
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
	case "s":
		if m.doc.Story != "" {
			m.story = !m.story
			m.offset = 0
			m.setLines()
		}
	}
	return m, nil
}

// setLines splits the text on screen into lines: the story when it is
// selected and the doc has one, the ticket otherwise.
func (m *Model) setLines() {
	if m.doc.Story == "" {
		m.story = false
	}
	text := m.doc.Body
	if m.story {
		text = m.doc.Story
	}
	m.lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
	m.clamp()
}

// bodyHeight is the number of ticket lines on screen: the pane height less
// the header and the footer.
func (m Model) bodyHeight() int {
	return max(m.height-chromeLines, 1)
}

func (m Model) maxOffset() int {
	return max(len(m.lines)-m.bodyHeight(), 0)
}

func (m *Model) scroll(n int) {
	m.offset += n
	m.clamp()
}

func (m *Model) clamp() {
	m.offset = min(max(m.offset, 0), m.maxOffset())
}

// View draws the header, the visible ticket lines and the footer on the
// alternate screen with mouse wheel reporting.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) render() string {
	var b strings.Builder
	switch {
	case m.err != nil && errors.Is(m.err, env.ErrNoActiveEpisode):
		b.WriteString(render.Wrap("no active episode; kubedrill start <ref> opens one and this pane shows its ticket", m.width))
		return b.String()
	case m.err != nil && !m.loaded:
		b.WriteString(render.Wrap("ticket: "+m.err.Error(), m.width))
		return b.String()
	case !m.loaded:
		return "loading the ticket..."
	}
	header := m.doc.Header
	if m.story {
		header = "story of " + header
	}
	b.WriteString(render.Truncate(render.StyleBold.Render(header), m.width))
	b.WriteByte('\n')
	end := min(m.offset+m.bodyHeight(), len(m.lines))
	shown := 0
	for _, l := range m.lines[m.offset:end] {
		b.WriteString(render.Truncate(l, m.width))
		b.WriteByte('\n')
		shown++
	}
	for ; shown < m.bodyHeight(); shown++ {
		b.WriteByte('\n')
	}
	b.WriteString(render.Truncate(render.StyleSkip.Render(m.footer(end)), m.width))
	return b.String()
}

// footer names the visible lines, the scroll keys and s when the doc has a
// story; the shell pane keeps the keyboard, so the wheel is the first way
// named.
func (m Model) footer(end int) string {
	pos := fmt.Sprintf("lines %d-%d of %d", min(m.offset+1, len(m.lines)), end, len(m.lines))
	if m.maxOffset() == 0 {
		pos = "all " + render.Plural(len(m.lines), "line") + " shown"
	}
	if m.err != nil {
		pos += "; reload failed: " + m.err.Error()
	}
	switch {
	case m.story:
		return pos + "  wheel scrolls; click here for s (ticket) j/k space b g G"
	case m.doc.Story != "":
		return pos + "  wheel scrolls; click here for s (story) j/k space b g G"
	}
	return pos + "  wheel scrolls; click here for j/k space b g G"
}
