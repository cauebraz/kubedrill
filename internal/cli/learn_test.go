package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
)

type memConcepts struct {
	all []concepts.Concept
}

func (m *memConcepts) IDs() []string {
	ids := make([]string, 0, len(m.all))
	for _, c := range m.all {
		ids = append(ids, c.ID)
	}
	return ids
}

func (m *memConcepts) All() []concepts.Concept { return m.all }

func (m *memConcepts) Get(id string) (concepts.Concept, error) {
	for _, c := range m.all {
		if c.ID == id {
			return c, nil
		}
	}
	return concepts.Concept{}, fmt.Errorf("concept %q: %w", id, concepts.ErrNotFound)
}

func TestLearnWithoutEpisode(t *testing.T) {
	f := newFixture(t)
	code, _, errOut := f.run(t, "", "learn")
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	mustContain(t, errOut, "kubedrill: no active episode; kubedrill learn <id> prints one concept: pods, deployments, services")
}

func TestLearnActiveEpisode(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001", "--seed", "1")
	before := len(f.kube.calls)
	code, out, errOut := f.run(t, "", "learn")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out,
		"Fundamentals of Hello app is down (001-hello-down)\n\npods  Pods\n# Pods\n\nA pod wraps containers.\n\ndeployments  Deployments\n# Deployments\n\nA deployment rolls out ReplicaSets.\n",
	)
	mustNotContain(t, out, "Services", "The smallest deployable unit", "Declarative replica management")
	if len(f.kube.calls) != before {
		t.Errorf("learn touched the cluster: %v", f.kube.calls[before:])
	}
}

func TestLearnOne(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "learn", "services")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if want := "services  Services\n# Services\n\nA service selects pods.\n"; out != want {
		t.Errorf("stdout:\n got %q\nwant %q", out, want)
	}
	code, _, errOut = f.run(t, "", "learn", "operators")
	if code != 1 {
		t.Errorf("unknown exit: got %d, want 1", code)
	}
	mustContain(t, errOut, `kubedrill: concept "operators" not found; see kubedrill concepts`)
}

func TestConceptsTable(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "concepts")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	want := "ID           TITLE        SCENARIOS\n" +
		"pods         Pods         1\n" +
		"    The smallest deployable unit: one or more containers sharing a network\n" +
		"    namespace and storage.\n" +
		"deployments  Deployments  1\n" +
		"    Declarative replica management over ReplicaSets.\n" +
		"services     Services     0\n" +
		"    Stable virtual IP and DNS name in front of a selector.\n"
	if out != want {
		t.Errorf("stdout:\n got %q\nwant %q", out, want)
	}
	for _, l := range strings.Split(out, "\n") {
		if len(l) > render.DefaultWidth {
			t.Errorf("line wider than %d: %q", render.DefaultWidth, l)
		}
	}
	f.concepts.all = nil
	_, out, _ = f.run(t, "", "concepts")
	mustContain(t, out, "no concept pages found")
}

func TestLearnActiveWithoutConcepts(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "002", "--seed", "1")
	code, out, errOut := f.run(t, "", "learn")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if want := "002-service-dns lists no concepts; see kubedrill concepts\n"; out != want {
		t.Errorf("stdout:\n got %q\nwant %q", out, want)
	}
}

func TestLearnActiveMissingLibraryPage(t *testing.T) {
	f := newFixture(t)
	f.concepts.all = f.concepts.all[:1]
	f.run(t, "", "start", "001", "--seed", "1")
	code, out, errOut := f.run(t, "", "learn")
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	mustContain(t, errOut, `kubedrill: scenario 001-hello-down lists concept "deployments": concept "deployments": concept not found`)
	if strings.Contains(out, "Fundamentals of") {
		t.Errorf("stdout printed pages before the missing concept was reported:\n%s", out)
	}
}

func TestConceptsSummaryWrapped(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("kubelet reports container state ", 12))
	var out strings.Builder
	writeConcepts(&out, []concepts.Concept{{ID: "pods", Title: "Pods", Summary: long}}, nil)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 5 {
		t.Fatalf("lines: got %d (%q), want the header, the row and at least three summary lines", len(lines), out.String())
	}
	if got, want := lines[0], "ID    TITLE  SCENARIOS"; got != want {
		t.Errorf("header: got %q, want %q", got, want)
	}
	if got, want := lines[1], "pods  Pods   0"; got != want {
		t.Errorf("row: got %q, want %q", got, want)
	}
	var words []string
	for _, l := range lines[2:] {
		if !strings.HasPrefix(l, conceptsIndent) || strings.HasPrefix(l, conceptsIndent+" ") {
			t.Errorf("summary line %q: want exactly the indent %q", l, conceptsIndent)
		}
		if len(l) > conceptsSummaryWidth {
			t.Errorf("summary line %q: got %d columns, want <= %d", l, len(l), conceptsSummaryWidth)
		}
		if len(l) > render.DefaultWidth {
			t.Errorf("summary line %q: got %d columns, want <= %d", l, len(l), render.DefaultWidth)
		}
		words = append(words, strings.Fields(l)...)
	}
	if got := strings.Join(words, " "); got != long {
		t.Errorf("wrapped summary text:\n got %q\nwant %q", got, long)
	}
}

func TestStory(t *testing.T) {
	tests := []struct {
		name   string
		start  bool
		args   []string
		code   int
		out    []string
		notOut []string
		errOut string
	}{
		{name: "no episode and no ref", args: []string{"story"}, code: 1, errOut: "kubedrill: no active episode; kubedrill story <id> prints the story of one scenario"},
		{name: "ref without episode", args: []string{"story", "001"}, out: []string{"Story of Hello app is down (001-hello-down)", "The hello pods in task-hello never start.", "Point the image at a tag that exists."}},
		{name: "active episode", start: true, args: []string{"story"}, out: []string{"Story of Hello app is down (001-hello-down)", "The hello pods in task-hello-abcde never start.", "The fix and why it works", "This section opens when the run ends", "Watch the rollout."}, notOut: []string{"Point the image at a tag that exists."}},
		{name: "ref of the active episode", start: true, args: []string{"story", "001"}, out: []string{"This section opens when the run ends"}, notOut: []string{"Point the image at a tag that exists."}},
		{name: "scenario without story", start: true, args: []string{"story", "002"}, out: []string{"002-service-dns has no story yet; kubedrill learn prints its fundamentals"}, notOut: []string{"Story of"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.start {
				f.run(t, "", "start", "001", "--seed", "1")
			}
			before := len(f.kube.calls)
			code, out, errOut := f.run(t, "", tc.args...)
			if code != tc.code {
				t.Fatalf("exit: got %d, want %d (%s)", code, tc.code, errOut)
			}
			mustContain(t, out, tc.out...)
			mustNotContain(t, out, tc.notOut...)
			if tc.errOut != "" {
				mustContain(t, errOut, tc.errOut)
			}
			if len(f.kube.calls) != before {
				t.Errorf("story touched the cluster: %v", f.kube.calls[before:])
			}
		})
	}
}

func TestStoryFor(t *testing.T) {
	story := "## What happened\n\nDown.\n\n## The fix and why it works\n\nSet the tag.\n\n```\n## not a heading\n```\n\n## Check your work\n\nWatch.\n"
	tests := []struct {
		name  string
		ep    journal.Episode
		hints int
		open  bool
	}{
		{name: "unguided practice", ep: journal.Episode{Mode: journal.ModePractice}, hints: 4},
		{name: "guided practice", ep: journal.Episode{Mode: journal.ModePractice, Guided: true}, hints: 4, open: true},
		{name: "fourth hint revealed", ep: journal.Episode{Mode: journal.ModePractice, HintsUsed: 4}, hints: 4, open: true},
		{name: "third hint revealed", ep: journal.Episode{Mode: journal.ModePractice, HintsUsed: 3}, hints: 4},
		{name: "every hint of a three-rung ladder", ep: journal.Episode{Mode: journal.ModePractice, HintsUsed: 3}, hints: 3},
		{name: "exam", ep: journal.Episode{Mode: journal.ModeExam, HintsUsed: 4}, hints: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := storyFor(story, &tc.ep, tc.hints)
			if tc.open {
				if got != story {
					t.Errorf("storyFor: got %q, want the story unchanged", got)
				}
				return
			}
			mustContain(t, got, "## What happened", "## The fix and why it works", storyFixClosed, "## Check your work", "Watch.")
			mustNotContain(t, got, "Set the tag.", "## not a heading")
		})
	}
}

func TestTicketPointsAtStory(t *testing.T) {
	f := newFixture(t)
	_, out, _ := f.run(t, "", "start", "001", "--seed", "1")
	mustContain(t, out, "New to Kubernetes? kubedrill story tells this ticket from zero")
	_, out, _ = f.run(t, "", "start", "002", "--seed", "1", "--yes")
	mustContain(t, out, "started 002-service-dns")
	mustNotContain(t, out, "kubedrill story")
}
