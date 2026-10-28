package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyScenario(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func packSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	dst := filepath.Join(src, "scenarios", "L0", "901-storefront-pack")
	copyScenario(t, filepath.Join("..", "..", "scenarios", "L0", "001-storefront-down"), dst)
	meta := filepath.Join(dst, "scenario.toml")
	b, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	toml := strings.NewReplacer(
		`id = "001-storefront-down"`, `id = "901-storefront-pack"`,
		`module = "L0.1"`, `module = "L0.incidents"`,
		`exam = ["CKA:troubleshooting"]`, `exam = ["CKA:workloads"]`,
		`concepts = ["pod-lifecycle", "image-pull", "readiness-and-endpoints"]`, `concepts = ["pods"]`,
	).Replace(string(b))
	if err := os.WriteFile(meta, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "name: incidents\ntitle: Incidents\nmodules:\n  - level: L0\n    id: L0.incidents\n    title: Incidents\n    summary: From the rota\n    scenarios:\n      - id: 901-storefront-pack\n        ticket: Storefront is down\n        type: break-fix\n        feasibility: kind\n"
	if err := os.WriteFile(filepath.Join(src, "pack.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestPackAddRefusesCollisions(t *testing.T) {
	f := newFixture(t)
	src := packSource(t)
	if code, out, errOut := f.run(t, "", "pack", "add", src, "--yes"); code != 0 {
		t.Fatalf("pack add: %d %q %q", code, out, errOut)
	}
	manifest := filepath.Join(src, "pack.yaml")
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(b), "name: incidents", "name: incidents-two", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := f.run(t, "", "pack", "add", src, "--yes")
	if code == 0 || !strings.Contains(errOut, "module L0.incidents: the id is already in the ladder") || !strings.Contains(errOut, "already in the catalog") {
		t.Errorf("second pack with the same module and scenario: %d %q %q", code, out, errOut)
	}
}

func TestPackAddListRemove(t *testing.T) {
	f := newFixture(t)
	src := packSource(t)

	code, out, errOut := f.run(t, "", "pack", "list")
	if code != 0 || !strings.Contains(out, "no packs") {
		t.Fatalf("pack list before: %d %q %q", code, out, errOut)
	}

	code, out, errOut = f.run(t, "n\n", "pack", "add", src)
	if code != 0 || !strings.Contains(out, "Enable pack incidents? [y/N]") || !strings.Contains(out, "pack not enabled") {
		t.Fatalf("pack add declined: %d %q %q", code, out, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.journal.dir, "packs")); len(entries) != 0 {
		t.Errorf("staging left after a decline: %v", entries)
	}

	code, out, errOut = f.run(t, "", "pack", "add", src, "--yes")
	if code != 0 {
		t.Fatalf("pack add: %d %q %q", code, out, errOut)
	}
	for _, want := range []string{"pack incidents (Incidents) from " + src, "1 scenario(s):", "901-storefront-pack", "scripts that run on this machine", "solution/solve.sh", "pack incidents enabled in"} {
		if !strings.Contains(out, want) {
			t.Errorf("pack add output lacks %q:\n%s", want, out)
		}
	}

	code, out, _ = f.run(t, "", "pack", "list")
	if code != 0 || !strings.Contains(out, "incidents  Incidents  1 scenario(s)  "+src) {
		t.Errorf("pack list: %d %q", code, out)
	}

	code, out, errOut = f.run(t, "", "path")
	if code != 0 || !strings.Contains(out, "Incidents (pack incidents)") {
		t.Errorf("path with a pack: %d %q %q", code, out, errOut)
	}

	code, out, errOut = f.run(t, "", "pack", "add", src, "--yes")
	if code == 0 || !strings.Contains(errOut, "pack incidents is already enabled; kubedrill pack remove incidents first") {
		t.Errorf("pack add twice: %d %q %q", code, out, errOut)
	}

	code, out, errOut = f.run(t, "", "pack", "remove", "incidents", "--yes")
	if code != 0 || !strings.Contains(out, "pack incidents removed") {
		t.Fatalf("pack remove: %d %q %q", code, out, errOut)
	}
	if code, out, _ := f.run(t, "", "pack", "list"); code != 0 || !strings.Contains(out, "no packs") {
		t.Errorf("pack list after remove: %d %q", code, out)
	}
}
