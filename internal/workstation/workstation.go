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
// code belongs to the process inside the container.
var dockerLaunchCodes = []int{125, 126, 127}

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
// holds the linux kubedrill binary under bin/; the default is
// cluster.StateDir(). It lies outside the journal directory, which the
// container mounts read-write at /data.
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
		root:    root,
		dataDir: dataDir,
		out:     os.Stderr,
		now:     time.Now,
		run:     exec.CommandContext,
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		goarch:  runtime.GOARCH,
		goos:    runtime.GOOS,
		uid:     os.Getuid(),
		gid:     os.Getgid(),
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
// standard output.
func (m *Manager) probe(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := m.docker(ctx, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return out.String(), fmt.Errorf("docker %s: %w", strings.Join(args, " "), ctx.Err())
	}
	return out.String(), err
}

// EnsureImage returns the image tag, building the image from
// <root>/workstation when `docker image inspect` does not find it. It
// announces "rebuilding workstation image <tag> (sources changed)" when an
// older kubedrill-workstation tag exists and the first-time line otherwise,
// builds with --progress quiet, and ends with one "workstation image <tag>
// built in <elapsed>" line; on failure the build output is included in the
// error. After a successful build the older kubedrill-workstation tags are
// removed (never the current one, never a tag created within the last seven
// days; untagged images and tags that are not 12 hex digits, such as "dev",
// are skipped) and the count is printed as "removed <n> older workstation
// image(s)". The docker queries are bounded to ten seconds each; the build
// is not.
func (m *Manager) EnsureImage(ctx context.Context) (string, error) {
	tag, err := m.ImageTag()
	if err != nil {
		return "", err
	}
	if _, err := m.probe(ctx, "image", "inspect", tag); err == nil {
		return tag, nil
	}
	older := m.olderImages(ctx, tag)
	if len(older) > 0 {
		fmt.Fprintf(m.out, "rebuilding workstation image %s (sources changed)...\n", tag)
	} else {
		fmt.Fprintf(m.out, "building workstation image %s (first time only, a few minutes)...\n", tag)
	}
	start := m.now()
	build := m.docker(ctx, "build", "--progress", "quiet", "-t", tag, filepath.Join(m.root, imageDir))
	tail := &tailWriter{limit: stderrTail}
	build.Stdout = io.Discard
	build.Stderr = tail
	if err := build.Run(); err != nil {
		if msg := strings.TrimSpace(tail.String()); msg != "" {
			return "", fmt.Errorf("build workstation image %s: %s: %w", tag, msg, err)
		}
		return "", fmt.Errorf("build workstation image %s: %w", tag, err)
	}
	fmt.Fprintf(m.out, "workstation image %s built in %s\n", tag, m.now().Sub(start).Round(time.Second))
	removed := 0
	for _, old := range m.olderImages(ctx, tag) {
		if _, err := m.probe(ctx, "image", "rm", old); err == nil {
			removed++
		}
	}
	if removed > 0 {
		fmt.Fprintf(m.out, "removed %d older workstation image(s)\n", removed)
	}
	return tag, nil
}

// olderImages lists the kubedrill-workstation images whose tag has the
// ImageTag shape, differs from tag and was created more than seven days ago
// (an image whose creation time is missing or unreadable is kept), as
// repo:tag.
func (m *Manager) olderImages(ctx context.Context, tag string) []string {
	out, err := m.probe(ctx, "image", "ls", imageRepo, "--format", "{{.Tag}}\t{{.CreatedAt}}")
	if err != nil {
		return nil
	}
	current := strings.TrimPrefix(tag, imageRepo+":")
	cutoff := m.now().Add(-keepImages)
	var older []string
	for _, line := range strings.Split(out, "\n") {
		t, createdAt, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if t == current || !isImageTag(t) {
			continue
		}
		created, err := time.Parse(createdAtLayout, strings.TrimSpace(createdAt))
		if err != nil || !created.Before(cutoff) {
			continue
		}
		older = append(older, imageRepo+":"+t)
	}
	return older
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
// It rebuilds when the binary is missing or older than go.mod, go.sum or the
// newest non-test .go file under cmd/ and internal/ (testdata directories
// excluded). The go build is bounded to ten minutes on top of ctx.
func (m *Manager) EnsureBinary(ctx context.Context) (string, error) {
	out, err := m.binaryPath()
	if err != nil {
		return "", fmt.Errorf("build linux kubedrill: %w", err)
	}
	newest, err := newestSource(m.root)
	if err != nil {
		return "", fmt.Errorf("build linux kubedrill: %w", err)
	}
	if info, err := os.Stat(out); err == nil && !info.ModTime().Before(newest) {
		return out, nil
	}
	if err := atomicfile.MkdirPrivate(filepath.Dir(out)); err != nil {
		return "", fmt.Errorf("build linux kubedrill: %w", err)
	}
	bctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	cmd := m.run(bctx, "go", "build", "-buildvcs=false", "-trimpath", "-o", out, "./cmd/kubedrill")
	cmd.Dir = m.root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+m.goarch, "CGO_ENABLED=0")
	cmd.Stdout = m.out
	cmd.Stderr = m.out
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if bctx.Err() != nil && ctx.Err() == nil {
			return "", fmt.Errorf("build linux kubedrill into %s: gave up after %s: %w", out, buildTimeout, err)
		}
		return "", fmt.Errorf("build linux kubedrill into %s: %w", out, err)
	}
	return out, nil
}

func (m *Manager) binaryPath() (string, error) {
	state, err := m.state()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(m.root))
	return filepath.Join(state, "bin", "kubedrill-linux-"+m.goarch+"-"+hex.EncodeToString(sum[:])[:8]), nil
}

func (m *Manager) state() (string, error) {
	if m.stateDir != "" {
		return m.stateDir, nil
	}
	return cluster.StateDir()
}

func newestSource(root string) (time.Time, error) {
	var newest time.Time
	note := func(info fs.FileInfo) {
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return time.Time{}, fmt.Errorf("stat %s: %w", name, err)
		}
		note(info)
	}
	for _, sub := range []string{"cmd", "internal"} {
		dir := filepath.Join(root, sub)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			note(info)
			return nil
		})
		if err != nil {
			return time.Time{}, fmt.Errorf("walk %s: %w", dir, err)
		}
	}
	return newest, nil
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
// session, a docker client code (125, 126, 127) is reported with docker's
// message, and anything else is returned as is.
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
	if o.Kubeconfig == "" {
		return errors.New("start workstation: kubeconfig path is empty")
	}
	layout := o.Layout
	if layout == "" {
		layout = LayoutTmux
	}
	if !ValidLayout(layout) {
		return fmt.Errorf("start workstation: layout %q is not %q or %q", layout, LayoutTmux, LayoutShell)
	}
	tag, err := m.EnsureImage(ctx)
	if err != nil {
		return fmt.Errorf("start workstation: %w", err)
	}
	binary, err := m.EnsureBinary(ctx)
	if err != nil {
		return fmt.Errorf("start workstation: %w", err)
	}
	if err := m.checkKindNetwork(ctx); err != nil {
		return fmt.Errorf("start workstation: %w", err)
	}
	if err := m.ensureDataDirs(); err != nil {
		return fmt.Errorf("start workstation: %w", err)
	}
	audit := ""
	if m.auditDir != "" {
		if info, err := os.Stat(m.auditDir); err == nil && info.IsDir() {
			if audit, err = hostPath(m.auditDir); err != nil {
				return fmt.Errorf("start workstation: %w", err)
			}
		}
	}
	sources := make([]string, 4)
	for i, p := range []string{m.root, m.dataDir, o.Kubeconfig, filepath.Dir(binary)} {
		sources[i], err = hostPath(p)
		if err != nil {
			return fmt.Errorf("start workstation: %w", err)
		}
	}
	for _, p := range []string{sources[2], sources[3]} {
		if within(sources[1], p) {
			return fmt.Errorf("start workstation: %s is inside the journal directory %s, which the container mounts read-write; keep it in the cluster state directory (cluster.InternalKubeconfigPath, WithStateDir)", p, sources[1])
		}
	}
	binarySource := filepath.Join(sources[3], filepath.Base(binary))
	args := []string{"run", "--rm", "-it",
		"--network", kindNetwork,
		"--hostname", hostname,
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
	cmd := m.docker(ctx, args...)
	tail := m.attach(cmd)
	return sessionResult("start workstation", cmd.Run(), tail)
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
