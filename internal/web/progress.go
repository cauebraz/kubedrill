package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
)

// StageProgress is the learner's progress through one assignment.
type StageProgress struct {
	Meeting  StageResult `json:"meeting"`
	Quiz     StageResult `json:"quiz"`
	Learn    StageResult `json:"learn"`
	Practice StageResult `json:"practice"`
	DIY      StageResult `json:"diy"`
	Exam     StageResult `json:"exam"`
}

// StageResult records a completed stage and its best score.
type StageResult struct {
	Done  bool      `json:"done"`
	Score int       `json:"score,omitempty"`
	Total int       `json:"total,omitempty"`
	At    time.Time `json:"at,omitzero"`
}

// progressFile is the on-disk shape: progress per scenario, the stage of
// each episode the workspace started and when the welcome flow was done.
type progressFile struct {
	Assignments map[string]StageProgress `json:"assignments"`
	Episodes    map[string]Stage         `json:"episodes"`
	Onboarded   time.Time                `json:"onboarded,omitzero"`
}

// progressStore reads and writes the progress file on every call; the
// server serializes the calls.
type progressStore struct {
	path string
	now  func() time.Time
}

func (p *progressStore) load() (progressFile, error) {
	f := progressFile{Assignments: map[string]StageProgress{}, Episodes: map[string]Stage{}}
	if p.path == "" {
		return f, nil
	}
	b, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read progress %s: %w", p.path, err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("read progress %s: %w", p.path, err)
	}
	if f.Assignments == nil {
		f.Assignments = map[string]StageProgress{}
	}
	if f.Episodes == nil {
		f.Episodes = map[string]Stage{}
	}
	return f, nil
}

func (p *progressStore) save(f progressFile) error {
	if p.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(p.path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write progress %s: %w", p.path, err)
	}
	return nil
}

// all returns the progress per scenario; an unreadable file reads as empty.
func (p *progressStore) all() map[string]StageProgress {
	f, _ := p.load()
	return f.Assignments
}

// onboarded reports whether the welcome flow was completed or skipped. An
// unreadable progress file counts as done, so it never locks the learner
// on the welcome page; the page reports the file when it saves progress.
func (p *progressStore) onboarded() bool {
	f, err := p.load()
	return err != nil || !f.Onboarded.IsZero()
}

// onboard records the welcome flow as done.
func (p *progressStore) onboard() error {
	f, err := p.load()
	if err != nil {
		return err
	}
	f.Onboarded = p.now().UTC()
	return p.save(f)
}

func (p *progressStore) stageOf(episodeID string) Stage {
	f, _ := p.load()
	return f.Episodes[episodeID]
}

func (p *progressStore) tagEpisode(episodeID string, stage Stage) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	f.Episodes[episodeID] = stage
	return p.save(f)
}

// complete marks stage of scenario id done, keeping the best score.
func (p *progressStore) complete(id string, stage Stage, score, total int) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	sp := f.Assignments[id]
	var r *StageResult
	switch stage {
	case StageMeeting:
		r = &sp.Meeting
	case StageQuiz:
		r = &sp.Quiz
	case StageLearn:
		r = &sp.Learn
	case StagePractice:
		r = &sp.Practice
	case StageDIY:
		r = &sp.DIY
	case StageExam:
		r = &sp.Exam
	default:
		return fmt.Errorf("record progress: unknown stage %q", stage)
	}
	if !r.Done || total == 0 || r.Total == 0 || score*r.Total > r.Score*total {
		r.Score, r.Total = score, total
	}
	r.Done = true
	r.At = p.now().UTC()
	f.Assignments[id] = sp
	return p.save(f)
}

// results lists the stage results of sp in the order of the stages.
func results(sp *StageProgress) []*StageResult {
	return []*StageResult{&sp.Meeting, &sp.Quiz, &sp.Learn, &sp.Practice, &sp.DIY, &sp.Exam}
}

// anyDone reports whether a stage of sp is done.
func anyDone(sp StageProgress) bool {
	for _, r := range results(&sp) {
		if r.Done {
			return true
		}
	}
	return false
}

// mergeStages returns cur with each stage that is not done taken from old.
func mergeStages(cur, old StageProgress) StageProgress {
	c, o := results(&cur), results(&old)
	for i := range c {
		if !c[i].Done && o[i].Done {
			*c[i] = *o[i]
		}
	}
	return cur
}

// decodeStages reads a snapshot of stage progress: scenario id to its
// stages. An empty snapshot reads as none.
func decodeStages(raw json.RawMessage) (map[string]StageProgress, error) {
	out := map[string]StageProgress{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode saved stage progress: %w", err)
	}
	return out, nil
}

// snapshot is the stage progress of id (every assignment when id is empty)
// merged over prev, the snapshot an earlier reset of the same scope saved,
// as a reset marker keeps it: scenario id to its stages, only those with a
// stage done. It is nil when nothing is done.
func (p *progressStore) snapshot(id string, prev json.RawMessage) (json.RawMessage, error) {
	f, err := p.load()
	if err != nil {
		return nil, err
	}
	out, err := decodeStages(prev)
	if err != nil {
		return nil, err
	}
	for sid, sp := range f.Assignments {
		if id != "" && sid != id {
			continue
		}
		out[sid] = mergeStages(sp, out[sid])
	}
	for sid, sp := range out {
		if !anyDone(sp) {
			delete(out, sid)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode stage progress: %w", err)
	}
	return b, nil
}

// clear removes the stage progress of id (every assignment when id is
// empty) and, with welcome, the welcome flag, so the welcome flow shows
// again. It reports whether a stage was done.
func (p *progressStore) clear(id string, welcome bool) (bool, error) {
	f, err := p.load()
	if err != nil {
		return false, err
	}
	cleared := false
	for sid, sp := range f.Assignments {
		if id != "" && sid != id {
			continue
		}
		cleared = cleared || anyDone(sp)
		delete(f.Assignments, sid)
	}
	if welcome {
		f.Onboarded = time.Time{}
	}
	if !cleared && !welcome {
		return false, nil
	}
	return cleared, p.save(f)
}

// restore puts saved, a snapshot, back: each stage not done now takes its
// saved result. It reports whether a stage came back.
func (p *progressStore) restore(saved json.RawMessage) (bool, error) {
	old, err := decodeStages(saved)
	if err != nil {
		return false, err
	}
	if len(old) == 0 {
		return false, nil
	}
	f, err := p.load()
	if err != nil {
		return false, err
	}
	back := false
	for sid, sp := range old {
		merged := mergeStages(f.Assignments[sid], sp)
		back = back || anyDone(merged) && merged != f.Assignments[sid]
		f.Assignments[sid] = merged
	}
	if !back {
		return false, nil
	}
	return true, p.save(f)
}

// StageFile is the stage progress file of the browser workspace at one
// path, for kubedrill progress, which resets and restores it outside the
// server. Its methods read and write the file on every call.
type StageFile struct {
	p progressStore
}

// NewStageFile returns the stage progress file at path.
func NewStageFile(path string) *StageFile {
	return &StageFile{p: progressStore{path: path, now: time.Now}}
}

// Snapshot is the stage progress of id (every assignment when id is empty)
// merged over prev, a snapshot an earlier reset saved: the Stages a reset
// marker keeps. It is nil when no stage is done.
func (f *StageFile) Snapshot(id string, prev json.RawMessage) (json.RawMessage, error) {
	return f.p.snapshot(id, prev)
}

// Clear removes the stage progress of id (every assignment when id is
// empty) and reports whether a stage was done.
func (f *StageFile) Clear(id string) (bool, error) {
	return f.p.clear(id, false)
}

// Restore puts a snapshot back: each stage not done now takes its saved
// result. It reports whether a stage came back.
func (f *StageFile) Restore(saved json.RawMessage) (bool, error) {
	return f.p.restore(saved)
}
