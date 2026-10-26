package watch

import "sync"

// inflight counts the commands running against the session so Run can
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
