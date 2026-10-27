package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	fileStory       = "story.md"

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
	loadStory(abs, s, fail)
	loadSimulation(abs, s, fail)

	if len(errs) > 0 {
		return nil, fmt.Errorf("load scenario %s: %w", dir, errors.Join(errs...))
	}
	return s, nil
}

type failFunc func(file, format string, args ...any)

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

func hasHeading(doc, heading string) bool {
	_, ok := section(doc, heading)
	return ok
}

func section(doc, heading string) (body string, ok bool) {
	lines := strings.Split(doc, "\n")
	fenced := fenceMask(lines)
	start := -1
	for i, l := range lines {
		if !fenced[i] && strings.TrimRight(l, " \t\r") == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if !fenced[i] && (strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ")) {
			end = i
			break
		}
	}
	return strings.Trim(strings.Join(lines[start:end], "\n"), "\n\r \t"), true
}

// fenceMask reports, per line, whether the line is a code fence marker
// (a line starting with three backticks or tildes) or inside a fenced block,
// where a "#" line is a shell comment, not a heading.
func fenceMask(lines []string) []bool {
	out := make([]bool, len(lines))
	fenced := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			out[i] = true
			continue
		}
		out[i] = fenced
	}
	return out
}

// outsideFences keeps the matches of locs (byte offsets into doc) that start
// on a line outside a fenced code block.
func outsideFences(doc string, locs [][]int) [][]int {
	lines := strings.Split(doc, "\n")
	fenced := fenceMask(lines)
	var out [][]int
	for _, loc := range locs {
		if !fenced[strings.Count(doc[:loc[0]], "\n")] {
			out = append(out, loc)
		}
	}
	return out
}
