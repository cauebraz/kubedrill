package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"sigs.k8s.io/yaml"
)

const (
	fileMeta        = "scenario.toml"
	fileInstruction = "instruction.md"
	fileHints       = "hints.md"
	fileChecks      = "checks.yaml"
	dirSetup        = "setup"
	fileHook        = "hook.sh"
	dirSolution     = "solution"
	fileSolution    = "README.md"
	fileSolve       = "solve.sh"
	dirApproaches   = "approaches"
	fileSnippet     = "snippet.txt"
	fileContent     = "content.md"

	headingDone   = "## Done when"
	headingBefore = "## Before you start"
	maxBeforeLine = 8
	minWhyLen     = 20
	maxWhyLen     = 400

	minRootCauseLen = 20
	maxRootCauseLen = 300
	minHints        = 3
	maxSnippetLines = 8
	maxNameLen      = 253
)

var hintLadder = []string{
	"## Hint 1: where to look",
	"## Hint 2: what to observe",
	"## Hint 3: what it means",
	"## Hint 4: what to change",
}

var solutionSections = []string{
	"## Symptom",
	"## Candidate causes",
	"## Observation that splits them",
	"## Change",
	"## Verify",
	"## In the real world",
	"## Go deeper",
}

var mutatingVerbs = map[string]bool{
	"set": true, "edit": true, "patch": true, "apply": true, "create": true,
	"replace": true, "delete": true, "scale": true, "label": true,
	"annotate": true, "expose": true, "run": true, "autoscale": true,
	"cordon": true, "uncordon": true, "drain": true, "taint": true,
}

var mutatingRollout = map[string]bool{"undo": true, "restart": true, "pause": true, "resume": true}

var readVerbs = map[string]bool{
	"get": true, "describe": true, "logs": true, "explain": true, "top": true,
	"events": true, "auth": true, "api-resources": true, "api-versions": true,
	"version": true, "config": true, "diff": true, "wait": true,
	"port-forward": true, "exec": true, "debug": true, "cp": true,
	"attach": true, "cluster-info": true, "kustomize": true, "proxy": true,
	"certificate": true, "plugin": true, "completion": true, "alpha": true,
	"options": true, "help": true,
}

var (
	idPattern    = regexp.MustCompile(`^\d{3}-[a-z0-9-]+$`)
	levelPattern = regexp.MustCompile(`^L[0-6]$`)
	examPattern  = regexp.MustCompile(`^(CKA|CKAD|CKS):[a-z0-9-]+$`)
	hintPattern  = regexp.MustCompile(`(?m)^## Hint \d+(:[^\n]*)?[ \t]*$`)
	groupRe      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	resourceRe   = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	objectNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	checkIDRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	conceptIDRe  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

type metaFile struct {
	Scenario Meta      `toml:"scenario"`
	Variant  []Variant `toml:"variant"`
}

type checksFile struct {
	Checks []Check `json:"checks"`
}

// Load reads and validates the scenario directory dir. Every problem found is
// returned at once, joined with errors.Join, each one naming the file.
func Load(dir string) (*Scenario, error) {
	return load(dir, true)
}

func load(dir string, checkLayout bool) (*Scenario, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("load scenario %s: %w", dir, err)
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("load scenario %s: %w", dir, err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("load scenario %s: not a directory", dir)
	}

	var errs []error
	fail := func(file, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
	}

	s := &Scenario{Dir: abs}
	loadMeta(abs, s, checkLayout, fail)
	loadInstruction(abs, s, fail)
	loadHints(abs, s, fail)
	loadSetup(abs, s, fail)
	loadChecks(abs, s, fail)
	loadSolution(abs, s, fail)
	loadApproaches(abs, s, fail)

	if len(errs) > 0 {
		return nil, fmt.Errorf("load scenario %s: %w", dir, errors.Join(errs...))
	}
	return s, nil
}

type failFunc func(file, format string, args ...any)

func loadMeta(dir string, s *Scenario, checkLayout bool, fail failFunc) {
	var mf metaFile
	md, err := toml.DecodeFile(filepath.Join(dir, fileMeta), &mf)
	if err != nil {
		fail(fileMeta, "%v", err)
		return
	}
	for _, k := range md.Undecoded() {
		fail(fileMeta, "unknown key %q", k.String())
	}
	s.Meta = mf.Scenario
	s.Variants = mf.Variant
	m := s.Meta

	if m.ID == "" {
		fail(fileMeta, "scenario.id is required")
	} else if !idPattern.MatchString(m.ID) {
		fail(fileMeta, "scenario.id %q must match %s", m.ID, idPattern)
	} else if checkLayout && m.ID != filepath.Base(dir) {
		fail(fileMeta, "scenario.id %q does not match directory name %q", m.ID, filepath.Base(dir))
	}
	if m.Title == "" {
		fail(fileMeta, "scenario.title is required")
	}
	if m.Level == "" {
		fail(fileMeta, "scenario.level is required")
	} else if !levelPattern.MatchString(m.Level) {
		fail(fileMeta, "scenario.level %q must be L0..L6", m.Level)
	} else if checkLayout && m.Level != filepath.Base(filepath.Dir(dir)) {
		fail(fileMeta, "scenario.level %q does not match parent directory %q", m.Level, filepath.Base(filepath.Dir(dir)))
	}
	if m.Module == "" {
		fail(fileMeta, "scenario.module is required")
	}
	switch m.Type {
	case "":
		fail(fileMeta, "scenario.type is required")
	case TypeBreakFix, TypeBuild, TypeDesign, TypeIncident:
	default:
		fail(fileMeta, "scenario.type %q must be one of %s, %s, %s, %s", m.Type, TypeBreakFix, TypeBuild, TypeDesign, TypeIncident)
	}
	if m.Minutes <= 0 {
		fail(fileMeta, "scenario.minutes must be > 0, got %d", m.Minutes)
	}
	if m.Namespace == "" {
		fail(fileMeta, "scenario.namespace is required")
	} else if _, err := parseTemplate("scenario.namespace", m.Namespace); err != nil {
		fail(fileMeta, "scenario.namespace: %v", err)
	}
	for i, e := range m.Exam {
		if !examPattern.MatchString(e) {
			fail(fileMeta, "scenario.exam[%d] %q must look like CKA:domain-id, CKAD:domain-id or CKS:domain-id", i, e)
		}
	}
	for i, id := range m.Concepts {
		if !conceptIDRe.MatchString(id) {
			fail(fileMeta, "scenario.concepts[%d] %q must match %s", i, id, conceptIDRe)
		}
	}
	if (m.Type == TypeBreakFix || m.Type == TypeBuild) && len(m.Concepts) == 0 {
		fail(fileMeta, "scenario.concepts: type %s requires at least one concept id (docs/concepts/<id>.md)", m.Type)
	}
	seen := map[string]bool{}
	for i, v := range s.Variants {
		if v.Name == "" {
			fail(fileMeta, "variant[%d].name is required", i)
			continue
		}
		if seen[v.Name] {
			fail(fileMeta, "variant name %q is duplicated", v.Name)
		}
		seen[v.Name] = true
	}
	validateRootCauses(m, s.Variants, fail)
	validateFixTargets(m, fail)
}

func validateRootCauses(m Meta, variants []Variant, fail failFunc) {
	if m.Type == TypeBreakFix && len(variants) == 0 {
		fail(fileMeta, "type %s requires at least one [[variant]] with root_cause", m.Type)
	}
	for i, v := range variants {
		label := fmt.Sprintf("variant[%d]", i)
		if v.Name != "" {
			label = fmt.Sprintf("variant %q", v.Name)
		}
		n := len([]rune(strings.TrimSpace(v.RootCause)))
		switch {
		case n == 0 && m.Type == TypeBreakFix:
			fail(fileMeta, "%s: root_cause is required for type %s: one sentence naming the fault", label, m.Type)
		case n == 0:
		case n < minRootCauseLen || n > maxRootCauseLen:
			fail(fileMeta, "%s: root_cause must be %d to %d characters, got %d", label, minRootCauseLen, maxRootCauseLen, n)
		}
	}
}

func validateFixTargets(m Meta, fail failFunc) {
	if m.Type == TypeBreakFix && len(m.FixTargets) == 0 {
		fail(fileMeta, "scenario.fix_targets: type %s requires at least one group/resource/name entry", m.Type)
	}
	seen := map[string]bool{}
	for i, t := range m.FixTargets {
		if _, _, _, err := ParseFixTarget(t); err != nil {
			fail(fileMeta, "scenario.fix_targets[%d]: %v", i, err)
			continue
		}
		if seen[t] {
			fail(fileMeta, "scenario.fix_targets[%d] %q is duplicated", i, t)
		}
		seen[t] = true
	}
}

// ParseFixTarget splits a fix target "group/resource/name" into its parts.
// The core API group is written "core" and returned as the empty string, the
// way the API server reports it; resource is the lowercase plural resource
// name and name is the object name, for example "apps/deployments/hello" or
// "core/services/hello".
func ParseFixTarget(s string) (group, resource, name string, err error) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("fix target %q must be group/resource/name (core group written \"core\")", s)
	}
	group, resource, name = parts[0], parts[1], parts[2]
	var errs []error
	switch {
	case group == "":
		errs = append(errs, errors.New("group is empty, write \"core\" for the core group"))
	case group == "core":
		group = ""
	case !groupRe.MatchString(group) || len(group) > maxNameLen:
		errs = append(errs, fmt.Errorf("group %q is not a DNS subdomain", group))
	}
	if !resourceRe.MatchString(resource) {
		errs = append(errs, fmt.Errorf("resource %q must be a lowercase plural resource name such as deployments", resource))
	}
	if !objectNameRe.MatchString(name) || len(name) > maxNameLen {
		errs = append(errs, fmt.Errorf("name %q is not a DNS-1123 subdomain", name))
	}
	if len(errs) > 0 {
		return "", "", "", fmt.Errorf("fix target %q: %w", s, errors.Join(errs...))
	}
	return group, resource, name, nil
}

func loadInstruction(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileInstruction))
	if err != nil {
		fail(fileInstruction, "%v", err)
		return
	}
	s.Instruction = string(b)
	if _, err := parseTemplate(fileInstruction, s.Instruction); err != nil {
		fail(fileInstruction, "%v", err)
	}
	if !hasHeading(s.Instruction, headingDone) {
		fail(fileInstruction, "missing %q section", headingDone)
	}
	if body, ok := section(s.Instruction, headingBefore); ok {
		if n := len(strings.Split(body, "\n")); body != "" && n > maxBeforeLine {
			fail(fileInstruction, "%q is %d lines, at most %d allowed", headingBefore, n, maxBeforeLine)
		}
	}
}

func loadHints(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileHints))
	if err != nil {
		fail(fileHints, "%v", err)
		return
	}
	doc := string(b)
	if _, err := parseTemplate(fileHints, doc); err != nil {
		fail(fileHints, "%v", err)
		return
	}
	sections, err := parseHintSections(doc)
	if err != nil {
		fail(fileHints, "%v", err)
		return
	}
	validateHintLadder(sections, fail)
	for _, h := range sections {
		if cmd := mutatingKubectl(h.body); cmd != "" {
			fail(fileHints, "%q contains the mutating command %q; hints describe where to look and what to change, the learner types the change", h.heading, cmd)
		}
	}
	s.hintsDoc = doc
	s.Hints = make([]string, len(sections))
	for i, h := range sections {
		s.Hints[i] = h.body
	}
}

func validateHintLadder(sections []hintSection, fail failFunc) {
	if n := len(sections); n < minHints || n > len(hintLadder) {
		fail(fileHints, "has %d hints, want %d or %d: %s", n, minHints, len(hintLadder), strings.Join(hintLadder, ", "))
	}
	for i, h := range sections {
		if i >= len(hintLadder) {
			break
		}
		if h.heading != hintLadder[i] {
			fail(fileHints, "hint %d heading is %q, want %q", i+1, h.heading, hintLadder[i])
		}
	}
}

type hintSection struct {
	heading string
	body    string
}

func parseHintSections(doc string) ([]hintSection, error) {
	locs := hintPattern.FindAllStringIndex(doc, -1)
	if len(locs) == 0 {
		return nil, errors.New("no \"## Hint N\" section found, at least one is required")
	}
	var hints []hintSection
	var errs []error
	for i, loc := range locs {
		end := len(doc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		heading := strings.TrimSpace(doc[loc[0]:loc[1]])
		body := strings.TrimSpace(doc[loc[1]:end])
		if body == "" {
			errs = append(errs, fmt.Errorf("%s is empty", heading))
			continue
		}
		hints = append(hints, hintSection{heading: heading, body: body})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return hints, nil
}

func mutatingKubectl(text string) string {
	text = strings.NewReplacer("`", "\n", "|", "\n", ";", "\n", "&", "\n", "$(", "\n", "(", " ", ")", " ").Replace(text)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f != "kubectl" {
				continue
			}
			if verb := kubectlVerb(fields[i+1:]); verb != "" {
				return "kubectl " + verb
			}
		}
	}
	return ""
}

func kubectlVerb(args []string) string {
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		a = strings.Trim(a, `"'.,:`)
		switch {
		case a == "rollout":
			for _, b := range args[i+1:] {
				if strings.HasPrefix(b, "-") {
					continue
				}
				if b = strings.Trim(b, `"'.,:`); mutatingRollout[b] {
					return "rollout " + b
				}
				return ""
			}
			return ""
		case mutatingVerbs[a]:
			return a
		case readVerbs[a]:
			return ""
		}
	}
	return ""
}

func loadSetup(dir string, s *Scenario, fail failFunc) {
	setup := filepath.Join(dir, dirSetup)
	entries, err := os.ReadDir(setup)
	if err != nil {
		fail(dirSetup, "%v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rel := filepath.Join(dirSetup, e.Name())
		path := filepath.Join(setup, e.Name())
		switch {
		case e.Name() == fileHook:
			if err := mustBeExecutable(path); err != nil {
				fail(rel, "%v", err)
			}
			s.HookScript = path
		case strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml"):
			b, err := os.ReadFile(path)
			if err != nil {
				fail(rel, "%v", err)
				continue
			}
			if _, err := parseTemplate(rel, string(b)); err != nil {
				fail(rel, "%v", err)
				continue
			}
			s.SetupFiles = append(s.SetupFiles, path)
		default:
			fail(rel, "unexpected file, only *.yaml templates and hook.sh are allowed")
		}
	}
	sort.Strings(s.SetupFiles)
}

func loadChecks(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileChecks))
	if err != nil {
		fail(fileChecks, "%v", err)
		return
	}
	var cf checksFile
	if err := yaml.UnmarshalStrict(b, &cf); err != nil {
		fail(fileChecks, "%v", err)
		return
	}
	if len(cf.Checks) == 0 {
		fail(fileChecks, "checks list is empty, at least one check is required")
		return
	}
	ids := map[string]bool{}
	for i := range cf.Checks {
		c := &cf.Checks[i]
		label := fmt.Sprintf("checks[%d]", i)
		if c.ID != "" {
			label = fmt.Sprintf("check %q", c.ID)
		}
		cfail := func(format string, args ...any) {
			fail(fileChecks, "%s: %s", label, fmt.Sprintf(format, args...))
		}
		validateCheck(c, cfail)
		if c.ID != "" {
			if ids[c.ID] {
				cfail("id is duplicated")
			}
			ids[c.ID] = true
		}
	}
	for _, c := range cf.Checks {
		for _, d := range c.DependsOn {
			if !ids[d] {
				fail(fileChecks, "check %q: depends_on %q does not name an existing check", c.ID, d)
			}
		}
	}
	if cycle := findCycle(cf.Checks); len(cycle) > 0 {
		fail(fileChecks, "depends_on cycle: %s", strings.Join(cycle, " -> "))
	}
	s.Checks = cf.Checks
}

func validateCheck(c *Check, cfail func(format string, args ...any)) {
	if c.ID == "" {
		cfail("id is required")
	} else if !checkIDRe.MatchString(c.ID) {
		cfail("id must match %s", checkIDRe)
	}
	if c.Description == "" {
		cfail("description is required")
	}
	switch n := len(strings.TrimSpace(c.Why)); {
	case n == 0:
		cfail("why is required: one or two sentences on what the check proves and why it matters")
	case n < minWhyLen || n > maxWhyLen:
		cfail("why must be %d to %d characters, got %d", minWhyLen, maxWhyLen, n)
	}

	sources := 0
	if c.Resource != nil {
		sources++
		if c.Resource.APIVersion == "" {
			cfail("resource.apiVersion is required")
		}
		if c.Resource.Kind == "" {
			cfail("resource.kind is required")
		}
		if c.Resource.Name != "" && c.Resource.Selector != "" {
			cfail("resource.name and resource.selector are mutually exclusive")
		}
	}
	if c.Probe != nil {
		sources++
		if c.Probe.Image == "" {
			cfail("probe.image is required")
		}
		if len(c.Probe.Command) == 0 {
			cfail("probe.command is required")
		}
		if c.Probe.Image != "" && !strings.Contains(c.Probe.Image, "@sha256:") {
			cfail("probe.image must be pinned by digest (image:tag@sha256:...)")
		}
		if c.Probe.Timeout != "" {
			d, err := time.ParseDuration(c.Probe.Timeout)
			switch {
			case err != nil:
				cfail("probe.timeout: %v", err)
			case d < time.Second:
				cfail("probe.timeout %q must be at least 1s", c.Probe.Timeout)
			}
		}
	}
	if c.Script != "" {
		sources++
	}
	switch sources {
	case 0:
		cfail("exactly one source is required: resource, probe or script")
	case 1:
	default:
		cfail("exactly one source is allowed, got %d of resource, probe, script", sources)
	}
	if c.Path != "" && c.Resource == nil {
		cfail("path is only valid with a resource source")
	}
	if c.ExitCode != nil && c.Resource != nil {
		cfail("exit_code is only valid with a probe or script source")
	}

	validators := 0
	if c.Exists != nil {
		validators++
		if c.Resource == nil || c.Path != "" {
			cfail("exists applies to a resource without path; use not_empty for a path")
		}
	}
	if c.Equals != nil {
		validators++
	}
	if c.NotEquals != nil {
		validators++
	}
	if c.Matches != "" {
		validators++
		if _, err := regexp.Compile(c.Matches); err != nil {
			cfail("matches: %v", err)
		}
	}
	if c.Contains != "" {
		validators++
	}
	if c.GTE != nil {
		validators++
	}
	if c.LTE != nil {
		validators++
	}
	if c.NotEmpty {
		validators++
	}
	if c.ExitCode != nil {
		validators++
	}
	if validators == 0 {
		cfail("at least one validator is required: exists, equals, not_equals, matches, contains, gte, lte, not_empty, exit_code")
	}

	if c.Weight < 0 {
		cfail("weight must not be negative, got %v", c.Weight)
	} else if c.Weight == 0 {
		c.Weight = 1
	}
	for _, d := range c.DependsOn {
		if d == c.ID && c.ID != "" {
			cfail("depends_on itself")
		}
	}

	fields := templatedFields(c)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := parseTemplate(name, fields[name]); err != nil {
			cfail("%s: %v", name, err)
		}
	}
}

func templatedFields(c *Check) map[string]string {
	f := map[string]string{"description": c.Description}
	add := func(name string, p *string) {
		if p != nil {
			f[name] = *p
		}
	}
	if c.Resource != nil {
		f["resource.namespace"] = c.Resource.Namespace
		f["resource.name"] = c.Resource.Name
		f["resource.selector"] = c.Resource.Selector
	}
	f["path"] = c.Path
	add("equals", c.Equals)
	add("not_equals", c.NotEquals)
	f["matches"] = c.Matches
	f["contains"] = c.Contains
	add("gte", c.GTE)
	add("lte", c.LTE)
	f["script"] = c.Script
	if c.Probe != nil {
		f["probe.image"] = c.Probe.Image
		for i, a := range c.Probe.Command {
			f[fmt.Sprintf("probe.command[%d]", i)] = a
		}
	}
	for k, v := range f {
		if v == "" {
			delete(f, k)
		}
	}
	return f
}

func findCycle(checks []Check) []string {
	deps := make(map[string][]string, len(checks))
	ids := make([]string, 0, len(checks))
	for _, c := range checks {
		deps[c.ID] = c.DependsOn
		ids = append(ids, c.ID)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		color[id] = gray
		stack = append(stack, id)
		for _, d := range deps[id] {
			if _, known := deps[d]; !known {
				continue
			}
			switch color[d] {
			case gray:
				for i, v := range stack {
					if v == d {
						return append(append([]string{}, stack[i:]...), d)
					}
				}
			case white:
				if cyc := visit(d); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}
	for _, id := range ids {
		if color[id] == white {
			if cyc := visit(id); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

func loadSolution(dir string, s *Scenario, fail failFunc) {
	readme := filepath.Join(dirSolution, fileSolution)
	b, err := os.ReadFile(filepath.Join(dir, readme))
	if err != nil {
		fail(readme, "%v", err)
	} else {
		s.Solution = string(b)
		if _, err := parseTemplate(readme, s.Solution); err != nil {
			fail(readme, "%v", err)
		}
		validateSolutionSections(s.Solution, func(format string, args ...any) { fail(readme, format, args...) })
	}
	solve := filepath.Join(dir, dirSolution, fileSolve)
	if err := mustBeExecutable(solve); err != nil {
		fail(filepath.Join(dirSolution, fileSolve), "%v", err)
		return
	}
	s.SolveScript = solve
}

func validateSolutionSections(doc string, fail func(format string, args ...any)) {
	got := level2Headings(doc)
	if strings.Join(got, "\n") == strings.Join(solutionSections, "\n") {
		return
	}
	known := make(map[string]bool, len(solutionSections))
	for _, h := range solutionSections {
		known[h] = true
	}
	present := map[string]bool{}
	for _, h := range got {
		if !known[h] {
			fail("heading %q is not one of the solution sections: %s", h, strings.Join(solutionSections, ", "))
			continue
		}
		if present[h] {
			fail("heading %q appears more than once", h)
		}
		present[h] = true
	}
	var missing []string
	for _, h := range solutionSections {
		if !present[h] {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		fail("missing sections %s", strings.Join(missing, ", "))
	}
	var order []string
	for _, h := range got {
		if known[h] {
			order = append(order, h)
		}
	}
	if len(missing) == 0 && strings.Join(order, "\n") != strings.Join(solutionSections, "\n") {
		fail("sections are in the order %s, want %s", strings.Join(order, ", "), strings.Join(solutionSections, ", "))
	}
}

func level2Headings(doc string) []string {
	var out []string
	fenced := false
	for _, l := range strings.Split(doc, "\n") {
		t := strings.TrimRight(l, " \t\r")
		if strings.HasPrefix(strings.TrimSpace(t), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(t, "## ") {
			out = append(out, t)
		}
	}
	return out
}

func loadApproaches(dir string, s *Scenario, fail failFunc) {
	base := filepath.Join(dir, dirApproaches)
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fail(dirApproaches, "%v", err)
		return
	}
	for _, e := range entries {
		rel := filepath.Join(dirApproaches, e.Name())
		if !e.IsDir() {
			fail(rel, "unexpected file, approaches/ holds one directory per approach")
			continue
		}
		if !conceptIDRe.MatchString(e.Name()) {
			fail(rel, "approach name %q must be lowercase words joined by hyphens", e.Name())
		}
		a := Approach{Name: e.Name()}
		ok := true
		files, err := os.ReadDir(filepath.Join(base, e.Name()))
		if err != nil {
			fail(rel, "%v", err)
			continue
		}
		for _, f := range files {
			if f.Name() != fileSnippet && f.Name() != fileContent {
				fail(filepath.Join(rel, f.Name()), "unexpected file, an approach holds %s and %s", fileSnippet, fileContent)
			}
		}
		snippet, err := os.ReadFile(filepath.Join(base, e.Name(), fileSnippet))
		if err != nil {
			fail(filepath.Join(rel, fileSnippet), "%v", err)
			ok = false
		} else {
			a.Snippet = strings.TrimRight(string(snippet), "\n\r \t")
			if _, err := parseTemplate(filepath.Join(rel, fileSnippet), a.Snippet); err != nil {
				fail(filepath.Join(rel, fileSnippet), "%v", err)
				ok = false
			}
			switch n := len(strings.Split(a.Snippet, "\n")); {
			case strings.TrimSpace(a.Snippet) == "":
				fail(filepath.Join(rel, fileSnippet), "is empty, want 1 to %d lines", maxSnippetLines)
				ok = false
			case n > maxSnippetLines:
				fail(filepath.Join(rel, fileSnippet), "is %d lines, at most %d allowed", n, maxSnippetLines)
				ok = false
			}
		}
		body, err := os.ReadFile(filepath.Join(base, e.Name(), fileContent))
		if err != nil {
			fail(filepath.Join(rel, fileContent), "%v", err)
			ok = false
		} else if a.Body = strings.TrimSpace(string(body)); a.Body == "" {
			fail(filepath.Join(rel, fileContent), "is empty, explain when to choose this approach and its trade-offs")
			ok = false
		} else if _, err := parseTemplate(filepath.Join(rel, fileContent), a.Body); err != nil {
			fail(filepath.Join(rel, fileContent), "%v", err)
			ok = false
		}
		if ok {
			s.Approaches = append(s.Approaches, a)
		}
	}
	sort.Slice(s.Approaches, func(i, j int) bool { return s.Approaches[i].Name < s.Approaches[j].Name })
}

func mustBeExecutable(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errors.New("is a directory")
	}
	if st.Mode()&0o111 == 0 {
		return errors.New("is not executable (chmod +x)")
	}
	return nil
}

func parseTemplate(name, text string) (*template.Template, error) {
	return template.New(name).Option("missingkey=error").Parse(text)
}

func hasHeading(doc, heading string) bool {
	_, ok := section(doc, heading)
	return ok
}

func section(doc, heading string) (body string, ok bool) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t\r") == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
			end = i
			break
		}
	}
	return strings.Trim(strings.Join(lines[start:end], "\n"), "\n\r \t"), true
}
