package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// assertion states in words what a check reads and what it expects, built
// from the check fields: `Deployment hello: {.status.readyReplicas} equals "2"`.
// A spoiler check keeps its expected value back, since it names the fix.
func assertion(c scenario.Check) string {
	subject := checkSubject(c)
	validators := checkValidators(c)
	if c.Spoiler {
		validators = []string{"has the expected value (" + render.SpoilerHidden + ")"}
	}
	if len(validators) == 0 && c.Rollout != nil {
		return subject + " finished rolling out: every pod runs the current template"
	}
	if len(validators) == 0 {
		validators = []string{"is read without error"}
	}
	sep := ": "
	if c.Resource != nil && c.Path != "" {
		sep = " "
	}
	return subject + sep + strings.Join(validators, " and ")
}

func checkSubject(c scenario.Check) string {
	switch {
	case c.Resource != nil:
		r := c.Resource
		var s string
		switch {
		case r.Name != "":
			s = r.Kind + " " + r.Name
		case r.Selector != "":
			s = r.Kind + " list selected by " + r.Selector
		default:
			s = r.Kind + " list"
		}
		if r.Namespace != "" {
			s += " in namespace " + r.Namespace
		}
		if c.Path != "" {
			s += ": " + c.Path
		}
		return s
	case c.Probe != nil:
		s := "probe pod running " + strconv.Quote(strings.Join(c.Probe.Command, " "))
		if c.Probe.Image != "" {
			s += " (image " + c.Probe.Image + ")"
		}
		return s
	case c.Rollout != nil:
		s := c.Rollout.Kind + " " + c.Rollout.Name
		if c.Rollout.Namespace != "" {
			s += " in namespace " + c.Rollout.Namespace
		}
		return s
	case c.Script != "":
		return "script " + strconv.Quote(c.Script)
	}
	return "check " + c.ID
}

func checkValidators(c scenario.Check) []string {
	var out []string
	if c.Exists != nil {
		if *c.Exists {
			out = append(out, "exists")
		} else {
			out = append(out, "does not exist")
		}
	}
	if c.Equals != nil {
		out = append(out, "equals "+strconv.Quote(*c.Equals))
	}
	if c.NotEquals != nil {
		out = append(out, "does not equal "+strconv.Quote(*c.NotEquals))
	}
	if c.Matches != "" {
		out = append(out, "matches /"+c.Matches+"/")
	}
	if c.Contains != "" {
		out = append(out, "contains "+strconv.Quote(c.Contains))
	}
	if c.GTE != nil {
		out = append(out, ">= "+*c.GTE)
	}
	if c.LTE != nil {
		out = append(out, "<= "+*c.LTE)
	}
	if c.NotEmpty {
		out = append(out, "is not empty")
	}
	if c.ExitCode != nil {
		out = append(out, "exits with code "+strconv.Itoa(*c.ExitCode))
	}
	return out
}

// writeExplain lists every check of a rendered scenario: id, description,
// the assertion in words, dependencies, criticality and why. exam names
// kubedrill submit as the command that grades them.
func writeExplain(w io.Writer, id string, checks []scenario.Check, exam bool) {
	grades := "kubedrill check grades them"
	if exam {
		grades = "kubedrill submit grades them once"
	}
	fmt.Fprintf(w, "%s is checked by %s; %s, this list grades nothing\n", id, render.Plural(len(checks), "check"), grades)
	for i, c := range checks {
		fmt.Fprintln(w)
		desc := c.Description
		if desc == "" {
			desc = c.ID
		}
		fmt.Fprintf(w, "%d. %s: %s\n", i+1, c.ID, desc)
		fmt.Fprintf(w, "%sasserts: %s\n", render.DetailIndent, assertion(c))
		var notes []string
		if len(c.DependsOn) > 0 {
			notes = append(notes, "runs after "+strings.Join(c.DependsOn, ", "))
		}
		if c.Critical {
			notes = append(notes, "critical: if it does not pass (failed, errored or skipped) the score is 0")
		}
		if len(notes) > 0 {
			fmt.Fprintf(w, "%s%s\n", render.DetailIndent, strings.Join(notes, "; "))
		}
		if c.Why != "" {
			for _, l := range render.WhyLines(c.Why, render.WhyWidth) {
				fmt.Fprintln(w, l)
			}
		}
	}
}
