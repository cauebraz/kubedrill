package scenario

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadShortcuts(t *testing.T) {
	f := validFixture()
	f["shortcuts/restart.sh"] = "#!/usr/bin/env bash\n# why: a restart keeps the same broken template\nset -euo pipefail\n# variants: b\n"
	f["shortcuts/scale-up.sh"] = "#!/bin/bash\n# why: more replicas of the broken image\n# variants: b, a\nset -euo pipefail\n"
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", f)
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Shortcut{
		{Name: "restart", Script: filepath.Join(dir, "shortcuts", "restart.sh"), Why: "a restart keeps the same broken template"},
		{Name: "scale-up", Script: filepath.Join(dir, "shortcuts", "scale-up.sh"), Why: "more replicas of the broken image", Variants: []string{"b", "a"}},
	}
	if !reflect.DeepEqual(s.Shortcuts, want) {
		t.Errorf("Shortcuts = %+v, want %+v", s.Shortcuts, want)
	}
}

func TestLoadWithoutShortcuts(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Shortcuts) != 0 {
		t.Errorf("Shortcuts = %+v, want none", s.Shortcuts)
	}
}

func TestLoadShortcutErrors(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		body   string
		mode   os.FileMode
		wantIn []string
	}{
		{name: "no why", file: "a.sh", body: "#!/usr/bin/env bash\nset -e\n", wantIn: []string{"shortcuts/a.sh", `no "# why:" line`}},
		{name: "why after code", file: "a.sh", body: "#!/usr/bin/env bash\nset -e\n# why: a restart keeps the template\n", wantIn: []string{`no "# why:" line`}},
		{name: "short why", file: "a.sh", body: "#!/usr/bin/env bash\n# why: short\n", wantIn: []string{`"# why:" is 5 characters`}},
		{name: "two whys", file: "a.sh", body: "#!/usr/bin/env bash\n# why: one reason here\n# why: another reason here\n", wantIn: []string{`2 "# why:" lines`}},
		{name: "not bash", file: "a.sh", body: "#!/bin/sh\n# why: a restart keeps the template\n", wantIn: []string{"bash shebang"}},
		{name: "unknown variant", file: "a.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n# variants: a,c\n", wantIn: []string{`variant "c", which scenario.toml does not declare`}},
		{name: "duplicate variant", file: "a.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n# variants: a,a\n", wantIn: []string{`names variant "a" twice`}},
		{name: "empty variant", file: "a.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n# variants: a,\n", wantIn: []string{"empty variant name"}},
		{name: "two variant lines", file: "a.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n# variants: a\n# variants: b\n", wantIn: []string{`2 "# variants:" lines`}},
		{name: "not executable", file: "a.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n", mode: 0o644, wantIn: []string{"shortcuts/a.sh", "not executable"}},
		{name: "bad name", file: "Restart.sh", body: "#!/usr/bin/env bash\n# why: a restart keeps the template\n", wantIn: []string{"shortcuts/Restart.sh", "<kebab-name>.sh"}},
		{name: "not a script", file: "notes.md", body: "text\n", wantIn: []string{"shortcuts/notes.md", "unexpected entry"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
			mode := tt.mode
			if mode == 0 {
				mode = 0o755
			}
			if err := os.MkdirAll(filepath.Join(dir, "shortcuts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "shortcuts", tt.file), []byte(tt.body), mode); err != nil {
				t.Fatal(err)
			}
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load: got nil error, want one")
			}
			for _, w := range tt.wantIn {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Load error = %v, want it to contain %q", err, w)
				}
			}
		})
	}
}

func TestLoadShortcutDirectoryEntry(t *testing.T) {
	dir := writeFixture(t, t.TempDir(), "L1", "001-fixture", validFixture())
	if err := os.MkdirAll(filepath.Join(dir, "shortcuts", "nested.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "shortcuts/nested.sh: unexpected entry") {
		t.Errorf("Load error = %v, want shortcuts/nested.sh: unexpected entry", err)
	}
}

func TestShortcutAppliesTo(t *testing.T) {
	tests := []struct {
		variants []string
		variant  string
		want     bool
	}{
		{variants: nil, variant: "a", want: true},
		{variants: []string{"a", "b"}, variant: "b", want: true},
		{variants: []string{"a"}, variant: "b", want: false},
	}
	for _, tt := range tests {
		sh := Shortcut{Variants: tt.variants}
		if got := sh.AppliesTo(tt.variant); got != tt.want {
			t.Errorf("Shortcut{Variants: %v}.AppliesTo(%q) = %v, want %v", tt.variants, tt.variant, got, tt.want)
		}
	}
}

func TestRealShortcutsLoad(t *testing.T) {
	dirs, err := filepath.Glob("../../scenarios/L*/*/shortcuts")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		s, err := Load(filepath.Dir(d))
		if err != nil {
			t.Errorf("Load %s: %v", filepath.Dir(d), err)
			continue
		}
		if len(s.Shortcuts) == 0 {
			t.Errorf("%s: shortcuts/ holds no script", filepath.Dir(d))
		}
	}
}

func TestLintShortcuts(t *testing.T) {
	tests := []struct {
		name string
		file string
		body string
		want []string
	}{
		{
			name: "template shortcut left in place",
			file: "rollout-restart.sh",
			body: "#!/usr/bin/env bash\n# why: a restart keeps the broken template\n# kubedrill-template: delete\n",
			want: []string{`shortcuts/rollout-restart.sh: still contains the template marker "kubedrill-template"`},
		},
		{
			name: "valid shortcut",
			file: "scale-up.sh",
			body: "#!/usr/bin/env bash\n# why: more replicas of the broken image\n# variants: a\nset -euo pipefail\n",
		},
		{
			name: "unknown variant",
			file: "scale-up.sh",
			body: "#!/usr/bin/env bash\n# why: more replicas of the broken image\n# variants: z\n",
			want: []string{`shortcuts/scale-up.sh: "# variants: z" names variant "z", which scenario.toml does not declare`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := copyTree("../../scenarios/_template", filepath.Join(root, "scenarios", "_template")); err != nil {
				t.Fatal(err)
			}
			f := validFixture()
			f["shortcuts/"+tc.file] = tc.body
			writeFixture(t, root, "L1", "001-fixture", f)
			errs := Lint(root, Refs{})
			var joined []string
			for _, e := range errs {
				joined = append(joined, e.Error())
			}
			all := strings.Join(joined, "\n")
			for _, w := range tc.want {
				if !strings.Contains(all, filepath.Join("scenarios", "L1", "001-fixture")+": "+w) {
					t.Errorf("lint output lacks %q:\n%s", w, all)
				}
			}
			if got, want := len(errs), len(tc.want); got != want {
				t.Errorf("Lint returned %d errors, want %d:\n%s", got, want, all)
			}
		})
	}
}
