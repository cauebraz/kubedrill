package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"
)

const (
	maxRenderBytes = 4 << 20
	maxRangeCount  = 100000
	maxRenderSteps = 1000000
	stepFunc       = "kubedrillStep"
)

var (
	errRenderTooLarge = fmt.Errorf("rendered output exceeds %d bytes", maxRenderBytes)
	errRenderTooLong  = fmt.Errorf("template runs more than %d steps (range iterations plus template calls)", maxRenderSteps)
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

var reservedNamespaces = map[string]bool{
	"default":            true,
	"kube-system":        true,
	"kube-public":        true,
	"kube-node-lease":    true,
	"local-path-storage": true,
	"kubedrill-debug":    true,
}

type templateData struct {
	Seed      int64
	Namespace string
	Variant   string
	Vars      map[string]string
}

// RenderOption changes how Render specialises a scenario.
type RenderOption func(*renderConfig)

type renderConfig struct {
	namespace string
}

// WithNamespace makes Render use ns as the namespace instead of the rendered
// scenario.namespace, for example the per-episode namespace. The value is
// validated like a rendered scenario.namespace (DNS-1123 label, not
// reserved). An empty ns leaves the scenario namespace in place.
func WithNamespace(ns string) RenderOption {
	return func(c *renderConfig) { c.namespace = ns }
}

// VariantFor returns the variant Render picks for seed: the one with index
// (seed-1) mod len(Variants), or a variant named "default" without vars when
// the scenario declares none. Seed must be at least 1.
func (s *Scenario) VariantFor(seed int64) Variant {
	if len(s.Variants) == 0 {
		return Variant{Name: "default"}
	}
	return s.Variants[(seed-1)%int64(len(s.Variants))]
}

// Render specialises the scenario for seed: it picks the variant with index
// (seed-1) mod len(Variants), expands the setup templates, the string fields
// of every check, instruction.md, hints.md, solution/README.md and every
// approach's snippet.txt and content.md, and returns the result. Seed must be at least 1. Every template error is
// returned at once, each one naming its file.
func (s *Scenario) Render(seed int64, opts ...RenderOption) (*Rendered, error) {
	if seed < 1 {
		return nil, fmt.Errorf("render scenario %s: seed must be >= 1, got %d", s.Meta.ID, seed)
	}
	var cfg renderConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	variant := s.VariantFor(seed)
	vars := make(map[string]string, len(variant.Vars))
	for k, v := range variant.Vars {
		vars[k] = v
	}
	data := templateData{Seed: seed, Variant: variant.Name, Vars: vars}

	var errs []error
	var ns string
	if cfg.namespace != "" {
		ns = cfg.namespace
		if err := validateNamespace(ns); err != nil {
			errs = append(errs, fmt.Errorf("namespace override: %w", err))
		}
	} else {
		var err error
		ns, err = execute("scenario.toml: scenario.namespace", s.Meta.Namespace, data)
		if err != nil {
			errs = append(errs, err)
		} else if err := validateNamespace(ns); err != nil {
			errs = append(errs, fmt.Errorf("%s: scenario.namespace: %w", fileMeta, err))
		}
	}
	data.Namespace = ns

	manifests := make([]string, 0, len(s.SetupFiles))
	for _, path := range s.SetupFiles {
		rel := relTo(s.Dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		out, err := execute(rel, string(b), data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		manifests = append(manifests, out)
	}

	checks := make([]Check, 0, len(s.Checks))
	for _, c := range s.Checks {
		rc, err := renderCheck(c, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		checks = append(checks, rc)
	}

	instruction, err := execute(fileInstruction, s.Instruction, data)
	if err != nil {
		errs = append(errs, err)
	}
	hints, err := s.renderHints(data)
	if err != nil {
		errs = append(errs, err)
	}
	solution, err := execute(filepath.Join(dirSolution, fileSolution), s.Solution, data)
	if err != nil {
		errs = append(errs, err)
	}

	approaches := make([]Approach, 0, len(s.Approaches))
	for _, a := range s.Approaches {
		dir := filepath.Join(dirApproaches, a.Name)
		snippet, err := execute(filepath.Join(dir, fileSnippet), a.Snippet, data)
		if err != nil {
			errs = append(errs, err)
		}
		body, err := execute(filepath.Join(dir, fileContent), a.Body, data)
		if err != nil {
			errs = append(errs, err)
		}
		approaches = append(approaches, Approach{Name: a.Name, Snippet: snippet, Body: body})
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("render scenario %s seed %d: %w", s.Meta.ID, seed, errors.Join(errs...))
	}
	return &Rendered{
		Scenario:    s,
		Seed:        seed,
		Variant:     variant,
		Vars:        vars,
		Namespace:   ns,
		Manifests:   manifests,
		Checks:      checks,
		Instruction: instruction,
		Hints:       hints,
		Solution:    solution,
		Approaches:  approaches,
	}, nil
}

func (s *Scenario) renderHints(data templateData) ([]string, error) {
	if s.hintsDoc != "" {
		doc, err := execute(fileHints, s.hintsDoc, data)
		if err != nil {
			return nil, err
		}
		sections, err := parseHintSections(doc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fileHints, err)
		}
		out := make([]string, len(sections))
		for i, h := range sections {
			out[i] = h.body
		}
		return out, nil
	}
	out := make([]string, len(s.Hints))
	var errs []error
	for i, h := range s.Hints {
		r, err := execute(fileHints, h, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[i] = r
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func renderCheck(c Check, data templateData) (Check, error) {
	var errs []error
	field := func(name, value string) string {
		if value == "" {
			return value
		}
		out, err := execute(fmt.Sprintf("%s: check %q: %s", fileChecks, c.ID, name), value, data)
		if err != nil {
			errs = append(errs, err)
		}
		return out
	}
	ptr := func(name string, p *string) *string {
		if p == nil {
			return nil
		}
		v := field(name, *p)
		return &v
	}

	out := c
	out.Description = field("description", c.Description)
	out.Path = field("path", c.Path)
	out.Equals = ptr("equals", c.Equals)
	out.NotEquals = ptr("not_equals", c.NotEquals)
	out.Matches = field("matches", c.Matches)
	out.Contains = field("contains", c.Contains)
	out.GTE = ptr("gte", c.GTE)
	out.LTE = ptr("lte", c.LTE)
	out.Script = field("script", c.Script)
	out.DependsOn = append([]string(nil), c.DependsOn...)
	if c.Exists != nil {
		v := *c.Exists
		out.Exists = &v
	}
	if c.ExitCode != nil {
		v := *c.ExitCode
		out.ExitCode = &v
	}
	if c.Resource != nil {
		r := *c.Resource
		r.Namespace = field("resource.namespace", c.Resource.Namespace)
		r.Name = field("resource.name", c.Resource.Name)
		r.Selector = field("resource.selector", c.Resource.Selector)
		out.Resource = &r
	}
	if c.Probe != nil {
		p := *c.Probe
		p.Image = field("probe.image", c.Probe.Image)
		p.Command = make([]string, len(c.Probe.Command))
		for i, a := range c.Probe.Command {
			p.Command[i] = field(fmt.Sprintf("probe.command[%d]", i), a)
		}
		out.Probe = &p
	}
	if len(errs) > 0 {
		return Check{}, errors.Join(errs...)
	}
	return out, nil
}

func validateNamespace(ns string) error {
	if !dnsLabel.MatchString(ns) {
		return fmt.Errorf("namespace %q is not a DNS-1123 label (lowercase alphanumerics and hyphens, at most 63 characters)", ns)
	}
	if reservedNamespaces[ns] || strings.HasPrefix(ns, "kube-") {
		return fmt.Errorf("namespace %q is reserved; the reset deletes the scenario namespace, pick one owned by the scenario", ns)
	}
	return nil
}

func execute(name, text string, data templateData) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := parseTemplate(name, text)
	if err != nil {
		return "", err
	}
	t.Funcs(template.FuncMap{stepFunc: newStepBudget(maxRenderSteps)})
	w := &cappedBuilder{limit: maxRenderBytes}
	if err := t.Execute(w, data); err != nil {
		if errors.Is(err, errRenderTooLarge) {
			return "", fmt.Errorf("template: %s: %w", name, err)
		}
		if errors.Is(err, errRenderTooLong) {
			return "", fmt.Errorf("template: %s: %w", name, errRenderTooLong)
		}
		return "", err
	}
	return w.sb.String(), nil
}

type cappedBuilder struct {
	sb    strings.Builder
	limit int
}

func (w *cappedBuilder) Write(p []byte) (int, error) {
	if w.sb.Len()+len(p) > w.limit {
		return 0, errRenderTooLarge
	}
	return w.sb.Write(p)
}

func parseTemplate(name, text string) (*template.Template, error) {
	t, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{stepFunc: newStepBudget(0)}).Parse(text)
	if err != nil {
		return nil, err
	}
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}
		if err := checkRangeCounts(tt.Tree.Root); err != nil {
			return nil, fmt.Errorf("template: %s: %w", name, err)
		}
		guardSteps(tt.Tree.Root)
	}
	return t, nil
}

// newStepBudget returns the function guardSteps wires into every range
// pipeline and template call. It passes its last argument through and
// charges the budget one step per call plus the length of a ranged value.
func newStepBudget(budget int) func(args ...any) (any, error) {
	left := budget
	return func(args ...any) (any, error) {
		var v any
		if len(args) > 0 {
			v = args[len(args)-1]
		}
		cost, err := rangeCost(v)
		if err != nil {
			return nil, err
		}
		if cost >= left {
			left = 0
			return nil, errRenderTooLong
		}
		left -= cost + 1
		return v, nil
	}
}

func rangeCost(v any) (int, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0, nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n := rv.Int(); n > 0 {
			return int(min(n, int64(maxRenderSteps))), nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int(min(rv.Uint(), uint64(maxRenderSteps))), nil
	case reflect.Array, reflect.Slice, reflect.Map, reflect.String:
		return rv.Len(), nil
	case reflect.Chan, reflect.Func:
		return 0, fmt.Errorf("range over a %s is not allowed in a scenario template", rv.Kind())
	}
	return 0, nil
}

func guardSteps(n parse.Node) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			guardSteps(c)
		}
	case *parse.RangeNode:
		if n.Pipe != nil {
			n.Pipe.Cmds = append(n.Pipe.Cmds, stepCommand(n.Pipe.Pos))
		}
		guardBranch(&n.BranchNode)
	case *parse.IfNode:
		guardBranch(&n.BranchNode)
	case *parse.WithNode:
		guardBranch(&n.BranchNode)
	case *parse.TemplateNode:
		if n.Pipe == nil {
			n.Pipe = &parse.PipeNode{NodeType: parse.NodePipe, Pos: n.Pos, Line: n.Line}
		}
		n.Pipe.Cmds = append(n.Pipe.Cmds, stepCommand(n.Pos))
	}
}

func guardBranch(b *parse.BranchNode) {
	guardSteps(b.List)
	if b.ElseList != nil {
		guardSteps(b.ElseList)
	}
}

func stepCommand(pos parse.Pos) *parse.CommandNode {
	return &parse.CommandNode{NodeType: parse.NodeCommand, Pos: pos, Args: []parse.Node{parse.NewIdentifier(stepFunc).SetPos(pos)}}
}

func checkRangeCounts(n parse.Node) error {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := checkRangeCounts(c); err != nil {
				return err
			}
		}
	case *parse.RangeNode:
		if n.Pipe != nil && len(n.Pipe.Cmds) == 1 && len(n.Pipe.Cmds[0].Args) == 1 {
			if num, ok := n.Pipe.Cmds[0].Args[0].(*parse.NumberNode); ok && ((num.IsInt && num.Int64 > maxRangeCount) || (!num.IsInt && num.IsUint)) {
				return fmt.Errorf("range over %s exceeds %d iterations", num.Text, maxRangeCount)
			}
		}
		return checkBranch(&n.BranchNode)
	case *parse.IfNode:
		return checkBranch(&n.BranchNode)
	case *parse.WithNode:
		return checkBranch(&n.BranchNode)
	}
	return nil
}

func checkBranch(b *parse.BranchNode) error {
	if err := checkRangeCounts(b.List); err != nil {
		return err
	}
	if b.ElseList != nil {
		return checkRangeCounts(b.ElseList)
	}
	return nil
}

func relTo(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return path
	}
	return rel
}
