package cli

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

func (a *app) listCommand() *cobra.Command {
	var level string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scenarios with level, type, minutes, your median, the last 4 attempts and status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runList(level, true)
		},
	}
	cmd.Flags().StringVar(&level, "level", "", "only this level, for example L0")
	return cmd
}

func sortedScenarios(cat Catalog) []*scenario.Scenario {
	all := append([]*scenario.Scenario(nil), cat.All()...)
	sort.Slice(all, func(i, j int) bool { return all[i].Meta.ID < all[j].Meta.ID })
	return all
}

// listLegend explains the list columns and what a clean solve is.
const listLegend = "MIN: expected minutes; YOURS: your median solve; LAST4: clean solves among your last attempts (up to 4); clean = solved with 0 hints within MIN"

func (a *app) runList(level string, recommend bool) error {
	cat, err := a.getCatalog()
	if err != nil {
		return err
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	current, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	eps, err := allEpisodes(j)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, sc := range sortedScenarios(cat) {
		if level != "" && !strings.EqualFold(sc.Meta.Level, level) {
			continue
		}
		m := progress.ScenarioMastery(episodesOf(eps, sc.Meta.ID), scenarioInfo(sc))
		active := current != nil && current.ScenarioID == sc.Meta.ID
		rows = append(rows, []string{sc.Meta.ID, sc.Meta.Title, sc.Meta.Level, string(sc.Meta.Type), strconv.Itoa(sc.Meta.Minutes), render.Minutes(m.MedianSolve), lastCell(m), statusOf(m, active)})
	}
	if len(rows) == 0 {
		a.println("no scenarios found")
		return nil
	}
	header := []string{"ID", "TITLE", "LEVEL", "TYPE", "MIN", "YOURS", "LAST4", "STATUS"}
	if a.term.IsTerminal {
		header, rows = fitListTable(header, rows, a.term.Width())
	}
	table(a.stdout, header, rows)
	if !recommend {
		return nil
	}
	a.println(a.printer().WrapLine(listLegend, "  "))
	a.println()
	if current != nil {
		next := "kubedrill check"
		if current.Mode == journal.ModeExam {
			next = "kubedrill submit"
		}
		a.printf("Active: %s (seed %d); continue with: %s\n", current.ScenarioID, current.Seed, next)
		return nil
	}
	rec, ok := a.recommend(cat, eps)
	if !ok {
		a.println("every existing scenario is mastered and no review is due; curriculum.yaml lists the planned ones, CONTRIBUTING.md explains how to add one")
		return nil
	}
	a.printRecommendation(cat, rec, "Next")
	return nil
}

// listNarrowWidth is the terminal width under which the list table drops the
// TYPE column and cuts titles so the table fits.
const listNarrowWidth = 100

// listTitleMin is the shortest title column a narrow list keeps.
const listTitleMin = 12

// fitListTable makes the list table fit width columns on a narrow terminal:
// under listNarrowWidth it drops the TYPE column (index 3) and cuts the
// TITLE cells (index 1) with "..." so the widest row fits; at
// listNarrowWidth or more the table is returned unchanged.
func fitListTable(header []string, rows [][]string, width int) ([]string, [][]string) {
	if width <= 0 || width >= listNarrowWidth {
		return header, rows
	}
	const typeCol, titleCol = 3, 1
	header = slices.Delete(slices.Clone(header), typeCol, typeCol+1)
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, slices.Delete(slices.Clone(r), typeCol, typeCol+1))
	}
	others := 0
	for i := range header {
		if i == titleCol {
			continue
		}
		w := len(header[i])
		for _, r := range out {
			w = max(w, lipgloss.Width(r[i]))
		}
		others += w + 2
	}
	room := max(width-others, listTitleMin)
	for _, r := range out {
		r[titleCol] = render.Truncate(r[titleCol], room)
	}
	return header, out
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Cluster, active episode and progress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.getCluster()
			if err != nil {
				return err
			}
			switch exists, err := cl.Exists(cmd.Context()); {
			case err != nil:
				a.printf("cluster %s: unknown (%v)\n", cluster.Name, err)
			case exists && a.insideWorkstation():
				a.printf("cluster %s: present (inside the workstation)\n", cluster.Name)
			case exists:
				a.printf("cluster %s: present\n", cluster.Name)
			default:
				a.printf("cluster %s: missing; create it with: kubedrill up\n", cluster.Name)
			}
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			cur, err := j.Current()
			if err != nil {
				return fmt.Errorf("read current episode: %w", err)
			}
			if cur == nil {
				a.println("episode: none; start one with: kubedrill start <id>")
			} else {
				a.printf("episode: %s (seed %d, %s), elapsed %s, %s\n", cur.ScenarioID, cur.Seed, render.OrDash(string(cur.Mode)), render.Duration(cur.DurationAt(a.now())), render.Plural(cur.HintsUsed, "hint"))
			}
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			eps, err := allEpisodes(j)
			if err != nil {
				return err
			}
			solved, mastered := 0, 0
			all := cat.All()
			for _, sc := range all {
				m := progress.ScenarioMastery(episodesOf(eps, sc.Meta.ID), scenarioInfo(sc))
				if m.Solved > 0 {
					solved++
				}
				if m.Mastered {
					mastered++
				}
			}
			a.printf("solved: %d of %d scenarios (mastered: %d)\n", solved, len(all), mastered)
			a.printf("journal: %s\n", j.Dir())
			return nil
		},
	}
}

// solvedSet returns the ids with at least one solved episode.
func solvedSet(eps []journal.Episode) map[string]bool {
	set := map[string]bool{}
	for _, e := range eps {
		if e.Outcome == journal.OutcomeSolved {
			set[e.ScenarioID] = true
		}
	}
	return set
}

func (a *app) journalCommand() *cobra.Command {
	var ref string
	var last int
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "List recorded episodes, newest first",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runJournal(ref, last)
		},
	}
	cmd.Flags().StringVar(&ref, "scenario", "", "only episodes of this scenario (<ref>: id, numeric prefix or id prefix)")
	cmd.Flags().IntVar(&last, "last", defaultJournalLast, "number of episodes to show (0: all)")
	return cmd
}

func (a *app) runJournal(ref string, last int) error {
	if last < 0 {
		return fmt.Errorf("--last must be >= 0, got %d", last)
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	id := ""
	if ref != "" {
		cat, err := a.getCatalog()
		if err != nil {
			return err
		}
		sc, err := cat.Find(ref)
		if err != nil {
			return err
		}
		id = sc.Meta.ID
	}
	eps, err := j.Episodes(id)
	if err != nil {
		return fmt.Errorf("read episodes: %w", err)
	}
	a.warnJournalProblems(j)
	if len(eps) == 0 {
		if id != "" {
			a.printf("no episodes for %s\n", id)
		} else {
			a.println("no episodes recorded")
		}
		return nil
	}
	sort.SliceStable(eps, func(i, k int) bool { return eps[i].StartedAt.After(eps[k].StartedAt) })
	if last > 0 && len(eps) > last {
		eps = eps[:last]
	}
	rows := make([][]string, 0, len(eps))
	hidden := false
	for _, e := range eps {
		variant := e.Variant
		if e.EndedAt == nil {
			variant = "-"
			hidden = true
		}
		rows = append(rows, []string{
			e.StartedAt.Local().Format("2006-01-02 15:04"),
			e.ScenarioID,
			strconv.FormatInt(e.Seed, 10),
			modeCell(e),
			variant,
			outcomeWord(e.Outcome),
			render.Minutes(e.DurationAt(a.now())),
			strconv.Itoa(e.HintsUsed),
		})
	}
	table(a.stdout, []string{"STARTED", "SCENARIO", "SEED", "MODE", "VARIANT", "OUTCOME", "DURATION", "HINTS"}, rows)
	if hidden {
		a.println("- : hidden while the episode is active")
	}
	return nil
}

// modeCell is the MODE of a journal row: exam, review (a practice episode
// on a review seed) or practice.
func modeCell(e journal.Episode) string {
	switch {
	case e.Mode == journal.ModeExam:
		return "exam"
	case progress.IsReviewSeed(e.Seed):
		return "review"
	}
	return "practice"
}

func (a *app) warnJournalProblems(j Journal) {
	for _, p := range j.Problems() {
		fmt.Fprintf(a.stderr, "warning: journal: %s\n", p)
	}
}

func (a *app) loadCurriculum() (*curriculum.Curriculum, error) {
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	c, err := a.f.Curriculum(curriculum.DefaultPath(root))
	if err != nil {
		return nil, err
	}
	if err := a.mergePacks(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (a *app) pathCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the learning ladder with progress per module",
		Long: "path prints curriculum.yaml as a ladder: each level with its hours and capstone,\n" +
			"each module with planned, existing and solved scenario counts and the mastery\n" +
			"of its concepts, then the next recommended scenario (hidden while an episode\n" +
			"is active).",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runPath()
		},
	}
}

func (a *app) runPath() error {
	c, err := a.loadCurriculum()
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
	eps, err := allEpisodes(j)
	if err != nil {
		return err
	}
	solved := solvedSet(eps)
	mastery := progress.ConceptMastery(scenarioInfos(cat), eps)
	for i, lv := range c.Levels {
		if i > 0 {
			a.println()
		}
		a.printLevel(lv, solved, mastery)
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if cur != nil {
		a.println()
		a.printf("Active: %s (seed %d); the recommendation returns once it ends\n", cur.ScenarioID, cur.Seed)
		return nil
	}
	if rec, ok := a.recommend(cat, eps); ok {
		a.println()
		a.printRecommendation(cat, rec, "Next")
	}
	return nil
}

// pathConceptsIndent is where the concepts line of a module starts and the
// hanging indent of a wrapped module line; the continuation lines of the
// concepts line align after "concepts: ".
const pathConceptsIndent = "          "

// printLevel prints one level of the ladder: its header, the capstone and
// each module with its counts and the state of its concepts.
func (a *app) printLevel(lv curriculum.Level, solved map[string]bool, mastery map[string]progress.Mastery) {
	a.printf("%s  %s  (%d h)\n", a.printer().Style(render.StyleBold, lv.ID), lv.Title, lv.Hours)
	if lv.Capstone != "" {
		a.println(a.printer().WrapLine("    capstone: "+lv.Capstone, "              "))
	}
	for _, m := range lv.Modules {
		existing, done := 0, 0
		for _, ps := range m.Scenarios {
			if ps.ID == "" {
				continue
			}
			existing++
			if solved[ps.ID] {
				done++
			}
		}
		title := m.Title
		if m.Pack != "" {
			title += " (pack " + m.Pack + ")"
		}
		a.println(a.printer().WrapLine(fmt.Sprintf("    %-5s %s: %d planned, %d existing, %d solved", m.ID, title, len(m.Scenarios), existing, done), pathConceptsIndent))
		if len(m.Concepts) == 0 {
			continue
		}
		states := make([]string, 0, len(m.Concepts))
		for _, id := range m.Concepts {
			states = append(states, conceptState(id, mastery[id]))
		}
		a.println(a.printer().WrapLine(pathConceptsIndent+"concepts: "+strings.Join(states, ", "), pathConceptsIndent+"          "))
	}
}

func (a *app) examCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "exam <CKA|CKAD|CKS>",
		Short: "Coverage of one certification by domain",
		Args:  usageArgs(1, "usage: kubedrill exam <CKA|CKAD|CKS>"),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := a.loadCurriculum()
			if err != nil {
				return err
			}
			id := strings.ToUpper(args[0])
			if !hasExam(c, id) {
				return fmt.Errorf("unknown exam %q; valid: %s", id, strings.Join(examIDs(c), ", "))
			}
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			eps, err := allEpisodes(j)
			if err != nil {
				return err
			}
			cov, err := c.ExamCoverage(id, solvedSet(eps))
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(cov))
			for _, d := range cov {
				rows = append(rows, []string{d.Domain.Title, fmt.Sprintf("%d%%", d.Domain.Weight), fmt.Sprintf("%d/%d", d.Done, len(d.Scenarios))})
			}
			table(a.stdout, []string{"DOMAIN", "WEIGHT", "DONE/TOTAL"}, rows)
			return nil
		},
	}
}

func hasExam(c *curriculum.Curriculum, id string) bool {
	for _, e := range c.Exams {
		if e.ID == id {
			return true
		}
	}
	return false
}

func examIDs(c *curriculum.Curriculum) []string {
	ids := make([]string, 0, len(c.Exams))
	for _, e := range c.Exams {
		ids = append(ids, e.ID)
	}
	return ids
}

func (a *app) statsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Solves by level, median time, hints per solve, outcomes, verdicts and concepts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			eps, err := allEpisodes(j)
			if err != nil {
				return err
			}
			a.writeStats(progress.Stats(scenarioInfos(cat), eps), solvedVerdicts(eps))
			return nil
		},
	}
}

// statsLegend explains the concept columns.
const statsLegend = "LAST4: clean solves among the last 4 attempts (clean = solved with 0 hints within the expected minutes); SESSIONS: sittings among those clean solves, split by a 3 h gap; MASTERED: 3 clean of the last 4 on distinct seeds over 2 sessions"

// solvedVerdicts counts the telemetry verdict of every solved episode.
func solvedVerdicts(eps []journal.Episode) map[string]int {
	out := map[string]int{}
	for _, e := range eps {
		if e.Outcome == journal.OutcomeSolved && e.Telemetry != nil && e.Telemetry.Verdict != "" {
			out[string(e.Telemetry.Verdict)]++
		}
	}
	return out
}

// writeStats prints the summary; verdicts counts solved episodes only.
func (a *app) writeStats(s progress.Summary, verdicts map[string]int) {
	a.println(a.printer().Style(render.StyleBold, "Solves by level"))
	levels := sortedMapKeys(s.ByLevel)
	rows := make([][]string, 0, len(levels))
	for _, l := range levels {
		st := s.ByLevel[l]
		rows = append(rows, []string{l, strconv.Itoa(st.Attempts), strconv.Itoa(st.Solved)})
	}
	table(a.stdout, []string{"LEVEL", "ATTEMPTS", "SOLVED"}, rows)
	a.println()
	a.printf("median solve: %s\n", render.Minutes(s.MedianSolve))
	a.printf("hints per solve: %.1f\n", s.HintsPerSolve)
	a.println()
	a.println(a.printer().Style(render.StyleBold, "Outcomes"))
	outcomes := make([]string, 0, len(s.Outcomes))
	for o := range s.Outcomes {
		outcomes = append(outcomes, string(o))
	}
	sort.Strings(outcomes)
	rows = rows[:0]
	for _, o := range outcomes {
		rows = append(rows, []string{o, strconv.Itoa(s.Outcomes[journal.Outcome(o)])})
	}
	table(a.stdout, []string{"OUTCOME", "EPISODES"}, rows)
	a.println()
	a.println(a.printer().Style(render.StyleBold, "Verdicts"))
	rows = rows[:0]
	for _, v := range sortedMapKeys(verdicts) {
		rows = append(rows, []string{v, strconv.Itoa(verdicts[v])})
	}
	table(a.stdout, []string{"VERDICT", "SOLVES"}, rows)
	a.println()
	a.println(a.printer().Style(render.StyleBold, "Concepts"))
	rows = rows[:0]
	for _, c := range sortedMapKeys(s.Concepts) {
		m := s.Concepts[c]
		rows = append(rows, []string{c, strconv.Itoa(m.Attempts), strconv.Itoa(m.Solved), lastCell(m), strconv.Itoa(m.Sessions), yesNo(m.Mastered)})
	}
	table(a.stdout, []string{"CONCEPT", "ATTEMPTS", "SOLVED", "LAST4", "SESSIONS", "MASTERED"}, rows)
	a.println(a.printer().WrapLine(statsLegend, "  "))
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
