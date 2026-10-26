package scriptrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunExitCodeAndOutput(t *testing.T) {
	tests := []struct {
		name       string
		spec       Spec
		wantCode   int
		wantOutput string
	}{
		{name: "success", spec: Spec{Script: "echo ok"}, wantCode: 0, wantOutput: "ok\n"},
		{name: "exit code propagates", spec: Spec{Script: "echo out; echo err >&2; exit 4"}, wantCode: 4, wantOutput: "out\nerr\n"},
		{name: "env is the allowlist plus spec env", spec: Spec{Script: `echo "$X-${SECRET_TOKEN:-unset}"`, Env: []string{"X=1"}}, wantCode: 0, wantOutput: "1-unset\n"},
	}
	t.Setenv("SECRET_TOKEN", "leak")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Run(context.Background(), tt.spec)
			if err != nil {
				t.Fatalf("Run: got error %v, want nil", err)
			}
			if res.ExitCode != tt.wantCode {
				t.Errorf("exit code: got %d, want %d", res.ExitCode, tt.wantCode)
			}
			if res.Output != tt.wantOutput {
				t.Errorf("output: got %q, want %q", res.Output, tt.wantOutput)
			}
		})
	}
}

func TestRunFileDirAndStream(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(file, []byte("pwd -P\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	res, err := Run(context.Background(), Spec{File: file, Dir: dir, Stream: &stream})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := res.Output, dir+"\n"; got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}
	if got, want := stream.String(), dir+"\n"; got != want {
		t.Errorf("stream: got %q, want %q", got, want)
	}
}

func TestRunSpecErrors(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "neither", spec: Spec{}, want: "Script or File is required"},
		{name: "both", spec: Spec{Script: "true", File: "x.sh"}, want: "mutually exclusive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(context.Background(), tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Run: got error %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestRunOutputCap(t *testing.T) {
	tests := []struct {
		name          string
		script        string
		limit         int
		wantLen       int
		wantTruncated bool
	}{
		{name: "under the limit", script: "printf 'abc'", limit: 10, wantLen: 3},
		{name: "exactly the limit", script: "printf 'abcdefghij'", limit: 10, wantLen: 10},
		{name: "over the limit", script: "head -c 1000000 /dev/zero", limit: 1024, wantLen: 1024, wantTruncated: true},
		{name: "default limit", script: "head -c 200000 /dev/zero", wantLen: DefaultLimit, wantTruncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Run(context.Background(), Spec{Script: tt.script, Limit: tt.limit})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(res.Output) != tt.wantLen || res.Truncated != tt.wantTruncated {
				t.Errorf("output: got %d bytes truncated=%v, want %d bytes truncated=%v", len(res.Output), res.Truncated, tt.wantLen, tt.wantTruncated)
			}
		})
	}
}

func TestRunEndlessOutputIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	res, err := Run(ctx, Spec{Script: "yes", Limit: 4096})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: got error %v, want deadline exceeded", err)
	}
	if len(res.Output) != 4096 || !res.Truncated {
		t.Errorf("output: got %d bytes truncated=%v, want 4096 bytes truncated=true", len(res.Output), res.Truncated)
	}
}

func TestRunBackgroundChildIsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	res, err := Run(context.Background(), Spec{Script: "sleep 30 & echo $! > " + pidFile + "; echo ok"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: got error %v, want nil", err)
	}
	if res.ExitCode != 0 || res.Output != "ok\n" {
		t.Errorf("result: got code %d output %q, want code 0 output %q", res.ExitCode, res.Output, "ok\n")
	}
	if elapsed > time.Second {
		t.Errorf("elapsed: got %v, want under 1s", elapsed)
	}
	assertDead(t, pidFile)
}

func TestRunTimeoutKillsGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := Run(ctx, Spec{Script: "sleep 30 & echo $! > " + pidFile + "; wait"})
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: got error %v, want deadline exceeded", err)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code: got %d, want -1", res.ExitCode)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed: got %v, want under 3s", elapsed)
	}
	assertDead(t, pidFile)
}

func assertDead(t *testing.T, pidFile string) {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", b, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background child %d: got alive (kill 0 error %v), want dead", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
