package pack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const manifest = `name: incidents
title: Incidents from the on-call rota
source: https://example.com/postmortems
authors: [alice]
modules:
  - level: L0
    id: L0.incidents
    title: Incidents
    summary: Tickets from the rota
    scenarios:
      - id: 001-storefront-down
        ticket: Storefront is down
        type: break-fix
        feasibility: kind
`

func sourcePack(t *testing.T, manifestText string) string {
	t.Helper()
	src := t.TempDir()
	if err := copyTree(filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down"), mkdir(t, filepath.Join(src, "scenarios", "L0", "001-storefront-down"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pack.yaml"), []byte(manifestText), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(src, "scenarios", "L0", "001-storefront-down", "scenario.toml")
	b, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, []byte(strings.Replace(string(b), `module = "L0.1"`, `module = "L0.incidents"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFetchEnableListRemove(t *testing.T) {
	data := t.TempDir()
	src := sourcePack(t, manifest)
	st, err := Fetch(context.Background(), data, src, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if st.Manifest.Name != "incidents" || len(st.Scenarios) != 1 || st.Scenarios[0].Meta.ID != "001-storefront-down" {
		t.Fatalf("staged: got %+v with %d scenarios", st.Manifest, len(st.Scenarios))
	}
	if len(st.Scripts) == 0 || !strings.HasSuffix(st.Scripts[0], "solve.sh") {
		t.Errorf("scripts: got %v, want solve.sh listed", st.Scripts)
	}
	if entries, _ := os.ReadDir(Dir(data)); len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".staging-") {
		t.Errorf("staging: got %v, want one .staging-* directory", entries)
	}
	inst, err := st.Enable()
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if inst.Dir != filepath.Join(Dir(data), "incidents") {
		t.Errorf("Dir: got %s", inst.Dir)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, fileRecord)); err != nil {
		t.Errorf("record: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(inst.Dir, "scenarios", "L0", "001-storefront-down", "solution", "solve.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("solve.sh after the copy: %v, mode %v, want executable", err, fi)
	}
	packs, err := List(data)
	if err != nil || len(packs) != 1 || packs[0].Name != "incidents" || packs[0].Record.Source != src {
		t.Fatalf("List: got %+v, %v", packs, err)
	}
	roots, _ := Roots(data)
	if len(roots) != 1 || roots[0] != inst.Dir {
		t.Errorf("Roots: got %v", roots)
	}
	if _, err := Fetch(context.Background(), data, src, FetchOptions{}); err != nil {
		t.Fatalf("second Fetch: %v", err)
	} else if _, err := st.Enable(); err == nil {
		t.Error("Enable over an installed pack: got nil error")
	}
	if err := Remove(data, "incidents"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if packs, _ := List(data); len(packs) != 0 {
		t.Errorf("List after Remove: got %+v", packs)
	}
	if err := Remove(data, "../etc"); err == nil {
		t.Error("Remove with a path: got nil error")
	}
}

func TestFetchRefusesProblems(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		opts     FetchOptions
		want     string
	}{
		{"id already in the catalog", manifest, FetchOptions{ExistingIDs: []string{"001-storefront-down"}}, "already in the catalog"},
		{"scenario not listed", strings.Replace(manifest, "001-storefront-down", "009-other", 1), FetchOptions{}, "009-other"},
		{"bad name", strings.Replace(manifest, "name: incidents", "name: Bad Name", 1), FetchOptions{}, "must match"},
		{"module outside its level", strings.Replace(manifest, "id: L0.incidents", "id: L1.incidents", 1), FetchOptions{}, "must start with L0."},
		{"ref on a directory", manifest, FetchOptions{Ref: "v1"}, "--ref applies to a git source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			_, err := Fetch(context.Background(), data, sourcePack(t, tc.manifest), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Fetch: got %v, want an error containing %q", err, tc.want)
			}
			if entries, _ := os.ReadDir(Dir(data)); len(entries) != 0 {
				t.Errorf("staging left behind: %v", entries)
			}
		})
	}
}

func TestFetchThroughGit(t *testing.T) {
	data := t.TempDir()
	src := sourcePack(t, manifest)
	var calls [][]string
	git := func(_ context.Context, dir string, args ...string) (string, error) {
		calls = append(calls, append([]string{dir}, args...))
		switch args[0] {
		case "clone":
			return "", copyTree(src, args[len(args)-1])
		case "checkout":
			return "", nil
		case "rev-parse":
			return "abc123\n", nil
		}
		return "", errors.New("unexpected git call")
	}
	st, err := Fetch(context.Background(), data, "https://example.com/incidents.git", FetchOptions{Ref: "v1.0.0", Git: git})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if st.Record.Commit != "abc123" || st.Record.Ref != "v1.0.0" {
		t.Errorf("record: got %+v", st.Record)
	}
	if len(calls) != 3 || calls[0][1] != "clone" || calls[1][1] != "checkout" || calls[1][4] != "v1.0.0" || calls[2][1] != "rev-parse" {
		t.Errorf("git calls: got %v", calls)
	}
	if err := st.Discard(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(Dir(data)); len(entries) != 0 {
		t.Errorf("Discard left %v", entries)
	}
}

func TestListReportsBrokenPacks(t *testing.T) {
	data := t.TempDir()
	mkdir(t, filepath.Join(Dir(data), "broken"))
	packs, err := List(data)
	if err == nil || len(packs) != 0 {
		t.Fatalf("List: got %+v, %v, want no packs and an error", packs, err)
	}
	if packs, err := List(t.TempDir()); err != nil || packs != nil {
		t.Errorf("List without a packs dir: got %v, %v", packs, err)
	}
}
