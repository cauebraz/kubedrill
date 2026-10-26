package scenario

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadDiagnosisRules(t *testing.T) {
	long := strings.Repeat("x", 301)
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "break-fix variant without root_cause",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], "root_cause = \"The image tag b does not exist in the nginx repository.\"\n", "", 1)
			},
			want: []string{"scenario.toml: variant \"b\": root_cause is required for type break-fix"},
		},
		{
			name: "root_cause too short",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], "The image tag a does not exist in the nginx repository.", "bad tag", 1)
			},
			want: []string{"scenario.toml: variant \"a\": root_cause must be 20 to 300 characters, got 7"},
		},
		{
			name: "root_cause too long on a build scenario",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.NewReplacer(`type = "break-fix"`, `type = "build"`, "The image tag a does not exist in the nginx repository.", long).Replace(f["scenario.toml"])
			},
			want: []string{"scenario.toml: variant \"a\": root_cause must be 20 to 300 characters, got 301"},
		},
		{
			name: "break-fix without variants",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.SplitN(f["scenario.toml"], "[[variant]]", 2)[0]
			},
			want: []string{"scenario.toml: type break-fix requires at least one [[variant]] with root_cause"},
		},
		{
			name: "break-fix without fix_targets",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], "fix_targets = [\"apps/deployments/app\"]\n", "", 1)
			},
			want: []string{"scenario.toml: scenario.fix_targets: type break-fix requires at least one group/resource/name entry"},
		},
		{
			name: "malformed and duplicated fix_targets",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `fix_targets = ["apps/deployments/app"]`, `fix_targets = ["deployments/app", "apps/Deployments/app", "core/services/app", "core/services/app"]`, 1)
			},
			want: []string{
				"scenario.fix_targets[0]: fix target \"deployments/app\" must be group/resource/name",
				"scenario.fix_targets[1]: fix target \"apps/Deployments/app\": resource \"Deployments\"",
				"scenario.fix_targets[3] \"core/services/app\" is duplicated",
			},
		},
	}
	runLoadErrors(t, tests)
}

func TestLoadHintLadderRules(t *testing.T) {
	ladder := func(bodies ...string) string {
		var sb strings.Builder
		sb.WriteString("# Hints\n")
		for i, b := range bodies {
			sb.WriteString("\n" + hintLadder[i] + "\n\n" + b + "\n")
		}
		return sb.String()
	}
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name:   "two hints",
			mutate: func(f fixture) { f["hints.md"] = ladder("a", "b") },
			want:   []string{"hints.md: has 2 hints, want 3 or 4"},
		},
		{
			name: "five hints",
			mutate: func(f fixture) {
				f["hints.md"] = ladder("a", "b", "c", "d") + "\n## Hint 5: extra\n\ne\n"
			},
			want: []string{"hints.md: has 5 hints, want 3 or 4"},
		},
		{
			name: "headings out of order",
			mutate: func(f fixture) {
				f["hints.md"] = "## Hint 1: what to observe\n\na\n\n## Hint 2: where to look\n\nb\n\n## Hint 3: what it means\n\nc\n"
			},
			want: []string{
				"hints.md: hint 1 heading is \"## Hint 1: what to observe\", want \"## Hint 1: where to look\"",
				"hints.md: hint 2 heading is \"## Hint 2: where to look\", want \"## Hint 2: what to observe\"",
			},
		},
		{
			name: "bare numbered headings",
			mutate: func(f fixture) {
				f["hints.md"] = "## Hint 1\n\na\n\n## Hint 2\n\nb\n\n## Hint 3\n\nc\n"
			},
			want: []string{"hint 1 heading is \"## Hint 1\"", "hint 2 heading is \"## Hint 2\"", "hint 3 heading is \"## Hint 3\""},
		},
		{
			name: "mutating kubectl in a hint",
			mutate: func(f fixture) {
				f["hints.md"] = ladder("look", "observe", "means", "Run `kubectl -n {{ .Namespace }} set image deployment/app app=x`.")
			},
			want: []string{"hints.md: \"## Hint 4: what to change\" contains the mutating command \"kubectl set\""},
		},
		{
			name: "rollout undo in a hint",
			mutate: func(f fixture) {
				f["hints.md"] = ladder("look", "observe", "Try kubectl rollout undo deployment/app.")
			},
			want: []string{"\"## Hint 3: what it means\" contains the mutating command \"kubectl rollout undo\""},
		},
		{
			name: "template parse error in hints",
			mutate: func(f fixture) {
				f["hints.md"] = ladder("look {{ .Namespace", "observe", "means")
			},
			want: []string{"hints.md: template: hints.md:"},
		},
	}
	runLoadErrors(t, tests)
}

func TestLoadSolutionAndInstructionRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "missing sections",
			mutate: func(f fixture) {
				f["solution/README.md"] = "# Solution\n\n## Symptom\n\nx\n\n## Change\n\ny\n"
			},
			want: []string{"solution/README.md: missing sections ## Candidate causes, ## Observation that splits them, ## Verify, ## In the real world, ## Go deeper"},
		},
		{
			name: "sections out of order",
			mutate: func(f fixture) {
				f["solution/README.md"] = strings.Replace(strings.Replace(fixtureSolution, "## Change", "## TMP", 1), "## Verify", "## Change", 1)
				f["solution/README.md"] = strings.Replace(f["solution/README.md"], "## TMP", "## Verify", 1)
			},
			want: []string{"solution/README.md: sections are in the order ## Symptom, ## Candidate causes, ## Observation that splits them, ## Verify, ## Change"},
		},
		{
			name: "unknown and duplicated heading",
			mutate: func(f fixture) {
				f["solution/README.md"] = fixtureSolution + "\n## Root cause\n\nx\n\n## Go deeper\n\n- more\n"
			},
			want: []string{
				"solution/README.md: heading \"## Root cause\" is not one of the solution sections",
				"solution/README.md: heading \"## Go deeper\" appears more than once",
			},
		},
		{
			name: "template parse error in solution and instruction",
			mutate: func(f fixture) {
				f["solution/README.md"] = fixtureSolution + "{{ if }}\n"
				f["instruction.md"] += "{{ end }}\n"
			},
			want: []string{"solution/README.md: template: solution/README.md:", "instruction.md: template: instruction.md:"},
		},
	}
	runLoadErrors(t, tests)
}

func TestLoadApproachRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name: "snippet longer than 8 lines",
			mutate: func(f fixture) {
				f["approaches/long/snippet.txt"] = strings.Repeat("kubectl get pods\n", 9)
				f["approaches/long/content.md"] = "Why.\n"
			},
			want: []string{filepath.Join("approaches", "long", "snippet.txt") + ": is 9 lines, at most 8 allowed"},
		},
		{
			name: "empty snippet and content",
			mutate: func(f fixture) {
				f["approaches/empty/snippet.txt"] = "\n\n"
				f["approaches/empty/content.md"] = "  \n"
			},
			want: []string{
				filepath.Join("approaches", "empty", "snippet.txt") + ": is empty",
				filepath.Join("approaches", "empty", "content.md") + ": is empty",
			},
		},
		{
			name: "missing files, stray file, bad name",
			mutate: func(f fixture) {
				f["approaches/Bad_Name/notes.txt"] = "x\n"
				f["approaches/README.md"] = "x\n"
			},
			want: []string{
				filepath.Join("approaches", "Bad_Name") + ": approach name \"Bad_Name\" must be lowercase words joined by hyphens",
				filepath.Join("approaches", "Bad_Name", "notes.txt") + ": unexpected file",
				filepath.Join("approaches", "Bad_Name", "snippet.txt") + ": open",
				filepath.Join("approaches", "Bad_Name", "content.md") + ": open",
				filepath.Join("approaches", "README.md") + ": unexpected file, approaches/ holds one directory per approach",
			},
		},
	}
	runLoadErrors(t, tests)
}

func runLoadErrors(t *testing.T, tests []struct {
	name   string
	mutate func(f fixture)
	want   []string
}) {
	t.Helper()
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
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestLoadApproaches(t *testing.T) {
	f := validFixture()
	f["approaches/set-image/snippet.txt"] = "kubectl set image deployment/app app=nginx\nkubectl rollout status deployment/app\n\n"
	f["approaches/set-image/content.md"] = "\nFast, but drifts from the repository.\n"
	f["approaches/fix-manifest/snippet.txt"] = "kubectl apply -f app.yaml\n"
	f["approaches/fix-manifest/content.md"] = "Survives the next deploy.\n"
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Approach{
		{Name: "fix-manifest", Snippet: "kubectl apply -f app.yaml", Body: "Survives the next deploy."},
		{Name: "set-image", Snippet: "kubectl set image deployment/app app=nginx\nkubectl rollout status deployment/app", Body: "Fast, but drifts from the repository."},
	}
	if len(s.Approaches) != len(want) {
		t.Fatalf("Approaches = %+v, want %+v", s.Approaches, want)
	}
	for i := range want {
		if s.Approaches[i] != want[i] {
			t.Errorf("Approaches[%d] = %+v, want %+v", i, s.Approaches[i], want[i])
		}
	}
}

func TestLoadNoApproaches(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Approaches) != 0 {
		t.Errorf("Approaches = %+v, want none", s.Approaches)
	}
}

func TestMutatingKubectl(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{text: "`kubectl -n shop get pods`", want: ""},
		{text: "kubectl describe pod x | tail", want: ""},
		{text: "kubectl logs --previous app", want: ""},
		{text: "kubectl rollout status deployment/app", want: ""},
		{text: "kubectl config set-context --current", want: ""},
		{text: "kubectl get deploy -o yaml | kubectl apply -f -", want: "kubectl apply"},
		{text: "run `kubectl -n shop set image deployment/app app=x`", want: "kubectl set"},
		{text: "kubectl --namespace=shop edit deployment app", want: "kubectl edit"},
		{text: "kubectl --context kind-kubedrill -n shop patch svc app", want: "kubectl patch"},
		{text: "(kubectl create configmap x)", want: "kubectl create"},
		{text: "kubectl replace -f x.yaml", want: "kubectl replace"},
		{text: "kubectl delete pod -l app=app", want: "kubectl delete"},
		{text: "kubectl scale deployment app --replicas=2", want: "kubectl scale"},
		{text: "kubectl -n shop rollout undo deployment/app", want: "kubectl rollout undo"},
		{text: "kubectl label pod x a=b", want: "kubectl label"},
		{text: "kubectl annotate pod x a=b", want: "kubectl annotate"},
		{text: "kubectl expose deployment app --port 80", want: "kubectl expose"},
		{text: "kubectl run curl --image=curl", want: "kubectl run"},
		{text: "Set the image of the Deployment and apply it.", want: ""},
		{text: "kubectl", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			if got := mutatingKubectl(tc.text); got != tc.want {
				t.Errorf("mutatingKubectl(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestParseFixTarget(t *testing.T) {
	tests := []struct {
		in                    string
		group, resource, name string
		wantErr               string
	}{
		{in: "apps/deployments/hello", group: "apps", resource: "deployments", name: "hello"},
		{in: "core/services/hello", group: "", resource: "services", name: "hello"},
		{in: "networking.k8s.io/networkpolicies/deny-all", group: "networking.k8s.io", resource: "networkpolicies", name: "deny-all"},
		{in: "core/configmaps/app.settings", group: "", resource: "configmaps", name: "app.settings"},
		{in: "deployments/hello", wantErr: "must be group/resource/name"},
		{in: "apps/deployments/hello/extra", wantErr: "must be group/resource/name"},
		{in: "/services/hello", wantErr: "group is empty"},
		{in: "Apps/deployments/hello", wantErr: "group \"Apps\" is not a DNS subdomain"},
		{in: "apps/Deployments/hello", wantErr: "resource \"Deployments\""},
		{in: "apps/deployments/", wantErr: "name \"\" is not a DNS-1123 subdomain"},
		{in: "apps/deployments/Hello_1", wantErr: "name \"Hello_1\""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			g, r, n, err := ParseFixTarget(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseFixTarget(%q) err = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFixTarget(%q): %v", tc.in, err)
			}
			if g != tc.group || r != tc.resource || n != tc.name {
				t.Errorf("ParseFixTarget(%q) = %q, %q, %q, want %q, %q, %q", tc.in, g, r, n, tc.group, tc.resource, tc.name)
			}
		})
	}
}

func TestRenderTexts(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		opts          []RenderOption
		wantNamespace string
	}{
		{name: "scenario namespace", wantNamespace: "fx-2"},
		{name: "override", opts: []RenderOption{WithNamespace("fx-abcde")}, wantNamespace: "fx-abcde"},
		{name: "empty override keeps scenario namespace", opts: []RenderOption{WithNamespace("")}, wantNamespace: "fx-2"},
		{name: "nil option ignored", opts: []RenderOption{nil}, wantNamespace: "fx-2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := s.Render(2, tc.opts...)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			ns := tc.wantNamespace
			if r.Namespace != ns {
				t.Errorf("Namespace = %q, want %q", r.Namespace, ns)
			}
			if !strings.Contains(r.Instruction, "- runs nginx:b") {
				t.Errorf("Instruction not rendered:\n%s", r.Instruction)
			}
			wantHints := []string{"Look at pods in " + ns + ".", "Read events.", "The image nginx:b does not pull."}
			if len(r.Hints) != len(wantHints) {
				t.Fatalf("Hints = %q, want %q", r.Hints, wantHints)
			}
			for i := range wantHints {
				if r.Hints[i] != wantHints[i] {
					t.Errorf("Hints[%d] = %q, want %q", i, r.Hints[i], wantHints[i])
				}
			}
			for _, w := range []string{"Down in " + ns + ".", "Fix nginx:b."} {
				if !strings.Contains(r.Solution, w) {
					t.Errorf("Solution lacks %q:\n%s", w, r.Solution)
				}
			}
			if !strings.Contains(r.Manifests[0], "namespace: "+ns+"\n") {
				t.Errorf("manifest namespace not %q:\n%s", ns, r.Manifests[0])
			}
			if got := r.Checks[0].Resource.Namespace; got != ns {
				t.Errorf("check namespace = %q, want %q", got, ns)
			}
		})
	}
	if !strings.Contains(s.Hints[0], "{{ .Namespace }}") || !strings.Contains(s.Solution, "{{ .Namespace }}") || !strings.Contains(s.Instruction, "{{ .Vars.image }}") {
		t.Errorf("Render changed the raw Scenario texts")
	}
}

func TestRenderNamespaceOverrideRules(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"Bad_NS", "default", "kube-anything", strings.Repeat("a", 64)} {
		t.Run(ns, func(t *testing.T) {
			_, err := s.Render(1, WithNamespace(ns))
			if err == nil || !strings.Contains(err.Error(), "namespace override") {
				t.Errorf("Render(WithNamespace(%q)) err = %v, want namespace override error", ns, err)
			}
		})
	}
}

func TestRenderTextMissingKeyNamesFile(t *testing.T) {
	tests := []struct {
		file string
		edit func(f fixture)
	}{
		{file: "instruction.md", edit: func(f fixture) { f["instruction.md"] += "\n{{ .Vars.nope }}\n" }},
		{file: "hints.md", edit: func(f fixture) { f["hints.md"] += "\n{{ .Vars.nope }}\n" }},
		{file: "solution/README.md", edit: func(f fixture) { f["solution/README.md"] += "\n{{ .Vars.nope }}\n" }},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			f := validFixture()
			tc.edit(f)
			dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
			s, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Render(1)
			if err == nil {
				t.Fatal("Render succeeded, want missingkey error")
			}
			for _, w := range []string{"template: " + tc.file + ":", "\"nope\""} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q: %v", w, err)
				}
			}
		})
	}
}

func TestRenderInMemoryHints(t *testing.T) {
	s := &Scenario{
		Meta:        Meta{ID: "001-mem", Namespace: "mem"},
		Instruction: "ns {{ .Namespace }}",
		Hints:       []string{"look in {{ .Namespace }}", "plain"},
		Solution:    "no templates",
	}
	r, err := s.Render(1, WithNamespace("mem-x1"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if r.Instruction != "ns mem-x1" || r.Solution != "no templates" {
		t.Errorf("Instruction, Solution = %q, %q", r.Instruction, r.Solution)
	}
	if len(r.Hints) != 2 || r.Hints[0] != "look in mem-x1" || r.Hints[1] != "plain" {
		t.Errorf("Hints = %q, want [look in mem-x1 plain]", r.Hints)
	}
}

func TestLintNewRules(t *testing.T) {
	deploy := func(grace string) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app\n  namespace: {{ .Namespace }}\nspec:\n  template:\n    spec:\n" + grace + "      containers:\n        - name: app\n          image: {{ .Vars.image }}\n"
	}
	tests := []struct {
		name   string
		mutate func(f fixture)
		want   []string
	}{
		{
			name:   "deployment without grace period",
			mutate: func(f fixture) { f["setup/deployment.yaml"] = deploy("") },
			want:   []string{filepath.Join("setup", "deployment.yaml") + ": Deployment \"app\": spec.template.spec.terminationGracePeriodSeconds is unset (default 30), want 1"},
		},
		{
			name:   "deployment with grace period 30",
			mutate: func(f fixture) { f["setup/deployment.yaml"] = deploy("      terminationGracePeriodSeconds: 30\n") },
			want:   []string{filepath.Join("setup", "deployment.yaml") + ": Deployment \"app\": spec.template.spec.terminationGracePeriodSeconds is 30, want 1"},
		},
		{
			name: "second document in a multi-document file",
			mutate: func(f fixture) {
				f["setup/extra.yaml"] = "# leading comment\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n---\n" + strings.Replace(deploy(""), "name: app", "name: worker", 1) + "---\n"
			},
			want: []string{filepath.Join("setup", "extra.yaml") + ": Deployment \"worker\": spec.template.spec.terminationGracePeriodSeconds is unset"},
		},
		{
			name:   "invalid yaml",
			mutate: func(f fixture) { f["setup/bad.yaml"] = "kind: [Deployment\n" },
			want:   []string{filepath.Join("setup", "bad.yaml") + ": document 1 is not valid YAML"},
		},
		{
			name: "solution uses a var only one variant defines",
			mutate: func(f fixture) {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], `vars = { image = "nginx:a" }`, `vars = { image = "nginx:a", extra = "x" }`, 1)
				f["solution/README.md"] += "\n{{ .Vars.extra }}\n"
			},
			want: []string{"template: solution/README.md:33:8: executing \"solution/README.md\" at <.Vars.extra>: map has no entry for key \"extra\" (variant \"b\", seed 2)"},
		},
		{
			name: "approach still carries the template marker",
			mutate: func(f fixture) {
				f["approaches/first/snippet.txt"] = "kubectl get pods\n"
				f["approaches/first/content.md"] = "kubedrill-template: delete this line.\n"
			},
			want: []string{filepath.Join("approaches", "first") + ": still contains the template marker \"kubedrill-template\""},
		},
		{
			name: "solution still carries the template marker",
			mutate: func(f fixture) {
				f["solution/README.md"] = "<!-- kubedrill-template -->\n" + fixtureSolution
			},
			want: []string{filepath.Join("solution", "README.md") + ": still contains the template marker \"kubedrill-template\""},
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
			errs := Lint(root, nil, nil)
			var lines []string
			for _, e := range errs {
				lines = append(lines, e.Error())
			}
			all := strings.Join(lines, "\n")
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

func TestRealScenarioDiagnosisData(t *testing.T) {
	s, err := Load(realScenario)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Meta.FixTargets, ","); got != "apps/deployments/hello" {
		t.Errorf("FixTargets = %q, want apps/deployments/hello", got)
	}
	wantCause := map[string]string{
		"typo-tag":         "1.27-alpnie",
		"wrong-repo":       "library/ngnix",
		"private-registry": "registry.internal.example",
	}
	for _, v := range s.Variants {
		if !strings.Contains(v.RootCause, wantCause[v.Name]) {
			t.Errorf("variant %q root_cause %q lacks %q", v.Name, v.RootCause, wantCause[v.Name])
		}
	}
	var names []string
	for _, a := range s.Approaches {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "fix-manifest,set-image" {
		t.Errorf("Approaches = %q, want fix-manifest,set-image", got)
	}
	r, err := s.Render(1, WithNamespace("hello-world-k3x9z"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"instruction": r.Instruction, "solution": r.Solution, "hints": strings.Join(r.Hints, "\n")} {
		if strings.Contains(text, "{{") {
			t.Errorf("%s still holds a template action", name)
		}
		if strings.Contains(strings.ReplaceAll(text, "hello-world-k3x9z", ""), "hello-world") {
			t.Errorf("%s names the literal namespace hello-world instead of the episode namespace", name)
		}
	}
	if !strings.Contains(r.Instruction, "http://hello.hello-world-k3x9z.svc") {
		t.Errorf("instruction lacks the episode URL")
	}
	for _, a := range r.Approaches {
		if strings.Contains(a.Snippet+a.Body, "{{") {
			t.Errorf("approach %s still holds a template action", a.Name)
		}
		for _, line := range strings.Split(a.Snippet, "\n") {
			if strings.HasPrefix(line, "kubectl ") && !strings.HasPrefix(line, "kubectl -n hello-world-k3x9z ") {
				t.Errorf("approach %s line %q does not target the episode namespace", a.Name, line)
			}
		}
	}
}

func TestLintNamespaceLength(t *testing.T) {
	tests := []struct {
		name string
		ns   string
		want string
	}{
		{name: "57 characters", ns: strings.Repeat("a", 57)},
		{name: "58 characters", ns: strings.Repeat("a", 58), want: "scenario.toml: scenario.namespace \"" + strings.Repeat("a", 58) + "\" is 58 characters, at most 57 allowed"},
		{name: "templated 58 characters", ns: strings.Repeat("a", 56) + "-{{ .Seed }}", want: "is 58 characters, at most 57 allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := copyTree("../../scenarios/_template", filepath.Join(root, "scenarios", "_template")); err != nil {
				t.Fatal(err)
			}
			f := validFixture()
			f["scenario.toml"] = strings.Replace(f["scenario.toml"], `namespace = "fx-{{ .Seed }}"`, "namespace = \""+tc.ns+"\"", 1)
			writeFixture(t, root, "L1", "001-fixture", f)
			var lines []string
			for _, e := range Lint(root, nil, nil) {
				lines = append(lines, e.Error())
			}
			all := strings.Join(lines, "\n")
			if tc.want == "" {
				if all != "" {
					t.Errorf("Lint = %q, want no errors", all)
				}
				return
			}
			if !strings.Contains(all, tc.want) {
				t.Errorf("lint output lacks %q:\n%s", tc.want, all)
			}
		})
	}
}

func TestRenderApproaches(t *testing.T) {
	f := validFixture()
	f["approaches/set-image/snippet.txt"] = "kubectl -n {{ .Namespace }} set image deployment/app app={{ .Vars.image }}\n"
	f["approaches/set-image/content.md"] = "Patches {{ .Namespace }} only.\n"
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := s.Render(2, WithNamespace("fx-abcde"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []Approach{{Name: "set-image", Snippet: "kubectl -n fx-abcde set image deployment/app app=nginx:b", Body: "Patches fx-abcde only."}}
	if len(r.Approaches) != len(want) || r.Approaches[0] != want[0] {
		t.Errorf("Rendered.Approaches = %+v, want %+v", r.Approaches, want)
	}
	if got := s.Approaches[0].Snippet; !strings.Contains(got, "{{ .Namespace }}") {
		t.Errorf("Scenario.Approaches[0].Snippet = %q, want the raw template", got)
	}
}

func TestApproachTemplateErrors(t *testing.T) {
	t.Run("parse error at load", func(t *testing.T) {
		f := validFixture()
		f["approaches/bad/snippet.txt"] = "kubectl -n {{ .Namespace get pods\n"
		f["approaches/bad/content.md"] = "Body {{ end }}\n"
		_, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
		for _, w := range []string{
			filepath.Join("approaches", "bad", "snippet.txt") + ": template: " + filepath.Join("approaches", "bad", "snippet.txt") + ":",
			filepath.Join("approaches", "bad", "content.md") + ": template: " + filepath.Join("approaches", "bad", "content.md") + ":",
		} {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("Load err = %v, want %q", err, w)
			}
		}
	})
	t.Run("missing key at render", func(t *testing.T) {
		f := validFixture()
		f["approaches/bad/snippet.txt"] = "kubectl get pods\n"
		f["approaches/bad/content.md"] = "Uses {{ .Vars.nope }}.\n"
		s, err := Load(writeFixture(t, t.TempDir(), "L1", "001-fixture", f))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Render(1)
		w := "template: " + filepath.Join("approaches", "bad", "content.md") + ":"
		if err == nil || !strings.Contains(err.Error(), w) || !strings.Contains(err.Error(), `"nope"`) {
			t.Errorf("Render err = %v, want %q and \"nope\"", err, w)
		}
	})
}

func TestCatalogFindNotFoundError(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "L1", "001-fixture", validFixture())
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Find("nope")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("Find err = %T %v, want *NotFoundError", err, err)
	}
	if nf.Ref != "nope" {
		t.Errorf("Ref = %q, want nope", nf.Ref)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true")
	}
	if errors.Is(err, ErrAmbiguous) {
		t.Errorf("errors.Is(err, ErrAmbiguous) = true, want false")
	}
	if got, want := err.Error(), `find scenario "nope": scenario not found`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLoadTemplateErrorsSortedByField(t *testing.T) {
	f := validFixture()
	f["checks.yaml"] = strings.Replace(f["checks.yaml"], `    path: "{.spec.template.spec.containers[0].image}"
    equals: "{{ .Vars.image }}"`, `    path: "{{ .Bad"
    equals: "{{ .Bad"
    not_equals: "{{ .Bad"
    contains: "{{ .Bad"
    matches: "{{ .Bad"`, 1)
	f["checks.yaml"] = strings.Replace(f["checks.yaml"], "description: runs {{ .Vars.image }}", "description: runs {{ .Bad", 1)
	f["instruction.md"] = strings.Replace(f["instruction.md"], "- runs {{ .Vars.image }}", "- runs x", 1)
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	order := []string{"check \"image\": contains:", "check \"image\": description:", "check \"image\": equals:", "check \"image\": matches:", "check \"image\": not_equals:", "check \"image\": path:"}
	var first string
	for i := 0; i < 20; i++ {
		_, err := Load(dir)
		if err == nil {
			t.Fatal("Load succeeded, want template errors")
		}
		msg := err.Error()
		if i == 0 {
			first = msg
		} else if msg != first {
			t.Fatalf("Load error changed between runs:\n%s\n---\n%s", first, msg)
		}
		last := -1
		for _, w := range order {
			at := strings.Index(msg, w)
			if at < 0 {
				t.Fatalf("error lacks %q:\n%s", w, msg)
			}
			if at < last {
				t.Fatalf("%q is out of sorted order:\n%s", w, msg)
			}
			last = at
		}
	}
}

func TestLintPrivilegedSetup(t *testing.T) {
	pod := func(spec string) string {
		return "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n  namespace: {{ .Namespace }}\nspec:\n" + spec
	}
	const ctr = "  containers:\n    - name: c\n      image: busybox\n"
	tests := []struct {
		name  string
		setup string
		allow bool
		want  string
	}{
		{name: "privileged container", setup: pod(ctr + "      securityContext:\n        privileged: true\n"), want: "Pod \"p\": spec.containers[0].securityContext.privileged is true"},
		{name: "hostPath volume", setup: pod(ctr + "  volumes:\n    - name: v\n      hostPath:\n        path: /var/lib\n"), want: "Pod \"p\": spec.volumes[0].hostPath mounts a node path"},
		{name: "hostNetwork", setup: pod("  hostNetwork: true\n" + ctr), want: "Pod \"p\": spec.hostNetwork is true"},
		{name: "hostPID", setup: pod("  hostPID: true\n" + ctr), want: "Pod \"p\": spec.hostPID is true"},
		{name: "hostIPC", setup: pod("  hostIPC: true\n" + ctr), want: "Pod \"p\": spec.hostIPC is true"},
		{name: "capabilities add", setup: pod(ctr + "      securityContext:\n        capabilities:\n          add: [\"NET_ADMIN\"]\n"), want: "Pod \"p\": spec.containers[0].securityContext.capabilities.add adds capabilities"},
		{name: "docker.sock path", setup: pod(ctr + "      env:\n        - name: DOCKER_HOST\n          value: unix:///var/run/docker.sock\n"), want: "Pod \"p\": spec.containers[0].env[0].value names docker.sock"},
		{name: "privileged in a Deployment template", setup: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\n  namespace: {{ .Namespace }}\nspec:\n  template:\n    spec:\n      terminationGracePeriodSeconds: 1\n      containers:\n        - name: c\n          image: busybox\n          securityContext:\n            privileged: true\n", want: "Deployment \"d\": spec.template.spec.containers[0].securityContext.privileged is true"},
		{name: "hostPort", setup: pod(ctr + "      ports:\n        - containerPort: 80\n          hostPort: 8080\n"), want: "Pod \"p\": spec.containers[0].ports[0].hostPort binds a node port"},
		{name: "procMount Unmasked", setup: pod(ctr + "      securityContext:\n        procMount: Unmasked\n"), want: "Pod \"p\": spec.containers[0].securityContext.procMount is Unmasked"},
		{name: "seccompProfile Unconfined", setup: pod("  securityContext:\n    seccompProfile:\n      type: Unconfined\n" + ctr), want: "Pod \"p\": spec.securityContext.seccompProfile.type is Unconfined"},
		{name: "appArmorProfile Unconfined", setup: pod(ctr + "      securityContext:\n        appArmorProfile:\n          type: Unconfined\n"), want: "Pod \"p\": spec.containers[0].securityContext.appArmorProfile.type is Unconfined"},
		{name: "PersistentVolume spec.local", setup: "apiVersion: v1\nkind: PersistentVolume\nmetadata:\n  name: pv\nspec:\n  capacity:\n    storage: 1Gi\n  accessModes: [ReadWriteOnce]\n  local:\n    path: /mnt/disks/a\n", want: "PersistentVolume \"pv\": spec.local mounts a node path"},
		{name: "separator with a trailing comment before a privileged Pod", setup: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: {{ .Namespace }}\n--- # next\n" + pod(ctr+"      securityContext:\n        privileged: true\n"), want: "Pod \"p\": spec.containers[0].securityContext.privileged is true"},
		{name: "hostPort zero, RuntimeDefault and Default profiles pass", setup: pod("  securityContext:\n    seccompProfile:\n      type: RuntimeDefault\n" + ctr + "      ports:\n        - containerPort: 80\n          hostPort: 0\n      securityContext:\n        procMount: Default\n        appArmorProfile:\n          type: RuntimeDefault\n")},
		{name: "false flags and capabilities drop pass", setup: pod("  hostNetwork: false\n" + ctr + "      securityContext:\n        privileged: false\n        capabilities:\n          drop: [\"ALL\"]\n          add: []\n")},
		{name: "allow_privileged lifts the rule", setup: pod("  hostNetwork: true\n" + ctr + "      securityContext:\n        privileged: true\n"), allow: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := copyTree("../../scenarios/_template", filepath.Join(root, "scenarios", "_template")); err != nil {
				t.Fatal(err)
			}
			f := validFixture()
			f["setup/extra.yaml"] = tc.setup
			if tc.allow {
				f["scenario.toml"] = strings.Replace(f["scenario.toml"], "fix_targets = ", "allow_privileged = true\nfix_targets = ", 1)
			}
			writeFixture(t, root, "L1", "001-fixture", f)
			var lines []string
			for _, e := range Lint(root, nil, nil) {
				lines = append(lines, e.Error())
			}
			all := strings.Join(lines, "\n")
			if tc.want == "" {
				if all != "" {
					t.Errorf("Lint = %q, want no errors", all)
				}
				return
			}
			want := filepath.Join("scenarios", "L1", "001-fixture") + ": " + filepath.Join("setup", "extra.yaml") + ": " + tc.want
			if !strings.Contains(all, want) || !strings.Contains(all, "allow_privileged = true") {
				t.Errorf("lint output lacks %q:\n%s", want, all)
			}
			if len(lines) != 1 {
				t.Errorf("Lint returned %d errors, want 1:\n%s", len(lines), all)
			}
		})
	}
}

func TestRealScenarioImageCheck(t *testing.T) {
	s, err := Load(realScenario)
	if err != nil {
		t.Fatal(err)
	}
	var check *Check
	for i := range s.Checks {
		if s.Checks[i].ID == "image-correct" {
			check = &s.Checks[i]
		}
	}
	if check == nil {
		t.Fatal("check image-correct not found")
	}
	if !check.Spoiler {
		t.Errorf("image-correct Spoiler = false, want true")
	}
	r, err := s.Render(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Checks {
		if c.ID == "image-correct" && !c.Spoiler {
			t.Errorf("rendered image-correct Spoiler = false, want true")
		}
	}
	re := regexp.MustCompile(check.Matches)
	digest := "@sha256:" + strings.Repeat("0123456789abcdef", 4)
	accept := []string{
		"nginx:1.27-alpine",
		"docker.io/nginx:1.27-alpine",
		"docker.io/library/nginx:1.27-alpine",
		"nginx:1.27-alpine" + digest,
		"docker.io/library/nginx:1.27-alpine" + digest,
	}
	for _, img := range accept {
		if !re.MatchString(img) {
			t.Errorf("matches %q rejects %q, want accept", check.Matches, img)
		}
	}
	var reject []string
	for _, v := range s.Variants {
		reject = append(reject, v.Vars["image"])
	}
	if len(reject) != 3 {
		t.Fatalf("variant images = %q, want 3", reject)
	}
	reject = append(reject, "library/nginx:1.27-alpine", "nginx:1.27-alpine@sha256:abc")
	for _, img := range reject {
		if re.MatchString(img) {
			t.Errorf("matches %q accepts %q, want reject", check.Matches, img)
		}
	}
}
