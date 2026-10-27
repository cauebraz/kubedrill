package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/progress"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

// scenarioInfos is the catalog as progress sees it, sorted by id.
func scenarioInfos(cat Catalog) []progress.ScenarioInfo {
	all := sortedScenarios(cat)
	out := make([]progress.ScenarioInfo, 0, len(all))
	for _, sc := range all {
		out = append(out, scenarioInfo(sc))
	}
	return out
}

// scenarioInfo is sc as progress sees it.
func scenarioInfo(sc *scenario.Scenario) progress.ScenarioInfo {
	return progress.ScenarioInfo{ID: sc.Meta.ID, Level: sc.Meta.Level, Concepts: sc.Meta.Concepts, Minutes: sc.Meta.Minutes, Variants: len(sc.Variants)}
}

// allEpisodes reads every recorded episode, oldest first.
func allEpisodes(j Journal) ([]journal.Episode, error) {
	eps, err := j.Episodes("")
	if err != nil {
		return nil, fmt.Errorf("read episodes: %w", err)
	}
	sort.SliceStable(eps, func(i, k int) bool { return eps[i].StartedAt.Before(eps[k].StartedAt) })
	return eps, nil
}

func episodesOf(eps []journal.Episode, id string) []journal.Episode {
	var out []journal.Episode
	for _, e := range eps {
		if e.ScenarioID == id {
			out = append(out, e)
		}
	}
	return out
}

func withoutEpisode(eps []journal.Episode, id string) []journal.Episode {
	var out []journal.Episode
	for _, e := range eps {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

// recommend runs progress.Next over the catalog and the journal.
func (a *app) recommend(cat Catalog, eps []journal.Episode) (progress.Recommendation, bool) {
	return progress.Next(scenarioInfos(cat), eps, a.now(), a.rnd)
}

// recommendationLines is the recommendation block of list and path (label
// "Next") and of the solved banner (label "Recommended"): the scenario with
// its reason as a sentence, wrapped to the terminal width, then the command
// to run.
func recommendationLines(p render.Printer, cat Catalog, rec progress.Recommendation, label string) []string {
	title, meta := rec.ScenarioID, ""
	if sc, err := cat.Find(rec.ScenarioID); err == nil {
		title = sc.Meta.Title
		meta = fmt.Sprintf(" (%s, %s, %d min)", sc.Meta.ID, sc.Meta.Level, sc.Meta.Minutes)
	}
	cmd := "kubedrill start " + rec.ScenarioID
	if rec.Review {
		cmd += fmt.Sprintf(" --seed %d", rec.Seed)
	}
	return []string{
		p.WrapLine(fmt.Sprintf("%s: %s%s: %s", label, title, meta, reasonSentence(rec.Reason)), "  "),
		"  " + p.Style(render.StyleBold, cmd),
	}
}

// printRecommendation prints the lines of recommendationLines.
func (a *app) printRecommendation(cat Catalog, rec progress.Recommendation, label string) {
	for _, l := range recommendationLines(a.printer(), cat, rec, label) {
		a.println(l)
	}
}

var (
	practiceNewReason  = regexp.MustCompile(`^practice: level (\S+), new scenario$`)
	practiceRateReason = regexp.MustCompile(`^practice: level (\S+), solved (\d+) of last (\d+)$`)
	reviewReason       = regexp.MustCompile(`^review: concept (\S+) was mastered, last clean solve (\d+) days? ago$`)
)

// reasonSentence rewrites the terse reasons of progress.Next as sentences
// addressed to the learner; a reason in any other shape passes unchanged.
func reasonSentence(reason string) string {
	if m := practiceRateReason.FindStringSubmatch(reason); m != nil {
		switch {
		case m[3] == "1" && m[2] == "0":
			return fmt.Sprintf("you did not solve your last attempt at %s", m[1])
		case m[3] == "1":
			return fmt.Sprintf("you solved your last attempt at %s", m[1])
		}
		return fmt.Sprintf("you solved %s of your last %s attempts at %s", m[2], m[3], m[1])
	}
	if m := practiceNewReason.FindStringSubmatch(reason); m != nil {
		return fmt.Sprintf("you have not tried it yet; it is the next one at %s", m[1])
	}
	if m := reviewReason.FindStringSubmatch(reason); m != nil {
		days := m[2] + " days ago"
		switch m[2] {
		case "0":
			days = "today"
		case "1":
			days = "1 day ago"
		}
		return fmt.Sprintf("review: you mastered %s %s", m[1], days)
	}
	return reason
}

// lastCell prints the clean attempts of the window as "3/4", or "-".
func lastCell(m progress.Mastery) string {
	if m.Last == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", m.CleanInLast, m.Last)
}

func statusOf(m progress.Mastery, active bool) string {
	switch {
	case active:
		return "active"
	case m.Mastered:
		return "mastered"
	case m.Solved > 0:
		return "solved"
	default:
		return "todo"
	}
}

// conceptState is one concept in the path view: mastered, new, or the
// clean attempts of its window.
func conceptState(id string, m progress.Mastery) string {
	switch {
	case m.Mastered:
		return id + " mastered"
	case m.Attempts == 0:
		return id + " new"
	default:
		return fmt.Sprintf("%s %d/%d clean", id, m.CleanInLast, m.Last)
	}
}

// sharesConcept reports whether two scenarios list a common concept.
func sharesConcept(a, b *scenario.Scenario) bool {
	for _, x := range a.Meta.Concepts {
		for _, y := range b.Meta.Concepts {
			if x == y {
				return true
			}
		}
	}
	return false
}

// lastCheckFirst is the most recent "next time I check first" line from a
// debrief of a scenario sharing a concept with sc.
func lastCheckFirst(cat Catalog, sc *scenario.Scenario, eps []journal.Episode) string {
	for i := len(eps) - 1; i >= 0; i-- {
		e := eps[i]
		if e.Debrief == nil || strings.TrimSpace(e.Debrief.NextTimeFirst) == "" {
			continue
		}
		other, err := cat.Find(e.ScenarioID)
		if err != nil || !sharesConcept(sc, other) {
			continue
		}
		return strings.TrimSpace(e.Debrief.NextTimeFirst)
	}
	return ""
}

// laterExposure reports whether, before ep, the learner solved another
// scenario sharing a concept with sc or another variant of sc itself, the
// same episodes lastCheckFirst draws its reminder from.
func laterExposure(cat Catalog, sc *scenario.Scenario, ep *journal.Episode, eps []journal.Episode) bool {
	for _, e := range eps {
		if e.Outcome != journal.OutcomeSolved || e.ID == ep.ID || !e.StartedAt.Before(ep.StartedAt) {
			continue
		}
		if e.ScenarioID == sc.Meta.ID {
			if e.Variant != ep.Variant {
				return true
			}
			continue
		}
		if other, err := cat.Find(e.ScenarioID); err == nil && sharesConcept(sc, other) {
			return true
		}
	}
	return false
}
