// Package workstation builds and runs the learner's Docker workstation: a
// container with the Kubernetes tooling, the repository, the journal and the
// kubedrill binary mounted in, attached to the kind docker network.
package workstation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/journal"
)

const (
	imageRepo    = "kubedrill-workstation"
	imageDir     = "workstation"
	containerBin = "/usr/local/bin/kubedrill"
	repoMount    = "/workspace/repo"
	dataMount    = "/data"
	auditMount   = "/audit"
	auditLogIn   = auditMount + "/audit.log"
	kubeconfigIn = "/home/learner/.kube/config"
	homeIn       = "/home/learner"
	kindNetwork  = "kind"
	hostname     = "kubedrill"
	workdir      = "/workspace"
	waitDelay    = 5 * time.Second
	probeTimeout = 10 * time.Second
	buildTimeout = 10 * time.Minute
	keepImages   = 7 * 24 * time.Hour
	stderrTail   = 4096
	builderFile  = "builder.env"
	builderVar   = "GO_BUILD_IMAGE"

	// LayoutTmux opens a tmux session with `kubedrill ticket --follow`
	// above the shell on the left and `kubedrill watch` on the right.
	LayoutTmux = "tmux"
	// LayoutShell opens a single login shell.
	LayoutShell = "shell"
)

// imageFiles are the files whose content decides the image tag, in the order
// they are hashed.
var imageFiles = []string{"Dockerfile", "versions.env", "checksums.txt", "bashrc", "tmux.conf", "entrypoint.sh"}

// createdAtLayout is the format of docker image ls --format {{.CreatedAt}}.
const createdAtLayout = "2006-01-02 15:04:05 -0700 MST"

// hardening are the flags every workstation container runs with. No
// capability is added back: the image strips the file capability of ping,
// which then uses an unprivileged ICMP socket (net.ipv4.ping_group_range,
// which docker opens to every group in a container).
var hardening = []string{
	"--security-opt", "no-new-privileges",
	"--cap-drop", "ALL",
	"--pids-limit", "512",
	"--memory", "2g",
	"--init",
}

// dockerLaunchCodes are the exit codes the docker client reserves for its own
// failures (daemon error, command not runnable, command not found); any other
// code belongs to the process inside the container. 126 and 127 are also what
// bash returns after a command that is not runnable or not found, so
// sessionResult tells them apart by the stderr tail.
var dockerLaunchCodes = []int{dockerDaemonCode, 126, 127}

const dockerDaemonCode = 125

// dockerMarkers are the substrings, lowercased, of the messages the docker
// client and the OCI runtime print when they cannot start the command.
var dockerMarkers = []string{"docker", "daemon", "oci runtime", "exec failed", "exec:"}

// namesDocker reports whether msg is a docker client or runtime message.
func namesDocker(msg string) bool {
	lower := strings.ToLower(msg)
	for _, s := range dockerMarkers {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// ValidLayout reports whether s is LayoutTmux or LayoutShell.
func ValidLayout(s string) bool {
	return s == LayoutTmux || s == LayoutShell
}

// Runner builds the command for one external process; tests replace it.
type Runner func(ctx context.Context, name string, args ...string) *exec.Cmd

// Option configures a Manager.
type Option func(*Manager)

// WithOutput sets the writer that receives build progress; the default is
// os.Stderr.
func WithOutput(w io.Writer) Option {
	return func(m *Manager) {
		if w != nil {
			m.out = w
		}
	}
}

// WithAuditDir sets the host directory holding the cluster audit log
// (cluster.AuditDir). When it exists, Shell mounts it read-only at /audit
// and exports KUBEDRILL_AUDIT_LOG=/audit/audit.log, so the kubedrill inside
// the container reads the same log whatever journal it uses.
func WithAuditDir(dir string) Option {
	return func(m *Manager) {
		m.auditDir = dir
	}
}

// WithStateDir sets the cluster state directory (cluster.StateDir) that
// holds the linux kubedrill binary under bin/. It is required: there is
// no default, and Shell and the binary build fail without it. It lies
// outside the journal directory, which the container mounts read-write at
// /data.
func WithStateDir(dir string) Option {
	return func(m *Manager) {
		m.stateDir = dir
	}
}

// WithRunner replaces the function that creates external commands (docker,
// go); the default is exec.CommandContext.
func WithRunner(fn func(ctx context.Context, name string, args ...string) *exec.Cmd) Option {
	return func(m *Manager) {
		if fn != nil {
			m.run = fn
		}
	}
}

// Manager builds the workstation image and the linux kubedrill binary and
// starts containers. The zero value is not usable; use New.
type Manager struct {
	root     string
	dataDir  string
	stateDir string
	auditDir string
	out      io.Writer
	now      func() time.Time
	run      Runner
	lookPath func(file string) (string, error)
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	goarch   string
	goos     string
	uid      int
	gid      int
}

// New returns a Manager for the repository at root (which holds
// workstation/Dockerfile) and the journal directory dataDir.
func New(root, dataDir string, opts ...Option) *Manager {
	m := &Manager{
		root:     root,
		dataDir:  dataDir,
		out:      os.Stderr,
		now:      time.Now,
		run:      exec.CommandContext,
		lookPath: exec.LookPath,
		stdin:    os.Stdin,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		goarch:   runtime.GOARCH,
		goos:     runtime.GOOS,
		uid:      os.Getuid(),
		gid:      os.Getgid(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// ShellOptions describes one workstation session.
type ShellOptions struct {
	// Kubeconfig is the host path of a kubeconfig whose server address is
	// reachable from the kind docker network
	// (cluster.Manager.WriteInternalKubeconfig), mounted read-only. It must
	// lie outside the journal directory, which the container mounts
	// read-write: cluster.InternalKubeconfigPath is the place for it.
	Kubeconfig string
	// Layout is LayoutTmux (default) or LayoutShell.
	Layout string
	// Command, when set, replaces the layout and runs inside the container.
	Command []string
	// ClusterName is exported as KUBEDRILL_CLUSTER.
	ClusterName string
	// Exam exports KUBEDRILL_EXAM=1: no watch pane, the allowed-docs
	// banner, and PATH=/opt/exam/bin:/usr/bin:/bin, where /opt/exam/bin
	// holds kubectl, helm, yq, kubedrill and vim. /usr/bin and /bin stay on
	// PATH (jq, curl, git and the base system), and the repository stays
	// readable at /workspace/repo, solutions included: exam mode is a
	// practice aid against habit, not a sandbox (accepted risk).
	Exam bool
	// Deadline, when set, is exported as KUBEDRILL_DEADLINE in RFC 3339 (UTC)
	// and shown in the exam banner.
	Deadline time.Time
	// Web exports KUBEDRILL_WEB=1: the shell runs in the browser workspace,
	// which shows the ticket beside the terminal, so the welcome text
	// points there instead of printing it.
	Web bool
	// Name, when set, names the container, so a caller that cannot signal
	// the docker client (a closed browser tab) removes it with docker rm -f.
	Name string
}

// ImageTag returns the image reference for the current workstation files:
// "kubedrill-workstation:" followed by the first 12 hex digits of a sha256
// over Dockerfile, versions.env, checksums.txt, bashrc, tmux.conf and
// entrypoint.sh.
func (m *Manager) ImageTag() (string, error) {
	h := sha256.New()
	dir := filepath.Join(m.root, imageDir)
	for _, name := range imageFiles {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("compute workstation image tag: %w", err)
		}
		fmt.Fprintf(h, "%s\n%d\n", name, len(data))
		h.Write(data)
	}
	return imageRepo + ":" + hex.EncodeToString(h.Sum(nil))[:12], nil
}

// docker builds a docker client command with DOCKER_CLI_HINTS=false in its
// environment.
func (m *Manager) docker(ctx context.Context, args ...string) *exec.Cmd {
	cmd := m.run(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), "DOCKER_CLI_HINTS=false")
	return cmd
}

// probe runs a non-interactive docker query bounded to ten seconds, so a
// hung daemon fails the step instead of freezing it, and returns its
// standard output. A failure names the query and carries the last bytes of
// its standard error (a permission error on the docker socket, for
// example).
func (m *Manager) probe(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := m.docker(ctx, args...)
	var out bytes.Buffer
	tail := &tailWriter{limit: stderrTail}
	cmd.Stdout = &out
	cmd.Stderr = tail
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil {
		return out.String(), nil
	}
	op := "docker " + strings.Join(args, " ")
	if ctx.Err() != nil {
		return out.String(), fmt.Errorf("%s: %w", op, ctx.Err())
	}
	if msg := strings.TrimSpace(tail.String()); msg != "" {
		return out.String(), fmt.Errorf("%s: %s: %w", op, msg, err)
	}
	return out.String(), fmt.Errorf("%s: %w", op, err)
}

// EnsureImage returns the image tag, building the image from
// <root>/workstation when `docker image inspect` does not find it. It
// announces "rebuilding workstation image <tag> (sources changed)" when any
// other kubedrill-workstation tag with the ImageTag shape exists, whatever
// its age, and the first-time line otherwise, builds with --progress quiet,
// and ends with one "workstation image <tag> built in <elapsed>" line; on
// failure the build output is included in the error. After a successful
// build the older kubedrill-workstation tags are removed (never the current
// one, never a tag created within the last seven days; untagged images and
// tags that are not 12 hex digits, such as "dev", are skipped) and the count
// is printed as "removed <n> older workstation image(s)". The docker queries
// are bounded to ten seconds each; the build is bounded only by ctx, and
// canceling ctx kills the build's whole process group.
func (m *Manager) EnsureImage(ctx context.Context) (string, error) {
	tag, err := m.ImageTag()
	if err != nil {
		return "", err
	}
	if _, err := m.probe(ctx, "image", "inspect", tag); err == nil {
		return tag, nil
	}
	if len(m.otherImages(ctx, tag)) > 0 {
		fmt.Fprintf(m.out, "rebuilding workstation image %s (sources changed)...\n", tag)
	} else {
		fmt.Fprintf(m.out, "building workstation image %s (first time only, a few minutes)...\n", tag)
	}
	start := m.now()
	build := m.docker(ctx, "build", "--progress", "quiet", "-t", tag, filepath.Join(m.root, imageDir))
	tail := &tailWriter{limit: stderrTail}
	build.Stdout = io.Discard
	build.Stderr = tail
	killGroupOnCancel(build)
	if err := build.Run(); err != nil {
		if msg := strings.TrimSpace(tail.String()); msg != "" {
			return "", fmt.Errorf("build workstation image %s: %s: %w", tag, msg, err)
		}
		return "", fmt.Errorf("build workstation image %s: %w", tag, err)
	}
	fmt.Fprintf(m.out, "workstation image %s built in %s\n", tag, m.now().Sub(start).Round(time.Second))
	removed := 0
	for _, old := range m.prunable(m.otherImages(ctx, tag)) {
		if _, err := m.probe(ctx, "image", "rm", old); err == nil {
			removed++
		}
	}
	if removed > 0 {
		fmt.Fprintf(m.out, "removed %d older workstation image(s)\n", removed)
	}
	return tag, nil
}

// Prepare builds what Shell needs before a session can start, the image
// (EnsureImage) and the linux binary (EnsureBinary), with their progress
// written to out instead of the manager's output, so it can run while
// another step owns the terminal.
func (m *Manager) Prepare(ctx context.Context, out io.Writer) error {
	c := *m
	c.out = out
	if _, err := c.EnsureImage(ctx); err != nil {
		return fmt.Errorf("prepare workstation: %w", err)
	}
	if _, err := c.EnsureBinary(ctx); err != nil {
		return fmt.Errorf("prepare workstation: %w", err)
	}
	return nil
}

// killGroupOnCancel runs cmd in its own process group, kills the whole group
// when its context ends, and stops waiting for its output pipes waitDelay
// later, so a grandchild that keeps a pipe open cannot hold Run.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
}

// workstationImage is one kubedrill-workstation tag other than the current
// one; dated is false when its creation time is missing or unreadable.
type workstationImage struct {
	ref     string
	created time.Time
	dated   bool
}

// otherImages lists the kubedrill-workstation images whose tag has the
// ImageTag shape and differs from tag.
func (m *Manager) otherImages(ctx context.Context, tag string) []workstationImage {
	out, err := m.probe(ctx, "image", "ls", imageRepo, "--format", "{{.Tag}}\t{{.CreatedAt}}")
	if err != nil {
		return nil
	}
	current := strings.TrimPrefix(tag, imageRepo+":")
	var others []workstationImage
	for _, line := range strings.Split(out, "\n") {
		t, createdAt, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if t == current || !isImageTag(t) {
			continue
		}
		created, err := time.Parse(createdAtLayout, strings.TrimSpace(createdAt))
		others = append(others, workstationImage{ref: imageRepo + ":" + t, created: created, dated: err == nil})
	}
	return others
}

// prunable returns the images created more than seven days ago; an image
// without a readable creation time is kept.
func (m *Manager) prunable(images []workstationImage) []string {
	cutoff := m.now().Add(-keepImages)
	var refs []string
	for _, img := range images {
		if img.dated && img.created.Before(cutoff) {
			refs = append(refs, img.ref)
		}
	}
	return refs
}

// isImageTag reports whether t has the shape ImageTag produces (12 lowercase
// hex digits), so hand-made tags such as "dev" and untagged images are never
// pruned.
func isImageTag(t string) bool {
	if len(t) != 12 {
		return false
	}
	for _, c := range t {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// EnsureBinary cross-compiles cmd/kubedrill for linux on the host's
// architecture into <stateDir>/bin (outside the journal directory) and
// returns the path. The file name ends with the first 8 hex digits of a
// sha256 over the repository root so two checkouts keep separate binaries.
// It runs go build every time and lets the go build cache decide what is
// stale, so an added, changed or deleted source file always reaches the
// binary; a build with nothing to do returns in a fraction of a second and
// leaves the file untouched. Without go on PATH it builds inside the
// golang image of workstation/builder.env (goBuild). The build is bounded
// to ten minutes on top of ctx.
func (m *Manager) EnsureBinary(ctx context.Context) (string, error) {
	out, err := m.binaryPath()
	if err != nil {
		return "", fmt.Errorf("build linux kubedrill: %w", err)
	}
	if err := atomicfile.MkdirPrivate(filepath.Dir(out)); err != nil {
		return "", fmt.Errorf("build linux kubedrill: %w", err)
	}
	bctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	cmd, err := m.goBuild(bctx, out)
	if err != nil {
		return "", fmt.Errorf("build linux kubedrill into %s: %w", out, err)
	}
	cmd.Stdout = m.out
	cmd.Stderr = m.out
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if bctx.Err() != nil && ctx.Err() == nil {
			return "", fmt.Errorf("build linux kubedrill into %s: gave up after %s: %w", out, buildTimeout, err)
		}
		return "", fmt.Errorf("build linux kubedrill into %s: %w", out, err)
	}
	m.pruneBinaries(out)
	return out, nil
}

// goBuild returns the command that builds cmd/kubedrill for linux into
// out: the go on PATH when there is one, otherwise go inside the image
// GO_BUILD_IMAGE of workstation/builder.env (the file start.sh reads), with
// the module and build caches under <state>/go so later builds are
// incremental.
func (m *Manager) goBuild(ctx context.Context, out string) (*exec.Cmd, error) {
	args := []string{"build", "-buildvcs=false", "-trimpath", "-o", out, "./cmd/kubedrill"}
	if _, err := m.lookPath("go"); err == nil {
		cmd := m.run(ctx, "go", args...)
		cmd.Dir = m.root
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+m.goarch, "CGO_ENABLED=0")
		return cmd, nil
	}
	image, err := m.builderImage()
	if err != nil {
		return nil, err
	}
	state, err := m.state()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(state, "go")
	if err := atomicfile.MkdirPrivate(cache); err != nil {
		return nil, err
	}
	sources := make([]string, 3)
	for i, p := range []string{m.root, filepath.Dir(out), cache} {
		if sources[i], err = hostPath(p); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(m.out, "go is not on PATH; building kubedrill for the workstation inside %s\n", image)
	dargs := []string{"run", "--rm"}
	dargs = append(dargs, m.linuxUser()...)
	dargs = append(dargs,
		"-e", "HOME=/tmp",
		"-e", "GOCACHE=/cache/build",
		"-e", "GOMODCACHE=/cache/mod",
		"-e", "GOOS=linux",
		"-e", "GOARCH="+m.goarch,
		"-e", "CGO_ENABLED=0",
	)
	dargs = append(dargs, bindMount(sources[0], "/src", true)...)
	dargs = append(dargs, bindMount(sources[1], "/out", false)...)
	dargs = append(dargs, bindMount(sources[2], "/cache", false)...)
	dargs = append(dargs, "-w", "/src", image, "go", "build", "-buildvcs=false", "-trimpath", "-o", "/out/"+filepath.Base(out), "./cmd/kubedrill")
	return m.docker(ctx, dargs...), nil
}

// builderImage reads GO_BUILD_IMAGE from workstation/builder.env and
// requires it pinned by digest.
func (m *Manager) builderImage() (string, error) {
	path := filepath.Join(m.root, imageDir, builderFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("go is not on PATH and the build image is unknown: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), builderVar+"="); ok {
			if !strings.Contains(v, "@sha256:") {
				return "", fmt.Errorf("%s: %s %q is not pinned by digest", path, builderVar, v)
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("%s: no %s line", path, builderVar)
}

// pruneBinaries removes the linux binaries of other checkouts or
// architectures in the directory of keep that were last built more than
// seven days ago; the next shell of that checkout builds its binary again.
// A removal that fails is a warning on the output.
func (m *Manager) pruneBinaries(keep string) {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(keep), "kubedrill-linux-*"))
	if err != nil {
		return
	}
	cutoff := m.now().Add(-keepImages)
	for _, path := range matches {
		if path == keep {
			continue
		}
		st, err := os.Stat(path)
		if err != nil || st.IsDir() || !st.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(m.out, "warning: remove old workstation binary: %v\n", err)
		}
	}
}

func (m *Manager) binaryPath() (string, error) {
	state, err := m.state()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(m.root))
	return filepath.Join(state, "bin", "kubedrill-linux-"+m.goarch+"-"+hex.EncodeToString(sum[:])[:8]), nil
}

// errNoStateDir is the error of an operation that needs the cluster state
// directory on a Manager built without WithStateDir.
var errNoStateDir = errors.New("workstation: cluster state directory not set; build the manager with WithStateDir")

// state is the cluster state directory set by WithStateDir; there is no
// default, so the composition root resolves it once for every package.
func (m *Manager) state() (string, error) {
	if m.stateDir == "" {
		return "", errNoStateDir
	}
	return m.stateDir, nil
}

// hostPath resolves a bind-mount source: symlinks evaluated, absolute, and
// free of the characters the --mount syntax reserves.
func hostPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve mount source %s: %w", path, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve mount source %s: %w", path, err)
	}
	if strings.ContainsAny(abs, ",:") {
		return "", fmt.Errorf("mount source %s contains ',' or ':', which docker --mount does not accept; move the checkout or the data directory", abs)
	}
	return abs, nil
}

// within reports whether path is dir or lies under it; both are resolved
// with hostPath.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkJournalOverlap refuses a session whose journal directory data, which
// the container mounts read-write, contains the kubeconfig, the binary
// directory, the repository root or the cluster state directory, or lies
// inside the repository root or the state directory: the container could
// otherwise rewrite code or files the host later builds or trusts. All
// paths are resolved with hostPath.
func (m *Manager) checkJournalOverlap(data, kubeconfig, binDir string) error {
	for _, p := range []string{kubeconfig, binDir} {
		if within(data, p) {
			return fmt.Errorf("%s is inside the journal directory %s, which the container mounts read-write; keep it in the cluster state directory (cluster.InternalKubeconfigPath, WithStateDir)", p, data)
		}
	}
	state, err := m.state()
	if err != nil {
		return err
	}
	if state, err = hostPath(state); err != nil {
		return err
	}
	root, err := hostPath(m.root)
	if err != nil {
		return err
	}
	for _, d := range []struct{ name, path string }{{"repository", root}, {"cluster state directory", state}} {
		if within(data, d.path) || within(d.path, data) {
			return fmt.Errorf("the journal directory %s and the %s %s overlap; the container mounts the journal read-write and could change files the host builds or trusts, so set KUBEDRILL_DATA to a directory outside both", data, d.name, d.path)
		}
	}
	return nil
}

func bindMount(source, target string, readonly bool) []string {
	spec := "type=bind,source=" + source + ",target=" + target
	if readonly {
		spec += ",readonly"
	}
	return []string{"--mount", spec}
}

// checkKindNetwork fails when the kind docker network does not exist, which
// is the case before `kubedrill up` or when the cluster runs on another
// provider.
func (m *Manager) checkKindNetwork(ctx context.Context) error {
	if _, err := m.probe(ctx, "network", "inspect", kindNetwork); err != nil {
		return errors.New("kind docker network not found; was the cluster created with docker? (kubedrill up)")
	}
	return nil
}

// attach wires the terminal streams and the shutdown behavior of an
// interactive docker command: SIGTERM to the client on cancel, five seconds
// for it to exit, and the tail of stderr kept for error messages.
func (m *Manager) attach(cmd *exec.Cmd) *tailWriter {
	tail := &tailWriter{w: m.stderr, limit: stderrTail}
	cmd.Stdin = m.stdin
	cmd.Stdout = m.stdout
	cmd.Stderr = tail
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	return tail
}

// sessionResult turns the exit of an interactive docker command into the
// caller's error: a process exit inside the container is a normal end of
// session, 125 is docker's own failure, and 126 or 127 are docker's only
// when the stderr tail carries a docker message. Every session runs with a
// TTY, so the container's output reaches the client's stdout and its
// stderr holds only client messages; a bash that ends with 126 or 127
// because the learner's last command was not runnable or not found is a
// normal end of session. Anything else is returned as is.
func sessionResult(op string, err error, tail *tailWriter) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("%s: %w", op, err)
	}
	code := exitErr.ExitCode()
	if !slices.Contains(dockerLaunchCodes, code) {
		return nil
	}
	msg := strings.TrimSpace(tail.String())
	if code != dockerDaemonCode && !namesDocker(msg) {
		return nil
	}
	if msg == "" {
		return fmt.Errorf("%s: docker exited with status %d: %w", op, code, err)
	}
	return fmt.Errorf("%s: docker exited with status %d: %s: %w", op, code, msg, err)
}

// Shell runs the workstation container attached to the current terminal and
// returns when it exits. A shell that ends with any exit status is a normal
// end of session; only docker's own failures (exit codes 125, 126 and 127)
// are errors.
func (m *Manager) Shell(ctx context.Context, o ShellOptions) error {
	cmd, err := m.ShellCommand(ctx, o)
	if err != nil {
		return err
	}
	tail := m.attach(cmd)
	return sessionResult("start workstation", cmd.Run(), tail)
}

// ShellCommand prepares a workstation session (image, linux binary, kind
// network, data directories, mount checks) and returns its `docker run
// --rm -it` command without starting it or setting its standard streams,
// so the caller can attach it to a terminal of its own, such as the
// pseudo-terminal of the browser workspace. The command needs a TTY on its
// standard input.
func (m *Manager) ShellCommand(ctx context.Context, o ShellOptions) (*exec.Cmd, error) {
	if o.Kubeconfig == "" {
		return nil, errors.New("start workstation: kubeconfig path is empty")
	}
	layout := o.Layout
	if layout == "" {
		layout = LayoutTmux
	}
	if !ValidLayout(layout) {
		return nil, fmt.Errorf("start workstation: layout %q is not %q or %q", layout, LayoutTmux, LayoutShell)
	}
	tag, err := m.EnsureImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("start workstation: %w", err)
	}
	binary, err := m.EnsureBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("start workstation: %w", err)
	}
	if err := m.checkKindNetwork(ctx); err != nil {
		return nil, fmt.Errorf("start workstation: %w", err)
	}
	if err := m.ensureDataDirs(); err != nil {
		return nil, fmt.Errorf("start workstation: %w", err)
	}
	audit := ""
	if m.auditDir != "" {
		if info, err := os.Stat(m.auditDir); err == nil && info.IsDir() {
			if audit, err = hostPath(m.auditDir); err != nil {
				return nil, fmt.Errorf("start workstation: %w", err)
			}
		}
	}
	sources := make([]string, 4)
	for i, p := range []string{m.root, m.dataDir, o.Kubeconfig, filepath.Dir(binary)} {
		sources[i], err = hostPath(p)
		if err != nil {
			return nil, fmt.Errorf("start workstation: %w", err)
		}
	}
	if err := m.checkJournalOverlap(sources[1], sources[2], sources[3]); err != nil {
		return nil, fmt.Errorf("start workstation: %w", err)
	}
	binarySource := filepath.Join(sources[3], filepath.Base(binary))
	args := []string{"run", "--rm", "-it",
		"--network", kindNetwork,
		"--hostname", hostname,
	}
	if o.Name != "" {
		args = append(args, "--name", o.Name)
	}
	args = append(args, hardening...)
	args = append(args, bindMount(sources[0], repoMount, true)...)
	args = append(args, bindMount(sources[1], dataMount, false)...)
	args = append(args, bindMount(sources[2], kubeconfigIn, true)...)
	args = append(args, bindMount(binarySource, containerBin, true)...)
	if audit != "" {
		args = append(args, bindMount(audit, auditMount, true)...)
		args = append(args, "-e", "KUBEDRILL_AUDIT_LOG="+auditLogIn)
	}
	args = append(args,
		"-e", "KUBEDRILL_ROOT="+repoMount,
		"-e", "KUBEDRILL_DATA="+dataMount,
		"-e", "KUBECONFIG="+kubeconfigIn,
		"-e", "KUBEDRILL_WORKSTATION=1",
		"-e", "KUBEDRILL_CLUSTER="+o.ClusterName,
		"-e", "KUBEDRILL_NO_TERM_QUERY=1",
		"-e", "TERM",
	)
	if o.Exam {
		args = append(args, "-e", "KUBEDRILL_EXAM=1")
	}
	if o.Web {
		args = append(args, "-e", "KUBEDRILL_WEB=1")
	}
	if !o.Deadline.IsZero() {
		args = append(args, "-e", "KUBEDRILL_DEADLINE="+o.Deadline.UTC().Format(time.RFC3339))
	}
	args = append(args, m.linuxUser()...)
	args = append(args, "-w", workdir, tag)
	if len(o.Command) > 0 {
		args = append(args, o.Command...)
	} else {
		args = append(args, layout)
	}
	return m.docker(ctx, args...), nil
}

// linuxUser runs the container as the host user on Linux, where bind
// mounts keep host ownership, so files written to /data stay the learner's;
// Docker Desktop on macOS maps ownership itself.
func (m *Manager) linuxUser() []string {
	if m.goos != "linux" {
		return nil
	}
	return []string{"--user", strconv.Itoa(m.uid) + ":" + strconv.Itoa(m.gid), "-e", "HOME=" + homeIn}
}

// ensureDataDirs creates the shell log and recordings directories under the
// journal directory, which the container writes to through /data, and
// restricts them to their owner (0700): they hold every command the
// learner typed.
func (m *Manager) ensureDataDirs() error {
	for _, dir := range []string{filepath.Dir(journal.ShellLogPath(m.dataDir)), journal.RecordingsDir(m.dataDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s directory: %w", filepath.Base(dir), err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restrict %s directory: %w", filepath.Base(dir), err)
		}
	}
	return nil
}

// Replay plays an asciinema recording inside the workstation image with
// the journal directory mounted read-only at /data. cast is a file name
// under <dataDir>/recordings or a path inside the journal directory.
func (m *Manager) Replay(ctx context.Context, cast string) error {
	if cast == "" {
		return errors.New("replay: recording path is empty")
	}
	path := cast
	if !filepath.IsAbs(path) && filepath.Base(path) == path {
		path = filepath.Join(journal.RecordingsDir(m.dataDir), path)
	}
	data, err := hostPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("replay %s: %w", cast, err)
	}
	file, err := hostPath(path)
	if err != nil {
		return fmt.Errorf("replay %s: %w", cast, err)
	}
	rel, err := filepath.Rel(data, file)
	if err != nil || rel == "." || !within(data, file) {
		return fmt.Errorf("replay %s: recording is not inside the journal directory %s", cast, data)
	}
	tag, err := m.EnsureImage(ctx)
	if err != nil {
		return fmt.Errorf("replay %s: %w", cast, err)
	}
	args := []string{"run", "--rm", "-it", "--network", "none"}
	args = append(args, hardening...)
	args = append(args, bindMount(data, dataMount, true)...)
	args = append(args, "-e", "TERM")
	args = append(args, m.linuxUser()...)
	args = append(args, tag, "asciinema", "play", "-i", "2", dataMount+"/"+filepath.ToSlash(rel))
	cmd := m.docker(ctx, args...)
	tail := m.attach(cmd)
	return sessionResult("replay "+cast, cmd.Run(), tail)
}

// NodeShell opens a bash session inside one kind node container
// (<clusterName>-<node>); node is control-plane, worker or worker2. A shell
// that ends with any exit status is a normal end of session.
func (m *Manager) NodeShell(ctx context.Context, clusterName, node string) error {
	if nodes := cluster.NodeNames(); !slices.Contains(nodes, node) {
		return fmt.Errorf("node shell: node %q is not one of %s", node, strings.Join(nodes, ", "))
	}
	if clusterName == "" {
		return errors.New("node shell: cluster name is empty")
	}
	name := clusterName + "-" + node
	state, err := m.probe(ctx, "inspect", "-f", "{{.State.Running}}", name)
	if err != nil || strings.TrimSpace(state) != "true" {
		return fmt.Errorf("node %s is not running; create the cluster with: kubedrill up", name)
	}
	cmd := m.docker(ctx, "exec", "-it", name, "bash")
	tail := m.attach(cmd)
	return sessionResult("node shell on "+name, cmd.Run(), tail)
}

// DebugNamespace holds the node debugging pods NodeDebug creates. The
// checks and the telemetry read only the episode namespace, so these pods
// count neither in the grade nor in the debrief.
const DebugNamespace = "kubedrill-debug"

// NodeDebug opens a root shell on the kind node <clusterName>-<node> without
// docker, the way a real cluster allows it: kubectl debug
// node/<clusterName>-<node> --profile=sysadmin with image, chrooted into
// the node filesystem at /host. It applies DebugNamespace first, deletes the
// finished pods in it before the session and, after it, the debugging pod
// for this node that the session created (pods listed before the session,
// such as another live session's, are kept; the deletion outlives a
// cancelled ctx by up to 30 seconds). It runs the kubectl on PATH with the
// current KUBECONFIG. A session that kubectl ends with a non-zero status
// returns nil: kubectl has already printed its own message, and the status
// may be the last command of the node shell.
func (m *Manager) NodeDebug(ctx context.Context, clusterName, node, image string) error {
	if nodes := cluster.NodeNames(); !slices.Contains(nodes, node) {
		return fmt.Errorf("node debug: node %q is not one of %s", node, strings.Join(nodes, ", "))
	}
	if clusterName == "" {
		return errors.New("node debug: cluster name is empty")
	}
	name := clusterName + "-" + node
	apply := m.run(ctx, "kubectl", "apply", "-f", "-")
	apply.Stdin = strings.NewReader("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + DebugNamespace + "\n")
	if out, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("node debug %s: apply namespace %s: %w: %s", name, DebugNamespace, err, strings.TrimSpace(string(out)))
	}
	m.deleteFinishedDebugPods(ctx)
	before, listed := m.debugPods(ctx, name)
	defer func() {
		if !listed {
			return
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		after, ok := m.debugPods(cctx, name)
		if !ok {
			return
		}
		var created []string
		for _, p := range after {
			if !slices.Contains(before, p) {
				created = append(created, p)
			}
		}
		if len(created) > 0 {
			args := append([]string{"delete", "pods", "--namespace", DebugNamespace, "--ignore-not-found", "--wait=false"}, created...)
			_ = m.run(cctx, "kubectl", args...).Run()
		}
	}()
	cmd := m.run(ctx, "kubectl", "debug", "node/"+name, "--namespace", DebugNamespace, "-it", "--quiet",
		"--profile=sysadmin", "--image="+image, "--", "chroot", "/host", "/bin/bash", "-l")
	tail := m.attach(cmd)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err == nil || errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("node debug %s: %w: %s", name, err, strings.TrimSpace(tail.String()))
}

// debugPods lists the pods in DebugNamespace that kubectl debug created for
// node (named node-debugger-<node>-<suffix>); ok is false when kubectl
// fails.
func (m *Manager) debugPods(ctx context.Context, node string) (names []string, ok bool) {
	out, err := m.run(ctx, "kubectl", "get", "pods", "--namespace", DebugNamespace, "-o", "jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return nil, false
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "node-debugger-"+node+"-") {
			names = append(names, p)
		}
	}
	return names, true
}

// deleteFinishedDebugPods deletes the pods in DebugNamespace that are
// neither Pending nor Running, best effort: a failure leaves them for the
// next session or kubedrill down.
func (m *Manager) deleteFinishedDebugPods(ctx context.Context) {
	cmd := m.run(ctx, "kubectl", "delete", "pods", "--namespace", DebugNamespace,
		"--field-selector=status.phase!=Pending,status.phase!=Running", "--ignore-not-found", "--wait=false")
	_ = cmd.Run()
}

// RemoveContainer removes the container name, running or not; a container
// that is already gone is no error.
func (m *Manager) RemoveContainer(ctx context.Context, name string) error {
	if _, err := m.probe(ctx, "rm", "-f", name); err != nil && !strings.Contains(err.Error(), "No such container") {
		return fmt.Errorf("remove workstation container %s: %w", name, err)
	}
	return nil
}

// DockerAvailable reports whether the docker daemon answers `docker info`
// within ten seconds.
func (m *Manager) DockerAvailable(ctx context.Context) error {
	if _, err := m.probe(ctx, "info"); err != nil {
		return fmt.Errorf("docker is not available (is Docker Desktop or the daemon running?): %w", err)
	}
	return nil
}

// tailWriter forwards writes to w and keeps the last limit bytes.
type tailWriter struct {
	w     io.Writer
	limit int
	buf   []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	if t.w == nil {
		return len(p), nil
	}
	return t.w.Write(p)
}

func (t *tailWriter) String() string {
	return string(t.buf)
}
