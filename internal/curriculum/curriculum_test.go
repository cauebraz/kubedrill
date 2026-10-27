package curriculum

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exams = `
exams:
  - id: CKA
    title: Certified Kubernetes Administrator
    domains:
      - {id: troubleshooting, title: "Troubleshooting", weight: 30}
      - {id: workloads, title: "Workloads and Scheduling", weight: 15}
  - id: CKAD
    title: Certified Kubernetes Application Developer
    domains:
      - {id: environment, title: "Application Environment, Configuration and Security", weight: 25}
`

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "curriculum.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestDefaultPath(t *testing.T) {
	if got, want := DefaultPath("/repo"), filepath.Join("/repo", "curriculum.yaml"); got != want {
		t.Fatalf("DefaultPath: got %q, want %q", got, want)
	}
}

func TestLoadRealCurriculum(t *testing.T) {
	c, err := Load(DefaultPath(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Levels) != 7 {
		t.Fatalf("levels: got %d, want 7", len(c.Levels))
	}
	for i, lv := range c.Levels {
		if want := "L" + string(rune('0'+i)); lv.ID != want {
			t.Fatalf("level %d: got id %q, want %q", i, lv.ID, want)
		}
		if len(lv.Modules) == 0 {
			t.Fatalf("level %s: no modules", lv.ID)
		}
		for _, m := range lv.Modules {
			if len(m.Scenarios) == 0 {
				t.Fatalf("module %s: no scenarios", m.ID)
			}
			if len(m.Exam) == 0 {
				t.Fatalf("module %s: no exam refs", m.ID)
			}
			for _, sc := range m.Scenarios {
				switch sc.Type {
				case "break-fix", "build", "design", "incident":
				default:
					t.Fatalf("module %s: scenario %q has type %q", m.ID, sc.Ticket, sc.Type)
				}
				switch sc.Feasibility {
				case "kind":
					if sc.Addons != "" {
						t.Fatalf("module %s: scenario %q is kind but lists addons %q", m.ID, sc.Ticket, sc.Addons)
					}
				case "addon", "simulated":
					if sc.Addons == "" {
						t.Fatalf("module %s: scenario %q is %s but lists no addons", m.ID, sc.Ticket, sc.Feasibility)
					}
				default:
					t.Fatalf("module %s: scenario %q has feasibility %q", m.ID, sc.Ticket, sc.Feasibility)
				}
			}
		}
	}
	if len(c.Exams) != 3 {
		t.Fatalf("exams: got %d, want 3", len(c.Exams))
	}
	for _, ex := range c.Exams {
		sum := 0
		for _, d := range ex.Domains {
			sum += d.Weight
		}
		if sum != 100 {
			t.Fatalf("exam %s: weights sum to %d, want 100", ex.ID, sum)
		}
	}
	if got := c.Levels[0].Modules[0].Scenarios[0].ID; got != "001-storefront-down" {
		t.Fatalf("L0.1 first scenario id: got %q, want 001-storefront-down", got)
	}
	if got, want := strings.Join(c.Levels[0].Modules[0].Concepts, ","), "pod-lifecycle,image-pull,readiness-and-endpoints,labels-and-selectors,troubleshooting-loop,what-changed"; got != want {
		t.Fatalf("L0.1 concepts: got %q, want %q", got, want)
	}
	if got, want := strings.Join(c.ConceptIDs(), ","), "cluster-dns,control-plane-signals,image-pull,labels-and-selectors,pod-lifecycle,readiness-and-endpoints,resources-and-qos,troubleshooting-loop,what-changed"; got != want {
		t.Fatalf("ConceptIDs: got %q, want %q", got, want)
	}
	cov, err := c.ExamCoverage("CKA", map[string]bool{"001-storefront-down": true})
	if err != nil {
		t.Fatalf("ExamCoverage CKA: %v", err)
	}
	if len(cov) != 5 {
		t.Fatalf("CKA coverage: got %d domains, want 5", len(cov))
	}
	var ts *Coverage
	for i := range cov {
		if cov[i].Domain.ID == "troubleshooting" {
			ts = &cov[i]
		}
	}
	if ts == nil {
		t.Fatal("CKA coverage: troubleshooting domain missing")
	}
	if len(ts.Scenarios) == 0 || ts.Scenarios[0] != "001-storefront-down" || ts.Done != 1 {
		t.Fatalf("troubleshooting coverage: got %+v, want 001-storefront-down first and done 1", ts)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "bad level id",
			body: `
levels:
  - id: L7
    title: Beyond
    modules:
      - id: L7.1
        title: x
        exam: ["CKA:troubleshooting"]
        scenarios: []
` + exams,
			want: []string{`level "L7": id must match`},
		},
		{
			name: "module in wrong level",
			body: `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L1.1
        title: x
        exam: ["CKA:troubleshooting"]
        scenarios: []
` + exams,
			want: []string{`module "L1.1": id must start with L0.`},
		},
		{
			name: "unknown exam domain",
			body: `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:storage", "CKS:runtime", "bogus"]
        scenarios: []
` + exams,
			want: []string{
				"module L0.1: unknown domain storage in exam CKA",
				"module L0.1: unknown exam CKS",
				`module L0.1: exam ref "bogus" must be EXAM:domain`,
			},
		},
		{
			name: "duplicate scenario id",
			body: `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:troubleshooting"]
        scenarios:
          - id: 000-hello-down
            ticket: a
            type: break-fix
            feasibility: kind
      - id: L0.2
        title: y
        exam: ["CKA:workloads"]
        scenarios:
          - id: 000-hello-down
            ticket: b
            type: break-fix
            feasibility: kind
` + exams,
			want: []string{"module L0.2: scenario 000-hello-down already listed in module L0.1"},
		},
		{
			name: "duplicate level and module ids",
			body: `
levels:
  - id: L0
    title: a
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:troubleshooting"]
        scenarios: []
  - id: L0
    title: b
    modules:
      - id: L0.1
        title: y
        exam: ["CKA:troubleshooting"]
        scenarios: []
` + exams,
			want: []string{"level L0: duplicate id", "module L0.1: duplicate id"},
		},
		{
			name: "bad concept id shape",
			body: `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:troubleshooting"]
        concepts: ["Pod Lifecycle", "image--pull", "ok-id"]
        scenarios: []
` + exams,
			want: []string{
				`module L0.1: concept "Pod Lifecycle" must match`,
				`module L0.1: concept "image--pull" must match`,
			},
		},
		{
			name: "duplicate exam id",
			body: `
levels: []
exams:
  - id: CKA
    title: a
    domains:
      - {id: troubleshooting, title: t, weight: 30}
  - id: CKA
    title: b
    domains:
      - {id: workloads, title: w, weight: 15}
`,
			want: []string{"exam CKA: duplicate id"},
		},
		{
			name: "exam without id",
			body: `
levels: []
exams:
  - title: nameless
    domains:
      - {id: troubleshooting, title: t, weight: 30}
`,
			want: []string{"exam without id"},
		},
		{
			name: "domain without id",
			body: `
levels: []
exams:
  - id: CKA
    title: a
    domains:
      - {title: nameless, weight: 30}
`,
			want: []string{"exam CKA: domain without id"},
		},
		{
			name: "duplicate domain",
			body: `
levels: []
exams:
  - id: CKA
    title: a
    domains:
      - {id: troubleshooting, title: t, weight: 30}
      - {id: troubleshooting, title: t2, weight: 10}
`,
			want: []string{"exam CKA: duplicate domain troubleshooting"},
		},
		{
			name: "all errors reported at once",
			body: `
levels:
  - id: LX
    title: a
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:nope"]
        scenarios: []
` + exams,
			want: []string{`level "LX": id must match`, `module "L0.1": id must start with LX.`, "unknown domain nope"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeFixture(t, tc.body))
			if err == nil {
				t.Fatal("Load: got nil error, want validation error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Load error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load missing: got %v, want ErrNotExist", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := Load(writeFixture(t, `
levels:
  - id: L0
    title: a
    hour: 3
    modules: []
`+exams))
	if err == nil {
		t.Fatal("Load: got nil error, want unknown field error")
	}
}

func TestConceptIDs(t *testing.T) {
	c, err := Load(writeFixture(t, `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:troubleshooting"]
        concepts: [pod-lifecycle, image-pull]
        scenarios: []
      - id: L0.2
        title: y
        exam: ["CKA:workloads"]
        concepts: [image-pull, labels-and-selectors]
        scenarios: []
  - id: L1
    title: z
    modules:
      - id: L1.1
        title: w
        exam: ["CKA:workloads"]
        scenarios: []
`+exams))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := strings.Join(c.ConceptIDs(), ","), "image-pull,labels-and-selectors,pod-lifecycle"; got != want {
		t.Fatalf("ConceptIDs: got %q, want %q", got, want)
	}
	empty := &Curriculum{}
	if got := empty.ConceptIDs(); len(got) != 0 {
		t.Fatalf("ConceptIDs on empty curriculum: got %v, want none", got)
	}
}

func TestExamCoverage(t *testing.T) {
	c, err := Load(writeFixture(t, `
levels:
  - id: L0
    title: Foundations
    modules:
      - id: L0.1
        title: x
        exam: ["CKA:troubleshooting"]
        scenarios:
          - id: a
            ticket: a
            type: break-fix
            feasibility: kind
          - ticket: planned only
            type: build
            feasibility: kind
      - id: L0.2
        title: y
        exam: ["CKA:troubleshooting", "CKAD:environment"]
        scenarios:
          - id: b
            ticket: b
            type: break-fix
            feasibility: kind
          - id: c
            ticket: c
            type: build
            feasibility: kind
`+exams))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	solved := map[string]bool{"a": true, "c": true, "zzz": true}
	cov, err := c.ExamCoverage("CKA", solved)
	if err != nil {
		t.Fatalf("ExamCoverage CKA: %v", err)
	}
	if len(cov) != 2 {
		t.Fatalf("CKA coverage: got %d domains, want 2", len(cov))
	}
	ts := cov[0]
	if ts.Exam != "CKA" || ts.Domain.ID != "troubleshooting" || ts.Domain.Weight != 30 {
		t.Fatalf("coverage[0]: got %+v, want CKA troubleshooting weight 30", ts)
	}
	if got := strings.Join(ts.Scenarios, ","); got != "a,b,c" {
		t.Fatalf("troubleshooting scenarios: got %s, want a,b,c", got)
	}
	if ts.Done != 2 {
		t.Fatalf("troubleshooting Done: got %d, want 2", ts.Done)
	}
	wl := cov[1]
	if wl.Domain.ID != "workloads" || len(wl.Scenarios) != 0 || wl.Done != 0 {
		t.Fatalf("coverage[1]: got %+v, want empty workloads", wl)
	}

	ckad, err := c.ExamCoverage("CKAD", solved)
	if err != nil {
		t.Fatalf("ExamCoverage CKAD: %v", err)
	}
	if got := strings.Join(ckad[0].Scenarios, ","); got != "b,c" || ckad[0].Done != 1 {
		t.Fatalf("CKAD environment: got %s done %d, want b,c done 1", got, ckad[0].Done)
	}

	if _, err := c.ExamCoverage("LPIC", solved); !errors.Is(err, ErrUnknownExam) {
		t.Fatalf("ExamCoverage unknown: got %v, want ErrUnknownExam", err)
	}
}

func TestMerge(t *testing.T) {
	base := func() *Curriculum {
		return &Curriculum{Levels: []Level{{ID: "L0", Modules: []Module{{ID: "L0.1", Scenarios: []PlannedScenario{{ID: "001-a"}}}}}}}
	}
	mod := func(id, sc string) Module { return Module{ID: id, Scenarios: []PlannedScenario{{ID: sc}}} }
	cases := []struct {
		name  string
		level string
		mods  []Module
		want  string
	}{
		{"adds to the level", "L0", []Module{mod("L0.pack", "101-b")}, ""},
		{"unknown level", "L9", []Module{mod("L9.pack", "101-b")}, `level "L9" is not in the curriculum`},
		{"module outside the level", "L0", []Module{mod("L1.pack", "101-b")}, "must start with L0."},
		{"module id taken", "L0", []Module{mod("L0.1", "101-b")}, "already in the ladder"},
		{"scenario listed elsewhere", "L0", []Module{mod("L0.pack", "001-a")}, "already listed in module L0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			err := c.Merge(tc.level, "p", tc.mods)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Merge: %v", err)
				}
				if m := c.Levels[0].Modules; len(m) != 2 || m[1].Pack != "p" || m[1].ID != "L0.pack" {
					t.Errorf("modules after merge: %+v", m)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Merge: got %v, want %q", err, tc.want)
			}
		})
	}
}
