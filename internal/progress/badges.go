package progress

import (
	"fmt"
	"math"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

// BadgeState is where a badge stands.
type BadgeState string

// The badge states, from nothing started to every criterion held by
// mastered assignments.
const (
	BadgeLocked     BadgeState = "locked"
	BadgeInProgress BadgeState = "in-progress"
	BadgeEarned     BadgeState = "earned"
	BadgeMastered   BadgeState = "mastered"
)

// badgeArcs is the number of arcs in a badge ring, one per step of the
// troubleshooting loop.
const badgeArcs = 5

// badgePips is the number of variant pips under a badge.
const badgePips = 3

// Standing is what a badge reads from one assignment. Started means a run
// finished; Clean means at least one clean solve (IsClean); Mastered is
// ScenarioMastery's rule; CleanVariants is Mastery.CleanVariants;
// VariantsClean counts the distinct variants among every clean solve;
// FirstClean is when the first clean solve ended.
type Standing struct {
	ID            string
	Started       bool
	Clean         bool
	Mastered      bool
	CleanVariants int
	VariantsClean int
	FirstClean    *time.Time
}

// Badge is the state of one module, level or exam track badge. Lit is the
// number of lit arcs (0 to 5); Clean of Total is the count behind it
// (assignments solved clean, module badges earned, domains covered); Pips is
// the variants solved clean out of 3 that every part reaches; Variants sums
// the distinct variants solved clean; EarnedAt is when the badge was earned,
// when known.
type Badge struct {
	State    BadgeState
	Lit      int
	Clean    int
	Total    int
	Pips     int
	Variants int
	Caption  string
	EarnedAt *time.Time
}

// StandingOf reads the standing of scenario s from its episodes; eps should
// hold only that scenario's episodes.
func StandingOf(eps []journal.Episode, s ScenarioInfo) Standing {
	m := ScenarioMastery(eps, s)
	st := Standing{ID: s.ID, Started: m.Attempts > 0, Clean: m.Clean > 0, Mastered: m.Mastered, CleanVariants: m.CleanVariants}
	causes := map[string]bool{}
	for _, e := range finished(eps) {
		if !IsClean(e, s.Minutes) {
			continue
		}
		causes[variantKey(e)] = true
		if st.FirstClean == nil || e.EndedAt.Before(*st.FirstClean) {
			t := *e.EndedAt
			st.FirstClean = &t
		}
	}
	st.VariantsClean = len(causes)
	return st
}

// ModuleBadge is the badge of a module from the standings of its written
// assignments; planned assignments do not count. It is earned when every
// assignment has a clean solve, mastered when every assignment is
// mastered, in progress once a run of any of them finished, locked before.
// Lit is round(5 x clean / assignments), at least 1 once any clean solve
// exists and at most 4 while in progress, so 5 lit arcs always mean
// earned.
func ModuleBadge(as []Standing) Badge {
	n := len(as)
	if n == 0 {
		return Badge{State: BadgeLocked, Caption: "No assignment written yet"}
	}
	b := Badge{Total: n, Pips: badgePips}
	started, mastered := false, 0
	var last *time.Time
	for _, a := range as {
		started = started || a.Started
		if a.Clean {
			b.Clean++
			last = later(last, a.FirstClean)
		}
		if a.Mastered {
			mastered++
		}
		b.Pips = min(b.Pips, min(a.CleanVariants, badgePips))
		b.Variants += a.VariantsClean
	}
	b.Lit = arcs(b.Clean, n)
	switch {
	case mastered == n:
		b.State, b.EarnedAt = BadgeMastered, last
		b.Caption = "Every assignment mastered"
		if b.Pips == badgePips {
			b.Caption = fmt.Sprintf("Every assignment, %d variants clean", badgePips)
		}
	case b.Clean == n:
		b.State, b.EarnedAt = BadgeEarned, last
		b.Caption = "Every assignment solved clean"
	case started:
		b.State, b.Lit = BadgeInProgress, min(b.Lit, badgeArcs-1)
		b.Caption = fmt.Sprintf("%d of %d %s solved clean", b.Clean, n, noun(n, "assignment"))
	default:
		b.State = BadgeLocked
		b.Caption = fmt.Sprintf("Not started: no run finished in its %s", countNoun(n, "assignment"))
	}
	return b
}

// LevelBadge is the badge of a level from the badges of its modules that
// have written assignments, with the module rules over module states:
// earned when every module badge is earned, mastered when every one is
// mastered, in progress once any module is, locked before. Lit is the
// share of earned module badges, at least 1 once any clean solve exists,
// at most 4 while in progress.
func LevelBadge(mods []Badge) Badge {
	var in []Badge
	for _, m := range mods {
		if m.Total > 0 {
			in = append(in, m)
		}
	}
	n := len(in)
	if n == 0 {
		return Badge{State: BadgeLocked, Caption: "No assignment written yet"}
	}
	b := Badge{Total: n, Pips: badgePips}
	started, mastered, anyClean := false, 0, false
	var last *time.Time
	for _, m := range in {
		started = started || m.State != BadgeLocked
		anyClean = anyClean || m.Clean > 0
		switch m.State {
		case BadgeMastered:
			mastered++
			fallthrough
		case BadgeEarned:
			b.Clean++
			last = later(last, m.EarnedAt)
		}
		b.Pips = min(b.Pips, m.Pips)
		b.Variants += m.Variants
	}
	b.Lit = arcs(b.Clean, n)
	if anyClean {
		b.Lit = max(b.Lit, 1)
	}
	switch {
	case mastered == n:
		b.State, b.EarnedAt = BadgeMastered, last
		b.Caption = "Every module badge mastered"
	case b.Clean == n:
		b.State, b.EarnedAt = BadgeEarned, last
		b.Caption = "Every module badge earned"
	case started:
		b.State, b.Lit = BadgeInProgress, min(b.Lit, badgeArcs-1)
		b.Caption = fmt.Sprintf("%d of %d module badges earned", b.Clean, n)
	default:
		b.State = BadgeLocked
		b.Caption = fmt.Sprintf("Not started: no run finished in its %s", countNoun(n, "module"))
	}
	return b
}

// TrackBadge is the badge of an exam track from the standings of the
// written assignments of each exam domain. A domain is covered when one
// of its assignments has a clean solve; a domain without a written
// assignment cannot be covered, so the track cannot be earned while one
// exists. It is earned when every domain is covered, mastered when every
// domain has a mastered assignment, in progress once a domain is covered,
// locked before. Lit is the share of covered domains, at least 1 once any
// is covered, at most 4 while in progress.
func TrackBadge(domains [][]Standing) Badge {
	n := len(domains)
	if n == 0 {
		return Badge{State: BadgeLocked, Caption: "No exam domain listed"}
	}
	b := Badge{Total: n, Pips: badgePips}
	empty, mastered := 0, 0
	seen := map[string]bool{}
	var last *time.Time
	for _, d := range domains {
		if len(d) == 0 {
			empty++
			b.Pips = 0
			continue
		}
		covered, master, best := false, false, 0
		var first *time.Time
		for _, a := range d {
			if !seen[a.ID] {
				seen[a.ID] = true
				b.Variants += a.VariantsClean
			}
			if a.Clean {
				covered = true
				first = earlier(first, a.FirstClean)
			}
			master = master || a.Mastered
			best = max(best, min(a.CleanVariants, badgePips))
		}
		if covered {
			b.Clean++
			last = later(last, first)
		}
		if master {
			mastered++
		}
		b.Pips = min(b.Pips, best)
	}
	b.Lit = arcs(b.Clean, n)
	switch {
	case empty == 0 && mastered == n:
		b.State, b.EarnedAt = BadgeMastered, last
		b.Caption = "Every domain covered by a mastered assignment"
	case empty == 0 && b.Clean == n:
		b.State, b.EarnedAt = BadgeEarned, last
		b.Caption = "Every domain covered by clean solves"
	case b.Clean > 0:
		b.State, b.Lit = BadgeInProgress, min(b.Lit, badgeArcs-1)
		b.Caption = fmt.Sprintf("%d of %d domains covered by clean solves", b.Clean, n)
	default:
		b.State = BadgeLocked
		b.Caption = "No domain covered by a clean solve yet"
	}
	if empty > 0 {
		b.Caption += fmt.Sprintf("; %d of %d domains have assignments yet", n-empty, n)
	}
	return b
}

// arcs is round(5 x part / whole), at least 1 when part > 0.
func arcs(part, whole int) int {
	if whole <= 0 || part <= 0 {
		return 0
	}
	return max(1, int(math.Round(badgeArcs*float64(part)/float64(whole))))
}

func later(a, b *time.Time) *time.Time {
	if b == nil || (a != nil && !b.After(*a)) {
		return a
	}
	return b
}

func earlier(a, b *time.Time) *time.Time {
	if b == nil || (a != nil && !b.Before(*a)) {
		return a
	}
	return b
}

func noun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func countNoun(n int, word string) string {
	if n == 1 {
		return "one " + word
	}
	return fmt.Sprintf("%d %s", n, noun(n, word))
}
