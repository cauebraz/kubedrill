package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	conceptsCountHead    = "SCENARIOS"
	conceptsSummaryWidth = 78
	conceptsIndent       = "    "
)

func (a *app) learnCommand() *cobra.Command {
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
func (a *app) refuseInExam(cmd string) error {
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

func (a *app) learnOne(id string) error {
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

func (a *app) learnActive() error {
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
		return fmt.Errorf("%w; kubedrill learn <id> prints one concept: %s", env.ErrNoActiveEpisode, strings.Join(lib.IDs(), ", "))
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
	a.printf("%s\n", a.printer().Style(render.StyleBold, fmt.Sprintf("Fundamentals of %s (%s)", sc.Meta.Title, sc.Meta.ID)))
	for _, c := range pages {
		a.println()
		a.printConcept(c)
	}
	return nil
}

func (a *app) storyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "story [ref]",
		Short: "Read the ticket told from zero: the objects involved, the trail of commands and why the fix works",
		Long: "story prints the story of a scenario: what happened in plain words, the\n" +
			"Kubernetes objects involved explained from zero, the read-only commands that\n" +
			"follow the trail and what each output means, then why the fix works. Without\n" +
			"an argument it prints the story of the active episode, with its namespace;\n" +
			"with a ref it prints that scenario's. The browser workspace shows it as the\n" +
			"Story tab. It is refused during an exam.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := a.refuseInExam("story"); err != nil {
				return err
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return a.printStory(ref)
		},
	}
}

// printStory prints the story of the scenario ref names, or of the active
// episode when ref is empty, rendered for that episode when it runs the
// scenario.
func (a *app) printStory(ref string) error {
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if ref == "" && cur == nil {
		return fmt.Errorf("%w; kubedrill story <id> prints the story of one scenario", env.ErrNoActiveEpisode)
	}
	if ref == "" {
		ref = cur.ScenarioID
	}
	cat, err := a.getCatalog()
	if err != nil {
		return err
	}
	sc, err := cat.Find(ref)
	if err != nil {
		return err
	}
	var ep *journal.Episode
	if cur != nil && cur.ScenarioID == sc.Meta.ID {
		ep = cur
	}
	rd, err := renderFor(sc, ep)
	if err != nil {
		return err
	}
	story := rd.Story
	if ep != nil {
		story = storyFor(story, ep, len(rd.Hints))
	}
	if story == "" {
		a.printf("%s has no story yet; kubedrill learn prints its fundamentals\n", sc.Meta.ID)
		return nil
	}
	a.printf("%s\n\n", a.printer().Style(render.StyleBold, fmt.Sprintf("Story of %s (%s)", sc.Meta.Title, sc.Meta.ID)))
	a.printf("%s", a.printer().Markdown(story))
	return nil
}

const (
	storyFixHeading = "## The fix and why it works"
	storyFixRung    = 4
	storyFixClosed  = "This section opens when the run ends, after the fourth hint, or in the guided practice."
)

// storyFixOpen reports whether the active episode ep may read the fix
// section of its story: a guided practice run shows the fix anyway, and a
// learner who revealed the fourth hint (what to change) has been told it.
// Exam and unguided runs keep it closed until they end.
func storyFixOpen(ep *journal.Episode, hints int) bool {
	if ep.Mode == journal.ModeExam {
		return false
	}
	return ep.Guided || (hints >= storyFixRung && ep.HintsUsed >= storyFixRung)
}

// storyFor returns story as the active episode ep may read it: with the
// body of the fix section replaced by one line saying when it opens unless
// storyFixOpen allows it.
func storyFor(story string, ep *journal.Episode, hints int) string {
	if storyFixOpen(ep, hints) {
		return story
	}
	return withoutStoryFix(story)
}

// withoutStoryFix replaces the body of the fix section of story, up to the
// next level-two heading outside a code fence, with storyFixClosed.
func withoutStoryFix(story string) string {
	lines := strings.Split(story, "\n")
	out := make([]string, 0, len(lines))
	inFix, fence := false, false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
		}
		heading := !fence && strings.HasPrefix(l, "## ")
		switch {
		case heading && strings.TrimSpace(l) == storyFixHeading:
			inFix = true
			out = append(out, l, "", storyFixClosed, "")
		case heading:
			inFix = false
			out = append(out, l)
		case !inFix:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// printConcept prints the header line and the rendered page of c.
func (a *app) printConcept(c concepts.Concept) {
	a.printf("%s  %s\n", a.printer().Style(render.StyleAccent, c.ID), a.printer().Style(render.StyleBold, c.Title))
	a.printf("%s", a.printer().Markdown(c.Body))
}

func (a *app) conceptsCommand() *cobra.Command {
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
		for _, line := range strings.Split(render.Wrap(summary, conceptsSummaryWidth-len(conceptsIndent)), "\n") {
			fmt.Fprintf(w, "%s%s\n", conceptsIndent, line)
		}
	}
}
