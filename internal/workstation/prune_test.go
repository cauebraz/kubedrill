package workstation

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var pruneNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// imageListing formats docker image ls --format {{.Tag}}\t{{.CreatedAt}}
// lines; age is how long before pruneNow each tag was created.
func imageListing(entries ...struct {
	tag string
	age time.Duration
}) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.tag)
		b.WriteString("\t")
		b.WriteString(pruneNow.Add(-e.age).Format(createdAtLayout))
		b.WriteString("\n")
	}
	return b.String()
}

type imageEntry = struct {
	tag string
	age time.Duration
}

func removedImages(fr *fakeRunner) []string {
	var removed []string
	for _, c := range fr.joined() {
		if strings.HasPrefix(c, "docker image rm ") {
			removed = append(removed, strings.TrimPrefix(c, "docker image rm "))
		}
	}
	return removed
}

func pruneManager(t *testing.T, listing string) (*Manager, *fakeRunner, *bytes.Buffer) {
	t.Helper()
	root := writeRepo(t)
	fr := &fakeRunner{scripts: map[string]string{
		"docker image inspect": "false",
		"docker image ls":      "printf '%s' " + shellQuote(listing),
	}}
	var out bytes.Buffer
	m := New(root, t.TempDir(), WithRunner(fr.run), WithOutput(&out))
	m.now = func() time.Time { return pruneNow }
	return m, fr, &out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestEnsureImagePruneKeepsCurrentAndHandMadeTags(t *testing.T) {
	root := writeRepo(t)
	current, err := New(root, t.TempDir()).ImageTag()
	if err != nil {
		t.Fatal(err)
	}
	cur := strings.TrimPrefix(current, imageRepo+":")
	listing := imageListing(
		imageEntry{cur, 30 * 24 * time.Hour},
		imageEntry{"custom-build", 30 * 24 * time.Hour},
		imageEntry{"abc123", 30 * 24 * time.Hour},
		imageEntry{"dev", 30 * 24 * time.Hour},
		imageEntry{"<none>", 30 * 24 * time.Hour},
		imageEntry{"aaaaaaaaaaaa", 30 * 24 * time.Hour},
		imageEntry{"bbbbbbbbbbbb", 8 * 24 * time.Hour},
	)
	m, fr, out := pruneManager(t, listing)
	m.root = root
	if _, err := m.EnsureImage(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{imageRepo + ":aaaaaaaaaaaa", imageRepo + ":bbbbbbbbbbbb"}
	if got := removedImages(fr); !reflect.DeepEqual(got, want) {
		t.Errorf("image rm calls: got %v, want %v (never the current tag %s, custom-build, abc123, dev or <none>)", got, want, cur)
	}
	if !strings.Contains(out.String(), "removed 2 older workstation image(s)\n") {
		t.Errorf("output: got %q, want the count of removed images", out.String())
	}
}

func TestEnsureImagePruneKeepsRecentTags(t *testing.T) {
	tests := []struct {
		name        string
		listing     string
		wantRemoved []string
		wantMsg     string
	}{
		{
			name: "mixed ages",
			listing: imageListing(
				imageEntry{"aaaaaaaaaaaa", 6 * 24 * time.Hour},
				imageEntry{"bbbbbbbbbbbb", 7*24*time.Hour + time.Minute},
				imageEntry{"cccccccccccc", time.Hour},
			) + "dddddddddddd\n" + "eeeeeeeeeeee\tnot a date\n",
			wantRemoved: []string{imageRepo + ":bbbbbbbbbbbb"},
			wantMsg:     "(sources changed)...",
		},
		{
			name:    "only a one-day-old tag",
			listing: imageListing(imageEntry{"aaaaaaaaaaaa", 24 * time.Hour}),
			wantMsg: "(sources changed)...",
		},
		{
			name:    "only tags within seven days or undated",
			listing: imageListing(imageEntry{"aaaaaaaaaaaa", 6 * 24 * time.Hour}, imageEntry{"cccccccccccc", time.Hour}) + "eeeeeeeeeeee\tnot a date\n",
			wantMsg: "(sources changed)...",
		},
		{
			name:    "no other tag with the image tag shape",
			listing: imageListing(imageEntry{"dev", 24 * time.Hour}),
			wantMsg: "(first time only, a few minutes)...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, fr, out := pruneManager(t, tt.listing)
			if _, err := m.EnsureImage(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := removedImages(fr); !reflect.DeepEqual(got, tt.wantRemoved) {
				t.Errorf("image rm calls: got %v, want %v (tags created within 7 days or without a readable date are kept)", got, tt.wantRemoved)
			}
			if !strings.Contains(out.String(), tt.wantMsg) {
				t.Errorf("output: got %q, want the line ending %q", out.String(), tt.wantMsg)
			}
		})
	}
}

func TestEnsureImageBuildCancelKillsTheProcessGroup(t *testing.T) {
	fr := &fakeRunner{scripts: map[string]string{
		"docker image inspect": "false",
		"docker build":         "sleep 30 & wait",
	}}
	m := New(writeRepo(t), t.TempDir(), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.EnsureImage(ctx)
	if err == nil {
		t.Fatal("EnsureImage with the context over: got nil error, want the build error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("EnsureImage returned after %s, want the build and its grandchild killed on cancel", elapsed)
	}
	for _, c := range fr.calls {
		if len(c.args) > 0 && c.args[0] == "build" && c.cmd.WaitDelay != waitDelay {
			t.Errorf("docker build WaitDelay: got %v, want %v", c.cmd.WaitDelay, waitDelay)
		}
	}
}

func TestProbeErrorCarriesStderr(t *testing.T) {
	msg := "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"
	fr := &fakeRunner{scripts: map[string]string{"docker info": "echo " + shellQuote(msg) + " >&2; exit 1"}}
	err := New(t.TempDir(), t.TempDir(), WithRunner(fr.run)).DockerAvailable(context.Background())
	want := "docker is not available (is Docker Desktop or the daemon running?): docker info: " + msg + ": exit status 1"
	if err == nil || err.Error() != want {
		t.Errorf("DockerAvailable with a socket permission error: got %v, want %q", err, want)
	}
}

func TestProbeTimeoutNamesTheDeadline(t *testing.T) {
	fr := &fakeRunner{scripts: map[string]string{"docker info": "sleep 5"}}
	m := New(t.TempDir(), t.TempDir(), WithRunner(fr.run))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := m.DockerAvailable(ctx)
	if err == nil {
		t.Fatal("DockerAvailable with a hung daemon: got nil error")
	}
	for _, want := range []string{"docker is not available", "docker info: context deadline exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("DockerAvailable returned after %s, want it bounded by the context deadline", elapsed)
	}
}

func TestEnsureBinaryBuildIsBounded(t *testing.T) {
	root := writeRepo(t)
	fr := &fakeRunner{}
	deadline := make(chan time.Time, 1)
	run := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		d, _ := ctx.Deadline()
		deadline <- d
		return fr.run(ctx, name, args...)
	}
	m := New(root, t.TempDir(), WithStateDir(t.TempDir()), WithRunner(run), WithOutput(&bytes.Buffer{}))
	start := time.Now()
	if _, err := m.EnsureBinary(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := <-deadline
	if d.IsZero() || d.Sub(start) < 9*time.Minute || d.Sub(start) > 11*time.Minute {
		t.Errorf("go build deadline: got %v after start, want about 10 minutes", d.Sub(start))
	}
	if len(fr.calls) != 1 || fr.calls[0].cmd.WaitDelay != waitDelay {
		t.Errorf("go build: got %d calls with WaitDelay %v, want one call with %v", len(fr.calls), fr.calls[0].cmd.WaitDelay, waitDelay)
	}

	slow := &fakeRunner{scripts: map[string]string{"go build": "sleep 5"}}
	m = New(root, t.TempDir(), WithStateDir(t.TempDir()), WithRunner(slow.run), WithOutput(&bytes.Buffer{}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.EnsureBinary(ctx); err == nil || !strings.Contains(err.Error(), "build linux kubedrill into ") {
		t.Errorf("EnsureBinary with the context over: got %v, want the build error", err)
	}
}

func TestEnsureDataDirsUseJournalLayout(t *testing.T) {
	m, _, _, data, kubeconfig := shellHarness(t, nil)
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(data, "shell"), filepath.Join(data, "recordings")} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: got %v, %v, want a 0700 directory", dir, info, err)
		}
	}
}
