package cli

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/tui/ticket"
)

// ticketLoaderFor renders the ticket of the active episode of s the way
// kubedrill ticket prints it, wrapped at the pane width. The rendering is
// kept until the episode or the width changes, so a tick on the same
// episode reads only the active episode, not the whole journal.
func (a *app) ticketLoaderFor(s *env.Session) ticket.Loader {
	var (
		mu        sync.Mutex
		last      ticket.Doc
		lastWidth int
	)
	return func(width int) (ticket.Doc, error) {
		ep, rd, err := s.Current()
		if err != nil {
			return ticket.Doc{}, err
		}
		mu.Lock()
		defer mu.Unlock()
		if last.Episode == ep.ID && lastWidth == width {
			return last, nil
		}
		var buf bytes.Buffer
		p := a.term.PrinterAt(width)
		p.Out = &buf
		writeTicket(p, ep, rd, a.expectedLine(ep, rd.Scenario))
		last, lastWidth = ticket.Doc{
			Episode: ep.ID,
			Header:  fmt.Sprintf("%s (seed %d), namespace %s", ep.ScenarioID, ep.Seed, rd.Namespace),
			Body:    buf.String(),
		}, width
		return last, nil
	}
}

// runTicketFollow runs the ticket view until the learner quits or ctx ends.
func (a *app) runTicketFollow(ctx context.Context, s *env.Session, interval time.Duration) error {
	return ticket.Run(ctx, ticket.New(a.ticketLoaderFor(s), interval), a.stdout, a.rawStdin, a.teaOptions...)
}
