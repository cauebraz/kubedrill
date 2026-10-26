package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("content: got %q, %v, want new", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode: got %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir entries: got %d, want 1 (no temp file left)", len(entries))
	}
	if err := Write(filepath.Join(dir, "missing", "f"), nil, 0o600); err == nil {
		t.Errorf("Write into a missing dir: got nil error")
	}
}

func TestWriteIfChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	tests := []struct {
		name string
		data string
		perm os.FileMode
		want bool
	}{
		{"creates", "a", 0o600, true},
		{"same content skips", "a", 0o600, false},
		{"same content fixes mode", "a", 0o644, false},
		{"new content writes", "b", 0o600, true},
	}
	for _, tt := range tests {
		got, err := WriteIfChanged(path, []byte(tt.data), tt.perm)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %v, %v, want %v", tt.name, got, err, tt.want)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != tt.perm {
			t.Errorf("%s: mode got %v, want %v", tt.name, info.Mode().Perm(), tt.perm)
		}
	}
}

func TestWriteSetsModeDespiteUmask(t *testing.T) {
	dir := t.TempDir()
	for _, perm := range []os.FileMode{0o755, 0o644, 0o600} {
		path := filepath.Join(dir, perm.String())
		if err := Write(path, []byte("x"), perm); err != nil {
			t.Fatalf("Write %v: %v", perm, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Errorf("mode: got %v, want %v", info.Mode().Perm(), perm)
		}
	}
}

func TestWriteIfChangedSurfacesReadErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	wrote, err := WriteIfChanged(path, []byte("x"), 0o600)
	if err == nil || wrote {
		t.Fatalf("WriteIfChanged on a directory: got %v, %v, want the read error", wrote, err)
	}
	if !strings.HasPrefix(err.Error(), "write "+path+": ") || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error: got %q, want \"write <path>: ... is a directory\" (the read error, not a write error)", err)
	}
	if !errors.Is(err, syscall.EISDIR) {
		t.Errorf("error %v does not wrap EISDIR", err)
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		t.Errorf("directory replaced: %v, %v", info, statErr)
	}
}

func TestMkdirPrivate(t *testing.T) {
	base := t.TempDir()
	fresh := filepath.Join(base, "a", "b")
	if err := MkdirPrivate(fresh); err != nil {
		t.Fatalf("MkdirPrivate new: %v", err)
	}
	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MkdirPrivate(open); err != nil {
		t.Fatalf("MkdirPrivate existing: %v", err)
	}
	for _, dir := range []string{fresh, open} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode: got %v, want 0700", dir, info.Mode().Perm())
		}
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirPrivate(file); err == nil {
		t.Errorf("MkdirPrivate on a file: got nil error")
	}
}

func TestSyncDir(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{name: "existing directory", dir: dir},
		{name: "missing directory", dir: filepath.Join(dir, "missing"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := syncDir(tc.dir)
			if got := err != nil; got != tc.wantErr {
				t.Errorf("syncDir(%s): got %v, want error %v", tc.dir, err, tc.wantErr)
			}
		})
	}
}
