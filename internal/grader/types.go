// Package grader runs the checks of a rendered scenario against a cluster
// and produces a Grade with one Result per check. Checks form a DAG through
// depends_on: a check whose dependency failed is Skipped, a failed critical
// check zeroes the score, and a grader error is a Result with StatusError,
// never a panic; an errored check counts as not passed.
package grader

import "time"

// Status is the outcome of one check.
type Status string

// Check outcomes.
const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
	StatusError   Status = "error"
)

// Result is the learner-facing outcome of one check. Expected and Actual are
// filled whenever the validator can name them, so a failure reads as
// "expected 2, got 0". Why is copied from the check for every status so the
// learner reads what the check proves next to its outcome. Spoiler is
// copied from the check: its Actual names the root cause, so a learner view
// may hold it back until a hint or a diagnosis.
type Result struct {
	ID          string        `json:"id"`
	Description string        `json:"description"`
	Why         string        `json:"why,omitempty"`
	Status      Status        `json:"status"`
	Expected    string        `json:"expected,omitempty"`
	Actual      string        `json:"actual,omitempty"`
	Message     string        `json:"message,omitempty"`
	Critical    bool          `json:"critical,omitempty"`
	Weight      float64       `json:"weight"`
	Duration    time.Duration `json:"duration"`
	Spoiler     bool          `json:"spoiler,omitempty"`
}

// Grade aggregates the results of one grading run. Score is the weighted
// fraction of passed checks in [0, 1], forced to 0 when a critical check
// fails. Passed is true only when every check passed.
type Grade struct {
	Score   float64   `json:"score"`
	Passed  bool      `json:"passed"`
	Results []Result  `json:"results"`
	At      time.Time `json:"at"`
}

// Summary returns passed and total check counts.
func (g Grade) Summary() (passed, total int) {
	for _, r := range g.Results {
		if r.Status == StatusPass {
			passed++
		}
	}
	return passed, len(g.Results)
}
