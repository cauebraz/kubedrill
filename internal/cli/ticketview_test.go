package cli

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

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
	s, err := a.learnerSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := a.ticketLoaderFor(s)(50)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := f.journal.Current()
	if doc.Episode != cur.ID {
		t.Errorf("episode: got %q, want %q", doc.Episode, cur.ID)
	}
	mustContain(t, doc.Header, "001-hello-down (seed 1), namespace ")
	mustContain(t, doc.Body, "Checked by ")
	if out.Len() != 0 {
		t.Errorf("loader wrote to stdout: %q", out.String())
	}
	for _, l := range strings.Split(doc.Body, "\n") {
		if w := lipgloss.Width(l); w > 50 {
			t.Errorf("line wider than 50 columns (%d): %q", w, l)
		}
	}
}
