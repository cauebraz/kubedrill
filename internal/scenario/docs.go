package scenario

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var solutionSections = []string{
	"## Symptom",
	"## Candidate causes",
	"## Observation that splits them",
	"## Change",
	"## Verify",
	"## In the real world",
	"## Go deeper",
}

var storySections = []string{
	"## What happened",
	"## The pieces",
	"## Following the trail",
	"## The fix and why it works",
	"## Check your work",
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
		validateSections(s.Solution, solutionSections, "solution", func(format string, args ...any) { fail(readme, format, args...) })
	}
	solve := filepath.Join(dir, dirSolution, fileSolve)
	if err := mustBeExecutable(solve); err != nil {
		fail(filepath.Join(dirSolution, fileSolve), "%v", err)
		return
	}
	s.SolveScript = solve
}

func loadStory(dir string, s *Scenario, fail failFunc) {
	b, err := os.ReadFile(filepath.Join(dir, fileStory))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fail(fileStory, "%v", err)
		return
	}
	s.Story = string(b)
	if _, err := parseTemplate(fileStory, s.Story); err != nil {
		fail(fileStory, "%v", err)
	}
	validateSections(s.Story, storySections, "story", func(format string, args ...any) { fail(fileStory, format, args...) })
}

func validateSections(doc string, want []string, kind string, fail func(format string, args ...any)) {
	got := level2Headings(doc)
	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	known := make(map[string]bool, len(want))
	for _, h := range want {
		known[h] = true
	}
	present := map[string]bool{}
	for _, h := range got {
		if !known[h] {
			fail("heading %q is not one of the %s sections: %s", h, kind, strings.Join(want, ", "))
			continue
		}
		if present[h] {
			fail("heading %q appears more than once", h)
		}
		present[h] = true
	}
	var missing []string
	for _, h := range want {
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
	if len(missing) == 0 && strings.Join(order, "\n") != strings.Join(want, "\n") {
		fail("sections are in the order %s, want %s", strings.Join(order, ", "), strings.Join(want, ", "))
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
