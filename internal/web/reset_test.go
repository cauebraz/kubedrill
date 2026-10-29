package web_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/web"
	"github.com/cauebraz/kubedrill/internal/web/webtest"
)

// card finds the assignment id in the catalog with the badge of its module.
func card(t *testing.T, c *client, id string) (web.AssignmentCard, web.Badge) {
	t.Helper()
	var cat web.Catalog
	c.json("GET", "/api/catalog", "", &cat)
	for _, lv := range cat.Levels {
		for _, m := range lv.Modules {
			for _, a := range m.Assignments {
				if a.ID == id {
					return a, m.Badge
				}
			}
		}
	}
	t.Fatalf("no assignment %s in the catalog", id)
	return web.AssignmentCard{}, web.Badge{}
}

func TestResetAndRestoreProgress(t *testing.T) {
	c := newClient(t, newFake())
	const id = "003-crashloop"
	if code := c.json("POST", "/api/progress", `{"id":"`+id+`","stage":"learn","score":1,"total":1}`, nil); code != 200 {
		t.Fatalf("record learn: %d", code)
	}
	a, b := card(t, c, id)
	if a.Status != "mastered" || !a.Progress.Learn.Done || b.State != "mastered" {
		t.Fatalf("before: status %s learn %v badge %s", a.Status, a.Progress.Learn.Done, b.State)
	}
	var ch web.ProgressChange
	if code := c.json("POST", "/api/progress/reset", `{"id":"`+id+`"}`, &ch); code != 200 {
		t.Fatalf("reset: got %d", code)
	}
	if ch.ID != id || ch.All || ch.Runs != 3 || !ch.Stages {
		t.Errorf("reset: got %+v, want %s, 3 runs, stages cleared", ch, id)
	}
	a, b = card(t, c, id)
	if a.Status != "todo" || a.Progress.Learn.Done || b.State != "in-progress" {
		t.Errorf("after the reset: status %s learn %v badge %s, want todo, not done, in-progress", a.Status, a.Progress.Learn.Done, b.State)
	}
	if code := c.json("POST", "/api/progress/restore", `{"id":"`+id+`"}`, &ch); code != 200 {
		t.Fatalf("restore: got %d", code)
	}
	if ch.Runs != 3 || !ch.Stages {
		t.Errorf("restore: got %+v, want 3 runs and the stages back", ch)
	}
	a, b = card(t, c, id)
	if a.Status != "mastered" || !a.Progress.Learn.Done || b.State != "mastered" {
		t.Errorf("after the restore: status %s learn %v badge %s", a.Status, a.Progress.Learn.Done, b.State)
	}
	_, body := c.do("POST", "/api/progress/restore", `{"id":"`+id+`"}`, true)
	if !strings.Contains(body, "no reset recorded") {
		t.Errorf("second restore: got %s", body)
	}
}

func TestResetProgressRequests(t *testing.T) {
	c := newClient(t, newFake())
	tests := []struct {
		body string
		want int
	}{
		{`{}`, 400},
		{`{"id":"003-crashloop","all":true}`, 400},
		{`{"id":"003-crashloop","welcome":true}`, 400},
		{`{"id":"999-none"}`, 400},
		{`{"ID":"003","extra":1}`, 400},
	}
	for _, tt := range tests {
		if code := c.json("POST", "/api/progress/reset", tt.body, nil); code != tt.want {
			t.Errorf("reset %s: got %d, want %d", tt.body, code, tt.want)
		}
	}
	if res, _ := c.do("POST", "/api/progress/reset", `{"all":true}`, false); res.StatusCode != 403 {
		t.Errorf("reset without the X-Kubedrill header: got %d, want 403", res.StatusCode)
	}
	c.json("POST", "/api/onboarded", "", nil)
	var ch web.ProgressChange
	if code := c.json("POST", "/api/progress/reset", `{"all":true,"welcome":true}`, &ch); code != 200 || !ch.All || ch.ID != "" {
		t.Fatalf("reset everything: got %d %+v", code, ch)
	}
	var m struct {
		Onboarded bool `json:"onboarded"`
	}
	if c.json("GET", "/api/meta", "", &m); m.Onboarded {
		t.Error("meta after a reset with welcome: onboarded, want the welcome flow again")
	}
	if a, b := card(t, c, "011-rollout-stuck"); a.Status != "todo" || b.State != "locked" {
		t.Errorf("after resetting everything: %s %s, want todo locked", a.Status, b.State)
	}
	if code := c.json("POST", "/api/progress/restore", `{"all":true,"welcome":true}`, nil); code != 400 {
		t.Errorf("restore with welcome: got %d, want 400", code)
	}
	if code := c.json("POST", "/api/progress/restore", `{"all":true}`, &ch); code != 200 || !ch.All {
		t.Errorf("restore everything: got %d %+v", code, ch)
	}
	if a, _ := card(t, c, "011-rollout-stuck"); a.Status != "solved" {
		t.Errorf("after restoring everything: %s, want solved", a.Status)
	}
}

func TestResetProgressRefusesTheActiveRun(t *testing.T) {
	c := newClient(t, newFake())
	c.start(`{"id":"003-crashloop","stage":"diy"}`)
	for _, body := range []string{`{"id":"003-crashloop"}`, `{"all":true}`} {
		res, text := c.do("POST", "/api/progress/reset", body, true)
		if res.StatusCode != 409 || !strings.Contains(text, "end the active run first (kubedrill solution, or Give up in the page)") {
			t.Errorf("reset %s while 003 is active: got %d %s, want 409 with the way out", body, res.StatusCode, text)
		}
	}
	if code := c.json("POST", "/api/progress/reset", `{"id":"004-never-ready"}`, nil); code != 200 {
		t.Errorf("reset of another assignment: got %d, want 200", code)
	}
}

// resetDebriefs marks every debrief as a reset run.
type resetDebriefs struct{ *webtest.Fake }

func (r resetDebriefs) Debrief(ctx context.Context, id string) (web.Debrief, error) {
	d, err := r.Fake.Debrief(ctx, id)
	d.Reset = true
	return d, err
}

func TestDebriefOfAResetRunKeepsTheStage(t *testing.T) {
	f := newFake()
	c := newClient(t, resetDebriefs{f})
	_, sess, _, _ := c.start(`{"id":"001","stage":"exam"}`)
	f.Fix()
	f.Finish()
	var d web.Debrief
	if code := c.json("GET", "/api/debrief/"+sess.Active.EpisodeID, "", &d); code != 200 || !d.Reset {
		t.Fatalf("debrief: got %d %+v", code, d)
	}
	if a, _ := card(t, c, webtest.FullID); a.Progress.Exam.Done {
		t.Error("the debrief of a reset run marked its stage done again")
	}
}
