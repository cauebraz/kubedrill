package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAuditRotationAfterOpen(t *testing.T) {
	tests := []struct {
		name   string
		rotate bool
		want   string
	}{
		{"no rotation", false, "get core/pods/a 200"},
		{"rotation between open and listing", true, "get core/pods/a 200"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "audit.log")
			if err := os.WriteFile(path, []byte(auditJSON(fixtureNS, "", "pods", "a", "get", "kubectl/v1.37.0")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var hook func()
			if tc.rotate {
				hook = func() {
					if err := os.Rename(path, filepath.Join(dir, "audit-2026-10-25T10-00-06.000.log")); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			events, err := readAudit(path, at("10:00:00"), at("11:00:00"), fixtureNS, nil, hook)
			if err != nil {
				t.Fatalf("readAudit: %v", err)
			}
			if got := strings.Join(describe(events), "|"); got != tc.want {
				t.Errorf("events: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadShellLogSkipsOverlongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.jsonl")
	var b bytes.Buffer
	b.WriteString(`{"ts":"2026-10-25T10:00:01Z","cmd":"kubectl get pods","exit":0,"ms":5}` + "\n")
	b.WriteString(`{"ts":"2026-10-25T10:00:02Z","cmd":"` + strings.Repeat("x", maxLine+10) + `","exit":0,"ms":5}` + "\n")
	b.WriteString(`{"ts":"2026-10-25T10:00:03Z","cmd":"kubectl describe pod a","exit":0,"ms":5}` + "\n")
	b.WriteString(`{"ts":"2026-10-25T09:00:00Z","cmd":"kubectl before","exit":0,"ms":5}` + "\n")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds, err := ReadShellLog(path, at("10:00:00"), at("11:00:00"))
	if err != nil {
		t.Fatalf("ReadShellLog: %v", err)
	}
	var got []string
	for _, c := range cmds {
		got = append(got, c.Cmd)
	}
	if want := []string{"kubectl get pods", "kubectl describe pod a"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("commands: got %q, want %q", got, want)
	}
}

func TestEachLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		limit int
		want  []string
	}{
		{"plain", "a\nbb\n", 4, []string{"a", "bb"}},
		{"no trailing newline", "a\nbb", 4, []string{"a", "bb"}},
		{"crlf and blank", "a\r\n\nbb\n", 4, []string{"a", "bb"}},
		{"line at the limit kept", "abcd\nx\n", 4, []string{"abcd", "x"}},
		{"line over the limit skipped", "abcde\nx\n", 4, []string{"x"}},
		{"overlong last line skipped", "x\nabcdefgh", 4, []string{"x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			if err := eachLine(strings.NewReader(tc.input), tc.limit, func(l []byte) { got = append(got, string(l)) }); err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("lines: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRotateShellLog(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		rotated bool
	}{
		{"under the limit", 10, false},
		{"at the limit", 16, false},
		{"over the limit", 17, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "commands.jsonl")
			if err := os.WriteFile(path+".1", []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), tc.size), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := RotateShellLog(path, 16)
			if err != nil || got != tc.rotated {
				t.Fatalf("RotateShellLog: got %v, %v, want %v, nil", got, err, tc.rotated)
			}
			_, statErr := os.Stat(path)
			backup, _ := os.ReadFile(path + ".1")
			if tc.rotated && (!os.IsNotExist(statErr) || len(backup) != tc.size) {
				t.Errorf("after rotation: got stat %v and backup of %d bytes, want no file and %d bytes", statErr, len(backup), tc.size)
			}
			if !tc.rotated && (statErr != nil || string(backup) != "old") {
				t.Errorf("without rotation: got stat %v backup %q, want the file and old", statErr, backup)
			}
		})
	}
	if got, err := RotateShellLog(filepath.Join(t.TempDir(), "absent"), 16); got || err != nil {
		t.Errorf("missing file: got %v, %v, want false, nil", got, err)
	}
}
