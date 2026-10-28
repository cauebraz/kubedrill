package scenario

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	dirShortcuts      = "shortcuts"
	shortcutWhy       = "# why:"
	shortcutVariants  = "# variants:"
	minShortcutWhyLen = 10
	maxShortcutWhyLen = 200
)

var (
	shortcutNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*\.sh$`)
	bashShebangRe  = regexp.MustCompile(`^#!\s*(/usr/bin/env\s+bash|/bin/bash|/usr/bin/bash)\s*$`)
)

// Shortcut is one shortcuts/<name>.sh: a wrong or symptom-only fix a
// learner would plausibly try, which smoke runs on a fresh setup to prove
// that it leaves at least one check failing. The script is bash and runs
// like solution/solve.sh, with TASK_NS, SEED, VARIANT, VAR_* and
// KUBECONFIG. Its header, the comment lines right after the shebang,
// carries "# why: <one line>" (Why: what the shortcut gets wrong, shown
// when it passes every check) and optionally "# variants: a,b" (Variants:
// the variants it applies to; empty means every variant).
type Shortcut struct {
	Name     string
	Script   string
	Why      string
	Variants []string
}

// AppliesTo reports whether the shortcut runs on the variant named variant.
func (sh Shortcut) AppliesTo(variant string) bool {
	if len(sh.Variants) == 0 {
		return true
	}
	for _, v := range sh.Variants {
		if v == variant {
			return true
		}
	}
	return false
}

func loadShortcuts(dir string, s *Scenario, fail failFunc) {
	entries, err := os.ReadDir(filepath.Join(dir, dirShortcuts))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fail(dirShortcuts, "%v", err)
		return
	}
	names := map[string]bool{}
	for _, v := range s.Variants {
		names[v.Name] = true
	}
	if len(s.Variants) == 0 {
		names["default"] = true
	}
	for _, e := range entries {
		rel := filepath.Join(dirShortcuts, e.Name())
		if !e.Type().IsRegular() || !shortcutNameRe.MatchString(e.Name()) {
			fail(rel, "unexpected entry, only regular files named <kebab-name>.sh are allowed")
			continue
		}
		path := filepath.Join(dir, dirShortcuts, e.Name())
		if err := mustBeExecutable(path); err != nil {
			fail(rel, "%v", err)
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			fail(rel, "%v", err)
			continue
		}
		sh := Shortcut{Name: strings.TrimSuffix(e.Name(), ".sh"), Script: path}
		for _, p := range parseShortcutHeader(string(b), &sh, names) {
			fail(rel, "%s", p)
		}
		s.Shortcuts = append(s.Shortcuts, sh)
	}
	sort.Slice(s.Shortcuts, func(i, j int) bool { return s.Shortcuts[i].Name < s.Shortcuts[j].Name })
}

// parseShortcutHeader fills sh.Why and sh.Variants from the comment lines
// that follow the shebang and returns every problem found; variants holds
// the variant names the scenario declares.
func parseShortcutHeader(text string, sh *Shortcut, variants map[string]bool) []string {
	var problems []string
	sc := bufio.NewScanner(strings.NewReader(text))
	if !sc.Scan() || !bashShebangRe.MatchString(strings.TrimRight(sc.Text(), "\r")) {
		problems = append(problems, "first line must be a bash shebang (#!/usr/bin/env bash)")
	}
	whys, variantLines := 0, 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "#") {
			break
		}
		switch {
		case strings.HasPrefix(line, shortcutWhy):
			whys++
			sh.Why = strings.TrimSpace(strings.TrimPrefix(line, shortcutWhy))
		case strings.HasPrefix(line, shortcutVariants):
			variantLines++
			sh.Variants = nil
			seen := map[string]bool{}
			for _, v := range strings.Split(strings.TrimPrefix(line, shortcutVariants), ",") {
				v = strings.TrimSpace(v)
				switch {
				case v == "":
					problems = append(problems, fmt.Sprintf("%q has an empty variant name", line))
				case seen[v]:
					problems = append(problems, fmt.Sprintf("%q names variant %q twice", line, v))
				case !variants[v]:
					problems = append(problems, fmt.Sprintf("%q names variant %q, which scenario.toml does not declare", line, v))
				default:
					sh.Variants = append(sh.Variants, v)
				}
				seen[v] = true
			}
		}
	}
	switch {
	case whys == 0:
		problems = append(problems, fmt.Sprintf("header has no %q line; state in one line what the shortcut gets wrong", shortcutWhy))
	case whys > 1:
		problems = append(problems, fmt.Sprintf("header has %d %q lines, want one", whys, shortcutWhy))
	case len(sh.Why) < minShortcutWhyLen || len(sh.Why) > maxShortcutWhyLen:
		problems = append(problems, fmt.Sprintf("%q is %d characters, want %d to %d", shortcutWhy, len(sh.Why), minShortcutWhyLen, maxShortcutWhyLen))
	}
	if variantLines > 1 {
		problems = append(problems, fmt.Sprintf("header has %d %q lines, want at most one", variantLines, shortcutVariants))
	}
	return problems
}
