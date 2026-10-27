package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

const fileSimulation = "simulation.yaml"

const (
	minMeetingTurns = 2
	minQuizItems    = 2
	minGuideSteps   = 3
	minDiagramNodes = 2
	maxDiagramNodes = 9
	minChoices      = 2
)

// Simulation is the optional simulation.yaml: the guided assignment the
// browser workspace (kubedrill web) walks through before and around the
// episode. The meeting is a scripted conversation with the customer who
// opened the ticket, the diagram and the quiz check the learner's model of
// the objects, and the guide is the step list of the guided practice run.
// It is a template like every other scenario text, rendered per episode.
type Simulation struct {
	Customer Customer      `json:"customer"`
	Meeting  Meeting       `json:"meeting"`
	Diagram  Diagram       `json:"diagram"`
	Quiz     []QuizItem    `json:"quiz"`
	Guide    []GuideStep   `json:"guide"`
	Mentor   MentorOptions `json:"mentor,omitempty"`
}

// Customer is the person the meeting is held with.
type Customer struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Company string `json:"company"`
}

// Meeting is the scripted requirements conversation: the customer's
// Opening, then one Turn per question the learner picks, then Closing.
type Meeting struct {
	Opening string `json:"opening"`
	Turns   []Turn `json:"turns"`
	Closing string `json:"closing"`
}

// Turn offers Choices for the learner's next line; exactly one is Best.
type Turn struct {
	Prompt  string   `json:"prompt,omitempty"`
	Choices []Choice `json:"choices"`
}

// Choice is one line the learner can say, the customer's Reply, the
// mentor's Feedback on the choice and, for the best choice, the Fact it
// adds to the requirements board.
type Choice struct {
	Say      string `json:"say"`
	Reply    string `json:"reply"`
	Feedback string `json:"feedback"`
	Best     bool   `json:"best,omitempty"`
	Fact     string `json:"fact,omitempty"`
}

// Diagram is the object graph the Learn stage draws.
type Diagram struct {
	Nodes []DiagramNode `json:"nodes"`
	Edges []DiagramEdge `json:"edges"`
}

// DiagramNode is one object; Note is a short line under its label. The
// page lays nodes out in rows by their longest path from a source; Column,
// when set, orders the nodes inside a row (lower first).
type DiagramNode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Note   string `json:"note,omitempty"`
	Column int    `json:"column,omitempty"`
}

// DiagramEdge links two nodes by id.
type DiagramEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// QuizItem is one question with exactly one correct choice.
type QuizItem struct {
	Question string       `json:"question"`
	Choices  []QuizChoice `json:"choices"`
}

// QuizChoice is one answer and the explanation shown after it is picked.
type QuizChoice struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct,omitempty"`
	Explain string `json:"explain"`
}

// GuideStep is one step of the guided practice. Command, when set, is shown
// ready to copy; Check, when set, names the check whose pass marks the step
// done, otherwise the learner marks it.
type GuideStep struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Command string `json:"command,omitempty"`
	Check   string `json:"check,omitempty"`
}

// MentorOptions tunes the mentor panel; Intro is its first message.
type MentorOptions struct {
	Intro string `json:"intro,omitempty"`
}

// HasSimulation reports whether the scenario ships a simulation.yaml.
func (s *Scenario) HasSimulation() bool {
	return s.simulationDoc != ""
}

func loadSimulation(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileSimulation))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fail(fileSimulation, "%v", err)
		return
	}
	s.simulationDoc = string(b)
	if _, err := parseTemplate(fileSimulation, s.simulationDoc); err != nil {
		fail(fileSimulation, "%v", err)
		return
	}
	if len(s.Variants) == 0 {
		return
	}
	v := s.Variants[0]
	data := templateData{Seed: 1, Namespace: "ns", Variant: v.Name, Vars: v.Vars}
	sim, err := renderSimulation(s.simulationDoc, data)
	if err != nil {
		fail(fileSimulation, "%v", err)
		return
	}
	checks := make(map[string]bool, len(s.Checks))
	for _, c := range s.Checks {
		checks[c.ID] = true
	}
	for _, problem := range sim.problems(checks) {
		fail(fileSimulation, "%s", problem)
	}
}

func renderSimulation(doc string, data templateData) (*Simulation, error) {
	out, err := execute(fileSimulation, doc, data)
	if err != nil {
		return nil, err
	}
	var sim Simulation
	if err := yaml.UnmarshalStrict([]byte(out), &sim); err != nil {
		return nil, fmt.Errorf("%s: %w", fileSimulation, err)
	}
	return &sim, nil
}

// problems lists every rule the simulation breaks; checks holds the check
// ids a guide step may name.
func (sim *Simulation) problems(checks map[string]bool) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }

	if blank(sim.Customer.Name) || blank(sim.Customer.Role) {
		add("customer needs a name and a role")
	}
	if blank(sim.Meeting.Opening) || blank(sim.Meeting.Closing) {
		add("meeting needs an opening and a closing")
	}
	if len(sim.Meeting.Turns) < minMeetingTurns {
		add("meeting has %d turns, at least %d required", len(sim.Meeting.Turns), minMeetingTurns)
	}
	for i, t := range sim.Meeting.Turns {
		if len(t.Choices) < minChoices {
			add("meeting turn %d has %d choices, at least %d required", i+1, len(t.Choices), minChoices)
		}
		best := 0
		for j, c := range t.Choices {
			where := fmt.Sprintf("meeting turn %d choice %d%s", i+1, j+1, quoted(c.Say))
			if blank(c.Say) || blank(c.Reply) || blank(c.Feedback) {
				add("%s needs say, reply and feedback", where)
			}
			if c.Best {
				best++
				if blank(c.Fact) {
					add("%s is best but has no fact for the requirements board", where)
				}
			}
		}
		if best != 1 {
			add("meeting turn %d has %d best choices, exactly 1 required", i+1, best)
		}
	}

	if n := len(sim.Diagram.Nodes); n < minDiagramNodes || n > maxDiagramNodes {
		add("diagram has %d nodes, %d to %d allowed (a larger graph crosses its edges; split the story instead)", n, minDiagramNodes, maxDiagramNodes)
	}
	nodes := map[string]bool{}
	for i, n := range sim.Diagram.Nodes {
		switch {
		case !checkIDRe.MatchString(n.ID):
			add("diagram node %d id %q must be lowercase letters, digits and hyphens", i+1, n.ID)
		case nodes[n.ID]:
			add("diagram node id %q is repeated", n.ID)
		}
		nodes[n.ID] = true
		if blank(n.Label) {
			add("diagram node %q needs a label", n.ID)
		}
	}
	for i, e := range sim.Diagram.Edges {
		if !nodes[e.From] || !nodes[e.To] {
			add("diagram edge %d links %q to %q, which are not both nodes", i+1, e.From, e.To)
		}
	}

	if len(sim.Quiz) < minQuizItems {
		add("quiz has %d questions, at least %d required", len(sim.Quiz), minQuizItems)
	}
	for i, q := range sim.Quiz {
		if blank(q.Question) {
			add("quiz question %d is empty", i+1)
		}
		qwhere := fmt.Sprintf("quiz question %d%s", i+1, quoted(q.Question))
		if len(q.Choices) < minChoices {
			add("%s has %d choices, at least %d required", qwhere, len(q.Choices), minChoices)
		}
		correct := 0
		for j, c := range q.Choices {
			if blank(c.Text) || blank(c.Explain) {
				add("%s choice %d%s needs text and explain", qwhere, j+1, quoted(c.Text))
			}
			if c.Correct {
				correct++
			}
		}
		if correct != 1 {
			add("%s has %d correct choices, exactly 1 required", qwhere, correct)
		}
	}

	if len(sim.Guide) < minGuideSteps {
		add("guide has %d steps, at least %d required", len(sim.Guide), minGuideSteps)
	}
	for i, g := range sim.Guide {
		gwhere := fmt.Sprintf("guide step %d%s", i+1, quoted(g.Title))
		if blank(g.Title) || blank(g.Body) {
			add("%s needs a title and a body", gwhere)
		}
		if g.Check != "" && !checks[g.Check] {
			add("%s names check %q, which checks.yaml does not define", gwhere, g.Check)
		}
	}
	return out
}

// quoted is " (\"<first 40 characters of s>\")" for a non-empty s, so a lint
// message names the line the author is looking for.
func quoted(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40]) + "..."
	}
	return fmt.Sprintf(" (%q)", s)
}

// draftSections are the solution/README.md sections whose commands become
// guide steps, with the title each step gets.
var draftSections = []struct{ heading, title string }{
	{"## Observation that splits them", "Observe"},
	{"## Change", "Change one thing"},
	{"## Verify", "Verify"},
}

// DraftGuide turns the shell commands of the Observation, Change and
// Verify sections of the rendered solution into guide steps, one per
// command, with the episode namespace flag removed (the workstation shell
// already points at it). Bodies are placeholders for the author.
func DraftGuide(r *Rendered) []GuideStep {
	var out []GuideStep
	for _, sec := range draftSections {
		body, ok := section(r.Solution, sec.heading)
		if !ok {
			continue
		}
		for _, cmd := range shellCommands(body) {
			cmd = stripNamespace(cmd, r.Namespace)
			out = append(out, GuideStep{
				Title:   sec.title + ": " + firstWords(cmd, 3),
				Body:    "TODO: what to read in the output and why it matters.",
				Command: cmd,
			})
		}
	}
	return out
}

// GuideDrift lists the guide commands of the rendered simulation whose
// kubectl verb and resource appear in no command of the solution, which
// usually means the guide and the walkthrough teach different fixes.
func GuideDrift(r *Rendered) []string {
	if r.Simulation == nil {
		return nil
	}
	known := map[string]bool{}
	for _, cmd := range shellCommands(r.Solution) {
		known[commandKey(stripNamespace(cmd, r.Namespace))] = true
	}
	var out []string
	for i, g := range r.Simulation.Guide {
		if g.Command == "" {
			continue
		}
		if k := commandKey(g.Command); k != "" && !known[k] {
			out = append(out, fmt.Sprintf("guide step %d%s runs %q, which the solution walkthrough never runs", i+1, quoted(g.Title), k))
		}
	}
	return out
}

// shellCommands returns the command lines of the fenced blocks of doc,
// joining backslash continuations and dropping comments and blank lines.
func shellCommands(doc string) []string {
	var out []string
	lines := strings.Split(doc, "\n")
	mask := fenceMask(lines)
	cur := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !mask[i] || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasSuffix(t, "\\") {
			cur += strings.TrimSpace(strings.TrimSuffix(t, "\\")) + " "
			continue
		}
		out = append(out, strings.TrimSpace(cur+t))
		cur = ""
	}
	return out
}

func stripNamespace(cmd, ns string) string {
	if ns == "" {
		return cmd
	}
	for _, f := range []string{" -n " + ns, " --namespace " + ns, " --namespace=" + ns} {
		cmd = strings.ReplaceAll(cmd, f, "")
	}
	return cmd
}

// commandKey is "kubectl <verb> <resource>" of a kubectl command, or "".
func commandKey(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) < 2 || (f[0] != "kubectl" && f[0] != "k") {
		return ""
	}
	var words []string
	for _, w := range f[1:] {
		if strings.HasPrefix(w, "-") {
			continue
		}
		words = append(words, strings.SplitN(w, "/", 2)[0])
		if len(words) == 2 {
			break
		}
	}
	return "kubectl " + strings.Join(words, " ")
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}
