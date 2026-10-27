package scenario

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type metaFile struct {
	Scenario Meta      `toml:"scenario"`
	Variant  []Variant `toml:"variant"`
}

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
	for i, image := range m.Images {
		if !pinnedImageRe.MatchString(image) {
			fail(fileMeta, "scenario.images[%d] %q must be name:tag@sha256:<64 hex>", i, image)
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
