package render

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// progressQuiet is the step duration below which a progress line prints
// "done" without a time.
const progressQuiet = 100 * time.Millisecond

// Progress prints the step lines of a long command to one stream: Step
// prints "msg... " without a newline, and the next Step or Finish closes the
// line with "done" for a step under 0.1 s and "done (1.2s)" otherwise. While
// a diversion from Divert is in place, its sink receives the messages
// instead. It is safe for concurrent use.
type Progress struct {
	out io.Writer
	now func() time.Time

	mu    sync.Mutex
	open  bool
	start time.Time
	sink  func(string)
}

// NewProgress returns a Progress writing to out and timing the steps with
// now.
func NewProgress(out io.Writer, now func() time.Time) *Progress {
	return &Progress{out: out, now: now}
}

// Step closes an open line and prints "msg... ", or hands msg to the
// diversion sink.
func (p *Progress) Step(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sink := p.sink; sink != nil {
		sink(msg)
		return
	}
	p.endLocked(doneStatus(p.now().Sub(p.start)))
	fmt.Fprintf(p.out, "%s... ", msg)
	p.open = true
	p.start = p.now()
}

// Divert sends the step messages to sink until the returned restore runs,
// which puts back the previous destination.
func (p *Progress) Divert(sink func(string)) (restore func()) {
	p.mu.Lock()
	prev := p.sink
	p.sink = sink
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.sink = prev
		p.mu.Unlock()
	}
}

// Finish closes an open line with "done" or "done (1.2s)".
func (p *Progress) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLocked(doneStatus(p.now().Sub(p.start)))
}

// End closes an open line with status, or with a bare newline when status
// is empty (the step did not finish).
func (p *Progress) End(status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLocked(status)
}

func (p *Progress) endLocked(status string) {
	if !p.open {
		return
	}
	p.open = false
	fmt.Fprintln(p.out, status)
}

func doneStatus(d time.Duration) string {
	if d < progressQuiet {
		return "done"
	}
	return fmt.Sprintf("done (%.1fs)", d.Seconds())
}
