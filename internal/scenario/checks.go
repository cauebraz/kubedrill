package scenario

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/yaml"
)

type checksFile struct {
	Checks []Check `json:"checks"`
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
		validateCheck(*c, cfail)
		normalizeCheck(c)
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

type checkFail func(format string, args ...any)

func validateCheck(c Check, cfail checkFail) {
	validateCheckText(c, cfail)
	validateCheckSource(c, cfail)
	validateCheckValidators(c, cfail)
	if c.Weight < 0 {
		cfail("weight must not be negative, got %v", c.Weight)
	}
	for _, d := range c.DependsOn {
		if d == c.ID && c.ID != "" {
			cfail("depends_on itself")
		}
	}
	validateCheckTemplates(c, cfail)
}

func normalizeCheck(c *Check) {
	if c.Weight == 0 {
		c.Weight = 1
	}
}

func validateCheckText(c Check, cfail checkFail) {
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
}

func validateCheckSource(c Check, cfail checkFail) {
	sources := 0
	if c.Resource != nil {
		sources++
		validateResource(*c.Resource, cfail)
	}
	if c.Probe != nil {
		sources++
		validateProbe(*c.Probe, cfail)
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
}

func validateResource(r ResourceRef, cfail checkFail) {
	if r.APIVersion == "" {
		cfail("resource.apiVersion is required")
	}
	if r.Kind == "" {
		cfail("resource.kind is required")
	}
	if r.Name != "" && r.Selector != "" {
		cfail("resource.name and resource.selector are mutually exclusive")
	}
}

func validateProbe(p Probe, cfail checkFail) {
	if p.Image == "" {
		cfail("probe.image is required")
	}
	if len(p.Command) == 0 {
		cfail("probe.command is required")
	}
	if p.Image != "" && !strings.Contains(p.Image, "@sha256:") {
		cfail("probe.image must be pinned by digest (image:tag@sha256:...)")
	}
	if p.Timeout == "" {
		return
	}
	d, err := time.ParseDuration(p.Timeout)
	switch {
	case err != nil:
		cfail("probe.timeout: %v", err)
	case d < time.Second:
		cfail("probe.timeout %q must be at least 1s", p.Timeout)
	}
}

func validateCheckValidators(c Check, cfail checkFail) {
	if c.Exists != nil && (c.Resource == nil || c.Path != "") {
		cfail("exists applies to a resource without path; use not_empty for a path")
	}
	if c.Matches != "" {
		if _, err := regexp.Compile(c.Matches); err != nil {
			cfail("matches: %v", err)
		}
	}
	set := []bool{
		c.Exists != nil, c.Equals != nil, c.NotEquals != nil, c.Matches != "", c.Contains != "",
		c.GTE != nil, c.LTE != nil, c.NotEmpty, c.ExitCode != nil,
	}
	for _, ok := range set {
		if ok {
			return
		}
	}
	cfail("at least one validator is required: exists, equals, not_equals, matches, contains, gte, lte, not_empty, exit_code")
}

func validateCheckTemplates(c Check, cfail checkFail) {
	fields := templatedFields(&c)
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

func lintCheckValues(checks []Check) []error {
	var errs []error
	for _, c := range checks {
		cerr := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: check %q: %s", fileChecks, c.ID, fmt.Sprintf(format, args...)))
		}
		if c.Path != "" {
			if err := jsonpath.New(c.ID).Parse(c.Path); err != nil {
				cerr("path %q is not a valid JSONPath: %v", c.Path, err)
			}
		}
		for _, b := range []struct {
			name  string
			value *string
		}{{"gte", c.GTE}, {"lte", c.LTE}} {
			if b.value == nil {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(*b.value), 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				cerr("%s bound %q is not a finite number", b.name, *b.value)
			}
		}
	}
	return errs
}
