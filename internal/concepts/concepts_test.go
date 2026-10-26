package concepts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validDoc = `# Pod lifecycle

A pod moves through Pending, Running and a terminal phase; the kubelet reports
each container state under status.containerStatuses.

## The mechanism

Text.

## What you see on call

Text.

## Common failures

Text.

## Go deeper

Text.
`

func writeConcepts(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "concepts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadValid(t *testing.T) {
	root := writeConcepts(t, map[string]string{
		"pod-lifecycle.md": validDoc,
		"image-pull.md":    strings.Replace(validDoc, "# Pod lifecycle", "# Image pull", 1),
		"README.md":        "# Concepts\n\nindex, ignored\n",
		"notes.txt":        "ignored\n",
	})
	lib, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := strings.Join(lib.IDs(), ","), "image-pull,pod-lifecycle"; got != want {
		t.Errorf("IDs = %q, want %q", got, want)
	}
	all := lib.All()
	if len(all) != 2 || all[0].ID != "image-pull" || all[1].ID != "pod-lifecycle" {
		t.Errorf("All = %+v, want image-pull then pod-lifecycle", all)
	}
	c, err := lib.Get("pod-lifecycle")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.Title != "Pod lifecycle" {
		t.Errorf("Title = %q, want %q", c.Title, "Pod lifecycle")
	}
	wantSummary := "A pod moves through Pending, Running and a terminal phase; the kubelet reports each container state under status.containerStatuses."
	if c.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", c.Summary, wantSummary)
	}
	if c.Body != validDoc {
		t.Errorf("Body differs from the file content")
	}
	if want := filepath.Join(root, "docs", "concepts", "pod-lifecycle.md"); c.Path != want {
		t.Errorf("Path = %q, want %q", c.Path, want)
	}
	if _, err := lib.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(nope) err = %v, want ErrNotFound", err)
	}
}

func TestLoadMissingDir(t *testing.T) {
	root := t.TempDir()
	_, err := Load(root)
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	if want := filepath.Join(root, "docs", "concepts"); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name %q", err, want)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		file string
		body string
		want string
	}{
		{
			name: "missing section",
			file: "pod-lifecycle.md",
			body: strings.Replace(validDoc, "## Common failures\n\nText.\n\n", "", 1),
			want: `docs/concepts/pod-lifecycle.md: missing section "## Common failures"`,
		},
		{
			name: "wrong section order",
			file: "pod-lifecycle.md",
			body: strings.NewReplacer("## The mechanism", "## What you see on call", "## What you see on call", "## The mechanism").Replace(validDoc),
			want: "docs/concepts/pod-lifecycle.md: sections out of order",
		},
		{
			name: "two H1s",
			file: "pod-lifecycle.md",
			body: validDoc + "\n# Another title\n",
			want: "docs/concepts/pod-lifecycle.md: exactly one H1 is allowed, got 2",
		},
		{
			name: "title not first",
			file: "pod-lifecycle.md",
			body: "intro first\n" + validDoc,
			want: `docs/concepts/pod-lifecycle.md: first non-empty line must be the title "# Title"`,
		},
		{
			name: "bad filename",
			file: "Pod_Lifecycle.md",
			body: validDoc,
			want: `docs/concepts/Pod_Lifecycle.md: file name "Pod_Lifecycle.md" must be <id>.md`,
		},
		{
			name: "empty intro",
			file: "pod-lifecycle.md",
			body: strings.Replace(validDoc, "A pod moves through Pending, Running and a terminal phase; the kubelet reports\neach container state under status.containerStatuses.\n\n", "", 1),
			want: "docs/concepts/pod-lifecycle.md: an intro paragraph is required",
		},
		{
			name: "empty file",
			file: "pod-lifecycle.md",
			body: "\n\n",
			want: "docs/concepts/pod-lifecycle.md: file is empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeConcepts(t, map[string]string{tc.file: tc.body, "image-pull.md": validDoc})
			_, err := Load(root)
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error lacks %q:\n%v", tc.want, err)
			}
		})
	}
}

func TestLoadReportsEveryFile(t *testing.T) {
	root := writeConcepts(t, map[string]string{
		"a.md": "# A\n\nintro\n",
		"b.md": validDoc + "# B\n",
	})
	_, err := Load(root)
	if err == nil {
		t.Fatal("Load succeeded, want errors")
	}
	for _, w := range []string{"docs/concepts/a.md: missing section", "docs/concepts/b.md: exactly one H1"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q:\n%v", w, err)
		}
	}
}

func TestParseCRLF(t *testing.T) {
	root := writeConcepts(t, map[string]string{
		"pod-lifecycle.md": strings.ReplaceAll(validDoc, "\n", "\r\n"),
	})
	lib, err := Load(root)
	if err != nil {
		t.Fatalf("Load with CRLF endings: got error %v, want nil", err)
	}
	c, err := lib.Get("pod-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.Title, "Pod lifecycle"; got != want {
		t.Errorf("Title: got %q, want %q", got, want)
	}
	wantSummary := "A pod moves through Pending, Running and a terminal phase; the kubelet reports each container state under status.containerStatuses."
	if c.Summary != wantSummary {
		t.Errorf("Summary: got %q, want %q", c.Summary, wantSummary)
	}
	if strings.ContainsRune(c.Title, '\r') || strings.ContainsRune(c.Summary, '\r') {
		t.Errorf("Title %q or Summary %q contains a carriage return", c.Title, c.Summary)
	}
}

func TestParseTitleTrailingSpaces(t *testing.T) {
	c, errs := parse(strings.Replace(validDoc, "# Pod lifecycle\n", "# Pod lifecycle   \t\n", 1))
	if len(errs) != 0 {
		t.Fatalf("parse: got errors %v, want none", errs)
	}
	if got, want := c.Title, "Pod lifecycle"; got != want {
		t.Errorf("Title: got %q, want %q", got, want)
	}
}

func TestParseSectionHeadingVariants(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		wantErr string
	}{
		{name: "trailing spaces are accepted", heading: "## The mechanism   "},
		{name: "trailing tab is accepted", heading: "## The mechanism\t"},
		{name: "suffix in parentheses", heading: "## The mechanism (details)", wantErr: `missing section "## The mechanism"`},
		{name: "no space after hashes", heading: "##The mechanism", wantErr: `missing section "## The mechanism"`},
		{name: "H3 instead of H2", heading: "### The mechanism", wantErr: `missing section "## The mechanism"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(validDoc, "## The mechanism", tc.heading, 1)
			_, errs := parse(body)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("parse: got errors %v, want none", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("parse: got no errors, want one containing %q", tc.wantErr)
			}
			if got := errors.Join(errs...).Error(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("errors lack %q:\n%s", tc.wantErr, got)
			}
		})
	}
}

func TestParseDuplicateSection(t *testing.T) {
	body := strings.Replace(validDoc, "## Go deeper\n", "## Common failures\n\nAgain.\n\n## Go deeper\n", 1)
	_, errs := parse(body)
	if len(errs) == 0 {
		t.Fatal("parse: got no errors, want the duplicate section error")
	}
	want := `section "## Common failures" appears 2 times, want 1`
	if got := errors.Join(errs...).Error(); !strings.Contains(got, want) {
		t.Errorf("errors lack %q:\n%s", want, got)
	}
}

func TestParseIgnoresHeadingsInCodeFences(t *testing.T) {
	tests := []struct {
		name  string
		block string
	}{
		{name: "backtick fence with a comment", block: "```bash\n# list the pods\nkubectl get pods\n```\n"},
		{name: "tilde fence with a comment", block: "~~~\n# list the pods\n~~~\n"},
		{name: "fence holding a section heading", block: "````markdown\n## Go deeper\n```\n# nested\n```\n````\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(validDoc, "## The mechanism\n\nText.\n", "## The mechanism\n\nText.\n\n"+tt.block, 1)
			c, errs := parse(doc)
			if len(errs) != 0 {
				t.Fatalf("parse: got errors %v, want none", errs)
			}
			if got, want := c.Title, "Pod lifecycle"; got != want {
				t.Errorf("Title: got %q, want %q", got, want)
			}
		})
	}
	unclosed := strings.Replace(validDoc, "## The mechanism\n", "```\n## The mechanism\n", 1)
	if _, errs := parse(unclosed); len(errs) == 0 {
		t.Errorf("parse with an unclosed fence hiding every section: got no errors, want missing sections")
	}
}
