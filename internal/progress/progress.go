// Package progress turns the journal into learning decisions: whether an
// attempt was clean, mastery per scenario and per concept, the next scenario
// to practice or review, and summary statistics. Every function is pure over
// the episodes and the catalog it receives.
package progress

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

// PracticeSeedMax is the largest practice seed. Practice seeds are
// 1..PracticeSeedMax and review seeds PracticeSeedMax+1..ReviewSeedMax, so a
// review never repeats a practiced seed.
const PracticeSeedMax int64 = 499_999

// ReviewSeedMax is the largest review seed.
const ReviewSeedMax int64 = 999_999

const (
	minExposure  = 60 * time.Second
	window       = 4
	masteryClean = 3
	sessionGap   = 3 * time.Hour
	seedRetries  = 16
	maxDoublings = 16
)

// ScenarioInfo is what progress needs to know about one scenario of the
// catalog.
type ScenarioInfo struct {
	ID       string
	Level    string
	Concepts []string
	Minutes  int
	Variants int
}

// Mastery summarises the finished attempts of one scenario or one concept.
// Last is the number of attempts in the window (at most 4) and CleanInLast
// the clean ones among them. Sessions counts the distinct sessions among the
// clean attempts of the window; a session starts after a 3 hour gap.
// Mastered means at least 3 clean attempts in the last 4, on distinct seeds
// and on distinct variants (as many as the scenarios offer, up to 3), across
// at least 2 sessions. A hinted or failed attempt takes a slot in the
// window instead of resetting the count.
type Mastery struct {
	Attempts    int
	Solved      int
	Clean       int
	Last        int
	CleanInLast int
	Sessions    int
	Mastered    bool
	LastSolved  *time.Time
	MedianSolve time.Duration
}

// Recommendation is the scenario Next proposes, with the seed to run and
// the reason in one line. Review is true for a spaced review of a mastered
// concept.
type Recommendation struct {
	ScenarioID string
	Reason     string
	Seed       int64
	Review     bool
}

// LevelStats counts finished attempts and solves in one level.
type LevelStats struct {
	Attempts int
	Solved   int
}

// Summary is what Stats reports.
type Summary struct {
	ByLevel       map[string]LevelStats
	MedianSolve   time.Duration
	HintsPerSolve float64
	Outcomes      map[journal.Outcome]int
	Verdicts      map[string]int
	Concepts      map[string]Mastery
}

// IsReviewSeed reports whether seed lies in the review range,
// PracticeSeedMax+1..ReviewSeedMax.
func IsReviewSeed(seed int64) bool {
	return seed > PracticeSeedMax && seed <= ReviewSeedMax
}

// IsClean reports whether e is a clean solve: solved without the guide of
// the browser workspace (a guided practice run shows the fix), zero hints,
// within minutes (no limit when minutes <= 0) and, when the audit telemetry
// is known, at least one read before the first write.
func IsClean(e journal.Episode, minutes int) bool {
	if e.Outcome != journal.OutcomeSolved || e.Guided || e.HintsUsed > 0 || e.EndedAt == nil {
		return false
	}
	if minutes > 0 && e.EndedAt.Sub(e.StartedAt) > time.Duration(minutes)*time.Minute {
		return false
	}
	if t := e.Telemetry; t != nil && t.Verdict != "" && t.Verdict != journal.VerdictUnknown {
		if t.Writes > 0 && t.ReadsBeforeFirstWrite == 0 {
			return false
		}
	}
	return true
}

// ScenarioMastery computes the mastery of scenario s from its episodes;
// eps should hold only that scenario's episodes. Active episodes are
// ignored.
func ScenarioMastery(eps []journal.Episode, s ScenarioInfo) Mastery {
	return mastery(eps, func(journal.Episode) int { return s.Minutes }, variantCount(s))
}

// variantCount is the number of variants of s, at least 1.
func variantCount(s ScenarioInfo) int {
	return max(s.Variants, 1)
}

// conceptVariants is the number of distinct variants the scenarios in ids
// offer together.
func conceptVariants(byID map[string]ScenarioInfo, ids map[string]bool) int {
	n := 0
	for id := range ids {
		n += variantCount(byID[id])
	}
	return n
}

// ConceptMastery computes mastery per concept over the episodes of every
// catalog scenario carrying the concept. Concepts without any finished
// attempt are present with zero counts.
func ConceptMastery(cat []ScenarioInfo, eps []journal.Episode) map[string]Mastery {
	byID := index(cat)
	out := map[string]Mastery{}
	for concept, ids := range conceptScenarios(cat) {
		var in []journal.Episode
		for _, e := range eps {
			if ids[e.ScenarioID] {
				in = append(in, e)
			}
		}
		out[concept] = mastery(in, func(e journal.Episode) int { return byID[e.ScenarioID].Minutes }, conceptVariants(byID, ids))
	}
	return out
}

// FirstExposure returns the concepts, in the given order and without
// duplicates, that no finished episode of a catalog scenario has touched. A
// failed episode shorter than 60 seconds with zero hints and no diagnosis
// (an instant restart) is not an exposure.
func FirstExposure(concepts []string, cat []ScenarioInfo, eps []journal.Episode) []string {
	seen := map[string]bool{}
	byID := index(cat)
	for _, e := range finished(eps) {
		if e.Outcome == journal.OutcomeFailed && e.EndedAt.Sub(e.StartedAt) < minExposure && e.HintsUsed == 0 && e.Diagnosis == nil {
			continue
		}
		for _, c := range byID[e.ScenarioID].Concepts {
			seen[c] = true
		}
	}
	var out []string
	for _, c := range concepts {
		if !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	return out
}

// Next picks what to run now. A mastered concept is due for review when the
// time since its last clean solve reaches its interval: 1 day, doubling with
// each clean review solve (2, 4, 8 days...). The most overdue concept wins
// and its least recently attempted scenario runs on a review seed it has not
// used. Otherwise
// Next picks, in the lowest level with a scenario not yet mastered, the
// scenario with the highest p(1-p), where p is the solve rate over its last
// 4 attempts (0.5 when unattempted), on a practice seed it has not used.
// Ties go to the lower scenario ID. rnd(n) returns a value in [0, n); the
// result is deterministic given rnd. It returns false when every scenario is
// mastered and no review is due.
func Next(cat []ScenarioInfo, eps []journal.Episode, now time.Time, rnd func(n int64) int64) (Recommendation, bool) {
	if rnd == nil {
		rnd = rand.Int64N
	}
	done := finished(eps)
	if r, ok := nextReview(cat, done, now, rnd); ok {
		return r, true
	}
	return nextPractice(cat, done, rnd)
}

// due is one mastered concept whose review interval has passed.
type due struct {
	concept string
	overdue time.Duration
	last    time.Time
}

// DueConcepts lists the mastered concepts whose spaced review is due at
// now, most overdue first; Next recommends a scenario of the first one.
func DueConcepts(cat []ScenarioInfo, eps []journal.Episode, now time.Time) []string {
	dues := dueReviews(cat, finished(eps), now)
	out := make([]string, 0, len(dues))
	for _, d := range dues {
		out = append(out, d.concept)
	}
	return out
}

func dueReviews(cat []ScenarioInfo, eps []journal.Episode, now time.Time) []due {
	byID := index(cat)
	var dues []due
	for concept, ids := range conceptScenarios(cat) {
		var in []journal.Episode
		for _, e := range eps {
			if ids[e.ScenarioID] {
				in = append(in, e)
			}
		}
		minutes := func(e journal.Episode) int { return byID[e.ScenarioID].Minutes }
		if !mastery(in, minutes, conceptVariants(byID, ids)).Mastered {
			continue
		}
		var last time.Time
		reviews := 0
		for _, e := range in {
			if !IsClean(e, minutes(e)) {
				continue
			}
			if e.EndedAt.After(last) {
				last = *e.EndedAt
			}
			if IsReviewSeed(e.Seed) {
				reviews++
			}
		}
		interval := 24 * time.Hour << min(reviews, maxDoublings)
		if gap := now.Sub(last); gap >= interval {
			dues = append(dues, due{concept: concept, overdue: gap - interval, last: last})
		}
	}
	sort.Slice(dues, func(i, j int) bool {
		if dues[i].overdue != dues[j].overdue {
			return dues[i].overdue > dues[j].overdue
		}
		return dues[i].concept < dues[j].concept
	})
	return dues
}

func nextReview(cat []ScenarioInfo, eps []journal.Episode, now time.Time, rnd func(n int64) int64) (Recommendation, bool) {
	dues := dueReviews(cat, eps, now)
	if len(dues) == 0 {
		return Recommendation{}, false
	}
	d := dues[0]
	pick := ""
	var pickAt time.Time
	for _, s := range sortedCatalog(cat) {
		if !contains(s.Concepts, d.concept) {
			continue
		}
		var lastAt time.Time
		for _, e := range eps {
			if e.ScenarioID == s.ID && e.StartedAt.After(lastAt) {
				lastAt = e.StartedAt
			}
		}
		if pick == "" || lastAt.Before(pickAt) {
			pick, pickAt = s.ID, lastAt
		}
	}
	days := int(now.Sub(d.last) / (24 * time.Hour))
	draw := func() int64 { return PracticeSeedMax + 1 + rnd(ReviewSeedMax-PracticeSeedMax) }
	return Recommendation{
		ScenarioID: pick,
		Reason:     fmt.Sprintf("review: concept %s was mastered, last clean solve %d days ago", d.concept, days),
		Seed:       unusedSeed(eps, pick, draw),
		Review:     true,
	}, true
}

func nextPractice(cat []ScenarioInfo, eps []journal.Episode, rnd func(n int64) int64) (Recommendation, bool) {
	byScenario := map[string][]journal.Episode{}
	for _, e := range eps {
		byScenario[e.ScenarioID] = append(byScenario[e.ScenarioID], e)
	}
	levels := map[string][]ScenarioInfo{}
	for _, s := range sortedCatalog(cat) {
		if !ScenarioMastery(byScenario[s.ID], s).Mastered {
			levels[s.Level] = append(levels[s.Level], s)
		}
	}
	if len(levels) == 0 {
		return Recommendation{}, false
	}
	names := make([]string, 0, len(levels))
	for l := range levels {
		names = append(names, l)
	}
	sort.Strings(names)
	best, bestScore, bestSolved, bestLast := "", -1.0, 0, 0
	for _, s := range levels[names[0]] {
		recent := lastN(byScenario[s.ID], window)
		p := 0.5
		solved := 0
		for _, e := range recent {
			if e.Outcome == journal.OutcomeSolved {
				solved++
			}
		}
		if len(recent) > 0 {
			p = float64(solved) / float64(len(recent))
		}
		if score := p * (1 - p); score > bestScore+1e-9 {
			best, bestScore, bestSolved, bestLast = s.ID, score, solved, len(recent)
		}
	}
	seed := unusedSeed(eps, best, func() int64 { return 1 + rnd(PracticeSeedMax) })
	reason := fmt.Sprintf("practice: level %s, new scenario", names[0])
	if bestLast > 0 {
		reason = fmt.Sprintf("practice: level %s, solved %d of last %d", names[0], bestSolved, bestLast)
	}
	return Recommendation{ScenarioID: best, Reason: reason, Seed: seed}, true
}

// unusedSeed draws a seed with draw, redrawing up to 16 times while the
// scenario already has an episode on it.
func unusedSeed(eps []journal.Episode, scenarioID string, draw func() int64) int64 {
	used := map[int64]bool{}
	for _, e := range eps {
		if e.ScenarioID == scenarioID {
			used[e.Seed] = true
		}
	}
	seed := draw()
	for i := 0; i < seedRetries && used[seed]; i++ {
		seed = draw()
	}
	return seed
}

// Stats summarises finished episodes: attempts and solves per level of the
// catalog, the median solve time, hints per solve, outcome and telemetry
// verdict counts, and mastery per concept. Episodes of scenarios outside the
// catalog count in the outcome and verdict totals only.
func Stats(cat []ScenarioInfo, eps []journal.Episode) Summary {
	byID := index(cat)
	sum := Summary{
		ByLevel:  map[string]LevelStats{},
		Outcomes: map[journal.Outcome]int{},
		Verdicts: map[string]int{},
		Concepts: ConceptMastery(cat, eps),
	}
	var solves []time.Duration
	hints := 0
	for _, e := range finished(eps) {
		sum.Outcomes[e.Outcome]++
		if e.Telemetry != nil && e.Telemetry.Verdict != "" {
			sum.Verdicts[string(e.Telemetry.Verdict)]++
		}
		solved := e.Outcome == journal.OutcomeSolved
		if solved {
			solves = append(solves, e.EndedAt.Sub(e.StartedAt))
			hints += e.HintsUsed
		}
		if s, ok := byID[e.ScenarioID]; ok {
			ls := sum.ByLevel[s.Level]
			ls.Attempts++
			if solved {
				ls.Solved++
			}
			sum.ByLevel[s.Level] = ls
		}
	}
	sum.MedianSolve = median(solves)
	if len(solves) > 0 {
		sum.HintsPerSolve = float64(hints) / float64(len(solves))
	}
	return sum
}

// mastery summarises eps; variants is how many distinct variants the
// scenarios in scope offer, so a scenario with one variant can still be
// mastered while one with three needs three different root causes.
func mastery(eps []journal.Episode, minutes func(journal.Episode) int, variants int) Mastery {
	done := finished(eps)
	m := Mastery{Attempts: len(done)}
	var solves []time.Duration
	for _, e := range done {
		if e.Outcome == journal.OutcomeSolved {
			m.Solved++
			solves = append(solves, e.EndedAt.Sub(e.StartedAt))
			if m.LastSolved == nil || e.EndedAt.After(*m.LastSolved) {
				t := *e.EndedAt
				m.LastSolved = &t
			}
		}
		if IsClean(e, minutes(e)) {
			m.Clean++
		}
	}
	m.MedianSolve = median(solves)
	recent := lastN(done, window)
	m.Last = len(recent)
	type key struct {
		id   string
		seed int64
	}
	seeds := map[key]bool{}
	causes := map[string]bool{}
	var clean []journal.Episode
	for _, e := range recent {
		if IsClean(e, minutes(e)) {
			clean = append(clean, e)
			seeds[key{e.ScenarioID, e.Seed}] = true
			causes[variantKey(e)] = true
		}
	}
	m.CleanInLast = len(clean)
	m.Sessions = sessions(clean)
	m.Mastered = m.CleanInLast >= masteryClean && len(seeds) >= masteryClean &&
		len(causes) >= min(masteryClean, variants) && m.Sessions >= 2
	return m
}

// variantKey names the root cause an episode practised: its scenario and
// variant, or its seed for an episode recorded without a variant.
func variantKey(e journal.Episode) string {
	if e.Variant == "" {
		return fmt.Sprintf("%s seed %d", e.ScenarioID, e.Seed)
	}
	return e.ScenarioID + " variant " + e.Variant
}

func sessions(eps []journal.Episode) int {
	n := 0
	var prevEnd time.Time
	for i, e := range eps {
		if i == 0 || e.StartedAt.Sub(prevEnd) >= sessionGap {
			n++
		}
		prevEnd = *e.EndedAt
	}
	return n
}

func finished(eps []journal.Episode) []journal.Episode {
	out := make([]journal.Episode, 0, len(eps))
	for _, e := range eps {
		if e.Outcome != journal.OutcomeActive && e.EndedAt != nil {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func lastN(eps []journal.Episode, n int) []journal.Episode {
	done := finished(eps)
	if len(done) > n {
		return done[len(done)-n:]
	}
	return done
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return time.Duration(math.Round(float64(s[mid-1]+s[mid]) / 2))
}

func index(cat []ScenarioInfo) map[string]ScenarioInfo {
	out := make(map[string]ScenarioInfo, len(cat))
	for _, s := range cat {
		out[s.ID] = s
	}
	return out
}

func conceptScenarios(cat []ScenarioInfo) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range cat {
		for _, c := range s.Concepts {
			if out[c] == nil {
				out[c] = map[string]bool{}
			}
			out[c][s.ID] = true
		}
	}
	return out
}

func sortedCatalog(cat []ScenarioInfo) []ScenarioInfo {
	out := append([]ScenarioInfo(nil), cat...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
