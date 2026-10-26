package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// startOptions are the flags of kubedrill start.
type startOptions struct {
	seed    int64
	yes     bool
	noShell bool
	exam    bool
	layout  string
}

func (a *App) startCommand() *cobra.Command {
	var o startOptions
	cmd := &cobra.Command{
		Use:   "start <ref>",
		Short: "Start a scenario in a fresh namespace, print its ticket and open the workstation",
		Long: "start creates the episode namespace, applies the setup and records the episode.\n" +
			"On a terminal with docker available it opens the workstation (kubectl, k9s and\n" +
			"friends pinned to the training cluster), which shows the ticket; --no-shell or\n" +
			"KUBEDRILL_NO_SHELL=1 prints the ticket and stays on the host. --exam starts a\n" +
			"timed exam: no hints, no solution, no watch, one kubedrill submit.",
		Args: usageArgs(1, "usage: kubedrill start <ref>; see kubedrill list"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runStart(cmd.Context(), args[0], o)
		},
	}
	cmd.Flags().Int64Var(&o.seed, "seed", 0, "seed for the variant (0 or omitted: random)")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "leave an active episode without confirmation")
	cmd.Flags().BoolVar(&o.noShell, "no-shell", false, "print the ticket and stay on the host (also KUBEDRILL_NO_SHELL=1)")
	cmd.Flags().BoolVar(&o.exam, "exam", false, "exam mode: time box of the scenario minutes, graded once by kubedrill submit")
	addLayoutFlag(cmd, &o.layout)
	return cmd
}

func addLayoutFlag(cmd *cobra.Command, layout *string) {
	cmd.Flags().StringVar(layout, "layout", layoutTmux, "workstation layout: tmux (shell, ticket and kubedrill watch panes) or shell")
}

func (a *App) runStart(ctx context.Context, ref string, o startOptions) error {
	if o.seed < 0 {
		return fmt.Errorf("seed must be >= 1 (0 draws one), got %d", o.seed)
	}
	if err := checkLayout(o.layout); err != nil {
		return err
	}
	cat, err := a.getCatalog()
	if err != nil {
		return err
	}
	if _, err := cat.Find(ref); err != nil {
		return err
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	prev, ok, err := a.confirmLeave(j, o.yes)
	if err != nil || !ok {
		return err
	}
	ep, rd, err := a.startEpisode(ctx, j, ref, o, prev)
	if err != nil {
		return err
	}
	return a.presentEpisode(ctx, cat, j, ep, rd, o)
}

// confirmLeave returns the active episode start would leave, after the
// learner agreed to leave it (yes skips the question); ok is false when
// the learner kept it.
func (a *App) confirmLeave(j Journal, yes bool) (prev *journal.Episode, ok bool, err error) {
	prev, err = j.Current()
	if err != nil {
		return nil, false, fmt.Errorf("read current episode: %w", err)
	}
	if prev == nil {
		return nil, true, nil
	}
	what := "the active episode"
	if prev.Mode == journal.ModeExam {
		what = "the exam"
	}
	ok, err = a.confirm(fmt.Sprintf("Leave %s of %s? It will be recorded as failed.", what, prev.ScenarioID), yes)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		a.println("kept the episode active")
	}
	return prev, ok, nil
}

// startEpisode checks that the cluster exists, heals recorded faults, then
// starts the episode, leaving prev when there is one. The heal lines come
// before "leaving the episode", and a start that fails while prev is still
// current says the episode was kept.
func (a *App) startEpisode(ctx context.Context, j Journal, ref string, o startOptions, prev *journal.Episode) (*journal.Episode, *scenario.Rendered, error) {
	cl, err := a.getCluster()
	if err != nil {
		return nil, nil, err
	}
	exists, err := cl.Exists(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, fmt.Errorf("cluster %s: missing; create it with: kubedrill up", cluster.Name)
	}
	s, err := a.learnerSession()
	if err != nil {
		return nil, nil, err
	}
	mode := journal.ModePractice
	if o.exam {
		mode = journal.ModeExam
	}
	if prev != nil {
		if err := a.healFaults(ctx); err != nil {
			a.keptAfterFailedStart(j, prev)
			return nil, nil, err
		}
		a.finishProgress()
		a.printf("leaving the episode of %s (recorded as failed)\n", prev.ScenarioID)
	}
	ep, rd, err := s.Start(ctx, ref, env.StartOptions{Seed: o.seed, Mode: mode})
	if err != nil {
		a.endProgress("")
		a.keptAfterFailedStart(j, prev)
		return nil, nil, err
	}
	a.finishProgress()
	a.telemetryNotice(j)
	return ep, rd, nil
}

// keptAfterFailedStart prints that prev is still the active episode when a
// start failed before leaving it.
func (a *App) keptAfterFailedStart(j Journal, prev *journal.Episode) {
	if prev == nil {
		return
	}
	if cur, err := j.Current(); err == nil && cur != nil && cur.ID == prev.ID {
		a.printf("kept the episode of %s active\n", prev.ScenarioID)
	}
}

// healFaults undoes the recorded faults with one progress line per fault,
// "healed <action> on <node>", as Start would; Start then finds none.
func (a *App) healFaults(ctx context.Context) error {
	fm, err := a.getFaults()
	if err != nil || fm == nil {
		return err
	}
	active, err := fm.Active(ctx)
	if err != nil {
		return fmt.Errorf("heal injected faults: %w", err)
	}
	if len(active) == 0 {
		return nil
	}
	if err := fm.Heal(ctx); err != nil {
		return fmt.Errorf("heal injected faults: %w", err)
	}
	for _, f := range active {
		a.progressToStderr(fmt.Sprintf("healed %s on %s", f.Action, f.Node))
	}
	return nil
}

// presentEpisode prints what follows a started episode: the started line,
// the reminder from a past debrief, the worked example offer, then the
// ticket on the host or the workstation.
func (a *App) presentEpisode(ctx context.Context, cat Catalog, j Journal, ep *journal.Episode, rd *scenario.Rendered, o startOptions) error {
	ws, err := a.workstationToOpen(ctx, j, o)
	if err != nil {
		return err
	}
	a.printf("started %s (seed %d) in namespace %s\n", ep.ScenarioID, ep.Seed, rd.Namespace)
	eps, err := allEpisodes(j)
	if err != nil {
		return err
	}
	past := withoutEpisode(eps, ep.ID)
	if line := lastCheckFirst(cat, rd.Scenario, past); line != "" && ep.Mode != journal.ModeExam {
		a.println(a.wrapLine("Last time you said you would check first: "+line, "  "))
	}
	a.println()
	if ep.Mode != journal.ModeExam && a.interactive() {
		if err := a.offerWorkedExample(cat, ep, rd, past); err != nil {
			return err
		}
	}
	if ws == nil {
		a.printTicket(ep, rd)
		return nil
	}
	a.println(ticketInWorkstation)
	a.printFundamentals(ep, rd.Scenario)
	a.printTicketFooter(ep, rd)
	return a.openWorkstation(ctx, j, ws, o.layout, ep)
}

// workedExampleNamespace is the namespace a worked example renders in; it
// is never created.
const workedExampleNamespace = "worked-example"

// workedExampleSections are the parts of a sibling walkthrough a worked
// example shows; the observation, the change and the verification stay the
// learner's.
var workedExampleSections = []string{"## Symptom", "## Candidate causes"}

const workedExampleMethod = "Observation, change and verify are yours; the method is in kubedrill learn troubleshooting-loop"

// offerWorkedExample asks, on the first exposure to a concept, whether to
// print the symptom and candidate causes of a sibling variant before the
// ticket. An empty answer prints "worked example skipped", so an answer
// typed ahead of the prompt and lost does not pass silently.
func (a *App) offerWorkedExample(cat Catalog, ep *journal.Episode, rd *scenario.Rendered, past []journal.Episode) error {
	sc := rd.Scenario
	first := progress.FirstExposure(sc.Meta.Concepts, scenarioInfos(cat), past)
	if len(first) == 0 {
		return nil
	}
	sibling := siblingVariant(sc, ep)
	if sibling == nil {
		return nil
	}
	example := workedExample(sibling.Solution)
	if example == "" {
		return nil
	}
	switch strings.ToLower(a.ask(fmt.Sprintf("First time with %s. See a worked example from another variant before your ticket? [y/N]", conceptsLine(first)))) {
	case "":
		a.println("worked example skipped")
		return nil
	case "y", "yes":
	default:
		return nil
	}
	a.println(a.style(styleBold, fmt.Sprintf("Worked example (variant %s, not yours)", sibling.Variant.Name)))
	a.println()
	a.printf("%s", a.markdown(example))
	a.println()
	a.println(workedExampleMethod)
	a.println(a.style(styleBold, "End of the worked example; your ticket follows."))
	a.println()
	return nil
}

// workedExample keeps the Symptom and Candidate causes sections of a
// rendered walkthrough, in that order.
func workedExample(solution string) string {
	var parts []string
	for _, h := range workedExampleSections {
		if _, section, _, ok := splitSection(solution, h); ok {
			parts = append(parts, strings.TrimRight(section, "\n")+"\n")
		}
	}
	return strings.Join(parts, "\n")
}

// siblingVariant renders sc for a seed whose variant differs from the one
// ep runs, in a throwaway namespace, or returns nil when there is none.
func siblingVariant(sc *scenario.Scenario, ep *journal.Episode) *scenario.Rendered {
	n := int64(len(sc.Variants))
	for i := int64(1); i < n; i++ {
		rd, err := sc.Render(ep.Seed+i, scenario.WithNamespace(workedExampleNamespace))
		if err == nil && rd.Variant.Name != ep.Variant {
			return rd
		}
	}
	return nil
}

// workstationToOpen returns the workstation start will open, or nil when any
// gate keeps the learner on the host: the flag, the environment, a missing
// terminal, no wired workstation, or docker unavailable (a notice on stderr).
func (a *App) workstationToOpen(ctx context.Context, j Journal, o startOptions) (Workstation, error) {
	if o.noShell || a.getenv("KUBEDRILL_NO_SHELL") == "1" || a.insideWorkstation() || !a.isTerminal || !a.isInputTerminal {
		return nil, nil
	}
	ws, err := a.getWorkstation(j)
	if err != nil || ws == nil {
		return nil, err
	}
	if err := ws.DockerAvailable(ctx); err != nil {
		fmt.Fprintf(a.stderr, "workstation skipped: docker is unavailable (%s); kubectl runs on the host\n", err)
		return nil, nil
	}
	return ws, nil
}

// printTicket renders the instruction, the Fundamentals line, the footer
// and the next steps.
func (a *App) printTicket(ep *journal.Episode, rd *scenario.Rendered) {
	a.printf("%s", a.markdown(rd.Instruction))
	a.println()
	a.printFundamentals(ep, rd.Scenario)
	a.printTicketFooter(ep, rd)
	if ep.Mode == journal.ModeExam {
		a.println(a.wrapLine(a.examNextStepLine(), "  "))
		return
	}
	a.println(a.wrapLine(a.nextStepLine(), "  "))
}

// printTicketFooter names how many checks grade the ticket, the expected
// minutes next to the learner's median, and the exam deadline.
func (a *App) printTicketFooter(ep *journal.Episode, rd *scenario.Rendered) {
	sc := rd.Scenario
	a.printf("Checked by %s: %s\n", plural(len(rd.Checks), "check"), a.style(styleBold, "kubedrill check --explain"))
	expected := fmt.Sprintf("Expected: %d min", sc.Meta.Minutes)
	if j, err := a.getJournal(); err == nil {
		if eps, err := allEpisodes(j); err == nil {
			m := progress.ScenarioMastery(episodesOf(withoutEpisode(eps, ep.ID), sc.Meta.ID), sc.Meta.Minutes)
			if m.Solved > 0 && m.MedianSolve > 0 {
				expected += fmt.Sprintf(" (yours: %s median over %s)", minutesCell(m.MedianSolve), plural(m.Solved, "solve"))
			}
		}
	}
	a.println(a.wrapLine(expected, "  "))
	if ep.Mode == journal.ModeExam && ep.Deadline != nil {
		a.println(a.wrapLine(fmt.Sprintf("Exam deadline: %s (%d min time box); no hints, no solution, no watch", ep.Deadline.Local().Format("15:04:05"), sc.Meta.Minutes), "  "))
	}
}

// printFundamentals points at the concept pages of sc when it lists any,
// outside exam mode, where learn is refused.
func (a *App) printFundamentals(ep *journal.Episode, sc *scenario.Scenario) {
	if len(sc.Meta.Concepts) == 0 || ep.Mode == journal.ModeExam {
		return
	}
	a.println(a.wrapLine(fmt.Sprintf("Fundamentals: %s (%s)", a.style(styleBold, "kubedrill learn"), conceptsLine(sc.Meta.Concepts)), "  "))
}

func (a *App) nextStepLine() string {
	return "Next: " + a.style(styleBold, "kubedrill watch") + " (live checks) | " +
		a.style(styleBold, "kubedrill check") + " (grade once) | " +
		a.style(styleBold, "kubedrill hint") + " | " +
		a.style(styleBold, `kubedrill diagnose "<cause>"`)
}

func (a *App) examNextStepLine() string {
	return "Next: " + a.style(styleBold, "kubedrill submit") + " (grade once, ends the exam) | " +
		a.style(styleBold, "kubedrill check --explain") + " (what is graded)"
}

func (a *App) solvedNextLine(id string) string {
	return "Next: " + a.style(styleBold, "kubedrill solution "+id) + " (compare with the reference) | " + a.style(styleBold, "kubedrill list")
}

func (a *App) ticketCommand() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "Print the ticket of the active episode again",
		Long: "ticket prints the ticket of the active episode. --follow keeps it on screen\n" +
			"instead: a scrollable view (mouse wheel, j/k, space/b, g/G, q quits) that\n" +
			"follows resets and new starts; the workstation opens it above the shell.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if follow && (!a.isTerminal || !a.isInputTerminal) {
				return fmt.Errorf("ticket --follow needs a terminal on stdin and stdout; use kubedrill ticket in scripts and pipes")
			}
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			if follow {
				return a.runTicketFollow(cmd.Context(), s, defaultTicketInterval)
			}
			ep, rd, err := s.Current()
			if err != nil {
				return err
			}
			a.printf("%s (seed %d), namespace %s, elapsed %s\n\n", ep.ScenarioID, ep.Seed, rd.Namespace, fmtDuration(ep.DurationAt(a.now())))
			a.printTicket(ep, rd)
			return nil
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "keep the ticket on screen in a scrollable view that follows the active episode")
	return cmd
}

func (a *App) namespaceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "namespace",
		Short: "Print the namespace of the active episode",
		Long: "namespace prints the Kubernetes namespace the active episode runs in, for\n" +
			"shell prompts and scripts (the workstation uses it); exit 1 without an episode.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			_, rd, err := s.Current()
			if err != nil {
				return err
			}
			a.println(rd.Namespace)
			return nil
		},
	}
}

func (a *App) checkCommand() *cobra.Command {
	var explain bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Grade the active episode once",
		Long: "check grades the end state of the active episode once: one line per check with\n" +
			"expected and actual values, why under every failed check, then the summary.\n" +
			"Exit 2 when a check fails; the solve is recorded when all pass, followed by the\n" +
			"debrief. --explain lists every check and what it asserts without grading.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			cur, rd, err := s.Current()
			if err != nil {
				return err
			}
			if explain {
				writeExplain(a.stdout, rd.Scenario.Meta.ID, rd.Checks, cur.Mode == journal.ModeExam)
				return nil
			}
			if cur.Mode == journal.ModeExam {
				return examRefusal("check")
			}
			g, ep, err := s.Grade(cmd.Context())
			if err != nil {
				return err
			}
			if ep != nil && ep.EndedAt != nil && (!g.Passed || ep.Outcome != journal.OutcomeSolved) {
				return a.endedElsewhere(g, ep)
			}
			a.writeGrade(a.stdout, g, !g.Passed && !revealsSpoilers(cur))
			if !g.Passed {
				a.println("Next: fix what the failing checks name, then kubedrill check again | kubedrill hint")
				return ErrChecksFailed
			}
			_, err = a.afterSolve(cmd.Context(), ep)
			return err
		},
	}
	cmd.Flags().BoolVar(&explain, "explain", false, "list every check, what it asserts and why, without grading")
	return cmd
}

// endedElsewhere reports a check whose episode another process ended
// meanwhile (a submit, a reset or a solve in another pane): the grade, then
// the outcome that process recorded. It fails with ErrChecksFailed when a
// check did not pass.
func (a *App) endedElsewhere(g grader.Grade, ep *journal.Episode) error {
	a.writeGrade(a.stdout, g, false)
	a.printf("the episode of %s already ended as %s in another terminal; kubedrill debrief shows it\n", ep.ScenarioID, ep.Outcome)
	if !g.Passed {
		return ErrChecksFailed
	}
	return nil
}

func (a *App) hintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "hint",
		Short: "Show the next hint",
		Long: "hint prints the next unseen hint of the active episode and counts it in the\n" +
			"journal; a solve with hints does not count toward mastery. The last hint waits\n" +
			"for kubedrill diagnose. Once every hint is shown it says so and exits 0.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			cur, _, err := s.Current()
			if err != nil {
				return err
			}
			if cur.Mode == journal.ModeExam {
				return examRefusal("hint")
			}
			hint, index, total, err := s.Hint()
			if err != nil {
				switch {
				case errors.Is(err, env.ErrNoMoreHints):
					a.printf("all %d hints shown. Next: kubedrill solution (surrender) or keep investigating\n", total)
					return nil
				case errors.Is(err, env.ErrDiagnosisRequired):
					return diagnosisNeeded("the last hint")
				}
				return err
			}
			if n := len(cur.HintReveals); n > 0 && a.now().Sub(cur.HintReveals[n-1]) < hintNudgeWindow {
				fmt.Fprintln(a.stderr, hintNudge)
			}
			a.println(a.style(styleAccent, hintLabel(index, total)))
			a.printf("%s", a.markdown(hint))
			return nil
		},
	}
}

func (a *App) solutionCommand() *cobra.Command {
	var yes bool
	var approach string
	cmd := &cobra.Command{
		Use:   "solution [ref]",
		Short: "Show the walkthrough: surrenders the active episode, or shows a finished scenario",
		Long: "solution shows the reference walkthrough. With an active practice episode it\n" +
			"needs a recorded diagnosis, asks for confirmation, records the episode as\n" +
			"abandoned and prints the walkthrough. Without one it shows the scenario of the\n" +
			"most recent finished episode, or the one named by <ref>, with your path next to\n" +
			"the reference and the approaches; --approach <name> prints one approach.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return a.runSolution(cmd.Context(), ref, yes, approach)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	cmd.Flags().StringVar(&approach, "approach", "", "print the approach with this name (kubedrill solution lists them)")
	return cmd
}

func (a *App) runSolution(ctx context.Context, ref string, yes bool, approach string) error {
	s, err := a.learnerSession()
	if err != nil {
		return err
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cat, err := a.getCatalog()
	if err != nil {
		return err
	}
	current, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if current != nil && (ref == "" || sameScenario(cat, ref, current.ScenarioID)) {
		if approach != "" {
			return fmt.Errorf("the approaches of %s open after the episode ends; kubedrill check or kubedrill solution first", current.ScenarioID)
		}
		return a.surrender(ctx, s, current, yes)
	}
	if ref == "" {
		last, err := lastEndedEpisode(j)
		if err != nil {
			return err
		}
		if last == nil {
			return fmt.Errorf("%w; kubedrill solution <id> shows the walkthrough of a finished scenario", env.ErrNoActiveEpisode)
		}
		ref = last.ScenarioID
	}
	return a.finishedSolution(s, cat, j, ref, yes, approach)
}

// finishedSolution shows the walkthrough of a scenario without an active
// episode of it, after a confirmation when it was never solved or
// abandoned: one approach, or the walkthrough (with your path after a
// solve) and the approaches.
func (a *App) finishedSolution(s *env.Session, cat Catalog, j Journal, ref string, yes bool, approach string) error {
	sc, err := cat.Find(ref)
	if err != nil {
		return err
	}
	eps, err := allEpisodes(j)
	if err != nil {
		return err
	}
	mine := episodesOf(eps, sc.Meta.ID)
	if question := solutionQuestion(sc.Meta.ID, mine); question != "" {
		ok, err := a.confirm(question, yes)
		if err != nil {
			return err
		}
		if !ok {
			a.printf("kept the solution of %s hidden; start it with: kubedrill start %s\n", sc.Meta.ID, sc.Meta.ID)
			return nil
		}
	}
	last := latestEnded(mine)
	rd, err := renderFor(sc, last)
	if err != nil {
		return err
	}
	if approach != "" {
		return a.printApproach(rd, approach)
	}
	if last == nil || last.Outcome != journal.OutcomeSolved {
		a.printf("%s", a.markdown(rd.Solution))
	} else {
		if err := a.printSolvedSolution(s, cat, sc, rd, last, eps); err != nil {
			return err
		}
		a.printPath(last, rd.Solution)
	}
	a.printApproaches(rd)
	return nil
}

// solutionQuestion is the confirmation solution asks before showing the
// walkthrough of a scenario without an active episode, or "" when none is
// needed: never attempted, the latest episode was an exam (solved or not),
// or it ended neither solved nor abandoned.
func solutionQuestion(id string, eps []journal.Episode) string {
	latest := latestEpisode(eps)
	switch {
	case latest == nil:
		return fmt.Sprintf("Scenario %s was never solved or abandoned. Show the solution anyway?", id)
	case latest.Mode == journal.ModeExam && latest.Outcome == journal.OutcomeSolved:
		return fmt.Sprintf("Your latest episode of %s was an exam (solved); show the solution?", id)
	case latest.Mode == journal.ModeExam, latest.Outcome != journal.OutcomeSolved && latest.Outcome != journal.OutcomeAbandoned:
		return fmt.Sprintf("Your latest episode of %s ended %s; show the solution anyway?", id, latest.Outcome)
	}
	return ""
}

// latestEpisode returns the most recently started episode of eps, or nil.
func latestEpisode(eps []journal.Episode) *journal.Episode {
	var latest *journal.Episode
	for i := range eps {
		if latest == nil || eps[i].StartedAt.After(latest.StartedAt) {
			latest = &eps[i]
		}
	}
	return latest
}

// surrender ends the active practice episode after a diagnosis and a
// confirmation, then prints the walkthrough.
func (a *App) surrender(ctx context.Context, s *env.Session, current *journal.Episode, yes bool) error {
	if current.Mode == journal.ModeExam {
		return examRefusal("solution")
	}
	if current.Diagnosis == nil {
		return errSurrenderDiagnosis
	}
	ok, err := a.confirm(fmt.Sprintf("Surrender %s and show the solution? The episode will be recorded as abandoned.", current.ScenarioID), yes)
	if err != nil {
		return err
	}
	if !ok {
		a.println("kept the episode active")
		return nil
	}
	sol, err := s.Surrender(ctx)
	if err != nil {
		if errors.Is(err, env.ErrDiagnosisRequired) {
			return errSurrenderDiagnosis
		}
		return err
	}
	a.printf("episode of %s recorded as abandoned\n\n", current.ScenarioID)
	a.printf("%s", a.markdown(sol))
	return nil
}

// errSurrenderDiagnosis is the error of solution on an active episode
// without a diagnosis; --yes skips only the confirmation.
var errSurrenderDiagnosis = refusal{msg: `the solution needs your diagnosis first (--yes skips only the confirmation): kubedrill diagnose "<cause>" then kubedrill solution --yes`, cause: env.ErrDiagnosisRequired}

// renderFor renders sc as ep saw it (seed and namespace), or with seed 1
// when there is no episode.
func renderFor(sc *scenario.Scenario, ep *journal.Episode) (*scenario.Rendered, error) {
	if ep == nil {
		return sc.Render(1)
	}
	var opts []scenario.RenderOption
	if ep.Namespace != "" {
		opts = append(opts, scenario.WithNamespace(ep.Namespace))
	}
	return sc.Render(ep.Seed, opts...)
}

// latestEnded returns the most recently started ended episode of eps.
func latestEnded(eps []journal.Episode) *journal.Episode {
	var last *journal.Episode
	for i := range eps {
		e := &eps[i]
		if e.EndedAt == nil {
			continue
		}
		if last == nil || e.StartedAt.After(last.StartedAt) {
			last = e
		}
	}
	return last
}

const splitHeading = "## Observation that splits them"

// splitSection cuts md around the section that starts with heading: the
// text before it, the section itself (heading included) and the rest.
func splitSection(md, heading string) (before, section, after string, ok bool) {
	lines := strings.SplitAfter(md, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return md, "", "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[:start], ""), strings.Join(lines[start:end], ""), strings.Join(lines[end:], ""), true
}

// printSolvedSolution prints the walkthrough of a solved scenario. On a
// later exposure (another scenario sharing a concept, or another variant of
// this one, was solved earlier), a
// terminal first asks which principle made the splitting observation
// necessary and stores the answer, then reveals the section.
func (a *App) printSolvedSolution(s *env.Session, cat Catalog, sc *scenario.Scenario, rd *scenario.Rendered, ep *journal.Episode, eps []journal.Episode) error {
	before, section, after, ok := splitSection(rd.Solution, splitHeading)
	if !ok || !a.interactive() || !laterExposure(cat, sc, ep, eps) {
		a.printf("%s", a.markdown(rd.Solution))
		return nil
	}
	a.printf("%s", a.markdown(before))
	a.printf("%s", a.markdown(splitHeading+"\n"))
	if ep.Debrief != nil && ep.Debrief.Principle != "" {
		a.printf("Your principle: %s\n", ep.Debrief.Principle)
	} else if principle := a.ask("Which principle made this observation necessary? (one line)"); principle != "" {
		d := journal.Debrief{At: a.now()}
		if ep.Debrief != nil {
			d = *ep.Debrief
		}
		d.Principle = principle
		if err := s.RecordDebrief(ep.ID, d, nil); err != nil {
			return fmt.Errorf("record principle of %s: %w", ep.ID, err)
		}
		a.println("principle recorded")
	}
	a.println()
	a.printf("%s", a.markdown(section+after))
	return nil
}

// referenceCommands returns the command lines of the code blocks in the
// Change and Verify sections of a walkthrough.
func referenceCommands(md string) []string {
	var out []string
	for _, heading := range []string{"## Change", "## Verify"} {
		_, section, _, ok := splitSection(md, heading)
		if !ok {
			continue
		}
		inCode := false
		for _, l := range strings.Split(section, "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "```") {
				inCode = !inCode
				continue
			}
			if inCode && t != "" && !strings.HasPrefix(t, "#") {
				out = append(out, t)
			}
		}
	}
	return out
}

// printPath prints the learner's kubectl commands next to the reference
// commands of the walkthrough.
func (a *App) printPath(ep *journal.Episode, solution string) {
	a.println()
	a.println(a.style(styleBold, "Your path vs the reference"))
	var mine []string
	source := "none"
	if t := ep.Telemetry; t != nil {
		mine = t.Commands
		source = orDash(t.Source)
	}
	if len(mine) == 0 {
		a.printf("Your path: no kubectl commands recorded (telemetry: %s)\n", source)
		a.println(a.wrapLine("  commands typed outside the workstation are not logged; kubedrill shell records them", "  "))
	} else {
		a.printf("Your path (%s, from the shell log):\n", plural(len(mine), "kubectl command"))
		for i, c := range mine {
			a.printf("%4d. %s\n", i+1, c)
		}
	}
	ref := referenceCommands(solution)
	if len(ref) == 0 {
		return
	}
	a.println("Reference (## Change and ## Verify above):")
	for i, c := range ref {
		a.printf("%4d. %s\n", i+1, c)
	}
}

// printApproaches lists each approach of the rendered scenario with its
// rendered snippet, so the namespace in it is the episode's.
func (a *App) printApproaches(rd *scenario.Rendered) {
	if len(rd.Approaches) == 0 {
		return
	}
	a.println()
	a.printf("%s (%s prints one)\n", a.style(styleBold, "Approaches"), a.style(styleBold, "kubedrill solution --approach <name>"))
	for _, ap := range rd.Approaches {
		a.printf("  %s\n", ap.Name)
		for _, l := range strings.Split(strings.TrimRight(ap.Snippet, "\n"), "\n") {
			a.printf("      %s\n", l)
		}
	}
}

// printApproach prints one rendered approach: its snippet, then its content.
func (a *App) printApproach(rd *scenario.Rendered, name string) error {
	id := rd.Scenario.Meta.ID
	names := make([]string, 0, len(rd.Approaches))
	for _, ap := range rd.Approaches {
		if ap.Name == name {
			a.printf("%s\n\n", a.style(styleBold, fmt.Sprintf("Approach %s (%s)", ap.Name, id)))
			for _, l := range strings.Split(strings.TrimRight(ap.Snippet, "\n"), "\n") {
				a.printf("    %s\n", l)
			}
			a.println()
			a.printf("%s", a.markdown(ap.Body))
			return nil
		}
		names = append(names, ap.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("scenario %s has no approaches", id)
	}
	return fmt.Errorf("approach %q not found for %s; valid: %s", name, id, strings.Join(names, ", "))
}

// lastEndedEpisode returns the most recently started episode with an end
// time, or nil.
func lastEndedEpisode(j Journal) (*journal.Episode, error) {
	eps, err := j.Episodes("")
	if err != nil {
		return nil, fmt.Errorf("read episodes: %w", err)
	}
	var last *journal.Episode
	for i := range eps {
		e := &eps[i]
		if e.EndedAt == nil {
			continue
		}
		if last == nil || e.StartedAt.After(last.StartedAt) {
			last = e
		}
	}
	return last, nil
}

func sameScenario(cat Catalog, ref, id string) bool {
	sc, err := cat.Find(ref)
	return err == nil && sc.Meta.ID == id
}

// errResetInExam refuses reset, and the watch x key, during an exam.
var errResetInExam = refusal{msg: "reset is not available in exam mode; kubedrill submit ends the exam", cause: env.ErrExamMode}

// examRunning reports whether ep is an exam episode before its deadline.
func (a *App) examRunning(ep *journal.Episode) bool {
	return ep != nil && ep.Mode == journal.ModeExam && (ep.Deadline == nil || a.now().Before(*ep.Deadline))
}

func (a *App) resetCommand() *cobra.Command {
	var sameSeed bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Redeploy the active scenario with a new seed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			cur, _, err := s.Current()
			if err != nil {
				return err
			}
			if a.examRunning(cur) {
				return errResetInExam
			}
			var seed int64
			if sameSeed {
				seed = cur.Seed
			}
			ep, rd, err := s.Reset(cmd.Context(), cur.ScenarioID, seed)
			if err != nil {
				return err
			}
			a.finishProgress()
			line := fmt.Sprintf("reset %s: seed %d, namespace %s (hints reset to 0)", ep.ScenarioID, ep.Seed, rd.Namespace)
			if sameSeed {
				line += " (same seed)"
			}
			a.println(line)
			a.println(a.nextStepLine())
			return nil
		},
	}
	cmd.Flags().BoolVar(&sameSeed, "same-seed", false, "keep the current seed and variant")
	return cmd
}

func (a *App) diagnoseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   `diagnose "<text>"`,
		Short: "Record what you think is wrong before the last hint or the solution",
		Long: "diagnose records one line, 10 to 300 characters, naming the root cause you\n" +
			"suspect. The last hint and the solution wait for it; after the episode the\n" +
			"debrief puts it next to the reference root cause.",
		Args: usageArgs(1, "usage: "+diagnoseUsage),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			ep, err := s.Diagnose(args[0])
			if err != nil {
				return err
			}
			a.printf("diagnosis recorded for %s: %s\n", ep.ScenarioID, ep.Diagnosis.Text)
			if ep.Mode == journal.ModeExam {
				a.println("Next: change one thing, then kubedrill submit")
				return nil
			}
			a.println("Next: change one thing, then kubedrill check | kubedrill hint")
			return nil
		},
	}
}

// errSubmitPractice refuses submit on a practice episode.
var errSubmitPractice = refusal{msg: "submit grades exam episodes (kubedrill start <id> --exam); a practice episode is graded with kubedrill check", cause: env.ErrNotExam}

func (a *App) submitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "submit",
		Short: "Exam mode: grade once and end the exam",
		Long: "submit grades an exam episode once, prints every check and records the outcome:\n" +
			"solved when every check passes, failed otherwise. Exit 2 when a check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.learnerSession()
			if err != nil {
				return err
			}
			cur, _, err := s.Current()
			if err != nil {
				return err
			}
			if cur.Mode != journal.ModeExam {
				return errSubmitPractice
			}
			g, ep, err := s.Submit(cmd.Context())
			if err != nil {
				return err
			}
			a.writeGrade(a.stdout, g, false)
			if ep == nil {
				ep = cur
			}
			a.printf("exam submitted: %s in %s\n", ep.Outcome, minutesCell(ep.DurationAt(a.now())))
			if !g.Passed {
				a.println("Next: kubedrill debrief | kubedrill solution " + ep.ScenarioID + " (the reference)")
				return ErrChecksFailed
			}
			_, err = a.afterSolve(cmd.Context(), ep)
			return err
		},
	}
}
