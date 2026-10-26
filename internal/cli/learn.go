package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	conceptsCountHead    = "SCENARIOS"
	conceptsSummaryWidth = 78
	conceptsIndent       = "    "
)

func (a *App) learnCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn [id]",
		Short: "Read the fundamentals behind the active scenario, or one concept page",
		Long: "learn prints concept pages: the fundamentals behind a scenario, written to be\n" +
			"read before or during a practice episode. Without an argument it prints every concept\n" +
			"the active scenario lists, in order; with an id it prints that page only.\n" +
			"It is refused during an exam.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := a.refuseInExam("learn"); err != nil {
				return err
			}
			if len(args) == 1 {
				return a.learnOne(args[0])
			}
			return a.learnActive()
		},
	}
}

// refuseInExam returns the exam refusal of cmd when the active episode is
// an exam.
func (a *App) refuseInExam(cmd string) error {
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if cur != nil && cur.Mode == journal.ModeExam {
		return examRefusal(cmd)
	}
	return nil
}

func (a *App) learnOne(id string) error {
	lib, err := a.getConcepts()
	if err != nil {
		return err
	}
	c, err := lib.Get(id)
	if err != nil {
		if errors.Is(err, concepts.ErrNotFound) {
			return fmt.Errorf("concept %q not found; see kubedrill concepts", id)
		}
		return err
	}
	a.printConcept(c)
	return nil
}

func (a *App) learnActive() error {
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if cur == nil {
		lib, err := a.getConcepts()
		if err != nil {
			return err
		}
		return fmt.Errorf("no active episode; kubedrill learn <id> prints one concept: %s", strings.Join(lib.IDs(), ", "))
	}
	cat, err := a.getCatalog()
	if err != nil {
		return err
	}
	sc, err := cat.Find(cur.ScenarioID)
	if err != nil {
		return err
	}
	if len(sc.Meta.Concepts) == 0 {
		a.printf("%s lists no concepts; see kubedrill concepts\n", sc.Meta.ID)
		return nil
	}
	lib, err := a.getConcepts()
	if err != nil {
		return err
	}
	pages := make([]concepts.Concept, 0, len(sc.Meta.Concepts))
	for _, id := range sc.Meta.Concepts {
		c, err := lib.Get(id)
		if err != nil {
			return fmt.Errorf("scenario %s lists concept %q: %w", sc.Meta.ID, id, err)
		}
		pages = append(pages, c)
	}
	a.printf("%s\n", a.style(styleBold, fmt.Sprintf("Fundamentals of %s (%s)", sc.Meta.Title, sc.Meta.ID)))
	for _, c := range pages {
		a.println()
		a.printConcept(c)
	}
	return nil
}

// printConcept prints the header line and the rendered page of c.
func (a *App) printConcept(c concepts.Concept) {
	a.printf("%s  %s\n", a.style(styleAccent, c.ID), a.style(styleBold, c.Title))
	a.printf("%s", a.markdown(c.Body))
}

func (a *App) conceptsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "concepts",
		Short: "List the concept pages with the number of scenarios that reference each",
		Long: "concepts lists every page under docs/concepts/ with its id, title, the number of\n" +
			"scenarios that reference it and its summary. kubedrill learn <id> prints one\n" +
			"page; kubedrill learn prints those of the active scenario.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			lib, err := a.getConcepts()
			if err != nil {
				return err
			}
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			all := lib.All()
			if len(all) == 0 {
				a.println("no concept pages found")
				return nil
			}
			writeConcepts(a.stdout, all, cat.All())
			return nil
		},
	}
}

// writeConcepts prints the concepts table: ID, TITLE and SCENARIOS aligned,
// each row followed by its summary wrapped on indented lines so no line
// exceeds conceptsSummaryWidth columns.
func writeConcepts(w io.Writer, all []concepts.Concept, scenarios []*scenario.Scenario) {
	refs := map[string]int{}
	for _, sc := range scenarios {
		for _, id := range sc.Meta.Concepts {
			refs[id]++
		}
	}
	idW, titleW := len("ID"), len("TITLE")
	for _, c := range all {
		idW = max(idW, len(c.ID))
		titleW = max(titleW, len(c.Title))
	}
	fmt.Fprintf(w, "%-*s  %-*s  %s\n", idW, "ID", titleW, "TITLE", conceptsCountHead)
	for _, c := range all {
		fmt.Fprintf(w, "%-*s  %-*s  %s\n", idW, c.ID, titleW, c.Title, strconv.Itoa(refs[c.ID]))
		summary := strings.Join(strings.Fields(c.Summary), " ")
		if summary == "" {
			continue
		}
		for _, line := range strings.Split(wrap(summary, conceptsSummaryWidth-len(conceptsIndent)), "\n") {
			fmt.Fprintf(w, "%s%s\n", conceptsIndent, line)
		}
	}
}
