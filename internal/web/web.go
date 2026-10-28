// Package web is the browser workspace of kubedrill web: a local HTTP server
// on the loopback interface that walks the learner through an assignment
// (meeting with the customer, quiz, learn, guided practice, DIY) with a
// terminal into the workstation container in the page. It serves embedded
// static files and a JSON API, asks a Backend for everything that touches
// the cluster or the journal, and keeps the stage progress in one JSON file.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

//go:embed static
var staticFiles embed.FS

// Stage is one step of an assignment.
type Stage string

// The stages of an assignment, in order. Practice and DIY run episodes;
// the others run in the page.
const (
	StageMeeting  Stage = "meeting"
	StageQuiz     Stage = "quiz"
	StageLearn    Stage = "learn"
	StagePractice Stage = "practice"
	StageDIY      Stage = "diy"
	// StageExam is the exam rehearsal: a random variant in a time box of
	// the scenario minutes, no hints, no guide, no live checks, one submit.
	StageExam Stage = "exam"
)

// ErrActiveElsewhere is returned by Backend.Start when another episode is
// active and the request did not ask to replace it.
var ErrActiveElsewhere = errors.New("another episode is active")

// Backend is what the server needs from kubedrill: the catalog, the
// content of an assignment, the active episode and its live checks, the
// episode actions and the workstation command of a terminal. Its methods
// are called one at a time.
type Backend interface {
	Catalog(ctx context.Context) (Catalog, error)
	Assignment(ctx context.Context, id string) (Assignment, error)
	Session(ctx context.Context, live bool) (Session, error)
	Next(ctx context.Context) (Next, error)
	Start(ctx context.Context, id string, stage Stage, replace bool, progress func(step string)) (Session, error)
	Validate(ctx context.Context) (Validation, error)
	Submit(ctx context.Context) (Validation, error)
	Hint(ctx context.Context) (Hint, error)
	Diagnose(ctx context.Context, text string) error
	Debrief(ctx context.Context, episodeID string) (Debrief, error)
	RecordDebrief(ctx context.Context, episodeID string, answers DebriefAnswers) error
	Terminal(ctx context.Context, name string) (*exec.Cmd, func(), error)
}

// Catalog is the curriculum as the home page shows it, with the exam
// domains the assignments map to and the titles of the concepts the
// assignments carry (the catalog filter shows them; an id without one
// shows as the id).
type Catalog struct {
	Levels   []Level   `json:"levels"`
	Exams    []Exam    `json:"exams"`
	Concepts []Concept `json:"concepts,omitempty"`
}

// Exam is one certification and the learner's coverage of its domains.
type Exam struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	Domains []ExamDomain `json:"domains"`
}

// ExamDomain is one exam domain: its weight, the assignments that train it
// and how many of them the learner solved.
type ExamDomain struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Weight      int      `json:"weight"`
	Assignments []string `json:"assignments"`
	Solved      int      `json:"solved"`
}

// Next is what the home page offers first: the active episode when there
// is one, otherwise the recommended assignment and why.
type Next struct {
	Active         *Active `json:"active,omitempty"`
	ScenarioID     string  `json:"scenarioId,omitempty"`
	Title          string  `json:"title,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Review         bool    `json:"review,omitempty"`
	Minutes        int     `json:"minutes,omitempty"`
	ReviewsPending int     `json:"reviewsPending"`
}

// Level is one curriculum level.
type Level struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Modules []Module `json:"modules"`
}

// Module is one learning plan: its assignments and how many more are
// planned.
type Module struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Summary     string           `json:"summary"`
	Assignments []AssignmentCard `json:"assignments"`
	Planned     int              `json:"planned"`
	Badge       bool             `json:"badge"`
	Pack        string           `json:"pack,omitempty"`
}

// AssignmentCard is one scenario on the home page.
type AssignmentCard struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Minutes    int           `json:"minutes"`
	Concepts   []string      `json:"concepts"`
	Exam       []string      `json:"exam"`
	Requires   []string      `json:"requires"`
	Simulation bool          `json:"simulation"`
	Status     string        `json:"status"`
	Mastery    Mastery       `json:"mastery"`
	Progress   StageProgress `json:"progress"`
}

// Mastery is the CLI's mastery rule for one assignment: clean solves among
// the last attempts and the distinct variants solved, against what mastery
// needs.
type Mastery struct {
	Attempts       int  `json:"attempts"`
	Solved         int  `json:"solved"`
	CleanInLast    int  `json:"cleanInLast"`
	Last           int  `json:"last"`
	Variants       int  `json:"variants"`
	VariantsSolved int  `json:"variantsSolved"`
	Mastered       bool `json:"mastered"`
}

// Assignment is the content of one scenario for the stages that need no
// episode: the simulation, the ticket and the story as HTML.
type Assignment struct {
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Level       string               `json:"level"`
	Module      string               `json:"module"`
	ModuleTitle string               `json:"moduleTitle"`
	Minutes     int                  `json:"minutes"`
	Concepts    []Concept            `json:"concepts"`
	Exam        []string             `json:"exam"`
	Variants    int                  `json:"variants"`
	Simulation  *scenario.Simulation `json:"simulation"`
	Ticket      string               `json:"ticket"`
	Story       string               `json:"story"`
	Progress    StageProgress        `json:"progress"`
	Mastery     Mastery              `json:"mastery"`
}

// Concept is one concept page: the id, the title, the one-line summary and
// the body as markdown (HTML once the server converts it).
type Concept struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Body    string `json:"body,omitempty"`
}

// Session is the active episode, when there is one, with its live checks.
type Session struct {
	Active     *Active `json:"active"`
	Checks     []Check `json:"checks"`
	Passed     bool    `json:"passed"`
	CheckError string  `json:"checkError,omitempty"`
}

// Active describes the active episode. Stage is practice or diy for an
// episode the workspace started and empty for one started elsewhere.
type Active struct {
	EpisodeID  string               `json:"episodeId"`
	ScenarioID string               `json:"scenarioId"`
	Title      string               `json:"title"`
	Stage      Stage                `json:"stage"`
	Namespace  string               `json:"namespace"`
	Ticket     string               `json:"ticket"`
	Story      string               `json:"story,omitempty"`
	StartedAt  time.Time            `json:"startedAt"`
	Minutes    int                  `json:"minutes"`
	Exam       bool                 `json:"exam,omitempty"`
	Deadline   *time.Time           `json:"deadline,omitempty"`
	Guide      []scenario.GuideStep `json:"guide"`
	Mentor     string               `json:"mentor"`
	HintsUsed  int                  `json:"hintsUsed"`
	HintsTotal int                  `json:"hintsTotal"`
	Hints      []string             `json:"hints"`
	Diagnosis  string               `json:"diagnosis"`
}

// Check is one check result as the page shows it; while the episode runs,
// a failing spoiler check carries neither its expected nor its actual value.
type Check struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Why         string `json:"why,omitempty"`
	Status      string `json:"status"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	Message     string `json:"message,omitempty"`
}

// Validation is the result of grading the active episode on request.
type Validation struct {
	EpisodeID  string  `json:"episodeId"`
	ScenarioID string  `json:"scenarioId"`
	Passed     bool    `json:"passed"`
	Outcome    string  `json:"outcome,omitempty"`
	Score      float64 `json:"score"`
	Checks     []Check `json:"checks"`
	RootCause  string  `json:"rootCause,omitempty"`
	Diagnosis  string  `json:"diagnosis,omitempty"`
	Solution   string  `json:"solution,omitempty"`
}

// Debrief is the summary of an ended episode, as kubedrill debrief prints
// it: the run, the trajectory from telemetry, the diagnosis next to the
// root cause, and the learner's stored answers.
type Debrief struct {
	EpisodeID        string         `json:"episodeId"`
	ScenarioID       string         `json:"scenarioId"`
	Title            string         `json:"title"`
	Outcome          string         `json:"outcome"`
	Seconds          int            `json:"seconds"`
	Minutes          int            `json:"minutes"`
	Hints            int            `json:"hints"`
	Telemetry        string         `json:"telemetry"`
	ReadsBeforeWrite int            `json:"readsBeforeWrite"`
	FirstWrite       string         `json:"firstWrite,omitempty"`
	FirstWriteKind   string         `json:"firstWriteKind,omitempty"`
	FixTargets       []string       `json:"fixTargets"`
	Verdict          string         `json:"verdict,omitempty"`
	Diagnosis        string         `json:"diagnosis,omitempty"`
	RootCause        string         `json:"rootCause,omitempty"`
	Matched          *bool          `json:"matched,omitempty"`
	Answers          DebriefAnswers `json:"answers"`
	Solution         string         `json:"solution,omitempty"`
	Stage            Stage          `json:"stage,omitempty"`
}

// DebriefAnswers are the learner's reflections on an episode.
type DebriefAnswers struct {
	Matched         *bool  `json:"matched,omitempty"`
	RuledOutEarlier string `json:"ruledOutEarlier"`
	NextTimeFirst   string `json:"nextTimeFirst"`
}

// Hint is one revealed mentor hint; the backend fills HTML with markdown
// and the server converts it. The same holds for the Ticket, Story, Mentor,
// Hints, guide Body and Solution fields of the other types.
type Hint struct {
	HTML  string `json:"html"`
	Index int    `json:"index"`
	Total int    `json:"total"`
}

// Deps are the collaborators of a Server. ProgressPath is the JSON file of
// the stage progress; Token is the secret the first request presents in
// the t query parameter; Log receives one line per failed request.
//
// StaticDir, when set, serves the page files from that directory instead
// of the embedded copy, so an edit shows on reload; Preview names the
// assignment a preview run opens, which the page reads from /api/meta.
type Deps struct {
	Backend      Backend
	ProgressPath string
	Token        string
	Log          io.Writer
	Now          func() time.Time
	StaticDir    string
	Preview      string
}

// Server serves the workspace. The zero value is not usable; use New.
type Server struct {
	d Deps
	// busy serializes the calls into the backend; a waiter whose request
	// ends (the tab closed) leaves the queue instead of running late.
	busy     chan struct{}
	progress *progressStore
	static   http.Handler

	terms     sync.WaitGroup
	termsCtx  context.Context
	stopTerms context.CancelFunc
}

// New returns a Server over d.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = io.Discard
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	var sub fs.FS = os.DirFS(d.StaticDir)
	if d.StaticDir == "" {
		var err error
		if sub, err = fs.Sub(staticFiles, "static"); err != nil {
			panic(err)
		}
	}
	termsCtx, stopTerms := context.WithCancel(context.Background())
	return &Server{
		d:         d,
		busy:      make(chan struct{}, 1),
		progress:  &progressStore{path: d.ProgressPath, now: d.Now},
		static:    http.FileServer(http.FS(sub)),
		termsCtx:  termsCtx,
		stopTerms: stopTerms,
	}
}

// Serve answers on ln until ctx ends, then shuts down within five seconds.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("serve %s: %w", ln.Addr(), err)
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		<-errc
		s.closeTerminals(15 * time.Second)
		return nil
	}
}

// closeTerminals ends every open terminal (Shutdown does not track the
// hijacked websockets) and waits up to limit for their cleanup, which
// removes the workstation containers.
func (s *Server) closeTerminals(limit time.Duration) {
	s.stopTerms()
	done := make(chan struct{})
	go func() { s.terms.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
		fmt.Fprintln(s.d.Log, "web: terminals still closing after", limit)
	}
}

const cookieName = "kubedrill_token"

// Handler routes the workspace. Every request must name a loopback host;
// every route but the token exchange needs the token cookie, and requests
// that change state need the X-Kubedrill header, which a cross-site form
// cannot send.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog", s.api(s.catalog))
	mux.HandleFunc("GET /api/assignment/{id}", s.api(s.assignment))
	mux.HandleFunc("GET /api/session", s.api(s.session))
	mux.HandleFunc("GET /api/next", s.api(s.next))
	mux.HandleFunc("GET /api/meta", s.api(s.meta))
	mux.HandleFunc("GET /api/debrief/{id}", s.api(s.debrief))
	mux.HandleFunc("POST /api/debrief/{id}", s.api(s.recordDebrief))
	mux.HandleFunc("POST /api/start", s.start)
	mux.HandleFunc("POST /api/validate", s.api(s.validate))
	mux.HandleFunc("POST /api/submit", s.api(s.submit))
	mux.HandleFunc("POST /api/hint", s.api(s.hint))
	mux.HandleFunc("POST /api/diagnose", s.api(s.diagnose))
	mux.HandleFunc("POST /api/progress", s.api(s.recordProgress))
	mux.HandleFunc("POST /api/onboarded", s.api(s.onboarded))
	mux.Handle("GET /ws/terminal", s.terminal())
	mux.Handle("GET /", s.static)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "kubedrill web answers only on 127.0.0.1 or localhost", http.StatusMisdirectedRequest)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
			if !s.tokenOK(t) {
				http.Error(w, "this link is from another kubedrill web run; open the link it printed last", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !s.tokenOK(c.Value) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "Open the link kubedrill web printed in your terminal; it carries the token of this run.")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Kubedrill") != "1" {
			http.Error(w, "missing X-Kubedrill header", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/vendor/") && s.d.StaticDir == "" {
			w.Header().Set("Cache-Control", "private, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "kubedrill web answers only its own page", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(t string) bool {
	return s.d.Token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.d.Token)) == 1
}

// contentSecurityPolicy keeps scripts to the page's own files, so a gap in
// the markdown or diagram sanitizers cannot run code that reaches the
// terminal. Styles allow inline attributes: xterm.js and mermaid set them.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// loopbackHost reports whether the Host header names the loopback
// interface, which keeps a DNS-rebinding page from reaching the server.
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	switch strings.Trim(h, "[]") {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// apiFunc handles one API call and returns the value to encode.
type apiFunc func(r *http.Request) (any, error)

// httpError carries a status for the API error response.
type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return e.err.Error() }
func (e *httpError) Unwrap() error { return e.err }

func badRequest(format string, args ...any) error {
	return &httpError{status: http.StatusBadRequest, err: fmt.Errorf(format, args...)}
}

// acquire waits for the backend; it reports false when ctx ends first.
func (s *Server) acquire(ctx context.Context) bool {
	select {
	case s.busy <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) release() { <-s.busy }

// api serializes the calls into the backend and encodes the result or the
// error as JSON ({"error": "..."}).
func (s *Server) api(fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.acquire(r.Context()) {
			return
		}
		v, err := fn(r)
		s.release()
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status := http.StatusInternalServerError
			var he *httpError
			switch {
			case errors.As(err, &he):
				status = he.status
			case errors.Is(err, ErrActiveElsewhere):
				status = http.StatusConflict
			}
			fmt.Fprintf(s.d.Log, "web: %s %s: %v\n", r.Method, r.URL.Path, err)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("decode request: %v", err)
	}
	return nil
}

func (s *Server) catalog(r *http.Request) (any, error) {
	c, err := s.d.Backend.Catalog(r.Context())
	if err != nil {
		return nil, err
	}
	all := s.progress.all()
	for i := range c.Levels {
		for j := range c.Levels[i].Modules {
			m := &c.Levels[i].Modules[j]
			m.Badge = len(m.Assignments) > 0
			for k := range m.Assignments {
				a := &m.Assignments[k]
				a.Progress = all[a.ID]
				if !a.Progress.DIY.Done {
					m.Badge = false
				}
			}
		}
	}
	return c, nil
}

func (s *Server) assignment(r *http.Request) (any, error) {
	a, err := s.d.Backend.Assignment(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	a.Progress = s.progress.all()[a.ID]
	a.Ticket, a.Story = toHTML(a.Ticket), toHTML(a.Story)
	for i := range a.Concepts {
		a.Concepts[i].Body = toHTML(a.Concepts[i].Body)
	}
	return a, nil
}

func (s *Server) session(r *http.Request) (any, error) {
	live := r.URL.Query().Get("live") == "1"
	sess, err := s.d.Backend.Session(r.Context(), live)
	if err != nil {
		return nil, err
	}
	s.tagStage(&sess)
	return sess, nil
}

// tagStage fills the stage of an active episode the workspace started and
// turns its markdown fields into HTML.
func (s *Server) tagStage(sess *Session) {
	a := sess.Active
	if a == nil {
		return
	}
	a.Stage = s.progress.stageOf(a.EpisodeID)
	a.Ticket = toHTML(a.Ticket)
	a.Story = toHTML(a.Story)
	a.Mentor = toHTML(a.Mentor)
	for i := range a.Hints {
		a.Hints[i] = toHTML(a.Hints[i])
	}
	for i := range a.Guide {
		a.Guide[i].Body = toHTML(a.Guide[i].Body)
	}
}

// start runs Backend.Start and streams its progress as newline-delimited
// JSON: one {"step": "..."} per long step, then {"session": ...} or
// {"error": "...", "status": n}. The page shows the steps while the
// namespace is prepared.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Stage   Stage  `json:"stage"`
		Replace bool   `json:"replace"`
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	fail := func(status int, err error) {
		fmt.Fprintf(s.d.Log, "web: POST /api/start: %v\n", err)
		_ = enc.Encode(map[string]any{"error": err.Error(), "status": status})
	}
	if err := decode(r, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fail(http.StatusBadRequest, err)
		return
	}
	if req.Stage != StagePractice && req.Stage != StageDIY && req.Stage != StageExam {
		w.WriteHeader(http.StatusBadRequest)
		fail(http.StatusBadRequest, fmt.Errorf("stage %q does not run an episode; want practice, diy or exam", req.Stage))
		return
	}
	if !s.acquire(r.Context()) {
		return
	}
	defer s.release()
	w.WriteHeader(http.StatusOK)
	flush()
	var wmu sync.Mutex
	progress := func(step string) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(map[string]string{"step": step})
		flush()
	}
	sess, err := s.d.Backend.Start(r.Context(), req.ID, req.Stage, req.Replace, progress)
	wmu.Lock()
	defer wmu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrActiveElsewhere) {
			status = http.StatusConflict
		}
		fail(status, err)
		return
	}
	if sess.Active != nil {
		if err := s.progress.tagEpisode(sess.Active.EpisodeID, req.Stage); err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
	}
	s.tagStage(&sess)
	_ = enc.Encode(map[string]any{"session": sess})
}

func (s *Server) next(r *http.Request) (any, error) {
	n, err := s.d.Backend.Next(r.Context())
	if err != nil {
		return nil, err
	}
	if n.Active != nil {
		sess := Session{Active: n.Active}
		s.tagStage(&sess)
	}
	return n, nil
}

// meta tells the page how the server runs and whether the welcome flow
// was done; a preview counts as done.
func (s *Server) meta(*http.Request) (any, error) {
	return map[string]any{"preview": s.d.Preview, "dev": s.d.StaticDir != "", "onboarded": s.d.Preview != "" || s.progress.onboarded()}, nil
}

// onboarded records the welcome flow as done, completed or skipped.
func (s *Server) onboarded(*http.Request) (any, error) {
	if err := s.progress.onboard(); err != nil {
		return nil, err
	}
	return map[string]bool{"onboarded": true}, nil
}

// debrief returns the debrief of an ended episode and records a solved one
// as its stage, so a run ended outside the page (kubedrill check, kubedrill
// submit, an expiry) still counts.
func (s *Server) debrief(r *http.Request) (any, error) {
	d, err := s.d.Backend.Debrief(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, badRequest("%v", err)
	}
	d = s.debriefView(d)
	if d.Outcome == "solved" && d.Stage != "" {
		if err := s.progress.complete(d.ScenarioID, d.Stage, 1, 1); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// debriefView adds the stage the workspace started the episode in and
// renders the walkthrough.
func (s *Server) debriefView(d Debrief) Debrief {
	d.Stage = s.progress.stageOf(d.EpisodeID)
	d.Solution = toHTML(d.Solution)
	return d
}

func (s *Server) recordDebrief(r *http.Request) (any, error) {
	var a DebriefAnswers
	if err := decode(r, &a); err != nil {
		return nil, err
	}
	if len(a.RuledOutEarlier) > 500 || len(a.NextTimeFirst) > 500 {
		return nil, badRequest("an answer is at most 500 characters")
	}
	if err := s.d.Backend.RecordDebrief(r.Context(), r.PathValue("id"), a); err != nil {
		return nil, badRequest("%v", err)
	}
	d, err := s.d.Backend.Debrief(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return s.debriefView(d), nil
}

func (s *Server) validate(r *http.Request) (any, error) {
	v, err := s.d.Backend.Validate(r.Context())
	if err != nil {
		return nil, badRequest("%v", err)
	}
	v.Solution = toHTML(v.Solution)
	if v.Passed {
		if stage := s.progress.stageOf(v.EpisodeID); stage != "" {
			if err := s.progress.complete(v.ScenarioID, stage, 1, 1); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

// submit ends an exam run with its one grade (or its expiry past the
// deadline) and records a pass as the exam stage.
func (s *Server) submit(r *http.Request) (any, error) {
	v, err := s.d.Backend.Submit(r.Context())
	if err != nil {
		return nil, badRequest("%v", err)
	}
	v.Solution = toHTML(v.Solution)
	if v.Passed {
		if err := s.progress.complete(v.ScenarioID, StageExam, 1, 1); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (s *Server) hint(r *http.Request) (any, error) {
	h, err := s.d.Backend.Hint(r.Context())
	if err != nil {
		return nil, badRequest("%v", err)
	}
	h.HTML = toHTML(h.HTML)
	return h, nil
}

func (s *Server) diagnose(r *http.Request) (any, error) {
	var req struct {
		Text string `json:"text"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := s.d.Backend.Diagnose(r.Context(), req.Text); err != nil {
		return nil, badRequest("%v", err)
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) recordProgress(r *http.Request) (any, error) {
	var req struct {
		ID    string `json:"id"`
		Stage Stage  `json:"stage"`
		Score int    `json:"score"`
		Total int    `json:"total"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	switch req.Stage {
	case StageMeeting, StageQuiz, StageLearn:
	default:
		return nil, badRequest("stage %q is recorded by validate, not by the page", req.Stage)
	}
	if req.ID == "" || req.Score < 0 || req.Total < 0 || req.Score > req.Total {
		return nil, badRequest("progress needs an id and 0 <= score <= total")
	}
	if err := s.progress.complete(req.ID, req.Stage, req.Score, req.Total); err != nil {
		return nil, err
	}
	return s.progress.all()[req.ID], nil
}
