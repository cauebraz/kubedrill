package progress

import (
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

func TestStandingOf(t *testing.T) {
	day := 24 * time.Hour
	variant := func(name string) epOpt { return func(e *journal.Episode) { e.Variant = name } }
	guided := func(e *journal.Episode) { e.Guided = true }
	tests := []struct {
		name          string
		eps           []journal.Episode
		wantStarted   bool
		wantClean     bool
		wantMastered  bool
		wantVariants  int
		wantFirstMins time.Duration
	}{
		{name: "no episode"},
		{name: "a failed run is started, not clean", eps: []journal.Episode{ep("s", 1, 0, outcome(journal.OutcomeFailed))}, wantStarted: true},
		{name: "a guided solve is not clean", eps: []journal.Episode{ep("s", 1, 0, guided)}, wantStarted: true},
		{name: "a hinted solve is not clean", eps: []journal.Episode{ep("s", 1, 0, hints(1))}, wantStarted: true},
		{name: "an active run is not started", eps: []journal.Episode{ep("s", 1, 0, active())}},
		{
			name:          "two clean variants, the first clean solve is the earliest",
			eps:           []journal.Episode{ep("s", 1, 0, hints(1)), ep("s", 2, time.Hour, variant("a")), ep("s", 3, day, variant("b")), ep("s", 4, 2*day, variant("a"))},
			wantStarted:   true,
			wantClean:     true,
			wantVariants:  2,
			wantFirstMins: time.Hour + 5*time.Minute,
		},
		{
			name:          "mastered on three variants",
			eps:           []journal.Episode{ep("s", 1, 0, variant("a")), ep("s", 2, day, variant("b")), ep("s", 3, 2*day, variant("c"))},
			wantStarted:   true,
			wantClean:     true,
			wantMastered:  true,
			wantVariants:  3,
			wantFirstMins: 5 * time.Minute,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StandingOf(tc.eps, ScenarioInfo{ID: "s", Minutes: 10, Variants: 3})
			if got.ID != "s" || got.Started != tc.wantStarted || got.Clean != tc.wantClean || got.Mastered != tc.wantMastered || got.VariantsClean != tc.wantVariants {
				t.Errorf("standing: got %+v, want started=%v clean=%v mastered=%v variants=%d", got, tc.wantStarted, tc.wantClean, tc.wantMastered, tc.wantVariants)
			}
			switch {
			case tc.wantFirstMins == 0 && got.FirstClean != nil:
				t.Errorf("first clean: got %v, want none", got.FirstClean)
			case tc.wantFirstMins != 0 && (got.FirstClean == nil || !got.FirstClean.Equal(t0.Add(tc.wantFirstMins))):
				t.Errorf("first clean: got %v, want %v", got.FirstClean, t0.Add(tc.wantFirstMins))
			}
		})
	}
}

func at(h int) *time.Time {
	v := t0.Add(time.Duration(h) * time.Hour)
	return &v
}

func clean(id string, variants, h int) Standing {
	return Standing{ID: id, Started: true, Clean: true, CleanVariants: variants, VariantsClean: variants, FirstClean: at(h)}
}

func master(id string, variants, h int) Standing {
	s := clean(id, variants, h)
	s.Mastered = true
	return s
}

func started(id string) Standing { return Standing{ID: id, Started: true} }

func untouched(id string) Standing { return Standing{ID: id} }

type badgeWant struct {
	state    BadgeState
	lit      int
	clean    int
	total    int
	pips     int
	variants int
	caption  string
	earnedAt *time.Time
}

func checkBadge(t *testing.T, got Badge, want badgeWant) {
	t.Helper()
	if got.State != want.state || got.Lit != want.lit || got.Clean != want.clean || got.Total != want.total || got.Pips != want.pips || got.Variants != want.variants || got.Caption != want.caption {
		t.Errorf("badge:\n got  %s lit=%d clean=%d/%d pips=%d variants=%d %q\n want %s lit=%d clean=%d/%d pips=%d variants=%d %q",
			got.State, got.Lit, got.Clean, got.Total, got.Pips, got.Variants, got.Caption,
			want.state, want.lit, want.clean, want.total, want.pips, want.variants, want.caption)
	}
	switch {
	case want.earnedAt == nil && got.EarnedAt != nil:
		t.Errorf("earned at: got %v, want none", got.EarnedAt)
	case want.earnedAt != nil && (got.EarnedAt == nil || !got.EarnedAt.Equal(*want.earnedAt)):
		t.Errorf("earned at: got %v, want %v", got.EarnedAt, want.earnedAt)
	}
}

func TestModuleBadge(t *testing.T) {
	tests := []struct {
		name string
		in   []Standing
		want badgeWant
	}{
		{"no written assignment", nil, badgeWant{state: BadgeLocked, caption: "No assignment written yet"}},
		{"nothing run", []Standing{untouched("a"), untouched("b")}, badgeWant{state: BadgeLocked, total: 2, caption: "Not started: no run finished in its 2 assignments"}},
		{"one assignment, nothing run", []Standing{untouched("a")}, badgeWant{state: BadgeLocked, total: 1, caption: "Not started: no run finished in its one assignment"}},
		{"a run but no clean solve", []Standing{started("a"), untouched("b")}, badgeWant{state: BadgeInProgress, total: 2, caption: "0 of 2 assignments solved clean"}},
		{"2 of 4 clean", []Standing{clean("a", 1, 1), clean("b", 2, 2), started("c"), untouched("d")}, badgeWant{state: BadgeInProgress, lit: 3, clean: 2, total: 4, variants: 3, caption: "2 of 4 assignments solved clean"}},
		{"1 of 11 clean still lights an arc", append([]Standing{clean("a", 1, 1)}, make([]Standing, 10)...), badgeWant{state: BadgeInProgress, lit: 1, clean: 1, total: 11, variants: 1, caption: "1 of 11 assignments solved clean"}},
		{"9 of 10 clean lights 4 arcs, not 5", append([]Standing{clean("a", 1, 1), clean("b", 1, 1), clean("c", 1, 1), clean("d", 1, 1), clean("e", 1, 1), clean("f", 1, 1), clean("g", 1, 1), clean("h", 1, 1), clean("i", 1, 1)}, started("j")), badgeWant{state: BadgeInProgress, lit: 4, clean: 9, total: 10, variants: 9, caption: "9 of 10 assignments solved clean"}},
		{"every assignment clean", []Standing{clean("a", 1, 5), clean("b", 2, 3)}, badgeWant{state: BadgeEarned, lit: 5, clean: 2, total: 2, pips: 1, variants: 3, caption: "Every assignment solved clean", earnedAt: at(5)}},
		{"clean and partly mastered is earned", []Standing{master("a", 3, 1), clean("b", 3, 2)}, badgeWant{state: BadgeEarned, lit: 5, clean: 2, total: 2, pips: 3, variants: 6, caption: "Every assignment solved clean", earnedAt: at(2)}},
		{"every assignment mastered on 3 variants", []Standing{master("a", 3, 1), master("b", 4, 2)}, badgeWant{state: BadgeMastered, lit: 5, clean: 2, total: 2, pips: 3, variants: 7, caption: "Every assignment, 3 variants clean", earnedAt: at(2)}},
		{"mastered with a two-variant assignment", []Standing{master("a", 3, 1), master("b", 2, 2)}, badgeWant{state: BadgeMastered, lit: 5, clean: 2, total: 2, pips: 2, variants: 5, caption: "Every assignment mastered", earnedAt: at(2)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkBadge(t, ModuleBadge(tc.in), tc.want)
		})
	}
}

func TestLevelBadge(t *testing.T) {
	locked := ModuleBadge([]Standing{untouched("a")})
	running := ModuleBadge([]Standing{started("a"), untouched("b")})
	partial := ModuleBadge([]Standing{clean("a", 1, 1), untouched("b")})
	earned := ModuleBadge([]Standing{clean("c", 2, 4)})
	mastered := ModuleBadge([]Standing{master("d", 3, 6)})
	planned := ModuleBadge(nil)
	tests := []struct {
		name string
		in   []Badge
		want badgeWant
	}{
		{"no module with assignments", []Badge{planned}, badgeWant{state: BadgeLocked, caption: "No assignment written yet"}},
		{"every module locked", []Badge{locked, locked, planned}, badgeWant{state: BadgeLocked, total: 2, caption: "Not started: no run finished in its 2 modules"}},
		{"a module started without a clean solve", []Badge{running, locked}, badgeWant{state: BadgeInProgress, total: 2, caption: "0 of 2 module badges earned"}},
		{"a clean solve lights an arc before any module badge", []Badge{partial, locked, locked}, badgeWant{state: BadgeInProgress, lit: 1, total: 3, variants: 1, caption: "0 of 3 module badges earned"}},
		{"2 of 5 module badges", []Badge{earned, mastered, partial, locked, locked}, badgeWant{state: BadgeInProgress, lit: 2, clean: 2, total: 5, variants: 6, caption: "2 of 5 module badges earned"}},
		{"9 of 10 module badges lights 4 arcs", []Badge{earned, earned, earned, earned, earned, earned, earned, earned, earned, running}, badgeWant{state: BadgeInProgress, lit: 4, clean: 9, total: 10, variants: 18, caption: "9 of 10 module badges earned"}},
		{"every module earned", []Badge{earned, mastered, planned}, badgeWant{state: BadgeEarned, lit: 5, clean: 2, total: 2, pips: 2, variants: 5, caption: "Every module badge earned", earnedAt: at(6)}},
		{"every module mastered", []Badge{mastered, mastered}, badgeWant{state: BadgeMastered, lit: 5, clean: 2, total: 2, pips: 3, variants: 6, caption: "Every module badge mastered", earnedAt: at(6)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkBadge(t, LevelBadge(tc.in), tc.want)
		})
	}
}

func TestTrackBadge(t *testing.T) {
	tests := []struct {
		name string
		in   [][]Standing
		want badgeWant
	}{
		{"no domain", nil, badgeWant{state: BadgeLocked, caption: "No exam domain listed"}},
		{"assignments run, none clean", [][]Standing{{started("a")}, {untouched("b")}}, badgeWant{state: BadgeLocked, total: 2, caption: "No domain covered by a clean solve yet"}},
		{"domains without assignments are named", [][]Standing{{untouched("a")}, {}, {}, {}, {}}, badgeWant{state: BadgeLocked, total: 5, caption: "No domain covered by a clean solve yet; 1 of 5 domains have assignments yet"}},
		{"2 of 5 covered", [][]Standing{{clean("a", 1, 1)}, {started("b"), clean("c", 2, 3)}, {untouched("d")}, {untouched("e")}, {untouched("f")}}, badgeWant{state: BadgeInProgress, lit: 2, clean: 2, total: 5, variants: 3, caption: "2 of 5 domains covered by clean solves"}},
		{"every written domain covered is not earned while a domain has none", [][]Standing{{clean("a", 1, 1)}, {clean("b", 1, 2)}, {clean("c", 1, 3)}, {}, {}}, badgeWant{state: BadgeInProgress, lit: 3, clean: 3, total: 5, variants: 3, caption: "3 of 5 domains covered by clean solves; 3 of 5 domains have assignments yet"}},
		{"9 of 10 domains covered lights 4 arcs", [][]Standing{{clean("a", 1, 1)}, {clean("b", 1, 1)}, {clean("c", 1, 1)}, {clean("d", 1, 1)}, {clean("e", 1, 1)}, {clean("f", 1, 1)}, {clean("g", 1, 1)}, {clean("h", 1, 1)}, {clean("i", 1, 1)}, {untouched("j")}}, badgeWant{state: BadgeInProgress, lit: 4, clean: 9, total: 10, variants: 9, caption: "9 of 10 domains covered by clean solves"}},
		{"every domain covered", [][]Standing{{clean("a", 2, 1), untouched("b")}, {clean("c", 1, 7), clean("d", 3, 2)}}, badgeWant{state: BadgeEarned, lit: 5, clean: 2, total: 2, pips: 2, variants: 6, caption: "Every domain covered by clean solves", earnedAt: at(2)}},
		{"an assignment in two domains counts its variants once", [][]Standing{{master("a", 3, 1)}, {master("a", 3, 1)}}, badgeWant{state: BadgeMastered, lit: 5, clean: 2, total: 2, pips: 3, variants: 3, caption: "Every domain covered by a mastered assignment", earnedAt: at(1)}},
		{"a mastered assignment in every domain", [][]Standing{{master("a", 3, 1), started("b")}, {clean("c", 1, 2), master("d", 3, 4)}}, badgeWant{state: BadgeMastered, lit: 5, clean: 2, total: 2, pips: 3, variants: 7, caption: "Every domain covered by a mastered assignment", earnedAt: at(2)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkBadge(t, TrackBadge(tc.in), tc.want)
		})
	}
}
