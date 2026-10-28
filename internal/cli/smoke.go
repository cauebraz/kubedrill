package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// smokeOptions are the flags of kubedrill smoke.
type smokeOptions struct {
	force       bool
	verbose     bool
	noShortcuts bool
}

func (a *app) smokeCommand() *cobra.Command {
	var o smokeOptions
	cmd := &cobra.Command{
		Use:   "smoke [refs...]",
		Short: "Prove each scenario: checks fail after setup, pass after solution/solve.sh and stay failing after each shortcut, for every variant",
		Long: "smoke resets each scenario once per variant against the current cluster, asserts\n" +
			"that the checks fail right after setup, runs solution/solve.sh and asserts that\n" +
			"the checks pass. Then, for each shortcuts/<name>.sh that applies to the variant\n" +
			"(its \"# variants:\" header, every variant when absent), it resets the variant\n" +
			"again, runs the shortcut and asserts that at least one check still fails; a\n" +
			"shortcut that passes every check fails the run with its \"# why:\" line.\n" +
			"--no-shortcuts skips them. Episodes go to a throwaway journal and namespaces of\n" +
			"their own (the scenario namespace plus a random suffix), so the learner's\n" +
			"journal and episode are untouched; smoke still refuses a scenario the learner\n" +
			"has an active episode of (unless --force), since setup hooks may touch\n" +
			"cluster-wide state, and deletes every namespace it created at the end. Without\n" +
			"refs it runs the repository's scenarios; a pack scenario runs only when named.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSmoke(cmd.Context(), args, o)
		},
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "run even when the learner has an active episode of one of the scenarios")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "print solve.sh and shortcut output for passing runs too")
	cmd.Flags().BoolVar(&o.noShortcuts, "no-shortcuts", false, "skip the shortcuts/ scripts; only the setup and solve.sh proofs run")
	return cmd
}

func (a *app) runSmoke(ctx context.Context, refs []string, o smokeOptions) error {
	targets, err := a.smokeTargets(refs)
	if err != nil {
		return err
	}
	root, err := a.repoRoot()
	if err != nil {
		return err
	}
	if !o.force {
		if err := a.smokeGuard(targets); err != nil {
			return err
		}
	}
	dir, err := a.f.TempDir()
	if err != nil {
		return fmt.Errorf("smoke: create throwaway journal: %w", err)
	}
	defer os.RemoveAll(dir)
	j, err := a.f.Journal(dir)
	if err != nil {
		return fmt.Errorf("smoke: open throwaway journal in %s: %w", dir, err)
	}
	s, conn, err := a.getSession(ctx, j, nil, false)
	if err != nil {
		return err
	}
	kubeconfig, err := a.scriptKubeconfig(j)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "running scenario scripts from %s\n", root)
	namespaces := map[string]bool{}
	defer a.smokeCleanup(ctx, conn, j, namespaces)
	runs := smokePlan(targets, !o.noShortcuts)
	start := a.now()
	failures, err := a.smokeRuns(ctx, s, runs, kubeconfig, namespaces, o.verbose)
	if err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("smoke: %d of %d runs failed in %s", failures, len(runs), a.now().Sub(start).Round(time.Second))
	}
	a.printf("smoke: %d runs passed in %s\n", len(runs), a.now().Sub(start).Round(time.Second))
	return nil
}

// smokeCleanupTimeout bounds the namespace deletion at the end of smoke,
// which runs even after the command context is cancelled.
const smokeCleanupTimeout = 30 * time.Second

// smokeCleanup deletes the namespaces smoke recorded, then every other
// namespace labeled with the throwaway journal's ID: the one a failed
// Reset created before it could be recorded. It runs detached from ctx's
// cancellation, under smokeCleanupTimeout.
func (a *app) smokeCleanup(ctx context.Context, conn *lazyConn, j Journal, namespaces map[string]bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), smokeCleanupTimeout)
	defer cancel()
	for _, ns := range sortedMapKeys(namespaces) {
		if err := conn.DeleteNamespace(ctx, ns); err != nil {
			a.printf("warn delete namespace %s: %v\n", ns, err)
		}
	}
	selector := env.NamespaceSelector(j.JournalID())
	if _, err := conn.DeleteNamespaces(ctx, selector, ""); err != nil {
		a.printf("warn delete namespaces %s: %v\n", selector, err)
	}
}

// smokeTargets resolves refs to scenarios, or every scenario of the
// repository in id order when there are none; a pack scenario runs only
// when named, since its scripts are third-party code.
func (a *app) smokeTargets(refs []string) ([]*scenario.Scenario, error) {
	cat, err := a.getCatalog()
	if err != nil {
		return nil, err
	}
	var targets []*scenario.Scenario
	if len(refs) == 0 {
		for _, s := range sortedScenarios(cat) {
			if s.Pack == "" {
				targets = append(targets, s)
			}
		}
	}
	for _, ref := range refs {
		sc, err := cat.Find(ref)
		if err != nil {
			return nil, err
		}
		targets = append(targets, sc)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("smoke: no scenarios found")
	}
	return targets, nil
}

// smokeRun is one reset of a scenario variant: the setup and solve.sh
// proof when shortcut is nil, otherwise the proof that the shortcut leaves
// a check failing.
type smokeRun struct {
	sc       *scenario.Scenario
	seed     int64
	variant  string
	shortcut *scenario.Shortcut
}

// smokePlan lists the runs of targets in order: per variant seed the
// solve.sh run, then, when shortcuts is true, one run per shortcut that
// applies to that variant.
func smokePlan(targets []*scenario.Scenario, shortcuts bool) []smokeRun {
	var runs []smokeRun
	for _, sc := range targets {
		for _, seed := range sc.VariantSeeds() {
			variant := sc.VariantFor(seed).Name
			runs = append(runs, smokeRun{sc: sc, seed: seed, variant: variant})
			if !shortcuts {
				continue
			}
			for i := range sc.Shortcuts {
				if sc.Shortcuts[i].AppliesTo(variant) {
					runs = append(runs, smokeRun{sc: sc, seed: seed, variant: variant, shortcut: &sc.Shortcuts[i]})
				}
			}
		}
	}
	return runs
}

// smokeRuns executes runs in order, printing one ok or FAIL line per run
// with its duration (and the script output on a failure, or always when
// verbose), and
// returns the failure count; a cancelled context stops it with an error.
func (a *app) smokeRuns(ctx context.Context, s *env.Session, runs []smokeRun, kubeconfig string, namespaces map[string]bool, verbose bool) (int, error) {
	failures := 0
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return failures, fmt.Errorf("smoke: %w", err)
		}
		var output bytes.Buffer
		label := fmt.Sprintf("%s seed %d (variant %s)", run.sc.Meta.ID, run.seed, run.variant)
		var failed, detail string
		start := a.now()
		if run.shortcut == nil {
			failed = a.smokeSeed(ctx, s, run.sc, run.seed, kubeconfig, namespaces, &output)
		} else {
			failed, detail = a.smokeShortcut(ctx, s, run.sc, run.seed, *run.shortcut, kubeconfig, namespaces, &output)
			label += " shortcut " + run.shortcut.Name
		}
		took := a.now().Sub(start).Round(time.Second)
		switch {
		case failed != "":
			failures++
			a.printf("FAIL %s: %s (%s)\n", label, failed, took)
		case detail != "":
			a.printf("ok   %s: %s (%s)\n", label, detail, took)
		default:
			a.printf("ok   %s (%s)\n", label, took)
		}
		if output.Len() > 0 && (failed != "" || verbose) {
			a.printf("%s", indent(output.String()))
		}
	}
	return failures, nil
}

// smokeGuard refuses to run when the learner's active episode is of one of
// the targets, naming the namespace that episode runs in.
func (a *app) smokeGuard(targets []*scenario.Scenario) error {
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	active, err := j.Current()
	if err != nil {
		return fmt.Errorf("smoke: read current episode: %w", err)
	}
	if active == nil {
		return nil
	}
	for _, sc := range targets {
		if sc.Meta.ID == active.ScenarioID {
			where := "in namespace " + active.Namespace
			if active.Namespace == "" {
				where = "since " + active.StartedAt.Local().Format("2006-01-02 15:04")
			}
			return fmt.Errorf("an active episode of %s runs %s; setup hooks may change cluster-wide state, so finish it (kubedrill reset, or kubedrill diagnose \"<cause>\" then kubedrill solution --yes) or pass --force", active.ScenarioID, where)
		}
	}
	return nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "     " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// smokeSeed runs one reset -> observe -> solve -> grade cycle and returns
// an empty string, or the failure description. The namespace used is added
// to namespaces; solve.sh output goes to output.
func (a *app) smokeSeed(ctx context.Context, s *env.Session, sc *scenario.Scenario, seed int64, kubeconfig string, namespaces map[string]bool, output *bytes.Buffer) string {
	ep, rd, err := s.Reset(ctx, sc.Meta.ID, seed)
	if err != nil {
		return "reset: " + err.Error()
	}
	namespaces[rd.Namespace] = true
	g, err := s.Observe(ctx)
	if err != nil {
		return "observe after setup: " + err.Error()
	}
	if g.Passed {
		return "checks pass right after setup; the setup must leave the checks failing"
	}
	if sc.SolveScript == "" {
		return "solution/solve.sh is missing"
	}
	if err := a.hookRunner(output)(ctx, sc.SolveScript, env.ScriptEnv(rd, kubeconfig, ep.ID)); err != nil {
		return "solve.sh: " + err.Error()
	}
	g, _, err = s.Grade(ctx)
	if err != nil {
		return "grade after solve: " + err.Error()
	}
	if !g.Passed {
		return "checks still failing after solve.sh: " + failingChecks(g)
	}
	return ""
}

// smokeShortcut resets the variant of seed, runs the shortcut sh and
// grades without recording: the run passes when at least one check is not
// passing, and detail names those checks. It returns the failure
// description (empty on success) and detail. The namespace used is added
// to namespaces; the shortcut output goes to output.
func (a *app) smokeShortcut(ctx context.Context, s *env.Session, sc *scenario.Scenario, seed int64, sh scenario.Shortcut, kubeconfig string, namespaces map[string]bool, output *bytes.Buffer) (failed, detail string) {
	ep, rd, err := s.Reset(ctx, sc.Meta.ID, seed)
	if err != nil {
		return "reset: " + err.Error(), ""
	}
	namespaces[rd.Namespace] = true
	if err := a.hookRunner(output)(ctx, sh.Script, env.ScriptEnv(rd, kubeconfig, ep.ID)); err != nil {
		return fmt.Sprintf("shortcut %s: %v", sh.Name, err), ""
	}
	g, err := s.Observe(ctx)
	if err != nil {
		return fmt.Sprintf("observe after shortcut %s: %v", sh.Name, err), ""
	}
	if g.Passed {
		return fmt.Sprintf("shortcut %s passed every check: %s", sh.Name, sh.Why), ""
	}
	return "", "still failing " + failingIDs(g)
}

func failingIDs(g grader.Grade) string {
	var ids []string
	for _, r := range g.Results {
		if r.Status != grader.StatusPass {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return "(score below a full pass)"
	}
	return strings.Join(ids, ", ")
}

func failingChecks(g grader.Grade) string {
	var parts []string
	for _, r := range g.Results {
		if r.Status == grader.StatusPass {
			continue
		}
		part := fmt.Sprintf("%s %s", render.StatusTag(r.Status), r.ID)
		if r.Expected != "" || r.Actual != "" {
			part += fmt.Sprintf(" (expected %s, actual %s)", render.OrDash(r.Expected), render.OrDash(r.Actual))
		} else if r.Message != "" {
			part += " (" + r.Message + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}
