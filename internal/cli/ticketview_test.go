package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/env"
)

type fakeTicket struct {
	episode string
	lines   int
	err     error
	widths  []int
}

func (f *fakeTicket) load(width int) (ticketDoc, error) {
	f.widths = append(f.widths, width)
	if f.err != nil {
		return ticketDoc{}, f.err
	}
	body := make([]string, f.lines)
	for i := range body {
		body[i] = fmt.Sprintf("line %d of %s", i+1, f.episode)
	}
	return ticketDoc{episode: f.episode, header: "header " + f.episode, body: strings.Join(body, "\n") + "\n"}, nil
}

func loadedTicketModel(t *testing.T, f *fakeTicket, width, height int) ticketModel {
	t.Helper()
	m := newTicketModel(f.load, time.Second)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	next, _ = next.Update(runCmd(t, cmd))
	return next.(ticketModel)
}

func TestTicketViewShowsTopAndFooter(t *testing.T) {
	f := &fakeTicket{episode: "ep1", lines: 30}
	m := loadedTicketModel(t, f, 60, 12)
	view := m.render()
	rows := strings.Split(view, "\n")
	if got, want := len(rows), 12; got != want {
		t.Fatalf("rows: got %d, want %d\n%s", got, want, view)
	}
	for _, want := range []string{"header ep1", "line 1 of ep1", "line 10 of ep1", "lines 1-10 of 30"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "line 11 of ep1") {
		t.Errorf("view shows line 11 in a 10-line body:\n%s", view)
	}
	if got, want := f.widths, []int{60}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("load widths: got %v, want %v", got, want)
	}
}

func TestTicketViewScrollKeysAndWheel(t *testing.T) {
	cases := []struct {
		name string
		msgs []tea.Msg
		want int
	}{
		{"j scrolls one", []tea.Msg{key("j")}, 1},
		{"k at top stays", []tea.Msg{key("k")}, 0},
		{"space pages", []tea.Msg{tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}}, 9},
		{"G goes to end", []tea.Msg{key("G")}, 20},
		{"G then g goes to top", []tea.Msg{key("G"), key("g")}, 0},
		{"wheel down", []tea.Msg{tea.MouseWheelMsg{Button: tea.MouseWheelDown}}, ticketWheelStep},
		{"wheel past end clamps", []tea.Msg{key("G"), tea.MouseWheelMsg{Button: tea.MouseWheelDown}}, 20},
		{"wheel up after down", []tea.Msg{tea.MouseWheelMsg{Button: tea.MouseWheelDown}, tea.MouseWheelMsg{Button: tea.MouseWheelUp}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m tea.Model = loadedTicketModel(t, &fakeTicket{episode: "ep1", lines: 30}, 60, 12)
			for _, msg := range tc.msgs {
				m, _ = m.Update(msg)
			}
			if got := m.(ticketModel).offset; got != tc.want {
				t.Errorf("offset: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTicketViewKeepsOffsetOnReloadAndResetsOnNewEpisode(t *testing.T) {
	f := &fakeTicket{episode: "ep1", lines: 30}
	var m tea.Model = loadedTicketModel(t, f, 60, 12)
	m, _ = m.Update(key("G"))
	m, cmd := m.Update(ticketTickMsg(time.Now()))
	m, _ = m.Update(runCmd(t, firstCmd(t, cmd)))
	if got, want := m.(ticketModel).offset, 20; got != want {
		t.Fatalf("offset after reload of the same episode: got %d, want %d", got, want)
	}
	f.episode = "ep2"
	m, cmd = m.Update(ticketTickMsg(time.Now()))
	m, _ = m.Update(runCmd(t, firstCmd(t, cmd)))
	tm := m.(ticketModel)
	if got, want := tm.offset, 0; got != want {
		t.Errorf("offset after a new episode: got %d, want %d", got, want)
	}
	if view := tm.render(); !strings.Contains(view, "header ep2") {
		t.Errorf("view lacks the new episode header:\n%s", view)
	}
}

func TestTicketViewNoEpisodeAndErrors(t *testing.T) {
	f := &fakeTicket{err: env.ErrNoActiveEpisode}
	m := loadedTicketModel(t, f, 60, 12)
	if view := m.render(); !strings.Contains(view, "no active episode") {
		t.Errorf("view without an episode: got %q, want it to name no active episode", view)
	}
	f.err = nil
	f.episode, f.lines = "ep1", 5
	var next tea.Model = m
	next, cmd := next.Update(ticketTickMsg(time.Now()))
	next, _ = next.Update(runCmd(t, firstCmd(t, cmd)))
	f.err = errors.New("journal unreadable")
	next, cmd = next.Update(ticketTickMsg(time.Now()))
	next, _ = next.Update(runCmd(t, firstCmd(t, cmd)))
	view := next.(ticketModel).render()
	for _, want := range []string{"line 5 of ep1", "all 5 lines shown", "reload failed: journal unreadable"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestTicketViewQuitAndAltScreen(t *testing.T) {
	m := loadedTicketModel(t, &fakeTicket{episode: "ep1", lines: 3}, 60, 12)
	if _, cmd := m.Update(key("q")); !isQuit(cmd) {
		t.Error("q: got no quit, want quit")
	}
	v := m.View()
	if !v.AltScreen || v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("view: got AltScreen %v MouseMode %v, want true and cell motion", v.AltScreen, v.MouseMode)
	}
}

func TestTicketViewResizeReloadsAtNewWidth(t *testing.T) {
	f := &fakeTicket{episode: "ep1", lines: 3}
	m := loadedTicketModel(t, f, 60, 12)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 70, Height: 12})
	next.Update(runCmd(t, cmd))
	if got, want := fmt.Sprint(f.widths), "[60 70]"; got != want {
		t.Errorf("load widths: got %s, want %s", got, want)
	}
}

// firstCmd returns the load command of a tick batch: the first command of
// the batch, run without the tick that follows it.
func firstCmd(t *testing.T, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("got nil command, want a batch")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 || batch[0] == nil {
		t.Fatalf("got %T, want a batch starting with a load", cmd())
	}
	return batch[0]
}

func TestTicketFollowNeedsTerminal(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run(t, "", "start", "001", "--seed", "1", "--no-shell"); code != 0 {
		t.Fatalf("start: exit %d", code)
	}
	code, _, errOut := f.run(t, "", "ticket", "--follow")
	if code != 1 || !strings.Contains(errOut, "ticket --follow needs a terminal") {
		t.Errorf("ticket --follow without a terminal: got exit %d err %q, want exit 1 naming the terminal", code, errOut)
	}
}

func TestTicketLoaderRendersActiveEpisode(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run(t, "", "start", "001", "--seed", "1", "--no-shell"); code != 0 {
		t.Fatalf("start: exit %d", code)
	}
	var out strings.Builder
	a := newApp(Options{
		Factories:  f.factories(),
		Stdout:     &out,
		Getenv:     func(k string) string { return f.env[k] },
		Getwd:      func() (string, error) { return f.root, nil },
		IsTerminal: true,
		Now:        f.nowFunc(),
	})
	s, err := a.learnerSession()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := a.ticketLoaderFor(s)(50)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := f.journal.Current()
	if doc.episode != cur.ID {
		t.Errorf("episode: got %q, want %q", doc.episode, cur.ID)
	}
	mustContain(t, doc.header, "001-hello-down (seed 1), namespace ")
	mustContain(t, doc.body, "Checked by ")
	if out.Len() != 0 {
		t.Errorf("loader wrote to stdout: %q", out.String())
	}
	for _, l := range strings.Split(doc.body, "\n") {
		if w := lipgloss.Width(l); w > 50 {
			t.Errorf("line wider than 50 columns (%d): %q", w, l)
		}
	}
}
