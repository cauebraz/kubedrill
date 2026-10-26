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

func (a *app) smokeCommand() *cobra.Command {
	var force, verbose bool
	cmd := &cobra.Command{
		Use:   "smoke [refs...]",
		Short: "Prove each scenario: checks fail after setup and pass after solution/solve.sh, for every variant",
		Long: "smoke resets each scenario once per variant against the current cluster, asserts\n" +
			"that the checks fail right after setup, runs solution/solve.sh and asserts that\n" +
			"the checks pass. Episodes go to a throwaway journal and namespaces of their own\n" +
			"(the scenario namespace plus a random suffix), so the learner's journal and\n" +
			"episode are untouched; smoke still refuses a scenario the learner has an active\n" +
			"episode of (unless --force), since setup hooks may touch cluster-wide state,\n" +
			"and deletes every namespace it created at the end.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSmoke(cmd.Context(), args, force, verbose)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "run even when the learner has an active episode of one of the scenarios")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "print solve.sh output for passing runs too")
	return cmd
}

func (a *app) runSmoke(ctx context.Context, refs []string, force, verbose bool) error {
	targets, err := a.smokeTargets(refs)
	if err != nil {
		return err
	}
	root, err := a.repoRoot()
	if err != nil {
		return err
	}
	if !force {
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
	failures, err := a.smokeRuns(ctx, s, targets, kubeconfig, namespaces, verbose)
	if err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("smoke: %d of %d runs failed", failures, countRuns(targets))
	}
	a.printf("smoke: %d runs passed\n", countRuns(targets))
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

// smokeTargets resolves refs to scenarios, or every scenario in id order
// when there are none.
func (a *app) smokeTargets(refs []string) ([]*scenario.Scenario, error) {
	cat, err := a.getCatalog()
	if err != nil {
		return nil, err
	}
	var targets []*scenario.Scenario
	if len(refs) == 0 {
		targets = sortedScenarios(cat)
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

// smokeRuns runs every variant seed of every target, printing one ok or
// FAIL line per run (with the solve.sh output on a failure, or always
// when verbose), and returns the failure count; a cancelled context stops
// it with an error.
func (a *app) smokeRuns(ctx context.Context, s *env.Session, targets []*scenario.Scenario, kubeconfig string, namespaces map[string]bool, verbose bool) (int, error) {
	failures := 0
	for _, sc := range targets {
		for seed := int64(1); seed <= int64(seedCount(sc)); seed++ {
			if err := ctx.Err(); err != nil {
				return failures, fmt.Errorf("smoke: %w", err)
			}
			var output bytes.Buffer
			variant, failed := a.smokeSeed(ctx, s, sc, seed, kubeconfig, namespaces, &output)
			if failed != "" {
				failures++
				a.printf("FAIL %s seed %d (variant %s): %s\n", sc.Meta.ID, seed, variant, failed)
			} else {
				a.printf("ok   %s seed %d (variant %s)\n", sc.Meta.ID, seed, variant)
			}
			if output.Len() > 0 && (failed != "" || verbose) {
				a.printf("%s", indent(output.String()))
			}
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

func seedCount(sc *scenario.Scenario) int {
	if len(sc.Variants) == 0 {
		return 1
	}
	return len(sc.Variants)
}

func countRuns(targets []*scenario.Scenario) int {
	n := 0
	for _, sc := range targets {
		n += seedCount(sc)
	}
	return n
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "     " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// smokeSeed runs one reset -> observe -> solve -> grade cycle and returns the
// variant name and an empty string, or the failure description. The
// namespace used is added to namespaces; solve.sh output goes to output.
func (a *app) smokeSeed(ctx context.Context, s *env.Session, sc *scenario.Scenario, seed int64, kubeconfig string, namespaces map[string]bool, output *bytes.Buffer) (variant, failed string) {
	ep, rd, err := s.Reset(ctx, sc.Meta.ID, seed)
	if err != nil {
		if r, rerr := sc.Render(seed); rerr == nil {
			variant = r.Variant.Name
		}
		return variant, "reset: " + err.Error()
	}
	namespaces[rd.Namespace] = true
	variant = rd.Variant.Name
	g, err := s.Observe(ctx)
	if err != nil {
		return variant, "observe after setup: " + err.Error()
	}
	if g.Passed {
		return variant, "checks pass right after setup; the setup must leave the checks failing"
	}
	if sc.SolveScript == "" {
		return variant, "solution/solve.sh is missing"
	}
	if err := a.hookRunner(output)(ctx, sc.SolveScript, env.ScriptEnv(rd, kubeconfig, ep.ID)); err != nil {
		return variant, "solve.sh: " + err.Error()
	}
	g, _, err = s.Grade(ctx)
	if err != nil {
		return variant, "grade after solve: " + err.Error()
	}
	if !g.Passed {
		return variant, "checks still failing after solve.sh: " + failingChecks(g)
	}
	return variant, ""
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
