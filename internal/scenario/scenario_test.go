package scenario

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const realScenario = "../../scenarios/L0/000-hello-down"

type fixture map[string]string

func validFixture() fixture {
	return fixture{
		"scenario.toml": `[scenario]
id = "001-fixture"
title = "Fixture"
level = "L1"
module = "L1.1"
type = "break-fix"
minutes = 5
namespace = "fx-{{ .Seed }}"
exam = ["CKA:troubleshooting"]
concepts = ["pod-lifecycle", "image-pull"]
fix_targets = ["apps/deployments/app"]

[[variant]]
name = "a"
vars = { image = "nginx:a" }
root_cause = "The image tag a does not exist in the nginx repository."

[[variant]]
name = "b"
vars = { image = "nginx:b" }
root_cause = "The image tag b does not exist in the nginx repository."
`,
		"instruction.md":        "# Fixture\n\n## Ticket\n\nIt is down.\n\n## Before you start\n\nOne line.\n\n## Done when\n\n- Deployment exists\n- runs {{ .Vars.image }}\n",
		"hints.md":              fixtureHints,
		"setup/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n  namespace: {{ .Namespace }}\nspec:\n  template:\n    spec:\n      terminationGracePeriodSeconds: 1\n      containers:\n        - name: app\n          image: {{ .Vars.image }}\n",
		"checks.yaml": `checks:
  - id: exists
    description: deployment exists
    why: the Deployment is the object the team owns and everything below it depends on it
    critical: true
    resource:
      apiVersion: apps/v1
      kind: Deployment
      namespace: "{{ .Namespace }}"
      name: app
    exists: true
  - id: image
    description: runs {{ .Vars.image }}
    why: the image reference is the contract between the manifest and the registry
    depends_on: [exists]
    resource:
      apiVersion: apps/v1
      kind: Deployment
      name: app
    path: "{.spec.template.spec.containers[0].image}"
    equals: "{{ .Vars.image }}"
`,
		"solution/README.md": fixtureSolution,
		"solution/solve.sh":  "#!/usr/bin/env bash\nset -euo pipefail\n",
	}
}

const fixtureHints = "# Hints\n\n## Hint 1: where to look\n\nLook at pods in {{ .Namespace }}.\n\n## Hint 2: what to observe\n\nRead events.\n\n## Hint 3: what it means\n\nThe image {{ .Vars.image }} does not pull.\n"

const fixtureSolution = "# Solution\n\n## Symptom\n\nDown in {{ .Namespace }}.\n\n## Candidate causes\n\n- image\n\n## Observation that splits them\n\n```sh\n## not a heading inside a fence\n```\n\n## Change\n\nFix {{ .Vars.image }}.\n\n## Verify\n\nrollout status\n\n## In the real world\n\n- pin\n\n## Go deeper\n\n- docs\n"

func writeFixture(t *testing.T, root, level, id string, f fixture) string {
	t.Helper()
	dir := filepath.Join(root, "scenarios", level, id)
	for name, content := range f {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "setup")); err != nil {
		if err := os.MkdirAll(filepath.Join(dir, "setup"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadRealScenario(t *testing.T) {
	s, err := Load(realScenario)
	if err != nil {
		t.Fatalf("Load(%s): %v", realScenario, err)
	}
	if got, want := s.Meta.ID, "000-hello-down"; got != want {
		t.Errorf("Meta.ID = %q, want %q", got, want)
	}
	if got, want := s.Meta.Level, "L0"; got != want {
		t.Errorf("Meta.Level = %q, want %q", got, want)
	}
	if got, want := s.Meta.Type, TypeBreakFix; got != want {
		t.Errorf("Meta.Type = %q, want %q", got, want)
	}
	if got, want := s.Meta.Namespace, "hello-world"; got != want {
		t.Errorf("Meta.Namespace = %q, want %q", got, want)
	}
	if got, want := len(s.Variants), 3; got != want {
		t.Errorf("len(Variants) = %d, want %d", got, want)
	}
	if got, want := len(s.Hints), 4; got != want {
		t.Errorf("len(Hints) = %d, want %d", got, want)
	}
	if got, want := len(s.Checks), 4; got != want {
		t.Errorf("len(Checks) = %d, want %d", got, want)
	}
	if got, want := len(s.SetupFiles), 2; got != want {
		t.Errorf("len(SetupFiles) = %d, want %d", got, want)
	}
	for _, p := range []string{s.SolveScript, s.Dir} {
		if !filepath.IsAbs(p) {
			t.Errorf("path %q is not absolute", p)
		}
	}
	if s.HookScript != "" {
		t.Errorf("HookScript = %q, want empty", s.HookScript)
	}
	weights := map[string]float64{}
	for _, c := range s.Checks {
		weights[c.ID] = c.Weight
	}
	if got, want := weights["ready-replicas"], 2.0; got != want {
		t.Errorf("weight ready-replicas = %v, want %v", got, want)
	}
	if got, want := weights["deploy-exists"], 1.0; got != want {
		t.Errorf("weight deploy-exists (default) = %v, want %v", got, want)
	}
	if !strings.Contains(s.Instruction, "## Done when") {
		t.Errorf("Instruction lacks \"## Done when\"")
	}
	if !strings.Contains(s.Solution, "## Go deeper") {
		t.Errorf("Solution lacks \"## Go deeper\"")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name   string
		level  string
		id     string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "missing required meta fields",
			mutate: func(f fixture) {
				f["scenario.toml"] = "[scenario]\nid = \"001-fixture\"\nlevel = \"L1\"\n"
			},
			want: []string{"scenario.title is required", "scenario.module is required", "scenario.type is required", "scenario.minutes must be > 0", "scenario.namespace is required"},
		},
		{
			name: "bad id shape",
			id:   "fixture",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `id = "001-fixture"`, `id = "fixture"`, 1)
			},
			want: []string{"scenario.id \"fixture\" must match"},
		},
		{
			name:   "id does not match directory",
			id:     "002-other",
			mutate: func(f fixture) {},
			want:   []string{"does not match directory name \"002-other\""},
		},
		{
			name:   "level does not match parent",
			level:  "L2",
			mutate: func(f fixture) {},
			want:   []string{"scenario.level \"L1\" does not match parent directory \"L2\""},
		},
		{
			name: "bad level and type and exam",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.NewReplacer(`level = "L1"`, `level = "L9"`, `type = "break-fix"`, `type = "chaos"`, `"CKA:troubleshooting"`, `"troubleshooting"`).Replace(f["scenario.toml"])
			},
			want: []string{"must be L0..L6", "scenario.type \"chaos\"", "scenario.exam[0] \"troubleshooting\""},
		},
		{
			name: "cyclic depends_on",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    critical: true\n", "    depends_on: [image]\n", 1)
			},
			want: []string{"depends_on cycle"},
		},
		{
			name: "unknown depends_on",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "depends_on: [exists]", "depends_on: [nope]", 1)
			},
			want: []string{"depends_on \"nope\" does not name an existing check"},
		},
		{
			name: "two sources",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    exists: true\n", "    exists: true\n    script: kubectl get ns\n", 1)
			},
			want: []string{"exactly one source is allowed, got 2"},
		},
		{
			name: "no source and no validator",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: empty\n    description: nothing\n    why: twenty characters at least here\n"
			},
			want: []string{"check \"empty\": exactly one source is required", "check \"empty\": at least one validator is required"},
		},
		{
			name: "path with probe and exit_code with resource",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: probe\n    description: probe\n    why: twenty characters at least here\n    probe:\n      image: busybox\n      command: [true]\n    path: \"{.x}\"\n    exit_code: 0\n"
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    exists: true\n", "    exists: true\n    exit_code: 0\n", 1)
			},
			want: []string{"check \"probe\": path is only valid with a resource source", "check \"exists\": exit_code is only valid with a probe or script source"},
		},
		{
			name: "duplicate check id and negative weight and bad regexp",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: exists\n    description: dup\n    why: twenty characters at least here\n    weight: -1\n    script: kubectl get ns\n    matches: \"(\"\n"
			},
			want: []string{"id is duplicated", "weight must not be negative", "matches: error parsing regexp"},
		},
		{
			name: "duplicate variant name",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `name = "b"`, `name = "a"`, 1)
			},
			want: []string{"variant name \"a\" is duplicated"},
		},
		{
			name: "missing done when",
			mutate: func(f fixture) {
				f["instruction.md"] = "# Fixture\n\n## Ticket\n\nDown.\n"
			},
			want: []string{"instruction.md: missing \"## Done when\" section"},
		},
		{
			name: "before you start too long",
			mutate: func(f fixture) {
				f["instruction.md"] = "# Fixture\n\n## Before you start\n\n" + strings.Repeat("line\n", 9) + "\n## Done when\n\n- ok\n"
			},
			want: []string{"\"## Before you start\" is 9 lines, at most 8 allowed"},
		},
		{
			name: "no hints",
			mutate: func(f fixture) {
				f["hints.md"] = "# Hints\n\nnothing\n"
			},
			want: []string{"hints.md: no \"## Hint N\" section found"},
		},
		{
			name: "solve.sh not executable",
			mutate: func(f fixture) {
				f["solution/solve.txt"] = "x"
				delete(f, "solution/solve.sh")
			},
			want: []string{"solution/solve.sh: stat"},
		},
		{
			name: "bad template syntax in setup and check",
			mutate: func(f fixture) {
				f["setup/deployment.yaml"] = "image: {{ .Vars.image\n"
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], `equals: "{{ .Vars.image }}"`, `equals: "{{ .Vars.image"`, 1)
			},
			want: []string{"setup/deployment.yaml: template:", "check \"image\": equals: template:"},
		},
		{
			name: "unknown toml key",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], "minutes = 5\n", "minutes = 5\nminuets = 5\n", 1)
			},
			want: []string{"unknown key \"scenario.minuets\""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			level, id := "L1", "001-fixture"
			if tc.level != "" {
				level = tc.level
			}
			if tc.id != "" {
				id = tc.id
			}
			f := validFixture()
			tc.mutate(f)
			dir := writeFixture(t, t.TempDir(), level, id, f)
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load succeeded, want errors containing %q", tc.want)
			}
			msg := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error lacks %q:\n%s", w, msg)
				}
			}
		})
	}
}

func TestLoadValidFixture(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(s.Hints), 3; got != want {
		t.Errorf("len(Hints) = %d, want %d", got, want)
	}
	if got, want := s.Hints[1], "Read events."; got != want {
		t.Errorf("Hints[1] = %q, want %q", got, want)
	}
}

func TestRenderVariantCycling(t *testing.T) {
	s, err := Load(realScenario)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		seed  int64
		name  string
		image string
	}{
		{1, "typo-tag", "nginx:1.27-alpnie"},
		{2, "wrong-repo", "docker.io/library/ngnix:1.27-alpine"},
		{3, "private-registry", "registry.internal.example/platform/nginx:1.27-alpine"},
		{4, "typo-tag", "nginx:1.27-alpnie"},
	}
	for _, tc := range tests {
		r, err := s.Render(tc.seed)
		if err != nil {
			t.Fatalf("Render(%d): %v", tc.seed, err)
		}
		if r.Variant.Name != tc.name {
			t.Errorf("seed %d: variant = %q, want %q", tc.seed, r.Variant.Name, tc.name)
		}
		if got := r.Vars["image"]; got != tc.image {
			t.Errorf("seed %d: Vars[image] = %q, want %q", tc.seed, got, tc.image)
		}
		if r.Namespace != "hello-world" {
			t.Errorf("seed %d: Namespace = %q, want hello-world", tc.seed, r.Namespace)
		}
		if len(r.Manifests) != 2 || !strings.Contains(r.Manifests[0], "image: "+tc.image) {
			t.Errorf("seed %d: manifests do not contain image %q", tc.seed, tc.image)
		}
		if strings.Contains(strings.Join(r.Manifests, ""), "{{") {
			t.Errorf("seed %d: manifests still contain template syntax", tc.seed)
		}
		if got := r.Checks[0].Resource.Namespace; got != "hello-world" {
			t.Errorf("seed %d: check namespace = %q, want hello-world", tc.seed, got)
		}
		if r.Seed != tc.seed {
			t.Errorf("Seed = %d, want %d", r.Seed, tc.seed)
		}
	}
	for _, seed := range []int64{0, -1} {
		if _, err := s.Render(seed); err == nil {
			t.Errorf("Render(%d) succeeded, want error", seed)
		}
	}
}

func TestRenderTemplates(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Render(2)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := r.Namespace, "fx-2"; got != want {
		t.Errorf("Namespace = %q, want %q", got, want)
	}
	if got, want := r.Checks[0].Resource.Namespace, "fx-2"; got != want {
		t.Errorf("check namespace = %q, want %q", got, want)
	}
	if got, want := *r.Checks[1].Equals, "nginx:b"; got != want {
		t.Errorf("Equals = %q, want %q", got, want)
	}
	if got, want := r.Checks[1].Description, "runs nginx:b"; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if got, want := *s.Checks[1].Equals, "{{ .Vars.image }}"; got != want {
		t.Errorf("source check mutated: Equals = %q, want %q", got, want)
	}
	if !strings.Contains(r.Manifests[0], "namespace: fx-2") || !strings.Contains(r.Manifests[0], "image: nginx:b") {
		t.Errorf("manifest not rendered:\n%s", r.Manifests[0])
	}
	r.Vars["image"] = "changed"
	if s.Variants[1].Vars["image"] != "nginx:b" {
		t.Errorf("Rendered.Vars aliases the variant map")
	}
}

func TestRenderMissingKey(t *testing.T) {
	f := validFixture()
	f["setup/deployment.yaml"] = "image: {{ .Vars.missing }}\n"
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Render(1)
	if err == nil {
		t.Fatal("Render succeeded, want missingkey error")
	}
	for _, w := range []string{"setup/deployment.yaml", "missing"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q: %v", w, err)
		}
	}
}

func TestRenderNoVariants(t *testing.T) {
	f := validFixture()
	f["scenario.toml"] = strings.Replace(strings.SplitN(f["scenario.toml"], "[[variant]]", 2)[0], `type = "break-fix"`, `type = "build"`, 1)
	f["setup/deployment.yaml"] = "kind: Deployment\nmetadata:\n  namespace: {{ .Namespace }}\n"
	for _, name := range []string{"checks.yaml", "instruction.md", "hints.md", "solution/README.md"} {
		f[name] = strings.ReplaceAll(f[name], "{{ .Vars.image }}", "nginx")
	}
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Render(7)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Variant.Name, "default"; got != want {
		t.Errorf("Variant.Name = %q, want %q", got, want)
	}
	if r.Vars == nil || len(r.Vars) != 0 {
		t.Errorf("Vars = %v, want empty non-nil map", r.Vars)
	}
}

func TestParseHintsSections(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		want    []string
		wantErr bool
	}{
		{
			name: "two hints with heading noise",
			doc:  "# Hints\n\nintro\n\n## Hint 1\n\nfirst\nline two\n\n## Hint 2  \n\nsecond\n",
			want: []string{"first\nline two", "second"},
		},
		{name: "none", doc: "# Hints\n\n### Hint 1\n\nx\n", wantErr: true},
		{name: "empty body", doc: "## Hint 1\n\n## Hint 2\n\nx\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sections, err := parseHintSections(tc.doc)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			got := make([]string, len(sections))
			for i, h := range sections {
				got[i] = h.body
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hints %q, want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("hint %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCatalogFind(t *testing.T) {
	root := t.TempDir()
	for _, sc := range []struct{ level, id string }{
		{"L0", "000-hello-down"},
		{"L1", "001-fixture"},
		{"L1", "010-pods-pending"},
		{"L2", "011-pods-crash"},
	} {
		f := validFixture()
		f["scenario.toml"] = strings.NewReplacer(`id = "001-fixture"`, `id = "`+sc.id+`"`, `level = "L1"`, `level = "`+sc.level+`"`).Replace(f["scenario.toml"])
		writeFixture(t, root, sc.level, sc.id, f)
	}
	if err := os.MkdirAll(filepath.Join(root, "scenarios", "_template"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	all := c.All()
	wantOrder := []string{"000-hello-down", "001-fixture", "010-pods-pending", "011-pods-crash"}
	if len(all) != len(wantOrder) {
		t.Fatalf("All() returned %d scenarios, want %d", len(all), len(wantOrder))
	}
	for i, s := range all {
		if s.Meta.ID != wantOrder[i] {
			t.Errorf("All()[%d] = %q, want %q", i, s.Meta.ID, wantOrder[i])
		}
	}

	tests := []struct {
		ref     string
		want    string
		wantErr error
	}{
		{ref: "000-hello-down", want: "000-hello-down"},
		{ref: "000", want: "000-hello-down"},
		{ref: "010", want: "010-pods-pending"},
		{ref: "011-pods", want: "011-pods-crash"},
		{ref: "01", wantErr: ErrAmbiguous},
		{ref: "0", wantErr: ErrAmbiguous},
		{ref: "999", wantErr: ErrNotFound},
		{ref: "nope", wantErr: ErrNotFound},
		{ref: "", wantErr: ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			s, err := c.Find(tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Find(%q) err = %v, want %v", tc.ref, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Find(%q): %v", tc.ref, err)
			}
			if s.Meta.ID != tc.want {
				t.Errorf("Find(%q) = %q, want %q", tc.ref, s.Meta.ID, tc.want)
			}
		})
	}
}

func TestCatalogReportsBrokenScenario(t *testing.T) {
	root := t.TempDir()
	f := validFixture()
	delete(f, "hints.md")
	writeFixture(t, root, "L1", "001-fixture", f)
	if _, err := NewCatalog(root); err == nil || !strings.Contains(err.Error(), "hints.md") {
		t.Errorf("NewCatalog err = %v, want hints.md problem", err)
	}
}

func TestLintRealTree(t *testing.T) {
	concepts := []string{"pod-lifecycle", "image-pull", "readiness-and-endpoints"}
	for _, modules := range [][]string{nil, {"L0.1", "L0.2"}} {
		for _, ids := range [][]string{nil, concepts} {
			for _, e := range Lint("../..", modules, ids) {
				t.Errorf("lint(modules=%v, concepts=%v): %v", modules, ids, e)
			}
		}
	}
}

func TestLintReportsProblems(t *testing.T) {
	root := t.TempDir()
	f := validFixture()
	f["scenario.toml"] = strings.Replace(strings.SplitN(f["scenario.toml"], "[[variant]]", 2)[0], `type = "break-fix"`, `type = "build"`, 1)
	writeFixture(t, root, "L1", "001-fixture", f)
	errs := Lint(root, nil, nil)
	if len(errs) == 0 {
		t.Fatal("Lint returned no errors, want template missing and variant problems")
	}
	joined := make([]string, 0, len(errs))
	for _, e := range errs {
		joined = append(joined, e.Error())
	}
	all := strings.Join(joined, "\n")
	for _, w := range []string{
		filepath.Join("scenarios", "_template") + ": ",
		filepath.Join("scenarios", "L1", "001-fixture") + ": scenario.toml: level L1 requires at least two [[variant]] entries",
		filepath.Join("scenarios", "L1", "001-fixture") + ": template: setup/deployment.yaml",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("lint output lacks %q:\n%s", w, all)
		}
	}
}

func TestLoadCheckRules(t *testing.T) {
	probe := func(extra string) string {
		return "  - id: probe\n    description: probe\n    why: twenty characters at least here\n    probe:\n      image: busybox:1.36@sha256:" + strings.Repeat("a", 64) + "\n      command: [true]\n" + extra + "    exit_code: 0\n"
	}
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "exists with path",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    exists: true\n", "    path: \"{.metadata.name}\"\n    exists: true\n", 1)
			},
			want: []string{"check \"exists\": exists applies to a resource without path; use not_empty for a path"},
		},
		{
			name: "exists with probe",
			mutate: func(f fixture) {
				f["checks.yaml"] += probe("    exists: true\n")
			},
			want: []string{"check \"probe\": exists applies to a resource without path; use not_empty for a path"},
		},
		{
			name: "probe image without digest",
			mutate: func(f fixture) {
				f["checks.yaml"] += strings.Replace(probe(""), "@sha256:"+strings.Repeat("a", 64), "", 1)
			},
			want: []string{"check \"probe\": probe.image must be pinned by digest (image:tag@sha256:...)"},
		},
		{
			name: "probe timeout unparseable",
			mutate: func(f fixture) {
				f["checks.yaml"] += probe("      timeout: soon\n")
			},
			want: []string{"check \"probe\": probe.timeout: time: invalid duration"},
		},
		{
			name: "probe timeout below one second",
			mutate: func(f fixture) {
				f["checks.yaml"] += probe("      timeout: 500ms\n")
			},
			want: []string{"check \"probe\": probe.timeout \"500ms\" must be at least 1s"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.mutate(f)
			dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load succeeded, want errors containing %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%s", w, err)
				}
			}
		})
	}
	f := validFixture()
	f["checks.yaml"] += probe("      timeout: 1s\n")
	f["instruction.md"] += "- probe\n"
	if _, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f)); err != nil {
		t.Errorf("Load with a valid probe: %v", err)
	}
}

func TestRenderNamespaceRules(t *testing.T) {
	tests := []struct {
		ns   string
		want string
	}{
		{"default", "namespace \"default\" is reserved"},
		{"kube-system", "namespace \"kube-system\" is reserved"},
		{"kube-anything", "namespace \"kube-anything\" is reserved"},
		{"local-path-storage", "namespace \"local-path-storage\" is reserved"},
		{"kubedrill-debug", "namespace \"kubedrill-debug\" is reserved"},
		{"Shop", "namespace \"Shop\" is not a DNS-1123 label"},
		{"shop_{{ .Seed }}", "namespace \"shop_1\" is not a DNS-1123 label"},
		{"-shop", "namespace \"-shop\" is not a DNS-1123 label"},
	}
	for _, tc := range tests {
		t.Run(tc.ns, func(t *testing.T) {
			f := validFixture()
			f["scenario.toml"] = strings.Replace(f["scenario.toml"], `namespace = "fx-{{ .Seed }}"`, "namespace = \""+tc.ns+"\"", 1)
			s, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Render(1)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Render err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLintRules(t *testing.T) {
	tests := []struct {
		name     string
		modules  []string
		concepts []string
		mutate   func(f fixture)
		want     []string
	}{
		{
			name:    "module not in curriculum",
			modules: []string{"L0.1", "L1.2"},
			mutate:  func(f fixture) {},
			want:    []string{"scenario.toml: module \"L1.1\" is not in curriculum.yaml"},
		},
		{
			name:     "concept without a docs file",
			concepts: []string{"pod-lifecycle"},
			mutate:   func(f fixture) {},
			want:     []string{"scenario.toml: concept \"image-pull\" has no docs/concepts/image-pull.md"},
		},
		{
			name:     "empty concept list skips nothing",
			concepts: []string{},
			mutate:   func(f fixture) {},
			want: []string{
				"scenario.toml: concept \"pod-lifecycle\" has no docs/concepts/pod-lifecycle.md",
				"scenario.toml: concept \"image-pull\" has no docs/concepts/image-pull.md",
			},
		},
		{
			name: "template markers left in place",
			mutate: func(f fixture) {
				f["instruction.md"] = strings.Replace(f["instruction.md"], "# Fixture", "# Title in the reporter's words\n\n<!-- kubedrill-template: delete this block -->", 1)
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `namespace = "fx-{{ .Seed }}"`, "# kubedrill-template\nnamespace = \"template\"", 1)
				f["hints.md"] = "# kubedrill-template\n" + f["hints.md"]
				f["checks.yaml"] = "# kubedrill-template\n" + f["checks.yaml"]
			},
			want: []string{
				"instruction.md: still contains the template marker \"<!-- kubedrill-template: delete this block -->\"",
				"instruction.md: still contains the template marker \"Title in the reporter's words\"",
				"scenario.toml: still contains the template marker \"# kubedrill-template\"",
				"scenario.toml: still contains the template marker \"namespace = \\\"template\\\"\"",
				"hints.md: still contains the template marker \"# kubedrill-template\"",
				"checks.yaml: still contains the template marker \"# kubedrill-template\"",
			},
		},
		{
			name: "done when has fewer bullets than checks",
			mutate: func(f fixture) {
				f["instruction.md"] = strings.Replace(f["instruction.md"], "- runs {{ .Vars.image }}\n", "", 1)
			},
			want: []string{"instruction.md: \"## Done when\" has 1 bullets, checks.yaml has 2 checks"},
		},
		{
			name: "done when bullet differs from description",
			mutate: func(f fixture) {
				f["instruction.md"] = strings.Replace(f["instruction.md"], "- Deployment exists\n", "- the deployment exists\n", 1)
			},
			want: []string{"instruction.md: \"## Done when\" bullet 1 is \"the deployment exists\", check \"exists\" description is \"deployment exists\""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := copyTree("../../scenarios/_template", filepath.Join(root, "scenarios", "_template")); err != nil {
				t.Fatal(err)
			}
			f := validFixture()
			tc.mutate(f)
			writeFixture(t, root, "L1", "001-fixture", f)
			errs := Lint(root, tc.modules, tc.concepts)
			joined := make([]string, 0, len(errs))
			for _, e := range errs {
				joined = append(joined, e.Error())
			}
			all := strings.Join(joined, "\n")
			for _, w := range tc.want {
				if !strings.Contains(all, filepath.Join("scenarios", "L1", "001-fixture")+": "+w) {
					t.Errorf("lint output lacks %q:\n%s", w, all)
				}
			}
			if got, want := len(errs), len(tc.want); got != want {
				t.Errorf("Lint returned %d errors, want %d:\n%s", got, want, all)
			}
		})
	}
}

func TestScaffold(t *testing.T) {
	root := t.TempDir()
	if err := copyTree("../../scenarios/_template", filepath.Join(root, "scenarios", "_template")); err != nil {
		t.Fatal(err)
	}
	dir, err := Scaffold(root, "L2", "042-new-thing")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := filepath.Join(root, "scenarios", "L2", "042-new-thing"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(scaffolded): %v", err)
	}
	if s.Meta.ID != "042-new-thing" || s.Meta.Level != "L2" {
		t.Errorf("Meta = %q/%q, want 042-new-thing/L2", s.Meta.ID, s.Meta.Level)
	}
	errs := Lint(root, nil, nil)
	if len(errs) == 0 {
		t.Error("Lint after scaffold returned no errors, want template marker errors")
	}
	for _, e := range errs {
		if !strings.Contains(e.Error(), "template marker") {
			t.Errorf("Lint after scaffold: unexpected error %v", e)
		}
	}
	if _, err := Scaffold(root, "L2", "042-new-thing"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second Scaffold err = %v, want already exists", err)
	}
	for _, tc := range []struct{ level, id string }{{"L7", "043-x"}, {"L1", "x"}} {
		if _, err := Scaffold(root, tc.level, tc.id); err == nil {
			t.Errorf("Scaffold(%q, %q) succeeded, want error", tc.level, tc.id)
		}
	}
}

func TestLoadCheckAndFileRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f fixture)
		after  func(t *testing.T, dir string)
		want   []string
	}{
		{
			name: "check id missing",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - description: no id\n    why: twenty characters at least here\n    script: kubectl get ns\n    exit_code: 0\n"
			},
			want: []string{"checks[2]: id is required"},
		},
		{
			name: "check id bad shape",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: Bad_ID\n    description: bad id\n    why: twenty characters at least here\n    script: kubectl get ns\n    exit_code: 0\n"
			},
			want: []string{"check \"Bad_ID\": id must match ^[a-z0-9][a-z0-9-]*$"},
		},
		{
			name: "resource apiVersion and kind missing",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: bare\n    description: bare resource\n    why: twenty characters at least here\n    resource:\n      name: app\n    exists: true\n"
			},
			want: []string{"check \"bare\": resource.apiVersion is required", "check \"bare\": resource.kind is required"},
		},
		{
			name: "name and selector both set",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "      name: app\n    exists: true\n", "      name: app\n      selector: app=app\n    exists: true\n", 1)
			},
			want: []string{"check \"exists\": resource.name and resource.selector are mutually exclusive"},
		},
		{
			name: "probe image and command missing",
			mutate: func(f fixture) {
				f["checks.yaml"] += "  - id: probe\n    description: probe\n    why: twenty characters at least here\n    probe: {}\n    exit_code: 0\n"
			},
			want: []string{"check \"probe\": probe.image is required", "check \"probe\": probe.command is required"},
		},
		{
			name: "depends_on itself",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "depends_on: [exists]", "depends_on: [image]", 1)
			},
			want: []string{"check \"image\": depends_on itself"},
		},
		{
			name: "empty checks list",
			mutate: func(f fixture) {
				f["checks.yaml"] = "checks: []\n"
			},
			want: []string{"checks.yaml: checks list is empty, at least one check is required"},
		},
		{
			name: "unknown key in checks.yaml",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    critical: true\n", "    weigth: 2\n", 1)
			},
			want: []string{"checks.yaml:", "unknown field \"weigth\""},
		},
		{
			name:   "solve.sh not executable",
			mutate: func(f fixture) {},
			after: func(t *testing.T, dir string) {
				if err := os.Chmod(filepath.Join(dir, "solution", "solve.sh"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"solution/solve.sh: is not executable (chmod +x)"},
		},
		{
			name: "hook.sh not executable",
			mutate: func(f fixture) {
				f["setup/hook.sh"] = "#!/usr/bin/env bash\n"
			},
			after: func(t *testing.T, dir string) {
				if err := os.Chmod(filepath.Join(dir, "setup", "hook.sh"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"setup/hook.sh: is not executable (chmod +x)"},
		},
		{
			name: "unexpected file in setup",
			mutate: func(f fixture) {
				f["setup/notes.txt"] = "notes\n"
			},
			want: []string{"setup/notes.txt: unexpected file, only *.yaml templates and hook.sh are allowed"},
		},
		{
			name: "instruction.md missing",
			mutate: func(f fixture) {
				delete(f, "instruction.md")
			},
			want: []string{"instruction.md: open "},
		},
		{
			name: "solution README missing",
			mutate: func(f fixture) {
				delete(f, "solution/README.md")
			},
			want: []string{"solution/README.md: open "},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.mutate(f)
			dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
			if tc.after != nil {
				tc.after(t, dir)
			}
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load succeeded, want errors containing %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%s", w, err)
				}
			}
		})
	}
}

func TestLoadWhyAndConcepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "why missing",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "    why: the Deployment is the object the team owns and everything below it depends on it\n", "", 1)
			},
			want: []string{"check \"exists\": why is required"},
		},
		{
			name: "why blank",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "why: the Deployment is the object the team owns and everything below it depends on it", `why: "   "`, 1)
			},
			want: []string{"check \"exists\": why is required"},
		},
		{
			name: "why too short",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "why: the Deployment is the object the team owns and everything below it depends on it", "why: nineteen chars.....", 1)
			},
			want: []string{"check \"exists\": why must be 20 to 400 characters, got 19"},
		},
		{
			name: "why too long",
			mutate: func(f fixture) {
				f["checks.yaml"] = strings.Replace(f["checks.yaml"], "why: the Deployment is the object the team owns and everything below it depends on it", "why: "+strings.Repeat("x", 401), 1)
			},
			want: []string{"check \"exists\": why must be 20 to 400 characters, got 401"},
		},
		{
			name: "concept id shape",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `concepts = ["pod-lifecycle", "image-pull"]`, `concepts = ["Pod Lifecycle", "image--pull", "-x"]`, 1)
			},
			want: []string{
				"scenario.concepts[0] \"Pod Lifecycle\" must match",
				"scenario.concepts[1] \"image--pull\" must match",
				"scenario.concepts[2] \"-x\" must match",
			},
		},
		{
			name: "break-fix without concepts",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `concepts = ["pod-lifecycle", "image-pull"]`, "", 1)
			},
			want: []string{"scenario.concepts: type break-fix requires at least one concept id"},
		},
		{
			name: "build without concepts",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.NewReplacer(`concepts = ["pod-lifecycle", "image-pull"]`, "", `type = "break-fix"`, `type = "build"`).Replace(f["scenario.toml"])
			},
			want: []string{"scenario.concepts: type build requires at least one concept id"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.mutate(f)
			_, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
			if err == nil {
				t.Fatalf("Load succeeded, want errors containing %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%s", w, err)
				}
			}
		})
	}

	for _, typ := range []Type{TypeDesign, TypeIncident} {
		f := validFixture()
		f["scenario.toml"] = strings.NewReplacer(`concepts = ["pod-lifecycle", "image-pull"]`, "", `type = "break-fix"`, `type = "`+string(typ)+`"`).Replace(f["scenario.toml"])
		if _, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f)); err != nil {
			t.Errorf("Load type %s without concepts: %v", typ, err)
		}
	}

	f := validFixture()
	f["checks.yaml"] = strings.Replace(f["checks.yaml"], "why: the image reference is the contract between the manifest and the registry", "why: \"{{ .Vars.image }} is the contract with the registry\"", 1)
	s, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := s.Meta.Concepts, []string{"pod-lifecycle", "image-pull"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Meta.Concepts = %v, want %v", got, want)
	}
	r, err := s.Render(1)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := r.Checks[1].Why, "{{ .Vars.image }} is the contract with the registry"; got != want {
		t.Errorf("rendered Why = %q, want %q (why is not templated)", got, want)
	}
}

func TestLoadHookScript(t *testing.T) {
	f := validFixture()
	f["setup/hook.sh"] = "#!/usr/bin/env bash\n"
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := s.HookScript, filepath.Join(dir, "setup", "hook.sh"); got != want {
		t.Errorf("HookScript = %q, want %q", got, want)
	}
	if got, want := len(s.SetupFiles), 1; got != want {
		t.Errorf("len(SetupFiles) = %d, want %d (hook.sh is not a manifest)", got, want)
	}
}

func TestRenderHugeSeed(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Render(math.MaxInt64)
	if err != nil {
		t.Fatalf("Render(MaxInt64): %v", err)
	}
	if r.Seed != math.MaxInt64 {
		t.Errorf("Seed = %d, want %d", r.Seed, int64(math.MaxInt64))
	}
	found := false
	for _, v := range s.Variants {
		if v.Name == r.Variant.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("Variant.Name = %q, not one of the scenario variants", r.Variant.Name)
	}
	if got, want := r.Namespace, "fx-9223372036854775807"; got != want {
		t.Errorf("Namespace = %q, want %q", got, want)
	}
}

func TestWhyLengthBounds(t *testing.T) {
	const whyLine = "why: the Deployment is the object the team owns and everything below it depends on it"
	tests := []struct {
		name    string
		why     string
		wantErr string
	}{
		{name: "exactly 20", why: strings.Repeat("x", 20)},
		{name: "exactly 400", why: strings.Repeat("x", 400)},
		{name: "19", why: strings.Repeat("x", 19), wantErr: "check \"exists\": why must be 20 to 400 characters, got 19"},
		{name: "401", why: strings.Repeat("x", 401), wantErr: "check \"exists\": why must be 20 to 400 characters, got 401"},
		{name: "20 with surrounding spaces", why: "   " + strings.Repeat("x", 20) + "   "},
		{name: "19 padded to 25 with spaces", why: "   " + strings.Repeat("x", 19) + "   ", wantErr: "check \"exists\": why must be 20 to 400 characters, got 19"},
		{name: "400 with surrounding spaces", why: " " + strings.Repeat("x", 400) + " "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			f["checks.yaml"] = strings.Replace(f["checks.yaml"], whyLine, "why: \""+tc.why+"\"", 1)
			s, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: got error %v, want nil", err)
				}
				if got, want := s.Checks[0].Why, tc.why; got != want {
					t.Errorf("Checks[0].Why: got %q, want %q (stored as written)", got, want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error lacks %q:\n%s", tc.wantErr, err)
			}
		})
	}
}

func TestVariantForMatchesRender(t *testing.T) {
	s, err := Load(realScenario)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		seed int64
		want string
	}{
		{1, "typo-tag"},
		{2, "wrong-repo"},
		{3, "private-registry"},
		{4, "typo-tag"},
		{1_000_000, s.Variants[(1_000_000-1)%int64(len(s.Variants))].Name},
	}
	for _, tc := range tests {
		if got := s.VariantFor(tc.seed).Name; got != tc.want {
			t.Errorf("VariantFor(%d) = %q, want %q", tc.seed, got, tc.want)
		}
		r, err := s.Render(tc.seed)
		if err != nil {
			t.Fatalf("Render(%d): %v", tc.seed, err)
		}
		if got := r.Variant.Name; got != tc.want {
			t.Errorf("Render(%d).Variant = %q, want %q", tc.seed, got, tc.want)
		}
	}
	empty := &Scenario{}
	if got, want := empty.VariantFor(3).Name, "default"; got != want {
		t.Errorf("VariantFor without variants = %q, want %q", got, want)
	}
}
