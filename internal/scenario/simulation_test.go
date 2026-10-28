package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSimulation() *Simulation {
	return &Simulation{
		Customer: Customer{Name: "Dana", Role: "Team lead"},
		Meeting: Meeting{
			Opening: "hi",
			Closing: "bye",
			Turns: []Turn{
				{Choices: []Choice{{Say: "a", Reply: "r", Feedback: "f", Best: true, Fact: "x"}, {Say: "b", Reply: "r", Feedback: "f"}}},
				{Choices: []Choice{{Say: "a", Reply: "r", Feedback: "f"}, {Say: "b", Reply: "r", Feedback: "f", Best: true, Fact: "y"}}},
			},
		},
		Diagram: Diagram{Nodes: []DiagramNode{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Edges: []DiagramEdge{{From: "a", To: "b"}}},
		Quiz: []QuizItem{
			{Question: "q1", Choices: []QuizChoice{{Text: "t", Correct: true, Explain: "e"}, {Text: "u", Explain: "e"}}},
			{Question: "q2", Choices: []QuizChoice{{Text: "t", Explain: "e"}, {Text: "u", Correct: true, Explain: "e"}}},
		},
		Guide: []GuideStep{{Title: "1", Body: "b"}, {Title: "2", Body: "b", Check: "ready"}, {Title: "3", Body: "b"}},
	}
}

func TestSimulationProblems(t *testing.T) {
	checks := map[string]bool{"ready": true}
	tests := []struct {
		name   string
		change func(*Simulation)
		want   string
	}{
		{name: "valid"},
		{name: "no customer name", change: func(s *Simulation) { s.Customer.Name = "" }, want: "customer needs a name and a role"},
		{name: "one turn", change: func(s *Simulation) { s.Meeting.Turns = s.Meeting.Turns[:1] }, want: "meeting has 1 turns, at least 2 required"},
		{name: "two best choices", change: func(s *Simulation) {
			s.Meeting.Turns[0].Choices[1].Best, s.Meeting.Turns[0].Choices[1].Fact = true, "z"
		}, want: "meeting turn 1 has 2 best choices, exactly 1 required"},
		{name: "best without fact", change: func(s *Simulation) { s.Meeting.Turns[1].Choices[1].Fact = "" }, want: `meeting turn 2 choice 2 ("b") is best but has no fact`},
		{name: "choice without feedback", change: func(s *Simulation) { s.Meeting.Turns[0].Choices[1].Feedback = " " }, want: `meeting turn 1 choice 2 ("b") needs say, reply and feedback`},
		{name: "edge to a missing node", change: func(s *Simulation) { s.Diagram.Edges[0].To = "c" }, want: `diagram edge 1 links "a" to "c"`},
		{name: "repeated node", change: func(s *Simulation) { s.Diagram.Nodes[1].ID = "a" }, want: `diagram node id "a" is repeated`},
		{name: "bad node id", change: func(s *Simulation) { s.Diagram.Nodes[0].ID = "A B" }, want: `diagram node 1 id "A B"`},
		{name: "two correct answers", change: func(s *Simulation) { s.Quiz[0].Choices[1].Correct = true }, want: `quiz question 1 ("q1") has 2 correct choices`},
		{name: "one quiz question", change: func(s *Simulation) { s.Quiz = s.Quiz[:1] }, want: "quiz has 1 questions, at least 2 required"},
		{name: "ten nodes", change: func(s *Simulation) {
			s.Diagram.Nodes = nil
			for i := 0; i < 10; i++ {
				s.Diagram.Nodes = append(s.Diagram.Nodes, DiagramNode{ID: string(rune('a' + i)), Label: "N"})
			}
		}, want: "diagram has 10 nodes, 2 to 9 allowed"},
		{name: "long quoted text is cut", change: func(s *Simulation) {
			s.Meeting.Turns[0].Choices[1].Say = strings.Repeat("x", 60)
			s.Meeting.Turns[0].Choices[1].Reply = ""
		}, want: `("` + strings.Repeat("x", 40) + `...")`},
		{name: "unknown check", change: func(s *Simulation) { s.Guide[1].Check = "nope" }, want: `guide step 2 ("2") names check "nope"`},
		{name: "short guide", change: func(s *Simulation) { s.Guide = s.Guide[:2] }, want: "guide has 2 steps, at least 3 required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSimulation()
			if tt.change != nil {
				tt.change(s)
			}
			got := strings.Join(s.problems(checks), "\n")
			if tt.want == "" {
				if got != "" {
					t.Errorf("problems: got %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("problems: got %q, want one containing %q", got, tt.want)
			}
		})
	}
}

func TestRealScenarioSimulation(t *testing.T) {
	s, err := Load(filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.HasSimulation() {
		t.Fatal("001-storefront-down: HasSimulation false, want true")
	}
	for _, seed := range s.VariantSeeds() {
		rd, err := s.Render(seed, WithNamespace("shop-x1y2z"))
		if err != nil {
			t.Fatalf("Render seed %d: %v", seed, err)
		}
		sim := rd.Simulation
		if sim == nil || len(sim.Meeting.Turns) != 4 || len(sim.Guide) != 7 || sim.Customer.Name != "Dana" {
			t.Fatalf("seed %d simulation: got %+v", seed, sim)
		}
	}
}

func TestLoadRejectsBrokenSimulation(t *testing.T) {
	src := filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"unknown field": "customer:\n  name: Dana\n  role: lead\n  nickname: D\n",
		"bad template":  "customer:\n  name: {{ .Nope\n",
		"broken rules":  "customer:\n  name: Dana\n  role: lead\nmeeting:\n  opening: hi\n  closing: bye\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, fileSimulation), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(dir)
			if err == nil || !strings.Contains(err.Error(), fileSimulation) {
				t.Errorf("Load: got %v, want an error naming %s", err, fileSimulation)
			}
		})
	}
}

func TestLintChecksSimulationOfEveryVariant(t *testing.T) {
	src := filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fileSimulation)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(string(b), "  name: Dana\n", `  name: '{{ if eq .Variant "typo-tag" }}{{ else }}Dana{{ end }}'`+"\n", 1)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := load(dir, false)
	if err != nil {
		t.Fatalf("load (the first variant is valid): %v", err)
	}
	r, err := s.Render(mustSeedFor(t, s, "typo-tag"))
	if err != nil {
		t.Fatal(err)
	}
	got := lintSimulation(s, r)
	if len(got) != 1 || !strings.Contains(got[0].Error(), "customer needs a name and a role") {
		t.Errorf("lintSimulation on typo-tag: got %v, want the empty customer name", got)
	}
}

func mustSeedFor(t *testing.T, s *Scenario, variant string) int64 {
	t.Helper()
	seed, ok := s.SeedFor(variant)
	if !ok {
		t.Fatalf("no seed for %s", variant)
	}
	return seed
}

func TestDraftGuideAndDrift(t *testing.T) {
	s, err := Load(filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Render(s.VariantSeeds()[0], WithNamespace("shop-x1y2z"))
	if err != nil {
		t.Fatal(err)
	}
	steps := DraftGuide(r)
	if len(steps) < 3 {
		t.Fatalf("DraftGuide: got %d steps, want the Observation, Change and Verify commands", len(steps))
	}
	var change *GuideStep
	for i := range steps {
		if strings.Contains(steps[i].Command, " -n shop-x1y2z") {
			t.Errorf("step %q keeps the namespace flag: %q", steps[i].Title, steps[i].Command)
		}
		if strings.HasPrefix(steps[i].Title, "Change one thing") {
			change = &steps[i]
		}
	}
	if change == nil || change.Command != "kubectl set image deployment/storefront web=nginx:1.27-alpine" {
		t.Errorf("change step: got %+v", change)
	}
	if d := GuideDrift(r); len(d) != 0 {
		t.Errorf("GuideDrift on 001: got %v, want none", d)
	}
	r.Simulation.Guide = append(r.Simulation.Guide, GuideStep{Title: "Scale it", Command: "kubectl scale deployment/storefront --replicas=5"})
	if d := GuideDrift(r); len(d) != 1 || !strings.Contains(d[0], `"kubectl scale deployment"`) {
		t.Errorf("GuideDrift with a foreign step: got %v", d)
	}
}

func TestShellCommands(t *testing.T) {
	doc := "text\n\n```sh\n# a comment\nkubectl get pods\nkubectl run x \\\n  --image=y\n\n```\n\n`kubectl not-a-block`\n"
	got := strings.Join(shellCommands(doc), "|")
	if want := "kubectl get pods|kubectl run x --image=y"; got != want {
		t.Errorf("shellCommands: got %q, want %q", got, want)
	}
}
