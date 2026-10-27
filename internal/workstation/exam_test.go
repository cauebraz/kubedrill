package workstation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellExamEnvAndDataDirs(t *testing.T) {
	deadline := time.Date(2026, 10, 25, 21, 30, 0, 0, time.FixedZone("BRT", -3*3600))
	tests := []struct {
		name   string
		opts   ShellOptions
		want   []string
		reject []string
	}{
		{name: "practice", reject: []string{"KUBEDRILL_EXAM", "KUBEDRILL_DEADLINE"}},
		{name: "exam with deadline", opts: ShellOptions{Exam: true, Deadline: deadline}, want: []string{" -e KUBEDRILL_EXAM=1 ", " -e KUBEDRILL_DEADLINE=2026-10-26T00:30:00Z "}},
		{name: "exam without deadline", opts: ShellOptions{Exam: true}, want: []string{" -e KUBEDRILL_EXAM=1 "}, reject: []string{"KUBEDRILL_DEADLINE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, fr, _, data, kubeconfig := shellHarness(t, nil)
			m.goos = "darwin"
			o := tc.opts
			o.Kubeconfig = kubeconfig
			if err := runShell(m, context.Background(), o); err != nil {
				t.Fatalf("Shell: %v", err)
			}
			run, ok := dockerRun(fr)
			if !ok {
				t.Fatalf("no docker run in %v", fr.joined())
			}
			argv := " " + strings.Join(run.args, " ") + " "
			for _, w := range tc.want {
				if !strings.Contains(argv, w) {
					t.Errorf("argv lacks %q:\n%s", strings.TrimSpace(w), argv)
				}
			}
			for _, r := range tc.reject {
				if strings.Contains(argv, r) {
					t.Errorf("argv must not contain %q:\n%s", r, argv)
				}
			}
			for _, d := range []string{"shell", "recordings"} {
				if info, err := os.Stat(filepath.Join(data, d)); err != nil || !info.IsDir() {
					t.Errorf("data dir %s: got %v, want a directory before docker run", d, err)
				}
			}
		})
	}
}

func TestReplay(t *testing.T) {
	m, fr, _, data, _ := shellHarness(t, nil)
	rec := filepath.Join(data, "recordings")
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	cast := filepath.Join(rec, "lab-abcde-1792971997.cast")
	if err := os.WriteFile(cast, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "x.cast")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tag, _ := m.ImageTag()
	tests := []struct {
		name    string
		cast    string
		wantErr string
	}{
		{name: "file name under recordings", cast: "lab-abcde-1792971997.cast"},
		{name: "absolute path in the journal", cast: cast},
		{name: "outside the journal", cast: outside, wantErr: "is not inside the journal directory"},
		{name: "missing", cast: "nope.cast", wantErr: "replay nope.cast: resolve mount source"},
		{name: "empty", cast: "", wantErr: "replay: recording path is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fr.calls = nil
			err := m.Replay(context.Background(), tc.cast)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Replay: got %v, want %q", err, tc.wantErr)
				}
				if _, ok := dockerRun(fr); ok {
					t.Errorf("Replay ran docker run despite the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}
			run, ok := dockerRun(fr)
			if !ok {
				t.Fatalf("no docker run in %v", fr.joined())
			}
			argv := " " + strings.Join(run.args, " ") + " "
			for _, w := range []string{
				" run --rm -it --network none ",
				" --cap-drop ALL ",
				" --mount type=bind,source=" + resolved(t, data) + ",target=/data,readonly ",
				" " + tag + " asciinema play -i 2 /data/recordings/lab-abcde-1792971997.cast ",
			} {
				if !strings.Contains(argv, w) {
					t.Errorf("argv lacks %q:\n%s", strings.TrimSpace(w), argv)
				}
			}
		})
	}
}
