package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/telemetry"
	"github.com/cauebraz/kubedrill/internal/web"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

// defaultWebPort is the port kubedrill web listens on unless --port says
// otherwise.
const defaultWebPort = 8642

// webProgressFile, under the journal directory, holds the stage progress
// of the browser workspace.
const webProgressFile = "web-progress.json"

func (a *app) webCommand() *cobra.Command {
	var o webOptions
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the browser workspace on localhost: meeting, quiz, learn, guided practice and DIY",
		Long: "web runs the doctor and up like kubedrill without a command, builds the\n" +
			"workstation, then serves the browser workspace on 127.0.0.1 and opens it.\n" +
			"Each assignment walks through a meeting with the customer, a quiz, the\n" +
			"learn page, a guided practice run and a DIY run on another variant, with a\n" +
			"terminal into the workstation in the page. The link it prints carries a\n" +
			"token; Ctrl-C stops the server. --preview <ref> skips the cluster and opens\n" +
			"one assignment, reloading its files on every request, for authors.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runWeb(cmd.Context(), o)
		},
	}
	cmd.Flags().IntVar(&o.port, "port", defaultWebPort, "port on 127.0.0.1")
	cmd.Flags().BoolVar(&o.noOpen, "no-open", false, "print the link without opening a browser")
	cmd.Flags().StringVar(&o.preview, "preview", "", "open one assignment without the cluster and reload its files on every request (authoring)")
	cmd.Flags().StringVar(&o.staticDir, "static-dir", "", "serve the page files from this directory instead of the binary (front-end development)")
	return cmd
}

// webOptions are the flags of kubedrill web.
type webOptions struct {
	port      int
	noOpen    bool
	preview   string
	staticDir string
}

func (a *app) runWeb(ctx context.Context, o webOptions) error {
	if a.insideWorkstation() {
		return errors.New("kubedrill web runs on the host; exit the workstation first")
	}
	if o.port < 1 || o.port > 65535 {
		return fmt.Errorf("port must be 1 to 65535, got %d", o.port)
	}
	if o.staticDir != "" {
		if st, err := os.Stat(filepath.Join(o.staticDir, "index.html")); err != nil || st.IsDir() {
			return fmt.Errorf("--static-dir %s: no index.html there; point it at internal/web/static", o.staticDir)
		}
	}
	fragment := ""
	if o.preview != "" {
		cat, err := a.getCatalog()
		if err != nil {
			return err
		}
		sc, err := cat.Find(o.preview)
		if err != nil {
			return err
		}
		o.preview = sc.Meta.ID
		fragment = "#/a/" + sc.Meta.ID + "/meeting"
	} else if err := a.prepareWebCluster(ctx); err != nil {
		return err
	}

	j, err := a.getJournal()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.port)))
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:%d: %w; another kubedrill web may be running, or pick a port with --port", o.port, err)
	}
	token, err := webToken()
	if err != nil {
		ln.Close()
		return err
	}
	srv := web.New(web.Deps{
		Backend:      &webBackend{a: a, reload: o.preview != ""},
		ProgressPath: filepath.Join(j.Dir(), webProgressFile),
		Token:        token,
		Log:          a.stderr,
		StaticDir:    o.staticDir,
		Preview:      o.preview,
	})
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s%s", o.port, token, fragment)
	a.println()
	if o.preview != "" {
		a.printf("kubedrill web preview of %s: %s\n", o.preview, url)
		a.println("Edits to its files show on reload; the practice and DIY runs need the cluster (kubedrill up).")
	} else {
		a.printf("kubedrill web is ready: %s\n", url)
		a.println("Ctrl-C stops it; your progress stays in the journal.")
	}
	if !o.noOpen && a.getenv("KUBEDRILL_NO_BROWSER") != "1" {
		openBrowser(url)
	}
	return srv.Serve(ctx, ln)
}

// prepareWebCluster runs the doctor, the recreate offer and up, building
// the workstation meanwhile, as the default run does.
func (a *app) prepareWebCluster(ctx context.Context) error {
	tools, err := a.runDoctor(ctx)
	if err != nil {
		return err
	}
	for _, t := range tools {
		if t.Name == "docker" && !t.Found {
			return fmt.Errorf("docker is not available: %s; install it, start the daemon and run kubedrill web again", t.Install)
		}
	}
	cl, err := a.getCluster()
	if err != nil {
		return err
	}
	if a.interactive() {
		if err := a.offerRecreate(ctx, cl); err != nil {
			return err
		}
	}
	prep := a.startPreparation(ctx)
	defer prep.stop()
	a.printf("ensuring cluster %s (profile %s)...\n", cluster.Name, defaultProfile)
	if err := a.ensureCluster(ctx, cl, defaultProfile, nil); err != nil {
		return err
	}
	a.finishPreparation(ctx, prep)
	return nil
}

func webToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("web token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// openBrowser asks the desktop to open url; a failure leaves the printed
// link as the way in.
func openBrowser(url string) {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// webBackend answers the browser workspace with the app's catalog, journal,
// session and workstation. The server calls it one request at a time. With
// reload set (a preview run) the scenarios are read from disk again on
// every catalog or assignment request.
type webBackend struct {
	a      *app
	reload bool
}

func (b *webBackend) catalog() (Catalog, error) {
	if b.reload {
		b.a.catalog = nil
	}
	return b.a.getCatalog()
}

// webState is what several calls read together: the scenario catalog, the
// curriculum, every episode and the active one.
type webState struct {
	cat     Catalog
	eps     []journal.Episode
	current *journal.Episode
}

func (b *webBackend) state() (webState, error) {
	cat, err := b.catalog()
	if err != nil {
		return webState{}, err
	}
	j, err := b.a.getJournal()
	if err != nil {
		return webState{}, err
	}
	current, err := j.Current()
	if err != nil {
		return webState{}, fmt.Errorf("read current episode: %w", err)
	}
	eps, err := allEpisodes(j)
	if err != nil {
		return webState{}, err
	}
	return webState{cat: cat, eps: eps, current: current}, nil
}

// masteryView is the CLI mastery of sc with the distinct variants solved.
func masteryView(sc *scenario.Scenario, eps []journal.Episode) web.Mastery {
	mine := episodesOf(eps, sc.Meta.ID)
	m := progress.ScenarioMastery(mine, scenarioInfo(sc))
	variants := map[string]bool{}
	for _, e := range mine {
		if e.Outcome == journal.OutcomeSolved && e.Variant != "" {
			variants[e.Variant] = true
		}
	}
	return web.Mastery{
		Attempts:       m.Attempts,
		Solved:         m.Solved,
		CleanInLast:    m.CleanInLast,
		Last:           m.Last,
		Variants:       len(sc.Variants),
		VariantsSolved: len(variants),
		Mastered:       m.Mastered,
	}
}

func (b *webBackend) Catalog(ctx context.Context) (web.Catalog, error) {
	a := b.a
	c, err := a.loadCurriculum()
	if err != nil {
		return web.Catalog{}, err
	}
	st, err := b.state()
	if err != nil {
		return web.Catalog{}, err
	}
	out := web.Catalog{Levels: []web.Level{}, Exams: []web.Exam{}}
	solved := map[string]bool{}
	for _, lv := range c.Levels {
		level := web.Level{ID: lv.ID, Title: lv.Title, Before: lv.Before, After: lv.After, Modules: []web.Module{}}
		for _, m := range lv.Modules {
			mod := web.Module{ID: m.ID, Title: m.Title, Summary: m.Summary, Assignments: []web.AssignmentCard{}}
			for _, ps := range m.Scenarios {
				if ps.ID == "" {
					mod.Planned++
					continue
				}
				sc, err := st.cat.Find(ps.ID)
				if err != nil {
					mod.Planned++
					continue
				}
				ms := progress.ScenarioMastery(episodesOf(st.eps, sc.Meta.ID), scenarioInfo(sc))
				if ms.Solved > 0 {
					solved[sc.Meta.ID] = true
				}
				mod.Assignments = append(mod.Assignments, web.AssignmentCard{
					ID:         sc.Meta.ID,
					Title:      sc.Meta.Title,
					Minutes:    sc.Meta.Minutes,
					Concepts:   nonNil(sc.Meta.Concepts),
					Exam:       nonNil(sc.Meta.Exam),
					Requires:   nonNil(sc.Meta.Requires),
					Simulation: sc.HasSimulation(),
					Status:     statusOf(ms, st.current != nil && st.current.ScenarioID == sc.Meta.ID),
					Mastery:    masteryView(sc, st.eps),
				})
			}
			level.Modules = append(level.Modules, mod)
		}
		out.Levels = append(out.Levels, level)
	}
	for _, e := range c.Exams {
		cov, err := c.ExamCoverage(e.ID, solved)
		if err != nil {
			return web.Catalog{}, err
		}
		exam := web.Exam{ID: e.ID, Title: e.Title, Domains: []web.ExamDomain{}}
		for _, d := range cov {
			exam.Domains = append(exam.Domains, web.ExamDomain{ID: d.Domain.ID, Title: d.Domain.Title, Weight: d.Domain.Weight, Assignments: nonNil(d.Scenarios), Solved: d.Done})
		}
		out.Exams = append(out.Exams, exam)
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (b *webBackend) Assignment(ctx context.Context, id string) (web.Assignment, error) {
	a := b.a
	st, err := b.state()
	if err != nil {
		return web.Assignment{}, err
	}
	sc, err := st.cat.Find(id)
	if err != nil {
		return web.Assignment{}, err
	}
	seeds := sc.VariantSeeds()
	if len(seeds) == 0 {
		return web.Assignment{}, fmt.Errorf("scenario %s has no variant seed", sc.Meta.ID)
	}
	rd, err := sc.Render(seeds[0])
	if err != nil {
		return web.Assignment{}, err
	}
	out := web.Assignment{
		ID:         sc.Meta.ID,
		Title:      sc.Meta.Title,
		Level:      sc.Meta.Level,
		Module:     sc.Meta.Module,
		Minutes:    sc.Meta.Minutes,
		Concepts:   []web.Concept{},
		Exam:       nonNil(sc.Meta.Exam),
		Variants:   len(sc.Variants),
		Simulation: rd.Simulation,
		Ticket:     rd.Instruction,
		Story:      rd.Story,
		Mastery:    masteryView(sc, st.eps),
	}
	if lib, err := a.getConcepts(); err == nil && lib != nil {
		for _, cid := range sc.Meta.Concepts {
			if c, err := lib.Get(cid); err == nil {
				out.Concepts = append(out.Concepts, web.Concept{ID: c.ID, Title: c.Title, Summary: c.Summary, Body: c.Body})
			} else {
				out.Concepts = append(out.Concepts, web.Concept{ID: cid, Title: cid})
			}
		}
	}
	if c, err := a.loadCurriculum(); err == nil {
		for _, lv := range c.Levels {
			for _, m := range lv.Modules {
				for _, ps := range m.Scenarios {
					if ps.ID == sc.Meta.ID {
						out.Module, out.ModuleTitle = m.ID, m.Title
					}
				}
			}
		}
	}
	return out, nil
}

func (b *webBackend) Session(ctx context.Context, live bool) (web.Session, error) {
	b.a.expireIfDue(ctx)
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return web.Session{}, err
	}
	ep, rd, err := s.Current()
	if errors.Is(err, env.ErrNoActiveEpisode) {
		return web.Session{}, nil
	}
	if err != nil {
		return web.Session{}, err
	}
	out := web.Session{Active: activeView(ep, rd)}
	if !live || ep.Mode == journal.ModeExam {
		return out, nil
	}
	g, err := s.Observe(ctx)
	if err != nil {
		out.CheckError = err.Error()
		return out, nil
	}
	out.Checks, out.Passed = checkViews(g), g.Passed
	return out, nil
}

// Next is the active episode, or the recommendation of kubedrill list with
// its reason in words, plus the count of concepts due for review.
func (b *webBackend) Next(ctx context.Context) (web.Next, error) {
	st, err := b.state()
	if err != nil {
		return web.Next{}, err
	}
	out := web.Next{ReviewsPending: len(progress.DueConcepts(scenarioInfos(st.cat), st.eps, b.a.now()))}
	if st.current != nil {
		sess, err := b.Session(ctx, false)
		if err != nil {
			return web.Next{}, err
		}
		out.Active = sess.Active
		return out, nil
	}
	rec, ok := b.a.recommend(st.cat, st.eps)
	if !ok {
		return out, nil
	}
	sc, err := st.cat.Find(rec.ScenarioID)
	if err != nil {
		return out, nil
	}
	out.ScenarioID, out.Title, out.Minutes = sc.Meta.ID, sc.Meta.Title, sc.Meta.Minutes
	out.Reason, out.Review = reasonSentence(rec.Reason), rec.Review
	return out, nil
}

func activeView(ep *journal.Episode, rd *scenario.Rendered) *web.Active {
	v := &web.Active{
		EpisodeID:  ep.ID,
		ScenarioID: ep.ScenarioID,
		Title:      rd.Scenario.Meta.Title,
		Namespace:  rd.Namespace,
		Ticket:     rd.Instruction,
		Story:      rd.Story,
		StartedAt:  ep.StartedAt,
		Minutes:    rd.Scenario.Meta.Minutes,
		HintsUsed:  ep.HintsUsed,
		HintsTotal: len(rd.Hints),
		Guide:      []scenario.GuideStep{},
		Hints:      []string{},
		Exam:       ep.Mode == journal.ModeExam,
		Deadline:   ep.Deadline,
	}
	if v.Exam {
		v.Story, v.HintsTotal, v.Hints = "", 0, []string{}
		return v
	}
	if ep.HintsUsed <= len(rd.Hints) {
		v.Hints = append(v.Hints, rd.Hints[:ep.HintsUsed]...)
	}
	if ep.Diagnosis != nil {
		v.Diagnosis = ep.Diagnosis.Text
	}
	if rd.Simulation != nil {
		v.Guide = append(v.Guide, rd.Simulation.Guide...)
		v.Mentor = rd.Simulation.Mentor.Intro
	}
	return v
}

func checkViews(g grader.Grade) []web.Check {
	out := make([]web.Check, 0, len(g.Results))
	for _, r := range g.Results {
		c := web.Check{ID: r.ID, Description: r.Description, Why: r.Why, Status: string(r.Status), Expected: r.Expected, Message: r.Message}
		if !r.Spoiler {
			c.Actual = r.Actual
		}
		out = append(out, c)
	}
	return out
}

// Start leaves the active episode when replace is set and starts id on the
// seed of stage, sending each long step of the setup to progress.
func (b *webBackend) Start(ctx context.Context, id string, stage web.Stage, replace bool, progress func(string)) (web.Session, error) {
	a := b.a
	cat, err := b.catalog()
	if err != nil {
		return web.Session{}, err
	}
	sc, err := cat.Find(id)
	if err != nil {
		return web.Session{}, err
	}
	j, err := a.getJournal()
	if err != nil {
		return web.Session{}, err
	}
	prev, err := j.Current()
	if err != nil {
		return web.Session{}, fmt.Errorf("read current episode: %w", err)
	}
	if prev != nil && !replace {
		return web.Session{}, fmt.Errorf("%s is active (episode %s); leaving it records it as failed: %w", prev.ScenarioID, prev.ID, web.ErrActiveElsewhere)
	}
	seed, err := stageSeed(sc, stage)
	if err != nil {
		return web.Session{}, err
	}
	if progress != nil {
		restore := a.progress.Divert(progress)
		defer restore()
	}
	if _, _, err := a.startEpisode(ctx, j, sc.Meta.ID, startOptions{seed: seed, exam: stage == web.StageExam}, prev); err != nil {
		return web.Session{}, err
	}
	return b.Session(ctx, false)
}

// stageSeed picks the seed of a stage: practice runs the first variant, so
// the guide always meets the same planted cause; DIY runs another variant
// at random, or a fresh seed of the only one; an exam draws any seed.
func stageSeed(sc *scenario.Scenario, stage web.Stage) (int64, error) {
	seeds := sc.VariantSeeds()
	if len(seeds) == 0 {
		return 0, fmt.Errorf("scenario %s has no variant seed", sc.Meta.ID)
	}
	if stage == web.StageExam {
		return 0, nil
	}
	if stage == web.StagePractice || len(seeds) == 1 {
		if stage == web.StageDIY {
			return 0, nil
		}
		return seeds[0], nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(seeds)-1)))
	if err != nil {
		return 0, fmt.Errorf("pick a DIY variant: %w", err)
	}
	return seeds[1+n.Int64()], nil
}

func (b *webBackend) Validate(ctx context.Context) (web.Validation, error) {
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return web.Validation{}, err
	}
	ep, rd, err := s.Current()
	if errors.Is(err, env.ErrNoActiveEpisode) {
		return web.Validation{}, errors.New("no active episode; launch the practice or the DIY run first")
	}
	if err != nil {
		return web.Validation{}, err
	}
	g, done, err := s.Grade(ctx)
	if err != nil {
		return web.Validation{}, err
	}
	v := web.Validation{EpisodeID: ep.ID, ScenarioID: ep.ScenarioID, Passed: g.Passed, Score: g.Score, Checks: checkViews(g)}
	if g.Passed {
		v.RootCause = rd.Variant.RootCause
		v.Solution = rd.Solution
		if done != nil && done.Diagnosis != nil {
			v.Diagnosis = done.Diagnosis.Text
		}
	}
	return v, nil
}

// Submit ends the active exam: its one grade before the deadline, or its
// expiry after it. The root cause and the walkthrough come back either way,
// since the rehearsal is over.
func (b *webBackend) Submit(ctx context.Context) (web.Validation, error) {
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return web.Validation{}, err
	}
	ep, rd, err := s.Current()
	if errors.Is(err, env.ErrTimedOut) {
		return b.expireExam(ctx, s)
	}
	if errors.Is(err, env.ErrNoActiveEpisode) {
		return web.Validation{}, errors.New("no active exam; launch the exam rehearsal first")
	}
	if err != nil {
		return web.Validation{}, err
	}
	g, done, err := s.Submit(ctx)
	if errors.Is(err, env.ErrTimedOut) {
		return b.expireExam(ctx, s)
	}
	if err != nil {
		return web.Validation{}, err
	}
	v := web.Validation{EpisodeID: ep.ID, ScenarioID: ep.ScenarioID, Passed: g.Passed, Score: g.Score, Checks: checkViews(g), RootCause: rd.Variant.RootCause, Solution: rd.Solution}
	if done != nil {
		v.Outcome = outcomeWord(done.Outcome)
	}
	return v, nil
}

// expireExam ends an exam past its deadline and reports its last grade.
func (b *webBackend) expireExam(ctx context.Context, s *env.Session) (web.Validation, error) {
	j, err := b.a.getJournal()
	if err != nil {
		return web.Validation{}, err
	}
	cur, err := j.Current()
	if err != nil || cur == nil {
		return web.Validation{}, errors.New("no active exam")
	}
	rd, err := b.render(cur)
	if err != nil {
		return web.Validation{}, err
	}
	done, err := s.Expire(ctx)
	if done == nil {
		return web.Validation{}, err
	}
	v := web.Validation{EpisodeID: done.ID, ScenarioID: done.ScenarioID, Outcome: outcomeWord(done.Outcome), RootCause: rd.Variant.RootCause, Solution: rd.Solution}
	if n := len(done.Grades); n > 0 {
		g := done.Grades[n-1]
		v.Passed, v.Score, v.Checks = g.Passed && done.Outcome == journal.OutcomeSolved, g.Score, checkViews(g)
	}
	return v, nil
}

// render renders ep the way the session does, for an episode Current
// refuses because its exam deadline passed.
func (b *webBackend) render(ep *journal.Episode) (*scenario.Rendered, error) {
	cat, err := b.catalog()
	if err != nil {
		return nil, err
	}
	sc, err := cat.Find(ep.ScenarioID)
	if err != nil {
		return nil, err
	}
	return sc.Render(ep.Seed, scenario.WithNamespace(ep.Namespace), scenario.WithVariant(ep.Variant))
}

func (b *webBackend) Hint(ctx context.Context) (web.Hint, error) {
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return web.Hint{}, err
	}
	text, index, total, err := s.Hint()
	if err != nil {
		return web.Hint{}, err
	}
	return web.Hint{HTML: text, Index: index, Total: total}, nil
}

func (b *webBackend) Diagnose(ctx context.Context, text string) error {
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return err
	}
	_, err = s.Diagnose(text)
	return err
}

// episode finds the episode with id among every recorded one.
func (b *webBackend) episode(id string) (*journal.Episode, *scenario.Scenario, error) {
	st, err := b.state()
	if err != nil {
		return nil, nil, err
	}
	for i := range st.eps {
		if st.eps[i].ID != id {
			continue
		}
		sc, err := st.cat.Find(st.eps[i].ScenarioID)
		if err != nil {
			return nil, nil, err
		}
		return &st.eps[i], sc, nil
	}
	return nil, nil, fmt.Errorf("no episode %q in the journal", id)
}

// Debrief is what kubedrill debrief prints for the episode id.
func (b *webBackend) Debrief(ctx context.Context, id string) (web.Debrief, error) {
	ep, sc, err := b.episode(id)
	if err != nil {
		return web.Debrief{}, err
	}
	d := web.Debrief{
		EpisodeID:  ep.ID,
		ScenarioID: ep.ScenarioID,
		Title:      sc.Meta.Title,
		Outcome:    outcomeWord(ep.Outcome),
		Seconds:    int(ep.DurationAt(b.a.now()).Seconds()),
		Minutes:    sc.Meta.Minutes,
		Hints:      ep.HintsUsed,
		Telemetry:  "none",
		FixTargets: nonNil(sc.Meta.FixTargets),
		RootCause:  rootCause(sc, ep),
	}
	if rd, err := b.render(ep); err == nil {
		d.Solution = rd.Solution
	}
	if t := ep.Telemetry; t != nil && t.Source != "" && t.Source != "none" {
		d.Telemetry = t.Source
		d.ReadsBeforeWrite = t.ReadsBeforeFirstWrite
		d.FirstWrite = t.FirstWriteTarget
		d.Verdict = string(t.Verdict)
		switch {
		case t.FirstWriteTarget == "":
		case telemetry.MatchTarget(t.FirstWriteTarget, sc.Meta.FixTargets) && t.FirstWriteMetadataOnly:
			d.FirstWriteKind = "metadata-only"
		case telemetry.MatchTarget(t.FirstWriteTarget, sc.Meta.FixTargets):
			d.FirstWriteKind = "fix-target"
		default:
			d.FirstWriteKind = "other"
		}
	}
	if ep.Diagnosis != nil {
		d.Diagnosis = ep.Diagnosis.Text
		d.Matched = ep.Diagnosis.Matched
	}
	if ep.Debrief != nil {
		d.Answers.RuledOutEarlier = ep.Debrief.RuledOutEarlier
		d.Answers.NextTimeFirst = ep.Debrief.NextTimeFirst
	}
	d.Answers.Matched = d.Matched
	return d, nil
}

// RecordDebrief stores the learner's answers on the episode id, keeping a
// principle written from the terminal.
func (b *webBackend) RecordDebrief(ctx context.Context, id string, ans web.DebriefAnswers) error {
	ep, _, err := b.episode(id)
	if err != nil {
		return err
	}
	if ep.Outcome == journal.OutcomeActive {
		return fmt.Errorf("episode %s is still active; the debrief comes after it ends", id)
	}
	s, err := b.a.learnerSession(ctx)
	if err != nil {
		return err
	}
	d := journal.Debrief{RuledOutEarlier: strings.TrimSpace(ans.RuledOutEarlier), NextTimeFirst: strings.TrimSpace(ans.NextTimeFirst)}
	if ep.Debrief != nil {
		d.Principle = ep.Debrief.Principle
	}
	return s.RecordDebrief(id, d, ans.Matched)
}

// Terminal returns the docker run command of a workstation shell named
// name and a cleanup that removes the container.
func (b *webBackend) Terminal(ctx context.Context, name string) (*exec.Cmd, func(), error) {
	a := b.a
	j, err := a.getJournal()
	if err != nil {
		return nil, nil, err
	}
	ws, err := a.getWorkstation(j)
	if err != nil {
		return nil, nil, err
	}
	if ws == nil {
		return nil, nil, errNoWorkstation
	}
	cl, err := a.getCluster()
	if err != nil {
		return nil, nil, err
	}
	state, err := a.stateDir()
	if err != nil {
		return nil, nil, fmt.Errorf("workstation kubeconfig path: %w", err)
	}
	kubeconfig := cluster.InternalKubeconfigPathIn(state)
	if err := cl.WriteInternalKubeconfig(ctx, kubeconfig); err != nil {
		return nil, nil, fmt.Errorf("write workstation kubeconfig: %w", err)
	}
	opts := workstation.ShellOptions{
		Kubeconfig:  kubeconfig,
		ClusterName: cluster.Name,
		Name:        name,
		Web:         true,
	}
	if cur, err := j.Current(); err == nil && cur != nil && cur.Mode == journal.ModeExam {
		opts.Exam = true
		if cur.Deadline != nil {
			opts.Deadline = *cur.Deadline
		}
	}
	cmd, err := ws.ShellCommand(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = ws.RemoveContainer(cctx, name)
	}
	return cmd, cleanup, nil
}
