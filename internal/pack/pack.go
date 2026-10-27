// Package pack installs scenario packs: a directory or a git repository
// with a pack.yaml and a scenarios/ tree, copied under the data directory,
// pinned to a commit, linted before it is enabled and listed in the catalog
// and the ladder next to the repository's own scenarios.
package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	dirPacks     = "packs"
	fileManifest = "pack.yaml"
	fileRecord   = ".kubedrill-pack.json"
)

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	levelPattern = regexp.MustCompile(`^L[0-6]$`)
)

// Module is a curriculum module the pack adds to one level of the ladder;
// its id starts with the level, as in the repository curriculum.
type Module struct {
	Level string `json:"level"`
	curriculum.Module
}

// Manifest is pack.yaml: who made the pack, where it comes from and where
// its scenarios sit in the ladder.
type Manifest struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Source  string   `json:"source,omitempty"`
	Authors []string `json:"authors,omitempty"`
	Modules []Module `json:"modules"`
}

// Record is written next to the manifest when a pack is enabled: where it
// was fetched from and which commit it is pinned to.
type Record struct {
	Source  string    `json:"source"`
	Ref     string    `json:"ref,omitempty"`
	Commit  string    `json:"commit,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// Installed is one enabled pack under Dir(dataDir).
type Installed struct {
	Name     string
	Dir      string
	Manifest Manifest
	Record   Record
}

// Runner runs git with args in dir and returns its combined output; the
// default runs the git on PATH.
type Runner func(ctx context.Context, dir string, args ...string) (string, error)

// FetchOptions steer Fetch: Ref pins a clone to a tag, branch or commit;
// AllowPrivileged accepts scenarios whose setup escapes the pod sandbox;
// ExistingIDs are scenario ids already in the catalog, which the pack may
// not reuse; Git replaces the git on PATH.
type FetchOptions struct {
	Ref             string
	AllowPrivileged bool
	ExistingIDs     []string
	Git             Runner
}

// Staged is a fetched and linted pack waiting for Enable or Discard, with
// what a person should read before enabling it: the scenarios and every
// script that would run on the host.
type Staged struct {
	Manifest  Manifest
	Record    Record
	Scenarios []*scenario.Scenario
	Scripts   []string

	dir     string
	dataDir string
}

// Dir returns the directory that holds the installed packs.
func Dir(dataDir string) string { return filepath.Join(dataDir, dirPacks) }

// LoadManifest reads and validates dir/pack.yaml, reporting every problem.
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, fileManifest)
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("load pack manifest %s: %w", path, err)
	}
	var m Manifest
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("load pack manifest %s: %w", path, err)
	}
	var errs []error
	if !namePattern.MatchString(m.Name) {
		errs = append(errs, fmt.Errorf("name %q must match %s", m.Name, namePattern))
	}
	if m.Title == "" {
		errs = append(errs, errors.New("title is required"))
	}
	if len(m.Modules) == 0 {
		errs = append(errs, errors.New("modules: at least one module is required"))
	}
	ids := map[string]bool{}
	for i, mod := range m.Modules {
		if !levelPattern.MatchString(mod.Level) {
			errs = append(errs, fmt.Errorf("modules[%d]: level %q must match %s", i, mod.Level, levelPattern))
		}
		if !strings.HasPrefix(mod.ID, mod.Level+".") {
			errs = append(errs, fmt.Errorf("modules[%d]: id %q must start with %s.", i, mod.ID, mod.Level))
		}
		if mod.Title == "" {
			errs = append(errs, fmt.Errorf("modules[%d]: title is required", i))
		}
		if len(mod.Scenarios) == 0 {
			errs = append(errs, fmt.Errorf("modules[%d]: scenarios: at least one scenario id is required", i))
		}
		for j, sc := range mod.Scenarios {
			if sc.ID == "" {
				errs = append(errs, fmt.Errorf("modules[%d].scenarios[%d]: id is required in a pack (no planned entries)", i, j))
				continue
			}
			if ids[sc.ID] {
				errs = append(errs, fmt.Errorf("modules[%d].scenarios[%d]: scenario %s listed twice", i, j, sc.ID))
			}
			ids[sc.ID] = true
		}
	}
	if len(errs) > 0 {
		return Manifest{}, fmt.Errorf("load pack manifest %s: %w", path, errors.Join(errs...))
	}
	return m, nil
}

// List returns every pack under Dir(dataDir), sorted by name. A pack whose
// manifest or record cannot be read is left out and reported in the error,
// next to the packs that loaded.
func List(dataDir string) ([]Installed, error) {
	entries, err := os.ReadDir(Dir(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list packs: %w", err)
	}
	var out []Installed
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := load(filepath.Join(Dir(dataDir), e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errors.Join(errs...)
}

// Roots returns the directory of every installed pack, for the catalog.
func Roots(dataDir string) ([]string, error) {
	packs, err := List(dataDir)
	roots := make([]string, 0, len(packs))
	for _, p := range packs {
		roots = append(roots, p.Dir)
	}
	return roots, err
}

func load(dir string) (Installed, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return Installed{}, err
	}
	if m.Name != filepath.Base(dir) {
		return Installed{}, fmt.Errorf("pack %s: manifest name %q does not match its directory", dir, m.Name)
	}
	var r Record
	data, err := os.ReadFile(filepath.Join(dir, fileRecord))
	if err != nil {
		return Installed{}, fmt.Errorf("pack %s: %w", dir, err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Installed{}, fmt.Errorf("pack %s: read %s: %w", dir, fileRecord, err)
	}
	return Installed{Name: m.Name, Dir: dir, Manifest: m, Record: r}, nil
}

// Fetch copies source (a directory) or clones it (anything else, through
// git) into a staging directory under Dir(dataDir), loads its manifest and
// scenarios and lints them as kubedrill lint would. It returns the staged
// pack for the caller to show and then Enable or Discard; on any problem the
// staging directory is removed and every problem is reported.
func Fetch(ctx context.Context, dataDir, source string, o FetchOptions) (*Staged, error) {
	if err := os.MkdirAll(Dir(dataDir), 0o755); err != nil {
		return nil, fmt.Errorf("fetch pack %s: %w", source, err)
	}
	tmp, err := os.MkdirTemp(Dir(dataDir), ".staging-")
	if err != nil {
		return nil, fmt.Errorf("fetch pack %s: %w", source, err)
	}
	st := &Staged{dir: tmp, dataDir: dataDir, Record: Record{Source: source, Ref: o.Ref, AddedAt: time.Now().UTC()}}
	if err := st.fetch(ctx, source, o); err != nil {
		_ = st.Discard()
		return nil, fmt.Errorf("fetch pack %s: %w", source, err)
	}
	return st, nil
}

func (st *Staged) fetch(ctx context.Context, source string, o FetchOptions) error {
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		if o.Ref != "" {
			return errors.New("--ref applies to a git source, not a directory")
		}
		if err := copyTree(source, st.dir); err != nil {
			return err
		}
	} else {
		git := o.Git
		if git == nil {
			git = runGit
		}
		if out, err := git(ctx, "", "clone", "--quiet", "--", source, st.dir); err != nil {
			return fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(out))
		}
		if o.Ref != "" {
			if out, err := git(ctx, st.dir, "checkout", "--quiet", "--detach", o.Ref); err != nil {
				return fmt.Errorf("git checkout %s: %w: %s", o.Ref, err, strings.TrimSpace(out))
			}
		}
		out, err := git(ctx, st.dir, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("git rev-parse HEAD: %w: %s", err, strings.TrimSpace(out))
		}
		st.Record.Commit = strings.TrimSpace(out)
		if err := os.RemoveAll(filepath.Join(st.dir, ".git")); err != nil {
			return err
		}
	}
	m, err := LoadManifest(st.dir)
	if err != nil {
		return err
	}
	st.Manifest = m
	cat, err := scenario.NewCatalog(st.dir)
	if err != nil {
		return err
	}
	st.Scenarios = cat.All()
	planned := map[string]string{}
	var modules []string
	for _, mod := range m.Modules {
		modules = append(modules, mod.ID)
		for _, sc := range mod.Scenarios {
			planned[sc.ID] = mod.ID
		}
	}
	var errs []error
	errs = append(errs, scenario.LintPack(st.dir, scenario.Refs{Modules: modules, Planned: planned})...)
	existing := map[string]bool{}
	for _, id := range o.ExistingIDs {
		existing[id] = true
	}
	for _, s := range st.Scenarios {
		if existing[s.Meta.ID] {
			errs = append(errs, fmt.Errorf("scenario %s: the id is already in the catalog", s.Meta.ID))
		}
		if s.Meta.AllowPrivileged && !o.AllowPrivileged {
			errs = append(errs, fmt.Errorf("scenario %s: scenario.toml sets allow_privileged = true (its setup can escape the pod sandbox); rerun with --allow-privileged after reading it", s.Meta.ID))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	st.Scripts, err = scripts(st.dir)
	return err
}

// scripts lists every shell script of the pack, relative to its root: what
// runs on the host when a scenario is set up, solved or checked.
func scripts(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".sh") {
			rel, _ := filepath.Rel(root, path)
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// Enable moves the staged pack to Dir(dataDir)/<name> and writes its record.
// A pack of the same name must be removed first.
func (st *Staged) Enable() (Installed, error) {
	dest := filepath.Join(Dir(st.dataDir), st.Manifest.Name)
	if _, err := os.Stat(dest); err == nil {
		return Installed{}, fmt.Errorf("enable pack %s: %s exists; kubedrill pack remove %s first", st.Manifest.Name, dest, st.Manifest.Name)
	}
	data, err := json.MarshalIndent(st.Record, "", "  ")
	if err != nil {
		return Installed{}, fmt.Errorf("enable pack %s: %w", st.Manifest.Name, err)
	}
	if err := atomicfile.Write(filepath.Join(st.dir, fileRecord), append(data, '\n'), 0o644); err != nil {
		return Installed{}, fmt.Errorf("enable pack %s: %w", st.Manifest.Name, err)
	}
	if err := os.Rename(st.dir, dest); err != nil {
		return Installed{}, fmt.Errorf("enable pack %s: %w", st.Manifest.Name, err)
	}
	return Installed{Name: st.Manifest.Name, Dir: dest, Manifest: st.Manifest, Record: st.Record}, nil
}

// Discard removes the staging directory.
func (st *Staged) Discard() error {
	return os.RemoveAll(st.dir)
}

// Remove deletes the installed pack name.
func Remove(dataDir, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("remove pack %q: the name must match %s", name, namePattern)
	}
	dir := filepath.Join(Dir(dataDir), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("remove pack %s: %w", name, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove pack %s: %w", name, err)
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// copyTree copies src into dst (which exists and is empty), keeping file
// modes and skipping .git.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("copy %s: not a regular file", path)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
