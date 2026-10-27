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
}

// StageResult records a completed stage and its best score.
type StageResult struct {
	Done  bool      `json:"done"`
	Score int       `json:"score,omitempty"`
	Total int       `json:"total,omitempty"`
	At    time.Time `json:"at,omitzero"`
}

// progressFile is the on-disk shape: progress per scenario and the stage of
// each episode the workspace started.
type progressFile struct {
	Assignments map[string]StageProgress `json:"assignments"`
	Episodes    map[string]Stage         `json:"episodes"`
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
