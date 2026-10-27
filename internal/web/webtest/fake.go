// Package webtest is an in-memory web.Backend for the workspace's tests,
// the front-end dev server and the browser tests: a small curriculum with
// one assignment that has a simulation, episodes that start, grade, hint,
// diagnose and debrief without a cluster, and a terminal that runs a local
// command in place of the workstation.
package webtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/web"
)

// FullID is the assignment with a simulation; the others are lab only.
const FullID = "001-storefront-down"

// Fake is a web.Backend held in memory. Its exported fields steer it: a
// test sets Passed before Validate, HintErr to fail Hint, TerminalArgv to
// the command a terminal runs; the recorded fields (Starts, Cleaned) are read
// under Lock.
type Fake struct {
	mu sync.Mutex

	// Passed makes the next Validate pass and end the episode.
	Passed bool
	// HintErr, when set, is what Hint returns.
	HintErr error
	// TerminalArgv is the command a terminal runs; the default is an
	// interactive sh.
	TerminalArgv []string
	// StepDelay is the pause between the progress steps of Start.
	StepDelay time.Duration
	// Now is the clock; the default is time.Now.
	Now func() time.Time

	// Starts records "<id> <stage>" per Start; Cleaned the container names
	// whose cleanup ran.
	Starts  []string
	Cleaned []string

	active   *activeEpisode
	ended    map[string]*endedEpisode
	sequence int
	hints    []string
}

type activeEpisode struct {
	view      web.Active
	fixed     bool
	stage     web.Stage
	diagnosis string
}

type endedEpisode struct {
	debrief web.Debrief
}

// New returns a Fake with no episode.
func New() *Fake {
	return &Fake{ended: map[string]*endedEpisode{}, Now: time.Now}
}

// Lock and Unlock guard the recorded fields.
func (f *Fake) Lock()   { f.mu.Lock() }
func (f *Fake) Unlock() { f.mu.Unlock() }

// Fix makes the live checks of the active episode pass, as a learner's fix
// would.
func (f *Fake) Fix() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active != nil {
		f.active.fixed = true
	}
}

func (f *Fake) now() time.Time {
	if f.Now == nil {
		return time.Now()
	}
	return f.Now()
}

func card(id, title string, minutes int, sim bool, concepts, exam []string) web.AssignmentCard {
	return web.AssignmentCard{ID: id, Title: title, Minutes: minutes, Simulation: sim, Concepts: concepts, Exam: exam, Requires: []string{}, Status: "todo", Mastery: web.Mastery{Variants: 3}}
}

// Catalog is three levels: L0 with two plans and four assignments, L1 with
// one plan, L2 planned only.
func (f *Fake) Catalog(context.Context) (web.Catalog, error) {
	f.mu.Lock()
	activeID := ""
	if f.active != nil {
		activeID = f.active.view.ScenarioID
	}
	f.mu.Unlock()
	reading := []web.AssignmentCard{
		card(FullID, "Storefront is down after the cluster migration", 10, true, []string{"pod-lifecycle", "image-pull"}, []string{"CKA:troubleshooting"}),
		card("002-restart-triage", "Find the pod that keeps restarting", 12, false, []string{"pod-lifecycle"}, []string{"CKA:troubleshooting"}),
	}
	lifecycle := []web.AssignmentCard{
		card("003-crashloop", "Worker is in CrashLoopBackOff since the deploy", 15, false, []string{"pod-lifecycle"}, []string{"CKAD:observability"}),
		card("004-never-ready", "API never becomes Ready", 15, false, []string{"readiness-and-endpoints"}, []string{"CKAD:observability"}),
	}
	lifecycle[1].Requires = []string{"003-crashloop"}
	operator := []web.AssignmentCard{
		card("011-rollout-stuck", "Rollout stuck at 1 of 3 new replicas", 20, false, []string{"rollouts"}, []string{"CKAD:deployment"}),
	}
	for _, list := range [][]web.AssignmentCard{reading, lifecycle, operator} {
		for i := range list {
			if list[i].ID == activeID {
				list[i].Status = "active"
			}
		}
	}
	return web.Catalog{
		Levels: []web.Level{
			{ID: "L0", Title: "Foundations", After: "Reads any object and fixes one-object failures", Modules: []web.Module{
				{ID: "L0.1", Title: "Reading the cluster", Summary: "kubectl verbs, namespaces, events", Assignments: reading, Planned: 2},
				{ID: "L0.2", Title: "Pod lifecycle", Summary: "Phases, probes, restarts", Assignments: lifecycle, Planned: 3},
			}},
			{ID: "L1", Title: "Application operator", After: "Debugs an app in a namespace without help", Modules: []web.Module{
				{ID: "L1.1", Title: "Rollouts", Summary: "Deployments, strategies, history", Assignments: operator, Planned: 4},
			}},
			{ID: "L2", Title: "Cluster operator", After: "On-call for a cluster", Modules: []web.Module{
				{ID: "L2.1", Title: "Scheduling", Summary: "Taints, affinity, resources", Assignments: []web.AssignmentCard{}, Planned: 6},
			}},
		},
		Exams: []web.Exam{
			{ID: "CKA", Title: "Certified Kubernetes Administrator", Domains: []web.ExamDomain{
				{ID: "troubleshooting", Title: "Troubleshooting", Weight: 30, Assignments: []string{FullID, "002-restart-triage"}},
				{ID: "workloads", Title: "Workloads and scheduling", Weight: 15, Assignments: []string{}},
			}},
			{ID: "CKAD", Title: "Certified Kubernetes Application Developer", Domains: []web.ExamDomain{
				{ID: "observability", Title: "Application observability and maintenance", Weight: 15, Assignments: []string{"003-crashloop", "004-never-ready"}},
				{ID: "deployment", Title: "Application deployment", Weight: 20, Assignments: []string{"011-rollout-stuck"}},
			}},
		},
	}, nil
}

// Simulation is the simulation of FullID.
func Simulation() *scenario.Simulation {
	return &scenario.Simulation{
		Customer: scenario.Customer{Name: "Dana", Role: "Storefront team lead", Company: "ACME Retail"},
		Mentor:   scenario.MentorOptions{Intro: "Ask me for a hint whenever you are stuck."},
		Meeting: scenario.Meeting{
			Opening: "The storefront has been down since this morning. Where do you want to start?",
			Closing: "Ping me when it is back.",
			Turns: []scenario.Turn{
				{Prompt: "What do you ask first?", Choices: []scenario.Choice{
					{Say: "What changed right before it stopped working?", Reply: "We moved it to the new cluster this morning.", Feedback: "A failure right after a change usually comes from it.", Best: true, Fact: "It broke right after the move to the new cluster."},
					{Say: "Can you restart the pods?", Reply: "We did, they came back the same.", Feedback: "Acting before observing hides evidence."},
				}},
				{Prompt: "Agree on done.", Choices: []scenario.Choice{
					{Say: "Can I delete the namespace?", Reply: "Please do not.", Feedback: "Starting over destroys the evidence."},
					{Say: "What does fixed look like for you?", Reply: "Three ready pods serving the 2.14.0 page.", Feedback: "Agreeing on done lets you verify like the customer.", Best: true, Fact: "Done: 3 ready pods serve the 2.14.0 page."},
				}},
			},
		},
		Diagram: scenario.Diagram{
			Nodes: []scenario.DiagramNode{{ID: "deploy", Label: "Deployment storefront", Note: "keeps 3 pods"}, {ID: "pods", Label: "Pods x3", Note: "image pulled, probe passed"}, {ID: "service", Label: "Service storefront", Note: "routes to Ready pods"}},
			Edges: []scenario.DiagramEdge{{From: "deploy", To: "pods", Label: "creates"}, {From: "service", To: "pods", Label: "Ready only"}},
		},
		Quiz: []scenario.QuizItem{
			{Question: "Every pod shows ImagePullBackOff. What does that rule out?", Choices: []scenario.QuizChoice{
				{Text: "A crash or a failing probe.", Correct: true, Explain: "The pull happens before the container exists."},
				{Text: "Nothing.", Explain: "A pod that never starts a container cannot crash."},
			}},
			{Question: "Where does the fix belong?", Choices: []scenario.QuizChoice{
				{Text: "In the pods.", Explain: "New pods get the same template."},
				{Text: "In the Deployment's pod template.", Correct: true, Explain: "The pods copy the image from it."},
			}},
		},
		Guide: []scenario.GuideStep{
			{Title: "List the pods", Body: "Read the STATUS column.", Command: "kubectl get pods"},
			{Title: "Read the events", Body: "Find the `Failed to pull image` line.", Command: "kubectl get events --sort-by=.lastTimestamp"},
			{Title: "Change the image", Body: "Change one thing.", Command: "kubectl set image deployment/storefront web=nginx:1.27-alpine", Check: "image-correct"},
			{Title: "Watch the rollout", Body: "Wait for Ready.", Command: "kubectl rollout status deployment/storefront", Check: "ready-replicas"},
		},
	}
}

const ticket = "# Storefront is down after the cluster migration\n\n## Ticket\n\n> **From:** Dana\n>\n> The status page shows `0/3` ready.\n\n## Done when\n\n- deployment storefront has 3 ready replicas\n- deployment storefront runs the nginx:1.27-alpine image\n\n<script>alert(1)</script>\n"

const story = "# The storefront that never started\n\n## What happened\n\nThe team moved the storefront to a new cluster.\n"

// Assignment returns the content of id; only FullID has a simulation.
func (f *Fake) Assignment(_ context.Context, id string) (web.Assignment, error) {
	c, _ := f.Catalog(context.Background())
	for _, lv := range c.Levels {
		for _, m := range lv.Modules {
			for _, a := range m.Assignments {
				if !strings.HasPrefix(a.ID, id) {
					continue
				}
				out := web.Assignment{
					ID: a.ID, Title: a.Title, Level: lv.ID, Module: m.ID, ModuleTitle: m.Title, Minutes: a.Minutes,
					Exam: a.Exam, Variants: 3, Ticket: ticket, Story: story, Mastery: a.Mastery,
					Concepts: []web.Concept{{ID: "pod-lifecycle", Title: "Pod lifecycle", Summary: "From Pending to Running to Ready.", Body: "# Pod lifecycle\n\nA pod moves from **Pending** to **Running**.\n"}},
				}
				if a.Simulation {
					out.Simulation = Simulation()
				}
				return out, nil
			}
		}
	}
	return web.Assignment{}, fmt.Errorf("no scenario matches %q", id)
}

func (f *Fake) checks(fixed bool) []web.Check {
	status := "fail"
	if fixed {
		status = "pass"
	}
	return []web.Check{
		{ID: "deploy-exists", Description: "deployment storefront exists", Why: "The Deployment is the object the team owns.", Status: "pass"},
		{ID: "image-correct", Description: "deployment storefront runs the nginx:1.27-alpine image", Why: "The image reference decides whether the node can pull it.", Status: status, Expected: "nginx:1.27-alpine"},
		{ID: "ready-replicas", Description: "deployment storefront has 3 ready replicas", Why: "Ready pods are what the Service routes to.", Status: status, Expected: "3", Actual: map[bool]string{true: "3", false: "no value at {.status.readyReplicas}"}[fixed]},
	}
}

// Session returns the active episode and, with live, its checks.
func (f *Fake) Session(_ context.Context, live bool) (web.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return web.Session{}, nil
	}
	v := f.active.view
	v.Hints = append([]string{}, f.hints...)
	v.HintsUsed = len(f.hints)
	v.Diagnosis = f.active.diagnosis
	v.Guide = append([]scenario.GuideStep{}, v.Guide...)
	out := web.Session{Active: &v}
	if live {
		out.Checks = f.checks(f.active.fixed)
		out.Passed = f.active.fixed
	}
	return out, nil
}

// Next offers the active episode, or FullID until its DIY passed.
func (f *Fake) Next(ctx context.Context) (web.Next, error) {
	s, _ := f.Session(ctx, false)
	if s.Active != nil {
		return web.Next{Active: s.Active, ReviewsPending: 1}, nil
	}
	return web.Next{ScenarioID: FullID, Title: "Storefront is down after the cluster migration", Minutes: 10, Reason: "you have not tried it yet; it is the next one at L0", ReviewsPending: 1}, nil
}

// Start begins an episode of id, reporting three steps to progress.
func (f *Fake) Start(ctx context.Context, id string, stage web.Stage, replace bool, progress func(string)) (web.Session, error) {
	f.mu.Lock()
	if f.active != nil && !replace {
		f.mu.Unlock()
		return web.Session{}, fmt.Errorf("%s is active: %w", f.active.view.ScenarioID, web.ErrActiveElsewhere)
	}
	f.mu.Unlock()
	a, err := f.Assignment(ctx, id)
	if err != nil {
		return web.Session{}, err
	}
	for _, step := range []string{"creating namespace shop-abcde", "applying 3 setup manifests", "recording the episode"} {
		if progress != nil {
			progress(step)
		}
		if f.StepDelay > 0 {
			select {
			case <-time.After(f.StepDelay):
			case <-ctx.Done():
				return web.Session{}, ctx.Err()
			}
		}
	}
	f.mu.Lock()
	f.sequence++
	f.Starts = append(f.Starts, a.ID+" "+string(stage))
	view := web.Active{
		EpisodeID: fmt.Sprintf("%s-%d", a.ID, f.sequence), ScenarioID: a.ID, Title: a.Title,
		Namespace: "shop-abcde", Ticket: ticket, Story: story, StartedAt: f.now(), Minutes: a.Minutes,
		HintsTotal: 4, Mentor: "Ask me for a hint whenever you are stuck.", Guide: []scenario.GuideStep{},
	}
	if a.Simulation != nil {
		view.Guide = a.Simulation.Guide
	}
	f.active = &activeEpisode{view: view, stage: stage}
	f.hints = nil
	f.mu.Unlock()
	return f.Session(ctx, false)
}

// Validate grades the active episode: it passes when Passed is set or the
// episode was fixed, and a pass ends it.
func (f *Fake) Validate(context.Context) (web.Validation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return web.Validation{}, errors.New("no active episode; launch the practice or the DIY run first")
	}
	pass := f.Passed || f.active.fixed
	v := web.Validation{EpisodeID: f.active.view.EpisodeID, ScenarioID: f.active.view.ScenarioID, Passed: pass, Checks: f.checks(pass)}
	if !pass {
		v.Score = 1.0 / 3
		return v, nil
	}
	v.Score = 1
	v.RootCause = "The Deployment image points at a registry the new nodes cannot resolve."
	v.Diagnosis = f.active.diagnosis
	v.Solution = "## Change\n\n```\nkubectl set image deployment/storefront web=nginx:1.27-alpine\n```\n"
	f.ended[v.EpisodeID] = &endedEpisode{debrief: web.Debrief{
		EpisodeID: v.EpisodeID, ScenarioID: v.ScenarioID, Title: f.active.view.Title, Outcome: "solved",
		Seconds: int(f.now().Sub(f.active.view.StartedAt).Seconds()), Minutes: f.active.view.Minutes, Hints: len(f.hints),
		Telemetry: "audit+shell", ReadsBeforeWrite: 4, FirstWrite: "apps/deployments/storefront", FirstWriteKind: "fix-target",
		FixTargets: []string{"apps/deployments/storefront"}, Verdict: "root-cause", Diagnosis: f.active.diagnosis, RootCause: v.RootCause,
	}}
	f.active = nil
	return v, nil
}

var fakeHints = []string{
	"`0/3 ready` is a property of the pods. Start with `kubectl get pods`.",
	"Read the Events at the bottom of `kubectl describe pod`.",
	"The kubelet cannot pull the image: compare registry, repository and tag.",
	"Change the image of container `web` in the Deployment.",
}

// Hint reveals the next hint; the last one needs a diagnosis.
func (f *Fake) Hint(context.Context) (web.Hint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HintErr != nil {
		return web.Hint{}, f.HintErr
	}
	if f.active == nil {
		return web.Hint{}, errors.New("no active episode")
	}
	n := len(f.hints)
	if n >= len(fakeHints) {
		return web.Hint{}, errors.New("no more hints")
	}
	if n == len(fakeHints)-1 && f.active.diagnosis == "" {
		return web.Hint{}, errors.New("the last hint names the fix; commit to a cause first")
	}
	f.hints = append(f.hints, fakeHints[n])
	return web.Hint{HTML: fakeHints[n], Index: n + 1, Total: len(fakeHints)}, nil
}

// Diagnose records text on the active episode.
func (f *Fake) Diagnose(_ context.Context, text string) error {
	if n := len([]rune(strings.TrimSpace(text))); n < 10 || n > 300 {
		return fmt.Errorf("diagnosis must be 10 to 300 characters, got %d", n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return errors.New("no active episode")
	}
	f.active.diagnosis = strings.TrimSpace(text)
	return nil
}

// Debrief returns the debrief of an ended episode.
func (f *Fake) Debrief(_ context.Context, id string) (web.Debrief, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ended[id]
	if !ok {
		return web.Debrief{}, fmt.Errorf("no ended episode %q", id)
	}
	return e.debrief, nil
}

// RecordDebrief stores the answers on an ended episode.
func (f *Fake) RecordDebrief(_ context.Context, id string, ans web.DebriefAnswers) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.ended[id]
	if !ok {
		return fmt.Errorf("no ended episode %q", id)
	}
	e.debrief.Answers = ans
	e.debrief.Matched = ans.Matched
	return nil
}

// Terminal runs TerminalArgv (default an interactive sh) and records the
// cleanup of name.
func (f *Fake) Terminal(_ context.Context, name string) (*exec.Cmd, func(), error) {
	argv := f.TerminalArgv
	if len(argv) == 0 {
		argv = []string{"/bin/sh", "-i"}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PS1=learner@fake $ ", "TERM=xterm-256color", "PATH=/usr/bin:/bin"}
	cleanup := func() {
		f.mu.Lock()
		f.Cleaned = append(f.Cleaned, name)
		f.mu.Unlock()
	}
	return cmd, cleanup, nil
}
