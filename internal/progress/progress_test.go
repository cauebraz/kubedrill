package progress

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type epOpt func(*journal.Episode)

func hints(n int) epOpt { return func(e *journal.Episode) { e.HintsUsed = n } }
func took(d time.Duration) epOpt {
	return func(e *journal.Episode) { end := e.StartedAt.Add(d); e.EndedAt = &end }
}
func outcome(o journal.Outcome) epOpt {
	return func(e *journal.Episode) { e.Outcome = o }
}
func tele(reads, readsBefore, writes int, verdict string) epOpt {
	return func(e *journal.Episode) {
		e.Telemetry = &journal.Telemetry{Reads: reads, ReadsBeforeFirstWrite: readsBefore, Writes: writes, Verdict: verdict}
	}
}
func active() epOpt {
	return func(e *journal.Episode) { e.Outcome = journal.OutcomeActive; e.EndedAt = nil }
}

func ep(scenario string, seed int64, start time.Duration, opts ...epOpt) journal.Episode {
	e := journal.Episode{ID: scenario + "-" + start.String(), ScenarioID: scenario, Seed: seed, StartedAt: t0.Add(start), Outcome: journal.OutcomeSolved, Mode: journal.ModePractice}
	end := e.StartedAt.Add(5 * time.Minute)
	e.EndedAt = &end
	for _, o := range opts {
		o(&e)
	}
	return e
}

func TestIsClean(t *testing.T) {
	tests := []struct {
		name string
		e    journal.Episode
		want bool
	}{
		{"solved fast without hints", ep("s", 1, 0), true},
		{"exam solve", func() journal.Episode { e := ep("s", 1, 0); e.Mode = journal.ModeExam; return e }(), true},
		{"hinted", ep("s", 1, 0, hints(1)), false},
		{"over the time box", ep("s", 1, 0, took(11*time.Minute)), false},
		{"exactly the time box", ep("s", 1, 0, took(10*time.Minute)), true},
		{"abandoned", ep("s", 1, 0, outcome(journal.OutcomeAbandoned)), false},
		{"failed", ep("s", 1, 0, outcome(journal.OutcomeFailed)), false},
		{"timed out", ep("s", 1, 0, outcome(journal.OutcomeTimedOut)), false},
		{"active", ep("s", 1, 0, active()), false},
		{"write before any read", ep("s", 1, 0, tele(3, 0, 1, "root-cause")), false},
		{"read before write", ep("s", 1, 0, tele(3, 1, 1, "root-cause")), true},
		{"symptom-only still clean when read first", ep("s", 1, 0, tele(3, 2, 1, "symptom-only")), true},
		{"no writes", ep("s", 1, 0, tele(0, 0, 0, "no-writes")), true},
		{"telemetry unknown is not held against", ep("s", 1, 0, tele(0, 0, 0, "unknown")), true},
		{"unknown verdict ignores a write before any read", ep("s", 1, 0, tele(0, 0, 2, "unknown")), true},
		{"empty verdict ignores a write before any read", ep("s", 1, 0, tele(0, 0, 2, "")), true},
		{"no telemetry", ep("s", 1, 0), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsClean(tc.e, 10); got != tc.want {
				t.Errorf("IsClean: got %v, want %v", got, tc.want)
			}
		})
	}
	if !IsClean(ep("s", 1, 0, took(5*time.Hour)), 0) {
		t.Errorf("IsClean with minutes 0: got false, want true (no limit)")
	}
}

func TestScenarioMastery(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name string
		eps  []journal.Episode
		want Mastery
	}{
		{
			name: "three clean on distinct seeds over two sessions",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, time.Hour), ep("s", 3, day)},
			want: Mastery{Attempts: 3, Solved: 3, Clean: 3, Last: 3, CleanInLast: 3, Sessions: 2, Mastered: true},
		},
		{
			name: "same session is not enough",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, time.Hour), ep("s", 3, 2*time.Hour)},
			want: Mastery{Attempts: 3, Solved: 3, Clean: 3, Last: 3, CleanInLast: 3, Sessions: 1},
		},
		{
			name: "a gap just under 3h stays in the session",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, 3*time.Hour), ep("s", 3, 6*time.Hour)},
			want: Mastery{Attempts: 3, Solved: 3, Clean: 3, Last: 3, CleanInLast: 3, Sessions: 1},
		},
		{
			name: "a gap of exactly 3h after the previous end starts a session",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, 3*time.Hour+5*time.Minute), ep("s", 3, 3*time.Hour+10*time.Minute)},
			want: Mastery{Attempts: 3, Solved: 3, Clean: 3, Last: 3, CleanInLast: 3, Sessions: 2, Mastered: true},
		},
		{
			name: "repeated seed does not count twice",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 1, day), ep("s", 2, 2*day)},
			want: Mastery{Attempts: 3, Solved: 3, Clean: 3, Last: 3, CleanInLast: 3, Sessions: 3},
		},
		{
			name: "a hinted attempt counts against without resetting",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, day), ep("s", 3, 2*day, hints(2)), ep("s", 4, 3*day)},
			want: Mastery{Attempts: 4, Solved: 4, Clean: 3, Last: 4, CleanInLast: 3, Sessions: 3, Mastered: true},
		},
		{
			name: "two misses in the window block mastery",
			eps: []journal.Episode{
				ep("s", 1, 0), ep("s", 2, day), ep("s", 3, 2*day, hints(1)),
				ep("s", 4, 3*day, outcome(journal.OutcomeFailed)), ep("s", 5, 4*day),
			},
			want: Mastery{Attempts: 5, Solved: 4, Clean: 3, Last: 4, CleanInLast: 2, Sessions: 2},
		},
		{
			name: "old clean solves leave the window",
			eps: []journal.Episode{
				ep("s", 1, 0), ep("s", 2, day), ep("s", 3, 2*day),
				ep("s", 4, 3*day, outcome(journal.OutcomeAbandoned)), ep("s", 5, 4*day, outcome(journal.OutcomeTimedOut)),
			},
			want: Mastery{Attempts: 5, Solved: 3, Clean: 3, Last: 4, CleanInLast: 2, Sessions: 2},
		},
		{
			name: "active episode is ignored",
			eps:  []journal.Episode{ep("s", 1, 0), ep("s", 2, day, active())},
			want: Mastery{Attempts: 1, Solved: 1, Clean: 1, Last: 1, CleanInLast: 1, Sessions: 1},
		},
		{
			name: "telemetry write-first solves are not clean",
			eps:  []journal.Episode{ep("s", 1, 0, tele(2, 0, 1, "root-cause")), ep("s", 2, day), ep("s", 3, 2*day), ep("s", 4, 3*day)},
			want: Mastery{Attempts: 4, Solved: 4, Clean: 3, Last: 4, CleanInLast: 3, Sessions: 3, Mastered: true},
		},
		{
			name: "unordered input is sorted by start",
			eps:  []journal.Episode{ep("s", 3, 2*day), ep("s", 1, 0, outcome(journal.OutcomeFailed)), ep("s", 2, day)},
			want: Mastery{Attempts: 3, Solved: 2, Clean: 2, Last: 3, CleanInLast: 2, Sessions: 2},
		},
		{name: "no episodes", want: Mastery{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScenarioMastery(tc.eps, 10)
			got.LastSolved, got.MedianSolve = nil, 0
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mastery:\ngot  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestScenarioMasteryTimes(t *testing.T) {
	eps := []journal.Episode{
		ep("s", 1, 0, took(4*time.Minute)),
		ep("s", 2, time.Hour, took(8*time.Minute)),
		ep("s", 3, 2*time.Hour, took(6*time.Minute), outcome(journal.OutcomeFailed)),
		ep("s", 4, 3*time.Hour, took(10*time.Minute)),
		ep("s", 5, 4*time.Hour, took(2*time.Minute)),
	}
	m := ScenarioMastery(eps, 10)
	if m.MedianSolve != 6*time.Minute {
		t.Errorf("median of 4, 8, 10, 2 min: got %v, want 6m", m.MedianSolve)
	}
	want := t0.Add(4*time.Hour + 2*time.Minute)
	if m.LastSolved == nil || !m.LastSolved.Equal(want) {
		t.Errorf("LastSolved: got %v, want %v", m.LastSolved, want)
	}
	odd := ScenarioMastery(eps[:2], 10)
	if odd.MedianSolve != 6*time.Minute {
		t.Errorf("median of 4 and 8 min: got %v, want 6m", odd.MedianSolve)
	}
	if one := ScenarioMastery(eps[:1], 10); one.MedianSolve != 4*time.Minute {
		t.Errorf("median of one: got %v, want 4m", one.MedianSolve)
	}
}

var testCatalog = []ScenarioInfo{
	{ID: "000-hello", Level: "L0", Concepts: []string{"image-pull"}, Minutes: 10},
	{ID: "001-probe", Level: "L0", Concepts: []string{"readiness-and-endpoints", "pod-lifecycle"}, Minutes: 15},
	{ID: "002-dns", Level: "L0", Concepts: []string{"readiness-and-endpoints"}, Minutes: 10},
	{ID: "100-rollout", Level: "L1", Concepts: []string{"rollouts"}, Minutes: 20},
}

func TestConceptMastery(t *testing.T) {
	day := 24 * time.Hour
	eps := []journal.Episode{
		ep("001-probe", 1, 0),
		ep("002-dns", 1, day),
		ep("001-probe", 2, 2*day, took(16*time.Minute)),
		ep("002-dns", 2, 3*day),
		ep("999-gone", 1, 0),
	}
	got := ConceptMastery(testCatalog, eps)
	if len(got) != 4 {
		t.Fatalf("concepts: got %d (%v), want 4", len(got), got)
	}
	re := got["readiness-and-endpoints"]
	if re.Attempts != 4 || re.Clean != 3 || re.CleanInLast != 3 || !re.Mastered {
		t.Errorf("readiness-and-endpoints: got %+v, want 4 attempts, 3 clean (001 over 15 min is not), mastered (same seed number in two scenarios counts twice)", re)
	}
	pl := got["pod-lifecycle"]
	if pl.Attempts != 2 || pl.Clean != 1 || pl.Mastered {
		t.Errorf("pod-lifecycle: got %+v, want 2 attempts 1 clean", pl)
	}
	if ip := got["image-pull"]; ip.Attempts != 0 || ip.Mastered {
		t.Errorf("image-pull: got %+v, want zero", ip)
	}
}

func TestFirstExposure(t *testing.T) {
	eps := []journal.Episode{
		ep("000-hello", 1, 0, outcome(journal.OutcomeFailed)),
		ep("002-dns", 1, time.Hour, active()),
	}
	got := FirstExposure([]string{"readiness-and-endpoints", "pod-lifecycle", "image-pull", "pod-lifecycle", "unknown"}, testCatalog, eps)
	want := []string{"readiness-and-endpoints", "pod-lifecycle", "unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FirstExposure: got %v, want %v (failed attempts count as exposure, active ones do not)", got, want)
	}
	if got := FirstExposure([]string{"image-pull"}, testCatalog, eps); got != nil {
		t.Errorf("FirstExposure of a seen concept: got %v, want nil", got)
	}
}

func seq(values ...int64) func(n int64) int64 {
	i := 0
	return func(n int64) int64 {
		v := values[i%len(values)]
		i++
		return v % n
	}
}

func mastered(id string, start time.Duration) []journal.Episode {
	day := 24 * time.Hour
	return []journal.Episode{ep(id, 11, start), ep(id, 12, start+day), ep(id, 13, start+2*day)}
}

func TestNextPractice(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name       string
		cat        []ScenarioInfo
		eps        []journal.Episode
		rnd        func(int64) int64
		wantID     string
		wantSeed   int64
		wantReason string
		wantOK     bool
	}{
		{
			name: "nothing attempted picks the lowest id of the lowest level",
			cat:  testCatalog, rnd: seq(41),
			wantID: "000-hello", wantSeed: 42, wantReason: "practice: level L0, new scenario", wantOK: true,
		},
		{
			name: "half solved beats always solved",
			cat:  testCatalog,
			eps: []journal.Episode{
				ep("000-hello", 1, 0), ep("000-hello", 2, time.Hour, hints(1)),
				ep("001-probe", 1, 0), ep("001-probe", 2, time.Hour, outcome(journal.OutcomeFailed)),
				ep("002-dns", 1, 0, outcome(journal.OutcomeFailed)), ep("002-dns", 2, time.Hour, outcome(journal.OutcomeFailed)),
			},
			rnd:    seq(0),
			wantID: "001-probe", wantSeed: 1, wantReason: "practice: level L0, solved 1 of last 2", wantOK: true,
		},
		{
			name: "unattempted ties with half solved and loses to the lower id",
			cat:  testCatalog,
			eps: []journal.Episode{
				ep("000-hello", 1, 0), ep("000-hello", 2, time.Hour, hints(1)), ep("000-hello", 3, 2*time.Hour),
				ep("001-probe", 1, 0), ep("001-probe", 2, time.Hour, outcome(journal.OutcomeFailed)),
			},
			rnd:    seq(7),
			wantID: "001-probe", wantSeed: 8, wantReason: "practice: level L0, solved 1 of last 2", wantOK: true,
		},
		{
			name:   "mastered level moves to the next one",
			cat:    testCatalog,
			eps:    append(append(mastered("000-hello", 0), mastered("001-probe", 0)...), mastered("002-dns", 0)...),
			rnd:    seq(99),
			wantID: "100-rollout", wantSeed: 100, wantReason: "practice: level L1, new scenario", wantOK: true,
		},
		{
			name:   "used seeds are redrawn",
			cat:    []ScenarioInfo{{ID: "000-hello", Level: "L0", Minutes: 10}},
			eps:    []journal.Episode{ep("000-hello", 5, 0, outcome(journal.OutcomeFailed)), ep("000-hello", 6, day, outcome(journal.OutcomeFailed))},
			rnd:    seq(4, 5, 9),
			wantID: "000-hello", wantSeed: 10, wantReason: "practice: level L0, solved 0 of last 2", wantOK: true,
		},
		{
			name: "everything mastered and nothing due",
			cat:  []ScenarioInfo{{ID: "000-hello", Level: "L0", Minutes: 10}},
			eps:  mastered("000-hello", 0),
			rnd:  seq(1),
		},
		{name: "empty catalog", rnd: seq(1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := t0.Add(2*day + time.Hour)
			r, ok := Next(tc.cat, tc.eps, now, tc.rnd)
			if ok != tc.wantOK {
				t.Fatalf("Next ok: got %v (%+v), want %v", ok, r, tc.wantOK)
			}
			if !ok {
				return
			}
			want := Recommendation{ScenarioID: tc.wantID, Seed: tc.wantSeed, Reason: tc.wantReason}
			if r != want {
				t.Errorf("Next:\ngot  %+v\nwant %+v", r, want)
			}
			if r.Seed < 1 || r.Seed > PracticeSeedMax {
				t.Errorf("practice seed %d outside 1..%d", r.Seed, PracticeSeedMax)
			}
		})
	}
}

func TestNextDeterministic(t *testing.T) {
	eps := []journal.Episode{ep("000-hello", 1, 0, outcome(journal.OutcomeFailed))}
	a, _ := Next(testCatalog, eps, t0, seq(123, 456))
	b, _ := Next(testCatalog, eps, t0, seq(123, 456))
	if a != b {
		t.Errorf("Next with the same rnd: got %+v and %+v, want equal", a, b)
	}
}

func TestNextReview(t *testing.T) {
	day := 24 * time.Hour
	cat := []ScenarioInfo{
		{ID: "000-hello", Level: "L0", Concepts: []string{"image-pull"}, Minutes: 10},
		{ID: "003-pull-secret", Level: "L1", Concepts: []string{"image-pull"}, Minutes: 10},
	}
	base := mastered("000-hello", 0)
	lastClean := base[2].EndedAt
	review := func(seed int64, after time.Duration, opts ...epOpt) journal.Episode {
		return ep("000-hello", seed, lastClean.Sub(t0)+after, opts...)
	}
	tests := []struct {
		name     string
		eps      []journal.Episode
		now      time.Time
		wantRev  bool
		wantID   string
		interval string
	}{
		{name: "not due before one day", eps: base, now: lastClean.Add(day - time.Minute), wantRev: false},
		{name: "due after one day, least recently attempted scenario", eps: base, now: lastClean.Add(day), wantRev: true, wantID: "003-pull-secret"},
		{
			name:    "one clean review doubles the interval to two days",
			eps:     append(append([]journal.Episode(nil), base...), review(600_000, day)),
			now:     lastClean.Add(day + 5*time.Minute + 2*day - time.Minute),
			wantRev: false,
		},
		{
			name:    "due again after two days",
			eps:     append(append([]journal.Episode(nil), base...), review(600_000, day)),
			now:     lastClean.Add(day + 5*time.Minute + 2*day),
			wantRev: true, wantID: "003-pull-secret",
		},
		{
			name:    "two clean reviews make it four days",
			eps:     append(append([]journal.Episode(nil), base...), review(600_000, day), review(700_000, 4*day)),
			now:     lastClean.Add(4*day + 5*time.Minute + 4*day - time.Minute),
			wantRev: false,
		},
		{
			name:    "a hinted review does not extend the interval",
			eps:     append(append([]journal.Episode(nil), base...), review(600_000, day, hints(1))),
			now:     lastClean.Add(day + time.Hour),
			wantRev: true, wantID: "003-pull-secret",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := Next(cat, tc.eps, tc.now, seq(250_000))
			if !ok {
				t.Fatalf("Next: got none, want a recommendation")
			}
			if r.Review != tc.wantRev {
				t.Fatalf("Review: got %+v, want review %v", r, tc.wantRev)
			}
			if !tc.wantRev {
				if r.ScenarioID != "003-pull-secret" || r.Review {
					t.Errorf("practice fallback: got %+v, want practice of 003-pull-secret", r)
				}
				return
			}
			if r.ScenarioID != tc.wantID || r.Seed != 750_000 || !strings.HasPrefix(r.Reason, "review: concept image-pull") {
				t.Errorf("review: got %+v, want %s seed 750000 naming image-pull", r, tc.wantID)
			}
		})
	}
	seeds := map[int64]bool{}
	for _, v := range []int64{0, 499_999, 123} {
		r, _ := Next(cat, base, lastClean.Add(2*day), seq(v))
		if r.Seed <= PracticeSeedMax || r.Seed > ReviewSeedMax {
			t.Errorf("review seed %d outside %d..%d", r.Seed, PracticeSeedMax+1, ReviewSeedMax)
		}
		seeds[r.Seed] = true
	}
	if !seeds[500_000] || !seeds[999_999] {
		t.Errorf("review seed range ends: got %v, want 500000 and 999999 reachable", seeds)
	}
}

func TestStats(t *testing.T) {
	eps := []journal.Episode{
		ep("000-hello", 1, 0, took(4*time.Minute), tele(3, 2, 1, "root-cause")),
		ep("000-hello", 2, time.Hour, took(8*time.Minute), hints(3), tele(1, 0, 2, "symptom-only")),
		ep("001-probe", 1, 2*time.Hour, outcome(journal.OutcomeAbandoned), hints(4)),
		ep("100-rollout", 1, 3*time.Hour, outcome(journal.OutcomeTimedOut)),
		ep("999-gone", 1, 4*time.Hour, took(30*time.Minute)),
		ep("002-dns", 1, 5*time.Hour, active()),
	}
	got := Stats(testCatalog, eps)
	wantLevels := map[string]LevelStats{"L0": {Attempts: 3, Solved: 2}, "L1": {Attempts: 1}}
	if !reflect.DeepEqual(got.ByLevel, wantLevels) {
		t.Errorf("ByLevel: got %v, want %v", got.ByLevel, wantLevels)
	}
	if got.MedianSolve != 8*time.Minute {
		t.Errorf("MedianSolve of 4, 8, 30 min: got %v, want 8m", got.MedianSolve)
	}
	if got.HintsPerSolve != 1 {
		t.Errorf("HintsPerSolve: got %v, want 1 (3 hints over 3 solves)", got.HintsPerSolve)
	}
	wantOutcomes := map[journal.Outcome]int{journal.OutcomeSolved: 3, journal.OutcomeAbandoned: 1, journal.OutcomeTimedOut: 1}
	if !reflect.DeepEqual(got.Outcomes, wantOutcomes) {
		t.Errorf("Outcomes: got %v, want %v", got.Outcomes, wantOutcomes)
	}
	wantVerdicts := map[string]int{"root-cause": 1, "symptom-only": 1}
	if !reflect.DeepEqual(got.Verdicts, wantVerdicts) {
		t.Errorf("Verdicts: got %v, want %v", got.Verdicts, wantVerdicts)
	}
	if got.Concepts["image-pull"].Attempts != 2 || got.Concepts["image-pull"].Clean != 1 {
		t.Errorf("Concepts[image-pull]: got %+v, want 2 attempts 1 clean", got.Concepts["image-pull"])
	}
	empty := Stats(nil, nil)
	if empty.MedianSolve != 0 || empty.HintsPerSolve != 0 || len(empty.ByLevel) != 0 {
		t.Errorf("Stats of nothing: got %+v, want zero", empty)
	}
}

func TestIsReviewSeed(t *testing.T) {
	tests := []struct {
		seed int64
		want bool
	}{{0, false}, {1, false}, {PracticeSeedMax, false}, {PracticeSeedMax + 1, true}, {ReviewSeedMax, true}, {ReviewSeedMax + 1, false}}
	for _, tt := range tests {
		if got := IsReviewSeed(tt.seed); got != tt.want {
			t.Errorf("IsReviewSeed(%d): got %v, want %v", tt.seed, got, tt.want)
		}
	}
}

func TestNextReviewAvoidsUsedSeeds(t *testing.T) {
	day := 24 * time.Hour
	cat := []ScenarioInfo{{ID: "000-hello", Level: "L0", Concepts: []string{"image-pull"}, Minutes: 10}}
	eps := mastered("000-hello", 0)
	eps = append(eps, ep("000-hello", 750_000, 3*day, outcome(journal.OutcomeFailed)))
	r, ok := Next(cat, eps, t0.Add(30*day), seq(250_000, 250_000, 300_000))
	if !ok || !r.Review {
		t.Fatalf("Next: got %+v, %v, want a review", r, ok)
	}
	if r.Seed != 800_000 {
		t.Errorf("review seed: got %d, want 800000 (750000 is already used by the scenario)", r.Seed)
	}
}

func TestFirstExposureIgnoresInstantRestarts(t *testing.T) {
	diag := func(e *journal.Episode) { e.Diagnosis = &journal.Diagnosis{Text: "the image tag is wrong"} }
	tests := []struct {
		name string
		e    journal.Episode
		want []string
	}{
		{"failed after 10s, nothing used", ep("000-hello", 1, 0, outcome(journal.OutcomeFailed), took(10*time.Second)), []string{"image-pull"}},
		{"failed after 60s", ep("000-hello", 1, 0, outcome(journal.OutcomeFailed), took(60*time.Second)), nil},
		{"failed fast with a hint", ep("000-hello", 1, 0, outcome(journal.OutcomeFailed), took(10*time.Second), hints(1)), nil},
		{"failed fast with a diagnosis", ep("000-hello", 1, 0, outcome(journal.OutcomeFailed), took(10*time.Second), diag), nil},
		{"solved fast", ep("000-hello", 1, 0, took(10*time.Second)), nil},
	}
	for _, tt := range tests {
		if got := FirstExposure([]string{"image-pull"}, testCatalog, []journal.Episode{tt.e}); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNextReviewTieBreaksByConcept(t *testing.T) {
	cat := []ScenarioInfo{
		{ID: "000-hello", Level: "L0", Concepts: []string{"zeta", "alpha", "mid"}, Minutes: 10},
	}
	eps := mastered("000-hello", 0)
	now := eps[len(eps)-1].EndedAt.Add(48 * time.Hour)
	for i := 0; i < 5; i++ {
		r, ok := nextReview(cat, eps, now, seq(250_000))
		if !ok || !strings.HasPrefix(r.Reason, "review: concept alpha ") {
			t.Fatalf("nextReview with equal overdue: got %+v, %v, want concept alpha (lowest id)", r, ok)
		}
	}
}
