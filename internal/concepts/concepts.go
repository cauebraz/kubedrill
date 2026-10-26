// Package concepts loads the fundamentals library under docs/concepts: one
// markdown file per concept, named <id>.md, with a fixed section layout so
// every concept reads the same way. Scenarios and curriculum modules refer to
// concepts by id; the cli lint cross-checks those ids against this library.
package concepts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrNotFound is returned by Get for an unknown concept id.
var ErrNotFound = errors.New("concept not found")

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Sections every concept file carries, as H2 headings, in this order after
// the title and the intro paragraph.
var requiredSections = []string{
	"## The mechanism",
	"## What you see on call",
	"## Common failures",
	"## Go deeper",
}

const (
	dirDocs     = "docs"
	dirConcepts = "concepts"
	fileReadme  = "README.md"
)

// Concept is one parsed file of docs/concepts. Body is the whole markdown
// text; Summary is the first paragraph after the title.
type Concept struct {
	ID      string
	Title   string
	Summary string
	Body    string
	Path    string
}

// Library is the validated set of concepts of one repository.
type Library struct {
	byID map[string]Concept
}

// Load reads every *.md file under root/docs/concepts except README.md,
// validates each one and returns the library. Every problem is reported at
// once, each prefixed with docs/concepts/<file>. A missing directory is an
// error naming its path.
func Load(root string) (*Library, error) {
	dir := filepath.Join(root, dirDocs, dirConcepts)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("load concepts: %w", err)
	}
	lib := &Library{byID: map[string]Concept{}}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == fileReadme || !strings.HasSuffix(name, ".md") {
			continue
		}
		rel := filepath.Join(dirDocs, dirConcepts, name)
		fail := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: %s", rel, fmt.Sprintf(format, args...)))
		}
		id := strings.TrimSuffix(name, ".md")
		if !idPattern.MatchString(id) {
			fail("file name %q must be <id>.md with id matching %s", name, idPattern)
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			fail("%v", err)
			continue
		}
		c, perrs := parse(string(b))
		for _, pe := range perrs {
			fail("%v", pe)
		}
		if len(perrs) > 0 || !idPattern.MatchString(id) {
			continue
		}
		c.ID = id
		c.Path = filepath.Join(dir, name)
		lib.byID[id] = c
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("load concepts: %w", errors.Join(errs...))
	}
	return lib, nil
}

func parse(body string) (Concept, []error) {
	var errs []error
	c := Concept{Body: body}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	first := -1
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = i
			break
		}
	}
	switch {
	case first < 0:
		return c, []error{errors.New("file is empty")}
	case !strings.HasPrefix(lines[first], "# ") || strings.TrimSpace(lines[first][2:]) == "":
		errs = append(errs, fmt.Errorf("first non-empty line must be the title \"# Title\", got %q", lines[first]))
	default:
		c.Title = strings.TrimSpace(lines[first][2:])
	}

	h1 := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "# ") {
			h1++
		}
	}
	if h1 > 1 {
		errs = append(errs, fmt.Errorf("exactly one H1 is allowed, got %d", h1))
	}

	if first >= 0 && c.Title != "" {
		var intro []string
		i := first + 1
		for ; i < len(lines) && strings.TrimSpace(lines[i]) == ""; i++ {
		}
		for ; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
			if strings.HasPrefix(lines[i], "#") {
				break
			}
			intro = append(intro, strings.TrimSpace(lines[i]))
		}
		if len(intro) == 0 {
			errs = append(errs, errors.New("an intro paragraph is required between the title and the first section"))
		}
		c.Summary = strings.Join(intro, " ")
	}

	var found []string
	for _, l := range lines {
		t := strings.TrimRight(l, " \t")
		for _, want := range requiredSections {
			if t == want {
				found = append(found, want)
			}
		}
	}
	for _, want := range requiredSections {
		n := 0
		for _, f := range found {
			if f == want {
				n++
			}
		}
		switch {
		case n == 0:
			errs = append(errs, fmt.Errorf("missing section %q", want))
		case n > 1:
			errs = append(errs, fmt.Errorf("section %q appears %d times, want 1", want, n))
		}
	}
	if len(found) == len(requiredSections) {
		for i, want := range requiredSections {
			if found[i] != want {
				errs = append(errs, fmt.Errorf("sections out of order: got %s, want %s", strings.Join(found, ", "), strings.Join(requiredSections, ", ")))
				break
			}
		}
	}
	return c, errs
}

// IDs returns every concept id, sorted.
func (l *Library) IDs() []string {
	out := make([]string, 0, len(l.byID))
	for id := range l.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// All returns every concept sorted by ID.
func (l *Library) All() []Concept {
	out := make([]Concept, 0, len(l.byID))
	for _, id := range l.IDs() {
		out = append(out, l.byID[id])
	}
	return out
}

// Get returns the concept with the given id or ErrNotFound.
func (l *Library) Get(id string) (Concept, error) {
	c, ok := l.byID[id]
	if !ok {
		return Concept{}, fmt.Errorf("concept %q: %w", id, ErrNotFound)
	}
	return c, nil
}
