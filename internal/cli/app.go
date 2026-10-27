// Package cli holds the kubedrill cobra commands, the session assembly and
// the command handlers; the terminal text toolkit lives in render and the
// two Bubble Tea screens in tui/watch and tui/ticket. Every collaborator
// arrives through Factories so the commands can be exercised with fakes;
// cmd/kubedrill wires the real packages.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

// Exit codes of the kubedrill binary.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitChecksFailed = 2
)

// ErrChecksFailed is returned by check when at least one check did not pass.
var ErrChecksFailed = errors.New("checks failed")

// Cluster is what the commands need from the kind cluster manager.
type Cluster interface {
	Doctor(ctx context.Context) ([]cluster.Tool, error)
	Exists(ctx context.Context) (bool, error)
	Ensure(ctx context.Context, profile string, data cluster.ProfileData) error
	RenderProfile(profile string, data cluster.ProfileData) ([]byte, error)
	Delete(ctx context.Context) error
	Kubeconfig() (path, contextName string, err error)
	WriteInternalKubeconfig(ctx context.Context, dstPath string) error
	AuditEnabled(ctx context.Context) (bool, error)
	AuditPolicyStale(ctx context.Context) (bool, error)
	Addons() []string
	BaseAddons() []string
	EnsureAddons(ctx context.Context, k cluster.AddonApplier, names []string) error
	PullImages(ctx context.Context, images []string) error
}

// Faults is what fault, heal, start, up and down need from the fault
// manager.
type Faults interface {
	Apply(ctx context.Context, action faults.Action, node string, netem faults.NetemOptions) error
	Heal(ctx context.Context) error
	Active(ctx context.Context) ([]faults.Fault, error)
	Forget(ctx context.Context) error
}

// TelemetryFunc summarizes the learner's trajectory for one episode; see
// env.Deps.Telemetry.
type TelemetryFunc = func(ep journal.Episode, rd *scenario.Rendered) journal.Telemetry

// Concepts is what the commands need from the concept library.
type Concepts interface {
	IDs() []string
	All() []concepts.Concept
	Get(id string) (concepts.Concept, error)
}

// Workstation is what start, shell, node and replay need from the
// containerized toolbox.
type Workstation interface {
	DockerAvailable(ctx context.Context) error
	Prepare(ctx context.Context, out io.Writer) error
	Shell(ctx context.Context, opts workstation.ShellOptions) error
	ShellCommand(ctx context.Context, opts workstation.ShellOptions) (*exec.Cmd, error)
	RemoveContainer(ctx context.Context, name string) error
	NodeShell(ctx context.Context, clusterName, node string) error
	NodeDebug(ctx context.Context, clusterName, node, image string) error
	Replay(ctx context.Context, cast string) error
}

// Journal is what the commands need from the episode store.
type Journal interface {
	env.Journal
	Episodes(scenarioID string) ([]journal.Episode, error)
	Problems() []string
	Dir() string
}

// Catalog is what the commands need from the scenario catalog.
type Catalog interface {
	All() []*scenario.Scenario
	Find(ref string) (*scenario.Scenario, error)
}

// Kube is what the commands need from the cluster client.
type Kube interface {
	env.Kube
	DeleteNamespace(ctx context.Context, ns string) error
	ApplyCluster(ctx context.Context, manifests []string) error
}

// Grader runs the checks of a rendered scenario.
type Grader = env.Grader

// Factories build the collaborators. Each is called at most once per run and
// only by the commands that need it, so list, path, exam, lint, new, journal
// and doctor work without a cluster. ScriptKubeconfig, when set, receives
// the journal directory, the cluster kubeconfig and its context and returns
// the kubeconfig path exported to setup hooks and solve scripts (a pinned
// copy holding only the cluster context, cluster.ScriptKubeconfigPath in the
// cluster state directory); when nil the cluster kubeconfig is exported as
// is. When ScriptKubeconfig is set, its error or an empty path stops the
// command that needs the cluster: script checks never fall back to the
// cluster kubeconfig. Connect receives the cluster kubeconfig, its context
// and that pinned path ("" when there is none). Run executes a scenario script with both
// output streams on out; when nil, env.RunScript is used. Workstation
// receives the repository root and the journal directory; when nil, start
// never opens a shell and shell and node report that no workstation is
// wired. StateDir returns the cluster state directory, resolved once by the
// caller; when nil, cluster.StateDir resolves it. The audit directory, the
// workstation kubeconfig and the fault record are paths under it. Faults
// receives that directory, where the manager records active faults; when nil, fault and heal report
// that no fault manager is wired and start heals nothing. Telemetry receives
// the journal directory (journal.ShellLogPath and journal.RecordingsDir name
// the shell log and the recordings under it) and a cached answer to "is the
// audit log on"; when nil, episodes carry no telemetry.
type Factories struct {
	Cluster          func(profilesDir string) Cluster
	Journal          func(dir string) (Journal, error)
	Catalog          func(root string) (Catalog, error)
	Concepts         func(root string) (Concepts, error)
	Workstation      func(root, dataDir string) Workstation
	Connect          func(kubeconfig, contextName, scriptKubeconfig string) (Kube, Grader, error)
	ScriptKubeconfig func(journalDir, kubeconfig, contextName string) (string, error)
	Curriculum       func(path string) (*curriculum.Curriculum, error)
	Lint             func(root string, refs scenario.Refs) []error
	Scaffold         func(root, level, id string) (string, error)
	Run              func(ctx context.Context, script string, env []string, out io.Writer) error
	TempDir          func() (string, error)
	Faults           func(stateDir string) Faults
	StateDir         func() (string, error)
	Telemetry        func(journalDir string, auditAvailable func() bool) TelemetryFunc
}

// Options configure one run of the command line. IsTerminal says whether
// stdout is a terminal; IsInputTerminal says the same for stdin and, when
// left false while IsTerminal is true, is detected from Stdin when it is an
// *os.File. Executable returns the path exported to hooks as KUBEDRILL_BIN
// (default os.Executable); Rand(n) returns a value in [0, n) for the
// recommendation seeds (default math/rand); NamespaceSuffix returns the
// random part of an episode namespace (default: env's); TermWidth returns
// the column count of the terminal on stdout, or 0 when unknown (default:
// asked of stdout when it is a terminal).
type Options struct {
	Factories       Factories
	Stdout          io.Writer
	Stderr          io.Writer
	Stdin           io.Reader
	Getenv          func(string) string
	Getwd           func() (string, error)
	IsTerminal      bool
	IsInputTerminal bool
	Now             func() time.Time
	Executable      func() (string, error)
	Rand            func(n int64) int64
	NamespaceSuffix func() string
	TermWidth       func() int
}

// app is the state shared by the commands of one run.
type app struct {
	f               Factories
	stdout          io.Writer
	stderr          io.Writer
	stdin           *bufio.Reader
	rawStdin        io.Reader
	interrupted     <-chan struct{}
	getenv          func(string) string
	getwd           func() (string, error)
	isInputTerminal bool
	term            render.Terminal
	now             func() time.Time
	executable      func() (string, error)
	rnd             func(n int64) int64
	suffix          func() string
	teaOptions      []tea.ProgramOption

	rootFlag string
	root     string

	catalog  Catalog
	journal  Journal
	cluster  Cluster
	concepts Concepts
	conn     *lazyConn
	session  *env.Session
	faults   Faults

	audit    auditCache
	progress *render.Progress
}

// Main runs kubedrill with args and returns the process exit code.
func Main(ctx context.Context, args []string, o Options) int {
	a := newApp(o)
	a.interrupted = ctx.Done()
	cmd := a.rootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(a.stdout)
	cmd.SetErr(a.stderr)
	err := cmd.ExecuteContext(ctx)
	a.progress.End("")
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, ErrChecksFailed) {
		return ExitChecksFailed
	}
	fmt.Fprintf(a.stderr, "kubedrill: %s\n", a.explain(err))
	return ExitError
}

func newApp(o Options) *app {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Getwd == nil {
		o.Getwd = os.Getwd
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Executable == nil {
		o.Executable = os.Executable
	}
	if o.Rand == nil {
		o.Rand = rand.Int64N
	}
	if o.Factories.Run == nil {
		o.Factories.Run = func(ctx context.Context, script string, scriptEnv []string, out io.Writer) error {
			return env.RunScript(out)(ctx, script, scriptEnv)
		}
	}
	if o.Factories.TempDir == nil {
		o.Factories.TempDir = func() (string, error) { return os.MkdirTemp("", "kubedrill-smoke-") }
	}
	if o.TermWidth == nil && o.IsTerminal {
		o.TermWidth = func() int { return render.TTYWidth(o.Stdout) }
	}
	inputTerminal := o.IsInputTerminal
	if !inputTerminal && o.IsTerminal {
		inputTerminal = render.IsTerminal(o.Stdin)
	}
	a := &app{
		f:               o.Factories,
		stdout:          o.Stdout,
		stderr:          o.Stderr,
		stdin:           bufio.NewReader(o.Stdin),
		rawStdin:        o.Stdin,
		getenv:          o.Getenv,
		getwd:           o.Getwd,
		isInputTerminal: inputTerminal,
		now:             o.Now,
		executable:      o.Executable,
		rnd:             o.Rand,
		suffix:          o.NamespaceSuffix,
		term: render.Terminal{
			Out:        o.Stdout,
			IsTerminal: o.IsTerminal,
			Color:      o.IsTerminal && o.Getenv("NO_COLOR") == "",
			Getenv:     o.Getenv,
			Columns:    o.TermWidth,
		},
		progress: render.NewProgress(o.Stderr, o.Now),
	}
	return a
}

// explain turns a command error into the learner-facing line.
func (a *app) explain(err error) string {
	var notFound *scenario.NotFoundError
	switch {
	case errors.Is(err, env.ErrNoActiveEpisode) && err.Error() != env.ErrNoActiveEpisode.Error():
		return err.Error()
	case errors.Is(err, env.ErrNoActiveEpisode):
		return "no active episode; start one with: kubedrill start <id> (see kubedrill list)"
	case errors.As(err, &notFound):
		return fmt.Sprintf("scenario %q not found; see kubedrill list", notFound.Ref)
	case errors.Is(err, scenario.ErrNotFound):
		return err.Error() + "; see kubedrill list"
	case errors.Is(err, env.ErrNamespaceMissing):
		return withoutCause(err, env.ErrNamespaceMissing)
	case errors.Is(err, env.ErrNamespaceForeign):
		return withoutCause(err, env.ErrNamespaceForeign)
	case errors.Is(err, env.ErrNoMoreHints):
		return err.Error() + "; kubedrill solution shows the walkthrough"
	case errors.Is(err, env.ErrTimedOut):
		return "the exam time box expired; kubedrill journal shows the outcome"
	}
	return err.Error()
}

// withoutCause is the text of err without the trailing ": <cause>" its
// wrapping added, so a message that already says what happened is not
// followed by the sentinel's words.
func withoutCause(err, cause error) string {
	return strings.TrimSuffix(err.Error(), ": "+cause.Error())
}

// repoRoot resolves the repository root from --root, KUBEDRILL_ROOT, or the
// first ancestor of the working directory holding curriculum.yaml and
// scenarios/.
func (a *app) repoRoot() (string, error) {
	if a.root != "" {
		return a.root, nil
	}
	candidates := []string{a.rootFlag, a.getenv("KUBEDRILL_ROOT")}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			return "", fmt.Errorf("resolve root %s: %w", c, err)
		}
		if !isRepoRoot(abs) {
			return "", fmt.Errorf("root %s lacks curriculum.yaml or scenarios/", abs)
		}
		a.root = abs
		return abs, nil
	}
	wd, err := a.getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if isRepoRoot(dir) {
			a.root = dir
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", fmt.Errorf("repository root not found from %s (a directory with curriculum.yaml and scenarios/); run inside the checkout or set --root or KUBEDRILL_ROOT", wd)
}

func isRepoRoot(dir string) bool {
	if st, err := os.Stat(filepath.Join(dir, "curriculum.yaml")); err != nil || st.IsDir() {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "scenarios"))
	return err == nil && st.IsDir()
}

func (a *app) getCatalog() (Catalog, error) {
	if a.catalog != nil {
		return a.catalog, nil
	}
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	c, err := a.f.Catalog(root)
	if err != nil {
		return nil, err
	}
	a.catalog = c
	return c, nil
}

func (a *app) getConcepts() (Concepts, error) {
	if a.concepts != nil {
		return a.concepts, nil
	}
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	lib, err := a.f.Concepts(root)
	if err != nil {
		return nil, err
	}
	a.concepts = lib
	return lib, nil
}

// getWorkstation returns the toolbox manager, or nil when none is wired.
func (a *app) getWorkstation(j Journal) (Workstation, error) {
	if a.f.Workstation == nil {
		return nil, nil
	}
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	return a.f.Workstation(root, j.Dir()), nil
}

// insideWorkstation reports whether this process already runs in the
// workstation container, where a shell must never be nested.
func (a *app) insideWorkstation() bool {
	return a.getenv("KUBEDRILL_WORKSTATION") == "1"
}

func (a *app) getJournal() (Journal, error) {
	if a.journal != nil {
		return a.journal, nil
	}
	j, err := a.f.Journal(a.getenv("KUBEDRILL_DATA"))
	if err != nil {
		return nil, err
	}
	a.journal = j
	return j, nil
}

func (a *app) getCluster() (Cluster, error) {
	if a.cluster != nil {
		return a.cluster, nil
	}
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	a.cluster = a.f.Cluster(filepath.Join(root, "cluster", "profiles"))
	return a.cluster, nil
}

func (a *app) getConn() (*lazyConn, error) {
	if a.conn != nil {
		return a.conn, nil
	}
	cl, err := a.getCluster()
	if err != nil {
		return nil, err
	}
	c := &lazyConn{cluster: cl, connect: a.f.Connect}
	if a.f.ScriptKubeconfig != nil {
		c.scriptFn = a.learnerScriptKubeconfig
	}
	a.conn = c
	return a.conn, nil
}

// scriptKubeconfig returns the kubeconfig path exported to scenario scripts
// and pins it as the one the cluster connection hands to script checks.
func (a *app) scriptKubeconfig(j Journal) (string, error) {
	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	path, err := a.pinnedKubeconfig(conn.cluster, j)
	if err != nil {
		return "", err
	}
	if a.f.ScriptKubeconfig != nil {
		conn.setScript(path)
	}
	return path, nil
}

// pinnedKubeconfig writes the kubeconfig of j's scripts through
// Factories.ScriptKubeconfig, or returns the cluster kubeconfig when that
// factory is nil. An empty pinned path is an error, so script checks never
// fall back to the whole cluster kubeconfig.
func (a *app) pinnedKubeconfig(cl Cluster, j Journal) (string, error) {
	path, name, err := cl.Kubeconfig()
	if err != nil {
		return "", err
	}
	if a.f.ScriptKubeconfig == nil {
		return path, nil
	}
	pinned, err := a.f.ScriptKubeconfig(j.Dir(), path, name)
	if err != nil {
		return "", fmt.Errorf("write script kubeconfig for context %s: %w", name, err)
	}
	if pinned == "" {
		return "", fmt.Errorf("write script kubeconfig for context %s: no path returned", name)
	}
	return pinned, nil
}

// learnerScriptKubeconfig is the pinned kubeconfig of the learner's journal.
func (a *app) learnerScriptKubeconfig(cl Cluster) (string, error) {
	j, err := a.getJournal()
	if err != nil {
		return "", err
	}
	return a.pinnedKubeconfig(cl, j)
}

// getSession builds the learner session on the given journal. The cluster
// connection is opened on the first call that touches it. progress, when not
// nil, receives the Reset progress lines.
func (a *app) getSession(ctx context.Context, j Journal, progress func(string), learner bool) (*env.Session, *lazyConn, error) {
	cat, err := a.getCatalog()
	if err != nil {
		return nil, nil, err
	}
	conn, err := a.getConn()
	if err != nil {
		return nil, nil, err
	}
	kubeconfig, err := a.scriptKubeconfig(j)
	if err != nil {
		return nil, nil, err
	}
	d := env.Deps{
		Kube:       conn,
		Grader:     conn,
		Journal:    j,
		Catalog:    cat,
		Hook:       a.hookRunner(a.stderr),
		Out:        a.stderr,
		Kubeconfig: kubeconfig,
		Progress:   progress,
		Now:        a.now,
		Suffix:     a.suffix,
	}
	fm, err := a.getFaults()
	if err != nil {
		return nil, nil, err
	}
	if fm != nil {
		d.Healer = fm
	}
	if learner && a.f.Telemetry != nil {
		auditCtx := context.WithoutCancel(ctx)
		d.Telemetry = a.f.Telemetry(j.Dir(), func() bool { return a.auditEnabled(auditCtx) })
	}
	return env.New(d), conn, nil
}

// getFaults returns the fault manager recording its state in the cluster
// state directory, so every journal and the smoke run share one record of
// the faults on the one cluster, or nil when none is wired.
func (a *app) getFaults() (Faults, error) {
	if a.faults != nil || a.f.Faults == nil {
		return a.faults, nil
	}
	dir, err := a.stateDir()
	if err != nil {
		return nil, err
	}
	a.faults = a.f.Faults(dir)
	return a.faults, nil
}

// stateDir returns the cluster state directory from Factories.StateDir, or
// from cluster.StateDir when none is wired.
func (a *app) stateDir() (string, error) {
	if a.f.StateDir != nil {
		return a.f.StateDir()
	}
	return cluster.StateDir()
}

// auditCache keeps the first answer to "is the audit log on" for the rest
// of the process.
type auditCache struct {
	mu    sync.Mutex
	known bool
	on    bool
}

// auditCheckTimeout bounds the one question to the cluster about the audit
// log.
const auditCheckTimeout = 5 * time.Second

// auditEnabled reports whether the cluster records the API audit log; an
// error or a timeout counts as off.
func (a *app) auditEnabled(ctx context.Context) bool {
	on, _ := a.auditState(ctx)
	return on
}

// auditState asks the cluster, under ctx with a 5 s timeout, whether the
// audit log is on; known is false when the question failed. The first
// answer is kept for the rest of the process; a failure is asked again.
func (a *app) auditState(ctx context.Context) (on, known bool) {
	a.audit.mu.Lock()
	defer a.audit.mu.Unlock()
	if a.audit.known {
		return a.audit.on, true
	}
	cl, err := a.getCluster()
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, auditCheckTimeout)
	defer cancel()
	on, err = cl.AuditEnabled(ctx)
	if err != nil {
		return false, false
	}
	a.audit.known, a.audit.on = true, on
	return on, true
}

// hookRunner adapts Factories.Run to the env hook signature with out fixed.
// The setup hook and the smoke solve.sh receive KUBEDRILL_BIN, the path of
// this binary, so a hook can call kubedrill fault.
func (a *app) hookRunner(out io.Writer) func(ctx context.Context, script string, env []string) error {
	return func(ctx context.Context, script string, env []string) error {
		if exe, err := a.executable(); err == nil && exe != "" {
			env = append(append([]string(nil), env...), "KUBEDRILL_BIN="+exe)
		}
		return a.f.Run(ctx, script, env, out)
	}
}

func (a *app) learnerSession(ctx context.Context) (*env.Session, error) {
	if a.session != nil {
		return a.session, nil
	}
	j, err := a.getJournal()
	if err != nil {
		return nil, err
	}
	s, _, err := a.getSession(ctx, j, a.progress.Step, true)
	if err != nil {
		return nil, err
	}
	a.session = s
	return s, nil
}

// lazyConn opens the cluster connection on first use and satisfies env.Kube,
// env.Grader and the DeleteNamespace the smoke command needs. It is safe for
// concurrent use; only a successful connection is kept. When scriptFn is
// set (a ScriptKubeconfig factory is wired), the connection is opened only
// with a pinned script kubeconfig, never with the whole cluster one.
type lazyConn struct {
	cluster  Cluster
	connect  func(kubeconfig, contextName, scriptKubeconfig string) (Kube, Grader, error)
	scriptFn func(Cluster) (string, error)

	mu     sync.Mutex
	script string
	kube   Kube
	grader Grader
}

// setScript records the pinned kubeconfig handed to Connect, unless the
// connection is already open.
func (c *lazyConn) setScript(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kube == nil {
		c.script = path
	}
}

func (c *lazyConn) open() (Kube, Grader, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kube != nil {
		return c.kube, c.grader, nil
	}
	path, name, err := c.cluster.Kubeconfig()
	if err != nil {
		return nil, nil, fmt.Errorf("connect to cluster: %w", err)
	}
	script := c.script
	if script == "" && c.scriptFn != nil {
		script, err = c.scriptFn(c.cluster)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to cluster %s: script checks need a kubeconfig pinned to that context: %w", name, err)
		}
		if script == "" {
			return nil, nil, fmt.Errorf("connect to cluster %s: script checks need a kubeconfig pinned to that context, got none", name)
		}
	}
	k, g, err := c.connect(path, name, script)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to cluster %s (kubeconfig %s): %w; is the cluster up? kubedrill up", name, path, err)
	}
	c.script, c.kube, c.grader = script, k, g
	return k, g, nil
}

func (c *lazyConn) ResetNamespace(ctx context.Context, ns string, labels map[string]string) error {
	k, _, err := c.open()
	if err != nil {
		return err
	}
	return k.ResetNamespace(ctx, ns, labels)
}

func (c *lazyConn) ApplyManifests(ctx context.Context, ns string, manifests []string) error {
	k, _, err := c.open()
	if err != nil {
		return err
	}
	return k.ApplyManifests(ctx, ns, manifests)
}

func (c *lazyConn) DeleteNamespaces(ctx context.Context, selector, keep string) (int, error) {
	k, _, err := c.open()
	if err != nil {
		return 0, err
	}
	return k.DeleteNamespaces(ctx, selector, keep)
}

func (c *lazyConn) UnavailableAPIServices(ctx context.Context) ([]string, error) {
	k, _, err := c.open()
	if err != nil {
		return nil, err
	}
	return k.UnavailableAPIServices(ctx)
}

func (c *lazyConn) ApplyCluster(ctx context.Context, manifests []string) error {
	k, _, err := c.open()
	if err != nil {
		return err
	}
	return k.ApplyCluster(ctx, manifests)
}

func (c *lazyConn) NamespaceLabels(ctx context.Context, ns string) (map[string]string, error) {
	k, _, err := c.open()
	if err != nil {
		return nil, err
	}
	return k.NamespaceLabels(ctx, ns)
}

func (c *lazyConn) DeleteNamespace(ctx context.Context, ns string) error {
	k, _, err := c.open()
	if err != nil {
		return err
	}
	return k.DeleteNamespace(ctx, ns)
}

func (c *lazyConn) Grade(ctx context.Context, rd *scenario.Rendered) (grader.Grade, error) {
	_, g, err := c.open()
	if err != nil {
		return grader.Grade{}, err
	}
	return g.Grade(ctx, rd)
}

// confirm asks a yes/no question. On a terminal it reads the answer from
// stdin; on a pipe it reads one line and accepts y or yes; without any input
// the answer is an error naming the --yes flag.
func (a *app) confirm(question string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	fmt.Fprintf(a.stdout, "%s [y/N] ", question)
	line, err := a.readLine()
	if errors.Is(err, errInterrupted) {
		a.println()
		return false, err
	}
	if err != nil && line == "" {
		if a.isInputTerminal {
			a.println()
			return false, nil
		}
		a.println()
		return false, errors.New("confirmation needed (stdin is not a terminal): rerun with --yes")
	}
	if !a.isInputTerminal {
		a.println()
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}

// errInterrupted is returned by a prompt when SIGINT or SIGTERM arrives
// while it waits for a line.
var errInterrupted = errors.New("interrupted")

// readLine reads one line from stdin and returns errInterrupted as soon as
// the run is interrupted (the context Main received is canceled), so a
// single Ctrl-C ends a command that waits at a prompt. The read runs in its
// own goroutine, which ends with the line; after an interruption it stays
// blocked on stdin until the process exits, which follows the returned
// error. One goroutine per prompt, never a read ahead: stdin is handed to
// docker run -it afterwards and must not lose a line to a pending read.
func (a *app) readLine() (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := a.stdin.ReadString('\n')
		done <- result{line, err}
	}()
	select {
	case r := <-done:
		return r.line, r.err
	case <-a.interrupted:
		return "", errInterrupted
	}
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.stdout, format, args...)
}

func (a *app) println(args ...any) {
	fmt.Fprintln(a.stdout, args...)
}
