// Package faults injects and heals node-level faults in the kind cluster
// with the docker CLI: pausing a node container, stopping its kubelet, and
// adding network delay or loss with tc netem on its eth0. Active faults are
// recorded in <stateDir>/faults.json, the cluster state directory
// (cluster.StateDir), so Heal can undo them from another process and from
// any journal; <stateDir>/faults.lock serializes the processes: a process
// that finds it held polls every 100 ms until it is free or its context
// ends. Every docker call goes through one helper bounded by dockerTimeout,
// 30 seconds: pause, unpause, systemctl and tc finish in under a second on
// a healthy daemon, so the bound only catches a hung daemon, and it is
// shorter than the 60 seconds the cluster package allows for creating and
// removing containers.
package faults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/cluster"
)

// Action names one fault operation.
type Action string

// The actions Apply accepts. Pause, StopKubelet and Netem inject a fault;
// Unpause, StartKubelet and ClearNetem undo one.
const (
	Pause        Action = "pause"
	Unpause      Action = "unpause"
	StopKubelet  Action = "stop-kubelet"
	StartKubelet Action = "start-kubelet"
	Netem        Action = "netem"
	ClearNetem   Action = "clear-netem"
)

const (
	stateFile     = "faults.json"
	lockFile      = "faults.lock"
	netDevice     = "eth0"
	dockerTimeout = 30 * time.Second
	lockPoll      = 100 * time.Millisecond
)

// ErrAlreadyActive is returned by Apply when the same non-netem fault is
// already active on the node.
var ErrAlreadyActive = errors.New("fault already active")

// ErrUnknownNode is returned by Apply for a node that is not one of
// cluster.NodeNames; the message lists the valid names.
var ErrUnknownNode = errors.New("unknown node")

// ErrNetemUnavailable reports that the node kernel cannot load the netem
// queueing discipline (sch_netem missing in the Docker VM).
var ErrNetemUnavailable = errors.New("tc netem is not available in this Docker VM kernel (sch_netem missing)")

// Actions returns every action Apply accepts, in a stable order.
func Actions() []Action {
	return []Action{Pause, Unpause, StopKubelet, StartKubelet, Netem, ClearNetem}
}

// NetemOptions are the netem parameters: Delay added to every packet and
// Loss as a percentage between 0 and 100.
type NetemOptions struct {
	Delay time.Duration `json:"delay,omitempty"`
	Loss  float64       `json:"loss,omitempty"`
}

// Fault is one active fault as recorded in faults.json.
type Fault struct {
	Action Action       `json:"action"`
	Node   string       `json:"node"`
	At     time.Time    `json:"at"`
	Netem  NetemOptions `json:"netem,omitzero"`
}

// Runner runs one external command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Option configures a Manager.
type Option func(*Manager)

// WithRunner replaces the function that runs docker; the default runs the
// command and returns its combined output.
func WithRunner(r Runner) Option {
	return func(m *Manager) {
		if r != nil {
			m.run = r
		}
	}
}

// WithNow replaces the clock used to stamp faults; the default is time.Now.
func WithNow(fn func() time.Time) Option {
	return func(m *Manager) {
		if fn != nil {
			m.now = fn
		}
	}
}

// Manager applies and heals faults on the nodes of one kind cluster. The
// zero value is not usable; use New.
type Manager struct {
	cluster  string
	stateDir string
	run      Runner
	now      func() time.Time
	mu       sync.Mutex
}

// New returns a Manager for the kind cluster clusterName that records active
// faults in stateDir/faults.json; stateDir is the cluster state directory
// (cluster.StateDir), shared by every journal.
func New(clusterName, stateDir string, opts ...Option) *Manager {
	m := &Manager{
		cluster:  clusterName,
		stateDir: stateDir,
		run:      combinedOutput,
		now:      time.Now,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func combinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Apply runs action on node (control-plane, worker or worker2) and updates
// the recorded state. Injecting a fault that is already active returns
// ErrAlreadyActive, except Netem, which replaces the previous parameters. A
// node outside cluster.NodeNames returns ErrUnknownNode. netem is read only
// for Netem. Each docker call is bounded to 30 seconds; waiting for the
// state lock ends with ctx. An injected fault is recorded before the docker
// call, so a process killed mid-call still leaves a record Heal undoes. The
// record is dropped again only when the call definitely did not apply the
// fault: docker exited non-zero while its context was still live, or the
// docker binary is missing. A call cut short by ctx or the 30 second bound
// keeps the record, because the daemon may still complete the request;
// every undo tolerates a fault that is not there, so Heal clears it.
func (m *Manager) Apply(ctx context.Context, action Action, node string, netem NetemOptions) error {
	if err := m.validate(action, node, netem); err != nil {
		return fmt.Errorf("fault %s on %s: %w", action, node, err)
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return fmt.Errorf("fault %s on %s: %w", action, node, err)
	}
	defer unlock()
	active, err := m.load()
	if err != nil {
		return fmt.Errorf("fault %s on %s: %w", action, node, err)
	}
	inject, undo := pairOf(action)
	idx := slices.IndexFunc(active, func(f Fault) bool { return f.Action == inject && f.Node == node })
	if action == inject && action != Netem && idx >= 0 {
		return fmt.Errorf("fault %s on %s: %w since %s", action, node, ErrAlreadyActive, active[idx].At.Format(time.RFC3339))
	}
	before := slices.Clone(active)
	if idx >= 0 {
		active = slices.Delete(active, idx, idx+1)
	}
	if action != undo {
		active = append(active, Fault{Action: action, Node: node, At: m.now().UTC(), Netem: netem})
		if err := m.save(active); err != nil {
			return fmt.Errorf("fault %s on %s: record intent: %w", action, node, err)
		}
	}
	if err := m.exec(ctx, action, node, netem, false); err != nil {
		if action != undo {
			if !notApplied(err) {
				return fmt.Errorf("fault %s on %s: %w; the fault may still apply, so its record is kept: run kubedrill heal", action, node, err)
			}
			if saveErr := m.save(before); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("drop intent record: %w", saveErr))
			}
		}
		return fmt.Errorf("fault %s on %s: %w", action, node, err)
	}
	if action == undo {
		if err := m.save(active); err != nil {
			return fmt.Errorf("fault %s on %s: %w", action, node, err)
		}
	}
	return nil
}

// lock takes the in-process mutex and an exclusive flock on
// <stateDir>/faults.lock, held across load, docker and save. A lock held by
// another process is retried every 100 ms until ctx ends.
func (m *Manager) lock(ctx context.Context) (func(), error) {
	m.mu.Lock()
	if err := atomicfile.MkdirPrivate(m.stateDir); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("lock fault state: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(m.stateDir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("lock fault state: %w", err)
	}
	ticker := time.NewTicker(lockPoll)
	defer ticker.Stop()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			m.mu.Unlock()
			return nil, fmt.Errorf("lock fault state: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			m.mu.Unlock()
			return nil, fmt.Errorf("lock fault state %s: %w", filepath.Join(m.stateDir, lockFile), ctx.Err())
		case <-ticker.C:
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		m.mu.Unlock()
	}, nil
}

// Heal undoes every recorded fault in reverse order of application. A fault
// that is already undone (node unpaused, qdisc gone, node container
// removed) counts as healed. Faults that fail to heal, and records whose
// action this version does not know, stay recorded and their errors are
// returned together.
func (m *Manager) Heal(ctx context.Context) error {
	unlock, err := m.lock(ctx)
	if err != nil {
		return fmt.Errorf("heal faults: %w", err)
	}
	defer unlock()
	active, err := m.load()
	if err != nil {
		return fmt.Errorf("heal faults: %w", err)
	}
	var remaining []Fault
	var errs []error
	for i := len(active) - 1; i >= 0; i-- {
		f := active[i]
		_, undo := pairOf(f.Action)
		if undo == "" {
			errs = append(errs, fmt.Errorf("heal %s on %s: unknown action; remove its record from %s after checking the node (docker ps)", f.Action, f.Node, m.path()))
			remaining = append([]Fault{f}, remaining...)
			continue
		}
		if err := m.exec(ctx, undo, f.Node, NetemOptions{}, true); err != nil {
			errs = append(errs, fmt.Errorf("heal %s on %s: %w", f.Action, f.Node, err))
			remaining = append([]Fault{f}, remaining...)
		}
	}
	if err := m.save(remaining); err != nil {
		errs = append(errs, fmt.Errorf("heal faults: %w", err))
	}
	return errors.Join(errs...)
}

// Active returns the recorded faults in order of application; no state
// file means no faults. Waiting for the state lock ends with ctx.
func (m *Manager) Active(ctx context.Context) ([]Fault, error) {
	unlock, err := m.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return m.load()
}

func pairOf(a Action) (inject, undo Action) {
	switch a {
	case Pause, Unpause:
		return Pause, Unpause
	case StopKubelet, StartKubelet:
		return StopKubelet, StartKubelet
	case Netem, ClearNetem:
		return Netem, ClearNetem
	}
	return "", ""
}

func (m *Manager) validate(action Action, node string, netem NetemOptions) error {
	var problems []string
	badNode := false
	if nodes := cluster.NodeNames(); !slices.Contains(nodes, node) {
		badNode = true
		problems = append(problems, fmt.Sprintf("node %q is not one of %s", node, strings.Join(nodes, ", ")))
	}
	if !slices.Contains(Actions(), action) {
		names := make([]string, 0, len(Actions()))
		for _, a := range Actions() {
			names = append(names, string(a))
		}
		problems = append(problems, fmt.Sprintf("action %q is not one of %s", action, strings.Join(names, ", ")))
	}
	if m.cluster == "" {
		problems = append(problems, "cluster name is empty")
	}
	if action == Netem {
		if netem.Delay < 0 {
			problems = append(problems, fmt.Sprintf("delay %s is negative", netem.Delay))
		}
		if math.IsNaN(netem.Loss) || netem.Loss < 0 || netem.Loss > 100 {
			problems = append(problems, fmt.Sprintf("loss %v is not between 0 and 100", netem.Loss))
		}
		if netem.Delay == 0 && netem.Loss == 0 {
			problems = append(problems, "netem needs a delay or a loss")
		}
	}
	if len(problems) > 0 {
		return &validationError{msg: strings.Join(problems, "; "), unknownNode: badNode}
	}
	return nil
}

// validationError lists every problem of one Apply call and matches
// ErrUnknownNode when the node was one of them.
type validationError struct {
	msg         string
	unknownNode bool
}

func (e *validationError) Error() string { return e.msg }

func (e *validationError) Is(target error) bool {
	return target == ErrUnknownNode && e.unknownNode
}

func (m *Manager) exec(ctx context.Context, action Action, node string, netem NetemOptions, healing bool) error {
	container := m.cluster + "-" + node
	var args []string
	switch action {
	case Pause:
		args = []string{"pause", container}
	case Unpause:
		args = []string{"unpause", container}
	case StopKubelet:
		args = []string{"exec", container, "systemctl", "stop", "kubelet"}
	case StartKubelet:
		args = []string{"exec", container, "systemctl", "start", "kubelet"}
	case Netem:
		args = append([]string{"exec", container, "tc", "qdisc", "replace", "dev", netDevice, "root", "netem"}, netemArgs(netem)...)
	case ClearNetem:
		args = []string{"exec", container, "tc", "qdisc", "del", "dev", netDevice, "root"}
	}
	dctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()
	out, err := m.run(dctx, "docker", args...)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	var exitErr *exec.ExitError
	definite := (errors.As(err, &exitErr) && dctx.Err() == nil) || errors.Is(err, exec.ErrNotFound)
	if !definite {
		if ctxErr := dctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			err = fmt.Errorf("%w (%w)", err, ctxErr)
		}
		err = &outcomeUnknownError{err: err}
	}
	if definite && alreadyUndone(action, msg) && (healing || action == Unpause || action == ClearNetem) {
		return nil
	}
	if definite && action == Netem && netemMissing(msg) {
		return fmt.Errorf("%w: %s", ErrNetemUnavailable, msg)
	}
	if msg != "" {
		return fmt.Errorf("docker %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
}

// outcomeUnknownError marks a docker call that may have reached the daemon
// although it reported a failure.
type outcomeUnknownError struct{ err error }

func (e *outcomeUnknownError) Error() string { return e.err.Error() }

func (e *outcomeUnknownError) Unwrap() error { return e.err }

func notApplied(err error) bool {
	var unknown *outcomeUnknownError
	return !errors.As(err, &unknown)
}

func netemArgs(o NetemOptions) []string {
	var args []string
	if o.Delay > 0 {
		args = append(args, "delay", strconv.FormatInt(o.Delay.Microseconds(), 10)+"us")
	}
	if o.Loss > 0 {
		args = append(args, "loss", strconv.FormatFloat(o.Loss, 'f', -1, 64)+"%")
	}
	return args
}

func alreadyUndone(action Action, msg string) bool {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "no such container") {
		return true
	}
	switch action {
	case Unpause:
		return strings.Contains(lower, "is not paused")
	case ClearNetem:
		return strings.Contains(lower, "cannot delete qdisc with handle of zero") ||
			strings.Contains(lower, "no such file or directory") ||
			strings.Contains(lower, "cannot find specified qdisc")
	}
	return false
}

func netemMissing(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "specified qdisc kind is unknown") ||
		strings.Contains(lower, "unknown qdisc")
}

func (m *Manager) path() string {
	return filepath.Join(m.stateDir, stateFile)
}

func (m *Manager) load() ([]Fault, error) {
	data, err := os.ReadFile(m.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fault state: %w", err)
	}
	var faults []Fault
	if err := json.Unmarshal(data, &faults); err != nil {
		return nil, fmt.Errorf("parse fault state %s: %w; remove %s after checking the nodes (docker ps)", m.path(), err, m.path())
	}
	return faults, nil
}

func (m *Manager) save(faults []Fault) error {
	if len(faults) == 0 {
		if err := os.Remove(m.path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear fault state: %w", err)
		}
		return nil
	}
	if err := atomicfile.MkdirPrivate(m.stateDir); err != nil {
		return fmt.Errorf("write fault state: %w", err)
	}
	data, err := json.MarshalIndent(faults, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fault state: %w", err)
	}
	if err := atomicfile.Write(m.path(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write fault state: %w", err)
	}
	return nil
}
