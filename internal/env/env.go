// Package env is the learner's environment loop: start a scenario with a
// seed in its own namespace, observe the checks, grade, commit a diagnosis,
// take a hint, surrender, submit an exam. It owns the state machine (idle,
// active, finished) and the set of actions allowed in each state and mode,
// and composes scenario, kube, grader and journal.
package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/kube"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/scriptrun"
)

// ErrNoActiveEpisode is returned by actions that need an active episode.
var ErrNoActiveEpisode = errors.New("no active episode")

// ErrNoMoreHints is returned by Hint once every hint has been shown.
var ErrNoMoreHints = errors.New("no more hints")

// ErrNamespaceMissing is returned by Observe and Grade when the episode's
// namespace no longer exists.
var ErrNamespaceMissing = errors.New("namespace missing")

// ErrNamespaceForeign is returned by Observe and Grade when the episode's
// namespace carries another episode's seed.
var ErrNamespaceForeign = errors.New("namespace belongs to another episode")

// ErrDiagnosisRequired is returned by Hint for the last hint and by Surrender
// in practice mode when no diagnosis is recorded.
var ErrDiagnosisRequired = errors.New("diagnosis required")

// ErrExamMode is returned by actions an exam episode does not allow.
var ErrExamMode = errors.New("not available in exam mode")

// ErrTimedOut is returned by every action on an exam episode past its
// deadline except Expire.
var ErrTimedOut = errors.New("time box expired")

// ErrNotExam is returned by Submit and Expire on a practice episode.
var ErrNotExam = errors.New("not an exam episode")

// ErrNotExpired is returned by Expire before the exam deadline.
var ErrNotExpired = errors.New("time box not expired")

// ErrAPIServicesUnavailable is returned by Start when an aggregated API
// service is not available, which leaves namespace deletion stuck.
var ErrAPIServicesUnavailable = errors.New("API services unavailable")

const (
	seedLabel      = "kubedrill.io/seed"
	scenarioLabel  = "kubedrill.io/scenario"
	journalLabel   = "kubedrill.io/journal"
	managedByLabel = "app.kubernetes.io/managed-by"
	managedBy      = "kubedrill"
	suffixLen      = 5
	minDiagnosis   = 10
	maxDiagnosis   = 300
	expireGrace    = 30 * time.Second
	seedDraws      = 16
)

// MaxSeed is the largest seed Start accepts from Rand; larger or negative
// values are folded into 1..MaxSeed.
const MaxSeed = 1_000_000

const scriptTimeout = 10 * time.Minute

// Session is the environment for one learner against one cluster. The zero
// value is not usable; use New.
type Session struct {
	d Deps
}

// Deps are the collaborators a Session needs. Each is declared as the
// narrowest interface the session calls. Hook, Rand, Suffix, Now, Out and
// Progress have defaults when nil; Healer and Telemetry are optional;
// Kubeconfig, when set, is exported to hook scripts. Progress receives one
// line per long step of Start, for example "creating namespace task-hello-x1y2z",
// and one line per fault healed, for example "healed pause on worker2".
// Rand draws a seed when none is given (default: a practice seed); Suffix
// returns the 5 lowercase alphanumerics appended to the episode namespace
// (default kube.RandomSuffix). Telemetry is called when an episode ends by
// a solve, a surrender, a submit, an expiry or another start; the episode
// it receives always has EndedAt set (Now for one still active), so the
// telemetry window follows the session clock. Now also stamps every grade
// and every end time the session records.
type Deps struct {
	Kube       Kube
	Grader     Grader
	Journal    Journal
	Catalog    Catalog
	Hook       func(ctx context.Context, script string, env []string) error
	Rand       func() int64
	Suffix     func() string
	Out        io.Writer
	Kubeconfig string
	Progress   func(msg string)
	Healer     Healer
	Telemetry  func(ep journal.Episode, rd *scenario.Rendered) journal.Telemetry
	Now        func() time.Time
}

// Kube is what the session needs from a cluster client. ResetNamespace on a
// name that does not exist creates it. DeleteNamespaces deletes, without
// waiting, the namespaces matching the label selector except keep;
// DeleteNamespace deletes one namespace without waiting, a missing one
// being no error.
type Kube interface {
	ResetNamespace(ctx context.Context, ns string, labels map[string]string) error
	ApplyManifests(ctx context.Context, ns string, manifests []string) error
	NamespaceLabels(ctx context.Context, ns string) (map[string]string, error)
	DeleteNamespaces(ctx context.Context, selector string, keep string) (int, error)
	DeleteNamespace(ctx context.Context, ns string) error
	UnavailableAPIServices(ctx context.Context) ([]string, error)
}

// Healer undoes injected faults before an episode starts. Active lists the
// recorded faults, so Start can name each one Heal undid.
type Healer interface {
	Heal(ctx context.Context) error
	Active(ctx context.Context) ([]faults.Fault, error)
}

// Grader runs the checks of a rendered scenario.
type Grader interface {
	Grade(ctx context.Context, r *scenario.Rendered) (grader.Grade, error)
}

// Journal persists episodes. Modify changes the current episode under the
// journal lock and finishes it when the change sets a final outcome (see
// journal.Store.Modify); it fails with journal.ErrNoCurrent or
// journal.ErrNotCurrent when the episode is no longer current. ModifyEnded
// changes a finished episode under the same lock and fails with
// journal.ErrUnknownEpisode when no finished episode has the id.
type Journal interface {
	Current() (*journal.Episode, error)
	Start(e journal.Episode) error
	Modify(id string, fn func(*journal.Episode) error) (*journal.Episode, error)
	ModifyEnded(id string, fn func(*journal.Episode) error) (*journal.Episode, error)
	Episodes(scenarioID string) ([]journal.Episode, error)
	JournalID() string
}

// Catalog resolves scenario ids or prefixes to loaded scenarios.
type Catalog interface {
	Find(ref string) (*scenario.Scenario, error)
}

// StartOptions select the seed (0 draws one) and the mode of a new episode.
// An empty Mode is practice.
type StartOptions struct {
	Seed int64
	Mode journal.Mode
}

// New wires a Session.
func New(d Deps) *Session {
	if d.Out == nil {
		d.Out = os.Stderr
	}
	if d.Hook == nil {
		d.Hook = RunScript(d.Out)
	}
	if d.Rand == nil {
		d.Rand = func() int64 { return mrand.Int64N(progress.PracticeSeedMax) + 1 }
	}
	if d.Suffix == nil {
		d.Suffix = func() string { return kube.RandomSuffix(suffixLen) }
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Progress == nil {
		d.Progress = func(string) {}
	}
	return &Session{d: d}
}

// RunScript returns a hook runner that executes script with bash through
// scriptrun.Run, both output streams on out, and an environment made only
// of the allowlisted host variables (scriptrun.HostEnv: PATH, HOME, TMPDIR,
// LANG, LC_ALL, TERM, USER) plus env: environment variables other than the
// allowlist do not reach the script. The script runs as the host user and
// reads the host filesystem like any process of that user. The working
// directory is the script's directory. Every process in the script's group
// is killed when the script exits, when ctx ends or after ten minutes, so a
// background child never outlives the hook. A non-zero exit is an error
// naming the script and the status.
func RunScript(out io.Writer) func(ctx context.Context, script string, env []string) error {
	return func(ctx context.Context, script string, env []string) error {
		ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
		defer cancel()
		res, err := scriptrun.Run(ctx, scriptrun.Spec{File: script, Dir: filepath.Dir(script), Env: env, Stream: out})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("run %s: exit status %d", script, res.ExitCode)
		}
		return nil
	}
}

// ScriptEnv returns the KEY=VALUE pairs a scenario script (setup hook,
// solution or script check) receives on top of the host allowlist: TASK_NS
// (the episode namespace), SEED, VARIANT, one VAR_<KEY> per variant
// variable, KUBECONFIG when kubeconfig is not empty and KUBEDRILL_EPISODE
// when episodeID is not empty.
func ScriptEnv(rd *scenario.Rendered, kubeconfig, episodeID string) []string {
	env := []string{
		"TASK_NS=" + rd.Namespace,
		"SEED=" + strconv.FormatInt(rd.Seed, 10),
		"VARIANT=" + rd.Variant.Name,
	}
	keys := make([]string, 0, len(rd.Vars))
	for k := range rd.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "VAR_"+strings.ToUpper(k)+"="+rd.Vars[k])
	}
	if kubeconfig != "" {
		env = append(env, "KUBECONFIG="+kubeconfig)
	}
	if episodeID != "" {
		env = append(env, "KUBEDRILL_EPISODE="+episodeID)
	}
	return env
}

// Reset is Start in practice mode.
func (s *Session) Reset(ctx context.Context, ref string, seed int64) (*journal.Episode, *scenario.Rendered, error) {
	return s.Start(ctx, ref, StartOptions{Seed: seed, Mode: journal.ModePractice})
}

// Start begins an episode in a namespace of its own. In order: heal
// injected faults (one Progress line per fault healed), refuse when an
// aggregated API service is unavailable, create the episode namespace (the
// scenario namespace plus "-" and 5 random lowercase alphanumerics), apply
// the setup manifests, run the hook script, and only then finish the
// active episode as failed (timed out for an exam past its deadline), so a
// failed setup leaves it active; then record the new episode and, when it
// is still the current one, delete this journal's older episode namespaces
// without waiting. The clock starts once the setup is in place; an exam
// deadline is the start plus the scenario minutes. A seed of 0 draws one;
// for a scenario with more than one variant the draw is repeated (up to 16
// times) until the variant differs from the last episode of that scenario.
func (s *Session) Start(ctx context.Context, ref string, o StartOptions) (*journal.Episode, *scenario.Rendered, error) {
	sc, err := s.d.Catalog.Find(ref)
	if err != nil {
		return nil, nil, err
	}
	mode := o.Mode
	if mode == "" {
		mode = journal.ModePractice
	}
	if mode != journal.ModePractice && mode != journal.ModeExam {
		return nil, nil, fmt.Errorf("start %s: unknown mode %q", sc.Meta.ID, mode)
	}
	if mode == journal.ModeExam && sc.Meta.Minutes <= 0 {
		return nil, nil, fmt.Errorf("start %s in exam mode: scenario has no time box (minutes)", sc.Meta.ID)
	}
	seed := o.Seed
	if seed == 0 {
		seed = s.drawSeed(sc)
	}
	suffix := s.d.Suffix()
	rd, err := renderEpisode(sc, seed, suffix)
	if err != nil {
		return nil, nil, err
	}
	if err := s.prepareCluster(ctx); err != nil {
		return nil, nil, err
	}
	id := fmt.Sprintf("%s-%d-%s", sc.Meta.ID, s.d.Now().Unix(), suffix)
	if err := s.setup(ctx, sc, rd, id); err != nil {
		return nil, nil, err
	}
	if err := s.finishActive(ctx, rd.Namespace); err != nil {
		return nil, nil, err
	}
	ep, err := s.createEpisode(sc, rd, id, mode)
	if err != nil {
		return nil, nil, err
	}
	s.gc(ctx, ep.Namespace)
	return ep, rd, nil
}

// drawSeed draws a seed with Rand, redrawing up to 16 times while a
// scenario with several variants would repeat the variant of its last
// episode.
func (s *Session) drawSeed(sc *scenario.Scenario) int64 {
	seed := foldSeed(s.d.Rand())
	if len(sc.Variants) < 2 {
		return seed
	}
	eps, err := s.d.Journal.Episodes(sc.Meta.ID)
	if err != nil || len(eps) == 0 {
		return seed
	}
	last := eps[len(eps)-1].Variant
	for i := 1; i < seedDraws && sc.VariantFor(seed).Name == last; i++ {
		seed = foldSeed(s.d.Rand())
	}
	return seed
}

// renderEpisode renders sc with seed in the episode namespace, the
// scenario namespace plus "-" and suffix.
func renderEpisode(sc *scenario.Scenario, seed int64, suffix string) (*scenario.Rendered, error) {
	base, err := sc.Render(seed)
	if err != nil {
		return nil, fmt.Errorf("render scenario %s seed %d: %w", sc.Meta.ID, seed, err)
	}
	ns := base.Namespace + "-" + suffix
	rd, err := sc.Render(seed, scenario.WithNamespace(ns))
	if err != nil {
		return nil, fmt.Errorf("render scenario %s seed %d in namespace %s: %w", sc.Meta.ID, seed, ns, err)
	}
	return rd, nil
}

// prepareCluster heals injected faults and refuses to go on while an
// aggregated API service is unavailable.
func (s *Session) prepareCluster(ctx context.Context) error {
	if err := s.heal(ctx); err != nil {
		return err
	}
	down, err := s.d.Kube.UnavailableAPIServices(ctx)
	if err != nil {
		return fmt.Errorf("check API services: %w", err)
	}
	if len(down) > 0 {
		return fmt.Errorf("%w: %s; namespaces cannot be deleted until they recover; run: kubedrill up", ErrAPIServicesUnavailable, strings.Join(down, ", "))
	}
	return nil
}

// setup creates the episode namespace with its ownership labels, applies
// the setup manifests and runs the hook script.
func (s *Session) setup(ctx context.Context, sc *scenario.Scenario, rd *scenario.Rendered, id string) error {
	ns := rd.Namespace
	labels := map[string]string{
		managedByLabel: managedBy,
		scenarioLabel:  sc.Meta.ID,
		seedLabel:      strconv.FormatInt(rd.Seed, 10),
		journalLabel:   s.d.Journal.JournalID(),
	}
	s.d.Progress("creating namespace " + ns)
	if err := s.d.Kube.ResetNamespace(ctx, ns, labels); err != nil {
		return fmt.Errorf("create namespace %s: %w", ns, err)
	}
	s.d.Progress("applying setup")
	if err := s.d.Kube.ApplyManifests(ctx, ns, rd.Manifests); err != nil {
		return fmt.Errorf("apply setup of %s to namespace %s: %w", sc.Meta.ID, ns, err)
	}
	if sc.HookScript != "" {
		if err := s.d.Hook(ctx, sc.HookScript, ScriptEnv(rd, s.d.Kubeconfig, id)); err != nil {
			return fmt.Errorf("setup hook of %s: %w", sc.Meta.ID, err)
		}
	}
	return nil
}

// createEpisode records the new active episode and returns it as the
// journal stores it.
func (s *Session) createEpisode(sc *scenario.Scenario, rd *scenario.Rendered, id string, mode journal.Mode) (*journal.Episode, error) {
	now := s.d.Now()
	ep := journal.Episode{
		ID:         id,
		ScenarioID: sc.Meta.ID,
		Seed:       rd.Seed,
		Variant:    rd.Variant.Name,
		StartedAt:  now,
		Outcome:    journal.OutcomeActive,
		Namespace:  rd.Namespace,
		Mode:       mode,
	}
	if mode == journal.ModeExam {
		deadline := now.Add(time.Duration(sc.Meta.Minutes) * time.Minute)
		ep.Deadline = &deadline
	}
	if err := s.d.Journal.Start(ep); err != nil {
		return nil, fmt.Errorf("record episode of %s: %w", sc.Meta.ID, err)
	}
	if cur, err := s.d.Journal.Current(); err == nil && cur != nil && cur.ID == id {
		ep = *cur
	}
	return &ep, nil
}

// gc deletes this journal's episode namespaces other than keep, without
// waiting, but only while keep is still the current episode's: when
// another process started an episode meanwhile, its cleanup is its own.
func (s *Session) gc(ctx context.Context, keep string) {
	cur, err := s.d.Journal.Current()
	if err != nil || cur == nil || cur.Namespace != keep {
		return
	}
	selector := NamespaceSelector(s.d.Journal.JournalID())
	if _, err := s.d.Kube.DeleteNamespaces(ctx, selector, keep); err != nil {
		fmt.Fprintf(s.d.Out, "warning: delete old episode namespaces: %v\n", err)
	}
}

// NamespaceSelector returns the label selector that matches every episode
// namespace kubedrill created for the journal with journalID.
func NamespaceSelector(journalID string) string {
	return managedByLabel + "=" + managedBy + "," + journalLabel + "=" + journalID
}

// heal undoes the injected faults and reports each one that is gone
// afterwards as "healed <action> on <node>". When the faults cannot be
// listed after Heal, none is reported as healed.
func (s *Session) heal(ctx context.Context) error {
	if s.d.Healer == nil {
		return nil
	}
	before, err := s.d.Healer.Active(ctx)
	if err != nil {
		return fmt.Errorf("heal injected faults: %w", err)
	}
	healErr := s.d.Healer.Heal(ctx)
	after, err := s.d.Healer.Active(ctx)
	if err != nil {
		after = before
	}
	left := map[string]bool{}
	for _, f := range after {
		left[faultKey(f)] = true
	}
	for _, f := range before {
		if !left[faultKey(f)] {
			s.d.Progress(fmt.Sprintf("healed %s on %s", f.Action, f.Node))
		}
	}
	if healErr != nil {
		return fmt.Errorf("heal injected faults: %w", healErr)
	}
	return nil
}

func faultKey(f faults.Fault) string {
	return string(f.Action) + " " + f.Node
}

// deleteNamespace deletes ns without waiting. A namespace that kubedrill did
// not create (kube.ErrNotManaged) is left alone with one Progress line; any
// other failure is a warning on Out, the
// next Start's cleanup retries it.
func (s *Session) deleteNamespace(ctx context.Context, ns string) {
	if ns == "" {
		return
	}
	if err := s.d.Kube.DeleteNamespace(ctx, ns); err != nil {
		if errors.Is(err, kube.ErrNotManaged) {
			s.d.Progress(fmt.Sprintf("kept namespace %s: %v", ns, err))
			return
		}
		fmt.Fprintf(s.d.Out, "warning: delete namespace %s: %v\n", ns, err)
	}
}

func foldSeed(seed int64) int64 {
	if seed >= 1 && seed <= MaxSeed {
		return seed
	}
	seed %= MaxSeed
	if seed < 0 {
		seed = -seed
	}
	return seed + 1
}

// finishActive ends the active episode before a new one starts: failed, or
// timed out for an exam past its deadline, with telemetry when the scenario
// still renders, and deletes its namespace unless it is keep. An episode
// another process ended meanwhile is left as that process recorded it.
func (s *Session) finishActive(ctx context.Context, keep string) error {
	cur, err := s.d.Journal.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if cur == nil {
		return nil
	}
	outcome := journal.OutcomeFailed
	if s.pastDeadline(cur) {
		outcome = journal.OutcomeTimedOut
	}
	now := s.d.Now()
	var tel *journal.Telemetry
	if rd, err := s.render(cur); err == nil {
		tel = s.telemetry(*cur, rd, now)
	}
	_, err = s.d.Journal.Modify(cur.ID, func(e *journal.Episode) error {
		if tel != nil {
			e.Telemetry = tel
		}
		e.Outcome = outcome
		e.EndedAt = &now
		return nil
	})
	if err != nil && !notCurrent(err) {
		return fmt.Errorf("finish episode of %s as %s: %w", cur.ScenarioID, outcome, err)
	}
	if cur.Namespace != keep {
		s.deleteNamespace(ctx, cur.Namespace)
	}
	return nil
}

// notCurrent reports whether err says the episode is no longer the current
// one, because another process ended it or started another.
func notCurrent(err error) bool {
	return errors.Is(err, journal.ErrNotCurrent) || errors.Is(err, journal.ErrNoCurrent)
}

// endedElsewhere returns the finished episode with id when err says it is
// no longer current, so a grade that raced with another process ending the
// episode reports the recorded end instead of an error.
func (s *Session) endedElsewhere(id string, err error) (*journal.Episode, bool) {
	if !notCurrent(err) {
		return nil, false
	}
	eps, lerr := s.d.Journal.Episodes("")
	if lerr != nil {
		return nil, false
	}
	for i := len(eps) - 1; i >= 0; i-- {
		if eps[i].ID == id && eps[i].Outcome != journal.OutcomeActive {
			e := eps[i]
			return &e, true
		}
	}
	return nil, false
}

func (s *Session) pastDeadline(ep *journal.Episode) bool {
	return pastDeadlineAt(ep, s.d.Now())
}

func pastDeadlineAt(ep *journal.Episode, at time.Time) bool {
	return ep.Mode == journal.ModeExam && ep.Deadline != nil && !at.Before(*ep.Deadline)
}

// active returns the active episode and its rendering, or ErrTimedOut for
// an exam past its deadline.
func (s *Session) active() (*journal.Episode, *scenario.Rendered, error) {
	return s.activeAt(s.d.Now())
}

// activeAt is active with the deadline judged at at.
func (s *Session) activeAt(at time.Time) (*journal.Episode, *scenario.Rendered, error) {
	ep, rd, err := s.Current()
	if err != nil {
		return nil, nil, err
	}
	if pastDeadlineAt(ep, at) {
		return nil, nil, fmt.Errorf("exam of %s ended at %s; run: kubedrill submit: %w", ep.ScenarioID, ep.Deadline.Format(time.RFC3339), ErrTimedOut)
	}
	return ep, rd, nil
}

// Observe grades the active practice episode without recording the grade.
// It is what the watch mode polls. An exam episode returns ErrExamMode: it
// is graded once, by Submit or Expire.
func (s *Session) Observe(ctx context.Context) (grader.Grade, error) {
	ep, rd, err := s.active()
	if err != nil {
		return grader.Grade{}, err
	}
	if ep.Mode == journal.ModeExam {
		return grader.Grade{}, fmt.Errorf("observe: %w; run: kubedrill submit", ErrExamMode)
	}
	return s.grade(ctx, rd)
}

// grade checks the namespace and runs the grader. A context that ended
// while grading returns its error instead of a grade, since the checks it
// cut short read as failures that the learner never caused. The grade is
// stamped with Now.
func (s *Session) grade(ctx context.Context, rd *scenario.Rendered) (grader.Grade, error) {
	id := rd.Scenario.Meta.ID
	if err := s.checkNamespace(ctx, rd); err != nil {
		return grader.Grade{}, err
	}
	g, err := s.d.Grader.Grade(ctx, rd)
	if err != nil {
		return grader.Grade{}, fmt.Errorf("grade %s: %w", id, err)
	}
	if err := ctx.Err(); err != nil {
		return grader.Grade{}, fmt.Errorf("grade %s: %w", id, err)
	}
	g.At = s.d.Now()
	return g, nil
}

// Grade grades the active practice episode, records the result and, when
// every check passes, records telemetry and finishes the episode as Solved.
// The grade is appended under the journal lock, so a hint or a diagnosis
// recorded meanwhile by another process is kept. When another process ended
// the episode while it was graded, the episode as that process recorded it
// is returned with the grade and no error. A context that ends while
// grading returns its error and records nothing. An exam episode returns
// ErrExamMode; it is graded once by Submit.
func (s *Session) Grade(ctx context.Context) (grader.Grade, *journal.Episode, error) {
	ep, rd, err := s.active()
	if err != nil {
		return grader.Grade{}, nil, err
	}
	if ep.Mode == journal.ModeExam {
		return grader.Grade{}, nil, fmt.Errorf("check: %w; run: kubedrill submit", ErrExamMode)
	}
	g, err := s.grade(ctx, rd)
	if err != nil {
		return grader.Grade{}, nil, err
	}
	outcome := journal.OutcomeActive
	if g.Passed {
		outcome = journal.OutcomeSolved
	}
	done, err := s.finish(ctx, ep, rd, &g, outcome, s.d.Now())
	if err != nil {
		if ended, ok := s.endedElsewhere(ep.ID, err); ok {
			return g, ended, nil
		}
		return g, nil, err
	}
	return g, done, nil
}

// finish appends g (when not nil) to the stored episode and, for any
// outcome but active, records telemetry and ends it with outcome at now, in
// one journal step. An episode that ends abandoned, failed or timed out has
// its namespace deleted without waiting; a solved one keeps it until the
// next Start's cleanup.
func (s *Session) finish(ctx context.Context, ep *journal.Episode, rd *scenario.Rendered, g *grader.Grade, outcome journal.Outcome, now time.Time) (*journal.Episode, error) {
	var tel *journal.Telemetry
	if outcome != journal.OutcomeActive {
		tel = s.telemetry(*ep, rd, now)
	}
	done, err := s.d.Journal.Modify(ep.ID, func(e *journal.Episode) error {
		if g != nil {
			e.Grades = append(e.Grades, *g)
		}
		if outcome == journal.OutcomeActive {
			return nil
		}
		if tel != nil {
			e.Telemetry = tel
		}
		e.Outcome = outcome
		e.EndedAt = &now
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("record episode of %s: %w", ep.ScenarioID, err)
	}
	if outcome != journal.OutcomeActive && outcome != journal.OutcomeSolved {
		s.deleteNamespace(ctx, ep.Namespace)
	}
	return done, nil
}

// telemetry summarizes ep up to its end, or up to now while it is still
// active; nil without a Telemetry dependency.
func (s *Session) telemetry(ep journal.Episode, rd *scenario.Rendered, now time.Time) *journal.Telemetry {
	if s.d.Telemetry == nil {
		return nil
	}
	if ep.EndedAt == nil {
		ep.EndedAt = &now
	}
	t := s.d.Telemetry(ep, rd)
	return &t
}

// Submit grades an exam episode once and finishes it as Solved when every
// check passes, Failed otherwise. The submit is judged at the moment it
// starts: a submit started before the deadline counts even when grading
// runs past it, and the episode ends at that moment. A context that ends
// while grading returns its error and leaves the exam active.
func (s *Session) Submit(ctx context.Context) (grader.Grade, *journal.Episode, error) {
	at := s.d.Now()
	ep, rd, err := s.activeAt(at)
	if err != nil {
		return grader.Grade{}, nil, err
	}
	if ep.Mode != journal.ModeExam {
		return grader.Grade{}, nil, fmt.Errorf("submit: %w; run: kubedrill check", ErrNotExam)
	}
	g, err := s.grade(ctx, rd)
	if err != nil {
		return grader.Grade{}, nil, err
	}
	outcome := journal.OutcomeFailed
	if g.Passed {
		outcome = journal.OutcomeSolved
	}
	done, err := s.finish(ctx, ep, rd, &g, outcome, at)
	if err != nil {
		if ended, ok := s.endedElsewhere(ep.ID, err); ok {
			return g, ended, nil
		}
		return g, nil, err
	}
	return g, done, nil
}

// Expire ends an exam episode past its deadline as TimedOut, after a final
// grade that is recorded but does not change the outcome, except that a
// pass graded within 30 seconds after the deadline (the learner was
// submitting as time ran out) counts as Solved. The deadline and the grace
// window are judged at the moment Expire starts, which is also the end
// time recorded, so a slow grade does not move either. The episode is finished
// even when the final grade fails; the grading error is returned with the
// finished episode. A context that ends while grading returns its error
// and finishes nothing.
func (s *Session) Expire(ctx context.Context) (*journal.Episode, error) {
	ep, rd, err := s.Current()
	if err != nil {
		return nil, err
	}
	if ep.Mode != journal.ModeExam || ep.Deadline == nil {
		return nil, fmt.Errorf("expire: %w", ErrNotExam)
	}
	at := s.d.Now()
	if !pastDeadlineAt(ep, at) {
		return nil, fmt.Errorf("expire: deadline %s: %w", ep.Deadline.Format(time.RFC3339), ErrNotExpired)
	}
	outcome := journal.OutcomeTimedOut
	g, gradeErr := s.grade(ctx, rd)
	if gradeErr != nil && ctx.Err() != nil {
		return nil, gradeErr
	}
	var final *grader.Grade
	if gradeErr == nil {
		final = &g
		if g.Passed && !at.After(ep.Deadline.Add(expireGrace)) {
			outcome = journal.OutcomeSolved
		}
	}
	done, err := s.finish(ctx, ep, rd, final, outcome, at)
	if err != nil {
		return nil, err
	}
	if gradeErr != nil {
		return done, fmt.Errorf("final grade of %s: %w", ep.ScenarioID, gradeErr)
	}
	return done, nil
}

// CheckNamespace verifies that the namespace of the active episode exists
// and carries its seed. It returns an error wrapping ErrNamespaceMissing or
// ErrNamespaceForeign, with the learner-facing text, when it does not, and
// ErrNoActiveEpisode without an episode.
func (s *Session) CheckNamespace(ctx context.Context) error {
	_, rd, err := s.Current()
	if err != nil {
		return err
	}
	return s.checkNamespace(ctx, rd)
}

// checkNamespace verifies that the episode's namespace exists and carries the
// episode's seed before a grade reads it.
func (s *Session) checkNamespace(ctx context.Context, rd *scenario.Rendered) error {
	labels, err := s.d.Kube.NamespaceLabels(ctx, rd.Namespace)
	if err != nil {
		return fmt.Errorf("check namespace %s: %w", rd.Namespace, err)
	}
	if labels == nil {
		return fmt.Errorf("namespace %s is missing; it was deleted or belongs to another episode; run: kubedrill reset: %w", rd.Namespace, ErrNamespaceMissing)
	}
	if got, ok := labels[seedLabel]; ok && got != strconv.FormatInt(rd.Seed, 10) {
		return fmt.Errorf("namespace %s belongs to another episode (seed %s); run: kubedrill reset: %w", rd.Namespace, got, ErrNamespaceForeign)
	}
	return nil
}

// Diagnose records the learner's one-line root cause (10 to 300
// characters, counted as runes) on the active episode, replacing an earlier
// one.
func (s *Session) Diagnose(text string) (*journal.Episode, error) {
	ep, _, err := s.active()
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if n := len([]rune(text)); n < minDiagnosis || n > maxDiagnosis {
		return nil, fmt.Errorf("diagnosis must be %d to %d characters, got %d", minDiagnosis, maxDiagnosis, n)
	}
	d := journal.Diagnosis{Text: text, At: s.d.Now()}
	done, err := s.d.Journal.Modify(ep.ID, func(e *journal.Episode) error {
		e.Diagnosis = &d
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("record diagnosis of %s: %w", ep.ScenarioID, err)
	}
	return done, nil
}

// Hint returns the next unseen hint and records its use and reveal time.
// Index is 1-based. The last hint needs a recorded diagnosis
// (ErrDiagnosisRequired); exam episodes have no hints (ErrExamMode). The
// hint count is read and advanced under the journal lock, so two processes
// asking at once reveal two different hints.
func (s *Session) Hint() (hint string, index, total int, err error) {
	ep, rd, err := s.active()
	if err != nil {
		return "", 0, 0, err
	}
	hints := rd.Hints
	if ep.Mode == journal.ModeExam {
		return "", ep.HintsUsed, len(hints), fmt.Errorf("hint: %w", ErrExamMode)
	}
	used := ep.HintsUsed
	_, err = s.d.Journal.Modify(ep.ID, func(e *journal.Episode) error {
		used = e.HintsUsed
		if e.HintsUsed >= len(hints) {
			return ErrNoMoreHints
		}
		if e.HintsUsed == len(hints)-1 && e.Diagnosis == nil {
			return fmt.Errorf("the last hint names the fix; commit to a cause first: kubedrill diagnose \"<one line>\": %w", ErrDiagnosisRequired)
		}
		hint = hints[e.HintsUsed]
		e.HintsUsed++
		e.HintReveals = append(e.HintReveals, s.d.Now())
		used = e.HintsUsed
		return nil
	})
	switch {
	case errors.Is(err, ErrNoMoreHints), errors.Is(err, ErrDiagnosisRequired):
		return "", used, len(hints), err
	case err != nil:
		return "", 0, 0, fmt.Errorf("record hint %d of %s: %w", used+1, ep.ScenarioID, err)
	}
	return hint, used, len(hints), nil
}

// Surrender finishes the active practice episode as Abandoned, with
// telemetry, and returns the rendered solution walkthrough. It needs a
// recorded diagnosis (ErrDiagnosisRequired); an exam episode returns
// ErrExamMode before its deadline and ErrTimedOut after it.
func (s *Session) Surrender(ctx context.Context) (solution string, err error) {
	ep, rd, err := s.active()
	if err != nil {
		return "", err
	}
	if ep.Mode == journal.ModeExam {
		return "", fmt.Errorf("solution: %w until the time box ends or the exam is submitted", ErrExamMode)
	}
	if ep.Diagnosis == nil {
		return "", fmt.Errorf("commit to a cause before reading the solution: kubedrill diagnose \"<one line>\": %w", ErrDiagnosisRequired)
	}
	if _, err := s.finish(ctx, ep, rd, nil, journal.OutcomeAbandoned, s.d.Now()); err != nil {
		return "", err
	}
	return rd.Solution, nil
}

// RecordDebrief attaches d to the episode with id, finished or active. When
// matched is not nil and the episode has a diagnosis, it records whether
// the diagnosis matched the reference root cause. A zero d.At is set to now.
func (s *Session) RecordDebrief(id string, d journal.Debrief, matched *bool) error {
	if d.At.IsZero() {
		d.At = s.d.Now()
	}
	apply := func(e *journal.Episode) {
		e.Debrief = &d
		if matched != nil && e.Diagnosis != nil {
			m := *matched
			diag := *e.Diagnosis
			diag.Matched = &m
			e.Diagnosis = &diag
		}
	}
	_, err := s.d.Journal.Modify(id, func(e *journal.Episode) error {
		apply(e)
		return nil
	})
	if err == nil {
		return nil
	}
	if !notCurrent(err) {
		return fmt.Errorf("record debrief of %s: %w", id, err)
	}
	if _, err := s.d.Journal.ModifyEnded(id, func(e *journal.Episode) error {
		apply(e)
		return nil
	}); err != nil {
		return fmt.Errorf("record debrief of %s: %w", id, err)
	}
	return nil
}

// Current returns the active episode and its rendered scenario, or
// ErrNoActiveEpisode. The scenario renders in the episode's namespace, so
// it reads the same as when the episode started.
func (s *Session) Current() (*journal.Episode, *scenario.Rendered, error) {
	ep, err := s.d.Journal.Current()
	if err != nil {
		return nil, nil, fmt.Errorf("read current episode: %w", err)
	}
	if ep == nil {
		return nil, nil, ErrNoActiveEpisode
	}
	rd, err := s.render(ep)
	if err != nil {
		return nil, nil, err
	}
	return ep, rd, nil
}

// render renders the scenario of ep with its seed in its namespace.
func (s *Session) render(ep *journal.Episode) (*scenario.Rendered, error) {
	sc, err := s.d.Catalog.Find(ep.ScenarioID)
	if err != nil {
		return nil, fmt.Errorf("current episode: %w", err)
	}
	rd, err := sc.Render(ep.Seed, scenario.WithNamespace(ep.Namespace))
	if err != nil {
		return nil, fmt.Errorf("render scenario %s seed %d: %w", ep.ScenarioID, ep.Seed, err)
	}
	return rd, nil
}
