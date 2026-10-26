// Package curriculum loads curriculum.yaml: the L0-L6 ladder, its modules,
// the scenarios planned or present in each module, and the mapping from
// modules to CKA, CKAD and CKS exam domains.
package curriculum

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// ErrUnknownExam is returned by ExamCoverage for an exam id not in the file.
var ErrUnknownExam = errors.New("curriculum: unknown exam")

var (
	levelIDPattern   = regexp.MustCompile(`^L[0-6]$`)
	conceptIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// Level is one rung of the ladder, for example L2 "Cluster operator".
type Level struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Before   string   `json:"before"`
	After    string   `json:"after"`
	Hours    int      `json:"hours"`
	Capstone string   `json:"capstone"`
	Modules  []Module `json:"modules"`
}

// Module groups scenarios of one topic inside a level. Concepts lists the
// ids of docs/concepts/<id>.md the module teaches; Load checks only the id
// shape, the cli lint checks that each file exists.
type Module struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Summary   string            `json:"summary"`
	Exam      []string          `json:"exam"`
	Concepts  []string          `json:"concepts,omitempty"`
	Scenarios []PlannedScenario `json:"scenarios"`
}

// PlannedScenario is a scenario idea or an existing scenario directory. ID
// is empty until the scenario exists under scenarios/.
type PlannedScenario struct {
	ID          string `json:"id,omitempty"`
	Ticket      string `json:"ticket"`
	Type        string `json:"type"`
	Feasibility string `json:"feasibility"`
	Addons      string `json:"addons,omitempty"`
}

// Exam is one certification with its weighted domains.
type Exam struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Domains []Domain `json:"domains"`
}

// Domain is one exam domain with its weight in percent.
type Domain struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Weight int    `json:"weight"`
}

// Curriculum is the parsed curriculum.yaml.
type Curriculum struct {
	Levels []Level `json:"levels"`
	Exams  []Exam  `json:"exams"`
}

// Coverage is the learner's progress against one exam domain.
type Coverage struct {
	Exam      string
	Domain    Domain
	Scenarios []string
	Done      int
}

// DefaultPath returns the curriculum file at the repository root.
func DefaultPath(root string) string {
	return filepath.Join(root, "curriculum.yaml")
}

// Load parses path and validates it: unique exam, domain, level and module
// ids, level ids of the right shape, module ids prefixed by their level,
// exam refs naming an existing exam and domain, concept ids of the right
// shape, and each scenario listed in at most one module. All errors are
// reported at once.
func Load(path string) (*Curriculum, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load curriculum %s: %w", path, err)
	}
	var c Curriculum
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("load curriculum %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("load curriculum %s: %w", path, err)
	}
	return &c, nil
}

func (c *Curriculum) validate() error {
	var errs []error
	exams := map[string]map[string]bool{}
	for _, ex := range c.Exams {
		if ex.ID == "" {
			errs = append(errs, errors.New("exam without id"))
			continue
		}
		if _, dup := exams[ex.ID]; dup {
			errs = append(errs, fmt.Errorf("exam %s: duplicate id", ex.ID))
			continue
		}
		domains := map[string]bool{}
		for _, d := range ex.Domains {
			if d.ID == "" {
				errs = append(errs, fmt.Errorf("exam %s: domain without id", ex.ID))
				continue
			}
			if domains[d.ID] {
				errs = append(errs, fmt.Errorf("exam %s: duplicate domain %s", ex.ID, d.ID))
			}
			domains[d.ID] = true
		}
		exams[ex.ID] = domains
	}

	levels := map[string]bool{}
	modules := map[string]bool{}
	scenarios := map[string]string{}
	for _, lv := range c.Levels {
		if !levelIDPattern.MatchString(lv.ID) {
			errs = append(errs, fmt.Errorf("level %q: id must match %s", lv.ID, levelIDPattern))
		}
		if levels[lv.ID] {
			errs = append(errs, fmt.Errorf("level %s: duplicate id", lv.ID))
		}
		levels[lv.ID] = true
		for _, m := range lv.Modules {
			if !strings.HasPrefix(m.ID, lv.ID+".") {
				errs = append(errs, fmt.Errorf("module %q: id must start with %s.", m.ID, lv.ID))
			}
			if modules[m.ID] {
				errs = append(errs, fmt.Errorf("module %s: duplicate id", m.ID))
			}
			modules[m.ID] = true
			for _, ref := range m.Exam {
				examID, domainID, ok := strings.Cut(ref, ":")
				if !ok || examID == "" || domainID == "" {
					errs = append(errs, fmt.Errorf("module %s: exam ref %q must be EXAM:domain", m.ID, ref))
					continue
				}
				domains, known := exams[examID]
				if !known {
					errs = append(errs, fmt.Errorf("module %s: unknown exam %s in %q", m.ID, examID, ref))
					continue
				}
				if !domains[domainID] {
					errs = append(errs, fmt.Errorf("module %s: unknown domain %s in exam %s", m.ID, domainID, examID))
				}
			}
			for _, id := range m.Concepts {
				if !conceptIDPattern.MatchString(id) {
					errs = append(errs, fmt.Errorf("module %s: concept %q must match %s", m.ID, id, conceptIDPattern))
				}
			}
			for _, sc := range m.Scenarios {
				if sc.ID == "" {
					continue
				}
				if first, dup := scenarios[sc.ID]; dup {
					errs = append(errs, fmt.Errorf("module %s: scenario %s already listed in module %s", m.ID, sc.ID, first))
					continue
				}
				scenarios[sc.ID] = m.ID
			}
		}
	}
	return errors.Join(errs...)
}

// ConceptIDs returns the sorted set of concept ids named by any module.
func (c *Curriculum) ConceptIDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, lv := range c.Levels {
		for _, m := range lv.Modules {
			for _, id := range m.Concepts {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// ExamCoverage lists, for each domain of examID, the scenarios that cover it
// and how many of them are in solved.
func (c *Curriculum) ExamCoverage(examID string, solved map[string]bool) ([]Coverage, error) {
	var exam *Exam
	for i := range c.Exams {
		if c.Exams[i].ID == examID {
			exam = &c.Exams[i]
			break
		}
	}
	if exam == nil {
		return nil, fmt.Errorf("exam coverage %s: %w", examID, ErrUnknownExam)
	}
	out := make([]Coverage, 0, len(exam.Domains))
	for _, d := range exam.Domains {
		cov := Coverage{Exam: examID, Domain: d}
		ref := examID + ":" + d.ID
		for _, lv := range c.Levels {
			for _, m := range lv.Modules {
				if !slices.Contains(m.Exam, ref) {
					continue
				}
				for _, sc := range m.Scenarios {
					if sc.ID == "" {
						continue
					}
					cov.Scenarios = append(cov.Scenarios, sc.ID)
					if solved[sc.ID] {
						cov.Done++
					}
				}
			}
		}
		out = append(out, cov)
	}
	return out, nil
}
