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

// Sentinel errors returned by Catalog.Find.
var (
	ErrNotFound  = errors.New("scenario not found")
	ErrAmbiguous = errors.New("scenario reference is ambiguous")
)

// NotFoundError is the error Catalog.Find returns when no scenario matches
// Ref. errors.Is(err, ErrNotFound) reports true for it.
type NotFoundError struct {
	Ref string
}

// Error returns the text find scenario "<ref>": scenario not found.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("find scenario %q: %v", e.Ref, ErrNotFound)
}

// Is reports whether target is ErrNotFound.
func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

const dirScenarios = "scenarios"

var numericRef = regexp.MustCompile(`^\d{3}$`)

// Catalog is the set of scenarios found under <root>/scenarios/<level>/<id>/.
type Catalog struct {
	byID map[string]*Scenario
	ids  []string
}

// NewCatalog scans root/scenarios/<level>/<id>/ and loads every scenario
// directory. Directories whose name starts with "_" are skipped. A scenario
// that fails to load makes the catalog fail with every problem reported.
func NewCatalog(root string) (*Catalog, error) {
	dirs, err := scenarioDirs(root)
	if err != nil {
		return nil, fmt.Errorf("new catalog %s: %w", root, err)
	}
	c := &Catalog{byID: map[string]*Scenario{}}
	var errs []error
	for _, dir := range dirs {
		s, err := Load(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if other, dup := c.byID[s.Meta.ID]; dup {
			errs = append(errs, fmt.Errorf("scenario id %q is defined in both %s and %s", s.Meta.ID, other.Dir, s.Dir))
			continue
		}
		c.byID[s.Meta.ID] = s
		c.ids = append(c.ids, s.Meta.ID)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("new catalog %s: %w", root, errors.Join(errs...))
	}
	sort.Strings(c.ids)
	return c, nil
}

// All returns every scenario sorted by Meta.ID.
func (c *Catalog) All() []*Scenario {
	out := make([]*Scenario, 0, len(c.ids))
	for _, id := range c.ids {
		out = append(out, c.byID[id])
	}
	return out
}

// Find resolves ref to one scenario: the exact Meta.ID, a three-digit numeric
// prefix such as "001", or a prefix of the id that matches exactly one
// scenario. It returns a *NotFoundError (errors.Is ErrNotFound) when nothing
// matches and an error wrapping ErrAmbiguous when several ids match.
func (c *Catalog) Find(ref string) (*Scenario, error) {
	if s, ok := c.byID[ref]; ok {
		return s, nil
	}
	prefix := ref
	if numericRef.MatchString(ref) {
		prefix = ref + "-"
	}
	var matches []string
	if prefix != "" {
		for _, id := range c.ids {
			if strings.HasPrefix(id, prefix) {
				matches = append(matches, id)
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{Ref: ref}
	case 1:
		return c.byID[matches[0]], nil
	default:
		return nil, fmt.Errorf("find scenario %q: %w: matches %s", ref, ErrAmbiguous, strings.Join(matches, ", "))
	}
}

func scenarioDirs(root string) ([]string, error) {
	base := filepath.Join(root, dirScenarios)
	levels, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, lvl := range levels {
		if !lvl.IsDir() || strings.HasPrefix(lvl.Name(), "_") || strings.HasPrefix(lvl.Name(), ".") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(base, lvl.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dirs = append(dirs, filepath.Join(base, lvl.Name(), e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}
