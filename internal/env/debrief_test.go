package env

import (
	"sync"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

type racingJournal struct {
	*journal.Store
	once  sync.Once
	other func()
}

func (j *racingJournal) Episodes(scenarioID string) ([]journal.Episode, error) {
	eps, err := j.Store.Episodes(scenarioID)
	j.once.Do(j.other)
	return eps, err
}

func (j *racingJournal) ModifyEnded(id string, fn func(*journal.Episode) error) (*journal.Episode, error) {
	j.once.Do(j.other)
	return j.Store.ModifyEnded(id, fn)
}

func endedEpisode(t *testing.T, store *journal.Store) string {
	t.Helper()
	if err := store.Start(journal.Episode{ID: "001-hello-1-abcde", ScenarioID: "001-hello", Seed: 1, StartedAt: t0}); err != nil {
		t.Fatal(err)
	}
	ep, err := store.Finish(journal.OutcomeSolved)
	if err != nil {
		t.Fatal(err)
	}
	return ep.ID
}

func TestRecordDebriefKeepsConcurrentEndedUpdate(t *testing.T) {
	store, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := endedEpisode(t, store)
	j := &racingJournal{Store: store}
	j.other = func() {
		if _, err := store.ModifyEnded(id, func(e *journal.Episode) error {
			e.Telemetry = &journal.Telemetry{Source: "audit", Writes: 2}
			return nil
		}); err != nil {
			t.Errorf("concurrent ModifyEnded: %v", err)
		}
	}
	s := New(Deps{Journal: j, Now: func() time.Time { return t0 }})
	if err := s.RecordDebrief(id, journal.Debrief{NextTimeFirst: "events"}, nil); err != nil {
		t.Fatalf("RecordDebrief: %v", err)
	}
	eps, err := store.Episodes("")
	if err != nil || len(eps) != 1 {
		t.Fatalf("Episodes: got %d, %v, want 1 episode", len(eps), err)
	}
	if eps[0].Debrief == nil || eps[0].Debrief.NextTimeFirst != "events" {
		t.Errorf("debrief: got %+v, want next_time_first events", eps[0].Debrief)
	}
	if eps[0].Telemetry == nil || eps[0].Telemetry.Writes != 2 {
		t.Errorf("telemetry: got %+v, want the concurrent update with 2 writes", eps[0].Telemetry)
	}
}

func TestRecordDebriefConcurrentSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := endedEpisode(t, store)
	other, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	s := New(Deps{Journal: store, Now: func() time.Time { return t0 }})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = s.RecordDebrief(id, journal.Debrief{NextTimeFirst: "events"}, &yes)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = other.ModifyEnded(id, func(e *journal.Episode) error {
			e.Telemetry = &journal.Telemetry{Source: "audit", Writes: 2}
			return nil
		})
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	eps, err := store.Episodes("")
	if err != nil || len(eps) != 1 {
		t.Fatalf("Episodes: got %d, %v, want 1 episode", len(eps), err)
	}
	if eps[0].Debrief == nil || eps[0].Telemetry == nil {
		t.Errorf("episode: got debrief %+v telemetry %+v, want both set", eps[0].Debrief, eps[0].Telemetry)
	}
}
