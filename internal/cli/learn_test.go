package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/grader"
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
		if len(l) > markdownWidth {
			t.Errorf("line wider than %d: %q", markdownWidth, l)
		}
	}
	f.concepts.all = nil
	_, out, _ = f.run(t, "", "concepts")
	mustContain(t, out, "no concept pages found")
}

func TestWhyLines(t *testing.T) {
	r := grader.Result{ID: "x", Status: grader.StatusError, Message: "boom", Why: strings.Repeat("word ", 30)}
	got := resultLines(r, "[err ]", true, false)
	if len(got) < 4 {
		t.Fatalf("lines: got %v, want tag, message and at least two why lines", got)
	}
	if !strings.HasPrefix(got[2], "       why: word") || !strings.HasPrefix(got[3], "            word") {
		t.Errorf("why lines: %q", got[2:])
	}
	for _, l := range got {
		if len(l) > whyWidth {
			t.Errorf("line wider than %d: %q", whyWidth, l)
		}
	}
	skipped := grader.Result{ID: "s", Status: grader.StatusSkipped, Message: "dependency failed", Why: "not shown"}
	if lines := resultLines(skipped, "[skip]", true, false); strings.Contains(strings.Join(lines, "|"), "why:") {
		t.Errorf("skipped check printed why: %v", lines)
	}
	if lines := resultLines(r, "[err ]", false, false); strings.Contains(strings.Join(lines, "|"), "why:") {
		t.Errorf("why printed with withWhy=false: %v", lines)
	}
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

func TestStatusTagDefault(t *testing.T) {
	tests := []struct {
		status grader.Status
		want   string
	}{
		{status: grader.StatusPass, want: "[pass]"},
		{status: grader.StatusFail, want: "[fail]"},
		{status: grader.StatusSkipped, want: "[skip]"},
		{status: grader.StatusError, want: "[err ]"},
		{status: grader.Status("weird"), want: "[err ]"},
		{status: grader.Status(""), want: "[err ]"},
	}
	for _, tc := range tests {
		if got := statusTag(tc.status); got != tc.want {
			t.Errorf("statusTag(%q): got %q, want %q", tc.status, got, tc.want)
		}
		if len(tc.want) != 6 {
			t.Errorf("tag %q: got %d columns, want 6", tc.want, len(tc.want))
		}
	}
}

func TestPluralOne(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: 0, want: "0 hints"},
		{n: 1, want: "1 hint"},
		{n: 2, want: "2 hints"},
	}
	for _, tc := range tests {
		if got := plural(tc.n, "hint"); got != tc.want {
			t.Errorf("plural(%d, hint): got %q, want %q", tc.n, got, tc.want)
		}
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
		if len(l) > markdownWidth {
			t.Errorf("summary line %q: got %d columns, want <= %d", l, len(l), markdownWidth)
		}
		words = append(words, strings.Fields(l)...)
	}
	if got := strings.Join(words, " "); got != long {
		t.Errorf("wrapped summary text:\n got %q\nwant %q", got, long)
	}
}

func TestCheckWhyWidthExact(t *testing.T) {
	first := strings.Repeat("aaaaaaaaaa ", 5) + "bbbbbbbbbbb"
	if len(first) != whyWidth-len(detailIndent)-len("why: ") {
		t.Fatalf("test setup: first line text is %d columns, want %d", len(first), whyWidth-len(detailIndent)-len("why: "))
	}
	r := grader.Result{ID: "x", Status: grader.StatusFail, Expected: "2", Actual: "0", Why: first + " cc dd"}
	got := resultLines(r, "[fail]", true, false)
	want := []string{
		"[fail] x",
		"       expected: 2  actual: 0",
		"       why: " + first,
		"            cc dd",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("resultLines:\n got %q\nwant %q", got, want)
	}
	if len(got[2]) != whyWidth {
		t.Errorf("first why line: got %d columns, want exactly %d", len(got[2]), whyWidth)
	}
	one := strings.Repeat("aaaaaaaaaa ", 5) + "bbbbbbbbbbbb"
	lines := whyLines(one, whyWidth)
	if len(lines) != 2 || lines[0] != detailIndent+"why: "+strings.TrimSpace(strings.Repeat("aaaaaaaaaa ", 5)) || lines[1] != detailIndent+"     bbbbbbbbbbbb" {
		t.Errorf("whyLines one over the width: got %q, want the last word on a second line", lines)
	}
}
