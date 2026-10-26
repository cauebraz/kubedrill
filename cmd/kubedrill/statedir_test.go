package main

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestStateDirThreadedFromMain(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "elsewhere"))
	resolved := stateDir{path: filepath.Join(t.TempDir(), "state")}
	cause := errors.New("no home")
	noEnv := func(string) string { return "" }
	tests := []struct {
		name      string
		state     stateDir
		wantDir   string
		wantAudit string
	}{
		{"resolved", resolved, resolved.path, filepath.Join(resolved.path, "audit", "audit.log")},
		{"resolution error", stateDir{err: cause}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := factories(io.Discard, noEnv, time.Now, tc.state)
			got, err := f.StateDir()
			if got != tc.wantDir || !errors.Is(err, tc.state.err) {
				t.Errorf("StateDir: got %q, %v, want %q, %v", got, err, tc.wantDir, tc.state.err)
			}
			if got := auditLogPath(noEnv, tc.state); got != tc.wantAudit {
				t.Errorf("auditLogPath: got %q, want %q", got, tc.wantAudit)
			}
		})
	}
	if _, err := pinScriptKubeconfig(stateDir{err: cause}, "", "/nonexistent", "kind-kubedrill"); !errors.Is(err, cause) {
		t.Errorf("pinScriptKubeconfig with a state error: got %v, want the state error", err)
	}
}
