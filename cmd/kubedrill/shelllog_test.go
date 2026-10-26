package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/journal"
)

func TestEpisodeTelemetryShellLog(t *testing.T) {
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	ep := journal.Episode{Namespace: "task-hello-ab12c", StartedAt: start, EndedAt: &end}
	line := `{"ts":"2026-10-25T10:00:01Z","cmd":"kubectl get pods","exit":0,"ms":5}` + "\n"
	old := `{"ts":"2026-10-24T10:00:01Z","cmd":"` + strings.Repeat("x", 1<<20) + `","exit":0,"ms":5}` + "\n"
	tests := []struct {
		name     string
		write    func(t *testing.T, path string)
		commands int
		warning  string
		rotated  bool
	}{
		{
			name:     "small log kept in place",
			write:    func(t *testing.T, path string) { writeShell(t, path, line) },
			commands: 1,
		},
		{
			name:     "log over 10 MiB rotated after the read",
			write:    func(t *testing.T, path string) { writeShell(t, path, strings.Repeat(old, 11)+line) },
			commands: 1,
			rotated:  true,
		},
		{
			name: "unreadable log reported",
			write: func(t *testing.T, path string) {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			warning: "warning: telemetry without the shell log: read shell log:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := journal.ShellLogPath(dir)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			tc.write(t, path)
			var stderr bytes.Buffer
			got := episodeTelemetry(&stderr, dir, "", ep, nil, nil, false, end)
			if got.CommandsTotal != tc.commands {
				t.Errorf("commands: got %d, want %d", got.CommandsTotal, tc.commands)
			}
			if tc.warning == "" && stderr.Len() != 0 {
				t.Errorf("stderr: got %q, want nothing", stderr.String())
			}
			if tc.warning != "" && (!strings.HasPrefix(stderr.String(), tc.warning) || strings.Count(stderr.String(), "\n") != 1) {
				t.Errorf("stderr: got %q, want one line starting %q", stderr.String(), tc.warning)
			}
			_, err := os.Stat(path + ".1")
			if rotated := err == nil; rotated != tc.rotated {
				t.Errorf("rotated: got %v, want %v", rotated, tc.rotated)
			}
		})
	}
}

func writeShell(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
