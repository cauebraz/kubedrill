package scenario

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/yamldoc"
)

const dirTemplate = "_template"

var (
	tomlIDLine    = regexp.MustCompile(`(?m)^id\s*=\s*"[^"]*"`)
	tomlLevelLine = regexp.MustCompile(`(?m)^level\s*=\s*"[^"]*"`)
	doneBullet    = regexp.MustCompile(`^[-*]\s+`)
)

// templateMarkers are the lines the template carries so that a scaffolded
// scenario that was not edited is rejected by Lint.
var templateMarkers = map[string][]string{
	fileMeta:                                 {"# kubedrill-template", `namespace = "template"`},
	fileInstruction:                          {"<!-- kubedrill-template: delete this block -->", "Title in the reporter's words"},
	fileHints:                                {"# kubedrill-template"},
	fileChecks:                               {"# kubedrill-template"},
	filepath.Join(dirSolution, fileSolution): {"kubedrill-template"},
}

const approachMarker = "kubedrill-template"

// maxScenarioNamespaceLen leaves room for the episode suffix: an episode
// namespace is the rendered scenario.namespace, a hyphen and 5 characters,
// and must fit the 63-character DNS label limit.
const maxScenarioNamespaceLen = 57

// Refs are the ids a scenario may name, read from curriculum.yaml and
// docs/concepts. A nil list or map is not checked. Modules are the module
// ids, Concepts the concept page ids, ExamDomains the exam domains written
// EXAM:domain (CKA:troubleshooting), and Planned maps every scenario id that
// curriculum.yaml lists to the module that lists it.
type Refs struct {
	Modules     []string
	Concepts    []string
	ExamDomains []string
	Planned     map[string]string
}

// lintRefs is Refs as sets.
type lintRefs struct {
	modules, concepts, exams map[string]bool
	planned                  map[string]string
}

func setOf(ids []string) map[string]bool {
	if ids == nil {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// Lint loads every scenario directory under root/scenarios and the template
// at root/scenarios/_template and returns one error per problem, each
// prefixed with the directory relative to root. Beyond Load it renders every
// variant (undefined variables, namespace rules), requires at least two
// variants above L0, rejects duplicated ids, checks that scenario.module is
// one of moduleIDs when the list is not empty, checks that every
// scenario.concepts id is one of conceptIDs when that list is not nil,
// rejects scenarios that still carry the template markers, requires the
// "## Done when" bullets of instruction.md to equal the check descriptions in
// order, parses every rendered check path as a JSONPath and every gte and
// lte bound as a finite number, and requires every Deployment in setup/ to set
// spec.template.spec.terminationGracePeriodSeconds to 1 so that a reset does
// not wait for the default 30 second grace period. It rejects a rendered
// scenario.namespace longer than 57 characters, since the episode namespace
// appends a hyphen and 5 characters. Rendering every variant covers
// instruction.md, hints.md, solution/README.md, story.md and the approaches
// as well as the setup files and checks. Unless scenario.toml sets allow_privileged, it
// rejects setup manifests that set privileged: true, hostNetwork, hostPID,
// hostIPC or a hostPort, mount a hostPath, add capabilities, set procMount:
// Unmasked or an Unconfined seccompProfile or appArmorProfile, declare a
// PersistentVolume with spec.local, or name docker.sock. Setup files are cut
// into documents with yamldoc.Split, so a "--- # comment" separator starts a
// new document as it does for kubectl.
//
// refs cross-checks the scenarios with the curriculum and the concept
// library; see Refs.
func Lint(root string, refs Refs) []error {
	var out []error
	report := func(dir string, err error) {
		rel, rerr := filepath.Rel(root, dir)
		if rerr != nil {
			rel = dir
		}
		for _, e := range flatten(err) {
			out = append(out, fmt.Errorf("%s: %s", rel, e.Error()))
		}
	}
	lr := lintRefs{modules: setOf(refs.Modules), concepts: setOf(refs.Concepts), exams: setOf(refs.ExamDomains), planned: refs.Planned}

	dirs, err := scenarioDirs(root)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", dirScenarios, err)}
	}
	tpl := filepath.Join(root, dirScenarios, dirTemplate)
	if _, err := os.Stat(tpl); err != nil {
		report(tpl, err)
	} else if s, err := load(tpl, false); err != nil {
		report(tpl, err)
	} else {
		report(tpl, lintScenario(s, false, lintRefs{}))
	}

	seen := map[string]string{}
	for _, dir := range dirs {
		s, err := Load(dir)
		if err != nil {
			report(dir, err)
			continue
		}
		if other, dup := seen[s.Meta.ID]; dup {
			report(dir, fmt.Errorf("%s: scenario.id %q is also defined in %s", fileMeta, s.Meta.ID, other))
		}
		seen[s.Meta.ID] = dir
		report(dir, lintScenario(s, true, lr))
	}
	if lr.planned != nil {
		ids := make([]string, 0, len(lr.planned))
		for id := range lr.planned {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				out = append(out, fmt.Errorf("curriculum.yaml: module %s lists scenario %q, which has no directory under %s", lr.planned[id], id, dirScenarios))
			}
		}
	}
	return out
}

func lintScenario(s *Scenario, real bool, lr lintRefs) error {
	var errs []error
	if real && s.Meta.Level != "L0" && len(s.Variants) < 2 {
		errs = append(errs, fmt.Errorf("%s: level %s requires at least two [[variant]] entries, got %d", fileMeta, s.Meta.Level, len(s.Variants)))
	}
	if real && len(lr.modules) > 0 && !lr.modules[s.Meta.Module] {
		errs = append(errs, fmt.Errorf("%s: module %q is not in curriculum.yaml", fileMeta, s.Meta.Module))
	}
	if real && lr.concepts != nil {
		for _, id := range s.Meta.Concepts {
			if !lr.concepts[id] {
				errs = append(errs, fmt.Errorf("%s: concept %q has no docs/concepts/%s.md", fileMeta, id, id))
			}
		}
	}
	if real && lr.exams != nil {
		for _, e := range s.Meta.Exam {
			if !lr.exams[e] {
				errs = append(errs, fmt.Errorf("%s: exam %q is not a domain of curriculum.yaml exams", fileMeta, e))
			}
		}
	}
	if real && lr.planned != nil {
		switch module, ok := lr.planned[s.Meta.ID]; {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: curriculum.yaml lists no scenario %q; add id: %s to a planned scenario of module %s", fileMeta, s.Meta.ID, s.Meta.ID, s.Meta.Module))
		case module != s.Meta.Module:
			errs = append(errs, fmt.Errorf("%s: scenario.module is %q but curriculum.yaml lists %s under module %s", fileMeta, s.Meta.Module, s.Meta.ID, module))
		}
	}
	if real {
		errs = append(errs, lintMarkers(s)...)
	}
	errs = append(errs, lintDoneWhen(s)...)
	seeds := s.VariantSeeds()
	if len(s.Variants) > 0 && len(seeds) != len(s.Variants) {
		errs = append(errs, fmt.Errorf("%s: %d variants but only %d are picked by a seed up to %d; rename the variants", fileMeta, len(s.Variants), len(seeds), maxSeedSearch))
	}
	seen := map[string]bool{}
	for _, seed := range seeds {
		variant := s.VariantFor(seed).Name
		r, err := s.Render(seed)
		problems := flatten(err)
		if err == nil {
			problems = lintGracePeriod(s, r)
			problems = append(problems, lintCheckValues(r.Checks)...)
			problems = append(problems, lintRenderedHints(s, r)...)
			problems = append(problems, lintSetupImages(s, r)...)
			if !s.Meta.AllowPrivileged {
				problems = append(problems, lintPrivileged(s, r)...)
			}
			if n := len(r.Namespace); n > maxScenarioNamespaceLen {
				problems = append(problems, fmt.Errorf("%s: scenario.namespace %q is %d characters, at most %d allowed so the episode suffix (a hyphen and 5 characters) fits the 63-character DNS label limit", fileMeta, r.Namespace, n, maxScenarioNamespaceLen))
			}
		}
		for _, e := range problems {
			if seen[e.Error()] {
				continue
			}
			seen[e.Error()] = true
			if len(s.Variants) > 1 {
				e = fmt.Errorf("%w (variant %q, seed %d)", e, variant, seed)
			}
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

func lintGracePeriod(s *Scenario, r *Rendered) []error {
	var errs []error
	for i, m := range r.Manifests {
		rel := fmt.Sprintf("manifest %d", i)
		if i < len(s.SetupFiles) {
			rel = relTo(s.Dir, s.SetupFiles[i])
		}
		for j, doc := range yamldoc.Split([]byte(m)) {
			var obj struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					Template struct {
						Spec struct {
							TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds"`
						} `json:"spec"`
					} `json:"template"`
				} `json:"spec"`
			}
			if err := yaml.Unmarshal(doc, &obj); err != nil {
				errs = append(errs, fmt.Errorf("%s: document %d is not valid YAML: %v", rel, j+1, err))
				continue
			}
			if obj.Kind != "Deployment" {
				continue
			}
			if g := obj.Spec.Template.Spec.TerminationGracePeriodSeconds; g == nil || *g != 1 {
				got := "unset (default 30)"
				if g != nil {
					got = fmt.Sprint(*g)
				}
				errs = append(errs, fmt.Errorf("%s: Deployment %q: spec.template.spec.terminationGracePeriodSeconds is %s, want 1 so the reset does not wait for old pods", rel, obj.Metadata.Name, got))
			}
		}
	}
	return errs
}

var hostFlags = map[string]bool{"hostNetwork": true, "hostPID": true, "hostIPC": true}

func lintPrivileged(s *Scenario, r *Rendered) []error {
	var errs []error
	for i, m := range r.Manifests {
		rel := fmt.Sprintf("manifest %d", i)
		if i < len(s.SetupFiles) {
			rel = relTo(s.Dir, s.SetupFiles[i])
		}
		for _, doc := range yamldoc.Split([]byte(m)) {
			var obj map[string]any
			if err := yaml.Unmarshal(doc, &obj); err != nil {
				continue
			}
			kind, _ := obj["kind"].(string)
			var name string
			if md, ok := obj["metadata"].(map[string]any); ok {
				name, _ = md["name"].(string)
			}
			fields := privilegedFields(obj, "")
			if kind == "PersistentVolume" {
				if spec, ok := obj["spec"].(map[string]any); ok && spec["local"] != nil {
					fields = append(fields, "spec.local mounts a node path")
				}
			}
			for _, f := range fields {
				errs = append(errs, fmt.Errorf("%s: %s %q: %s; the listed fields escape the pod sandbox and are refused unless scenario.toml sets allow_privileged = true, which needs maintainer review", rel, kind, name, f))
			}
		}
	}
	return errs
}

func privilegedFields(v any, path string) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := k
			if path != "" {
				child = path + "." + k
			}
			val := t[k]
			switch {
			case k == "privileged" && val == true:
				out = append(out, child+" is true")
			case hostFlags[k] && val == true:
				out = append(out, child+" is true")
			case k == "hostPath" && val != nil:
				out = append(out, child+" mounts a node path")
			case k == "add" && strings.HasSuffix(path, "capabilities"):
				if l, ok := val.([]any); ok && len(l) > 0 {
					out = append(out, child+" adds capabilities")
				}
			case k == "hostPort" && isNonZeroNumber(val):
				out = append(out, child+" binds a node port")
			case k == "procMount" && val == "Unmasked":
				out = append(out, child+" is Unmasked")
			case k == "type" && val == "Unconfined" && (strings.HasSuffix(path, "seccompProfile") || strings.HasSuffix(path, "appArmorProfile")):
				out = append(out, child+" is Unconfined")
			default:
				out = append(out, privilegedFields(val, child)...)
			}
		}
	case []any:
		for i, e := range t {
			out = append(out, privilegedFields(e, fmt.Sprintf("%s[%d]", path, i))...)
		}
	case string:
		if strings.Contains(t, "docker.sock") {
			out = append(out, path+" names docker.sock")
		}
	}
	return out
}

func isNonZeroNumber(v any) bool {
	switch n := v.(type) {
	case float64:
		return n != 0
	case int64:
		return n != 0
	case int:
		return n != 0
	}
	return false
}

func lintMarkers(s *Scenario) []error {
	var errs []error
	for file, markers := range templateMarkers {
		b, err := os.ReadFile(filepath.Join(s.Dir, file))
		if err != nil {
			continue
		}
		text := string(b)
		for _, m := range markers {
			if strings.Contains(text, m) {
				errs = append(errs, fmt.Errorf("%s: still contains the template marker %q; edit the scaffolded text", file, m))
			}
		}
	}
	for _, a := range s.Approaches {
		if strings.Contains(a.Snippet, approachMarker) || strings.Contains(a.Body, approachMarker) {
			errs = append(errs, fmt.Errorf("%s: still contains the template marker %q; edit the scaffolded text", filepath.Join(dirApproaches, a.Name), approachMarker))
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

func lintDoneWhen(s *Scenario) []error {
	body, ok := section(s.Instruction, headingDone)
	if !ok {
		return nil
	}
	var bullets []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !doneBullet.MatchString(line) {
			continue
		}
		bullets = append(bullets, strings.TrimSpace(doneBullet.ReplaceAllString(line, "")))
	}
	var errs []error
	if len(bullets) != len(s.Checks) {
		errs = append(errs, fmt.Errorf("%s: %q has %d bullets, %s has %d checks; one bullet per check in order", fileInstruction, headingDone, len(bullets), fileChecks, len(s.Checks)))
	}
	for i, c := range s.Checks {
		if i >= len(bullets) {
			break
		}
		if !strings.EqualFold(bullets[i], strings.TrimSpace(c.Description)) {
			errs = append(errs, fmt.Errorf("%s: %q bullet %d is %q, check %q description is %q", fileInstruction, headingDone, i+1, bullets[i], c.ID, c.Description))
		}
	}
	return errs
}

func flatten(err error) []error {
	if err == nil {
		return nil
	}
	var leaves []error
	var walk func(e error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range j.Unwrap() {
				walk(child)
			}
			return
		}
		if u, ok := e.(interface{ Unwrap() error }); ok {
			if inner := u.Unwrap(); inner != nil {
				if _, multi := inner.(interface{ Unwrap() []error }); multi {
					walk(inner)
					return
				}
			}
		}
		leaves = append(leaves, e)
	}
	walk(err)
	return leaves
}

// Scaffold copies root/scenarios/_template into root/scenarios/<level>/<id>,
// rewriting id and level in scenario.toml, and returns the new directory. It
// fails when the destination already exists or id and level are malformed.
func Scaffold(root, level, id string) (string, error) {
	if !levelPattern.MatchString(level) {
		return "", fmt.Errorf("scaffold scenario: level %q must be L0..L6", level)
	}
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("scaffold scenario: id %q must match %s", id, idPattern)
	}
	src := filepath.Join(root, dirScenarios, dirTemplate)
	if st, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("scaffold scenario %s: %w", id, err)
	} else if !st.IsDir() {
		return "", fmt.Errorf("scaffold scenario %s: %s is not a directory", id, src)
	}
	dst := filepath.Join(root, dirScenarios, level, id)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("scaffold scenario %s: %s already exists", id, dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("scaffold scenario %s: %w", id, err)
	}
	if err := copyTree(src, dst); err != nil {
		return "", fmt.Errorf("scaffold scenario %s: %w", id, err)
	}
	metaPath := filepath.Join(dst, fileMeta)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return "", fmt.Errorf("scaffold scenario %s: %w", id, err)
	}
	text := string(b)
	text = replaceFirst(tomlIDLine, text, fmt.Sprintf("id = %q", id))
	text = replaceFirst(tomlLevelLine, text, fmt.Sprintf("level = %q", level))
	if err := os.WriteFile(metaPath, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("scaffold scenario %s: %w", id, err)
	}
	return dst, nil
}

func replaceFirst(re *regexp.Regexp, text, repl string) string {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return text[:loc[0]] + repl + text[loc[1]:]
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("copy %s: not a regular file", path)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

// lintRenderedHints applies the hint ladder and the mutating command rule
// to hints.md as rendered for r, so a variable cannot carry a heading or a
// kubectl write the raw file does not show.
func lintRenderedHints(s *Scenario, r *Rendered) []error {
	if s.hintsDoc == "" {
		return nil
	}
	doc, err := execute(fileHints, s.hintsDoc, templateData{Seed: r.Seed, Namespace: r.Namespace, Variant: r.Variant.Name, Vars: r.Vars})
	if err != nil {
		return nil
	}
	sections, err := parseHintSections(doc)
	if err != nil {
		return []error{fmt.Errorf("%s: rendered: %w", fileHints, err)}
	}
	var errs []error
	validateHintLadder(sections, func(file, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: rendered: %s", file, fmt.Sprintf(format, args...)))
	})
	for _, h := range sections {
		if cmd := mutatingKubectl(h.body); cmd != "" {
			errs = append(errs, fmt.Errorf("%s: rendered: %q contains the mutating command %q; hints describe where to look and what to change, the learner types the change", fileHints, h.heading, cmd))
		}
	}
	return errs
}

// lintSetupImages requires every container image of the rendered setup to
// be pinned by digest, except an image that is the value of a variant
// variable: that one is the bug the variant plants (a wrong tag, registry
// or repository) and cannot carry a digest.
func lintSetupImages(s *Scenario, r *Rendered) []error {
	planted := map[string]bool{}
	for _, v := range r.Vars {
		planted[v] = true
	}
	var errs []error
	for i, m := range r.Manifests {
		rel := fmt.Sprintf("manifest %d", i)
		if i < len(s.SetupFiles) {
			rel = relTo(s.Dir, s.SetupFiles[i])
		}
		for _, ci := range manifestImages(m) {
			if strings.Contains(ci.image, "@sha256:") || planted[ci.image] {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %s %q container %q: image %q is not pinned; write image:tag@sha256:<digest>, or set it from a variant variable when the image is the bug", rel, ci.kind, ci.name, ci.container, ci.image))
		}
	}
	return errs
}
