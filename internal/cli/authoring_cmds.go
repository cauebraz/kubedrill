package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/curriculum"
)

func (a *app) lintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Validate every scenario directory, curriculum.yaml and the concept pages",
		Long: "lint validates every scenario directory under scenarios/, curriculum.yaml and\n" +
			"the concept pages under docs/concepts/, and checks that every concept a module\n" +
			"names has a page. It prints every problem and exits 1 when there is one.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			var errs []error
			var moduleIDs, conceptIDs []string
			lib, libErr := a.f.Concepts(root)
			if libErr != nil {
				errs = append(errs, libErr)
			} else {
				conceptIDs = lib.IDs()
			}
			c, err := a.f.Curriculum(curriculum.DefaultPath(root))
			if err != nil {
				errs = append(errs, err)
			} else {
				for _, lv := range c.Levels {
					for _, m := range lv.Modules {
						moduleIDs = append(moduleIDs, m.ID)
					}
				}
				if libErr == nil {
					errs = append(errs, missingConcepts(c.ConceptIDs(), conceptIDs)...)
				}
			}
			errs = append(a.f.Lint(root, moduleIDs, conceptIDs), errs...)
			for _, e := range errs {
				a.printf("%s\n", e)
			}
			if len(errs) > 0 {
				return fmt.Errorf("lint: %d problem(s)", len(errs))
			}
			a.println("lint: ok")
			return nil
		},
	}
}

// missingConcepts reports every curriculum concept id without a page in the
// library, in curriculum order.
func missingConcepts(wanted, library []string) []error {
	have := make(map[string]bool, len(library))
	for _, id := range library {
		have[id] = true
	}
	var errs []error
	for _, id := range wanted {
		if !have[id] {
			errs = append(errs, fmt.Errorf("curriculum.yaml: concept %q has no page in the concept library", id))
		}
	}
	return errs
}

func (a *app) newCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "new <level> <id>",
		Short: "Scaffold a scenario directory, for example: new L0 001-my-slug",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			dir, err := a.f.Scaffold(root, args[0], args[1])
			if err != nil {
				return err
			}
			a.printf("created %s\n", dir)
			a.println("Next:")
			a.println("  1. edit scenario.toml, instruction.md, hints.md, checks.yaml, setup/ and solution/")
			a.println("  2. kubedrill lint")
			a.printf("  3. kubedrill smoke %s\n", args[1])
			return nil
		},
	}
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			a.println(versionLine(debug.ReadBuildInfo))
			return nil
		},
	}
}

func versionLine(read func() (*debug.BuildInfo, bool)) string {
	info, ok := read()
	if !ok || info == nil {
		return "kubedrill (no build info)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	var rev, at, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = " (modified)"
			}
		}
	}
	parts := []string{"kubedrill", version}
	if rev != "" {
		parts = append(parts, "rev "+rev+modified)
	} else {
		parts = append(parts, "revision unknown (built from an unversioned tree)")
	}
	if at != "" {
		parts = append(parts, at)
	}
	parts = append(parts, info.GoVersion)
	return strings.Join(parts, " ")
}

func (a *app) profileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Kind profile tools for authors and CI",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.profileRenderCommand())
	return cmd
}

func (a *app) profileRenderCommand() *cobra.Command {
	var profile, out string
	cmd := &cobra.Command{
		Use:   "render --out <file>",
		Short: "Render cluster/profiles/<profile>.yaml into a kind configuration file",
		Long: "profile render executes cluster/profiles/<profile>.yaml with the data kubedrill up\n" +
			"uses (the audit directory under the cluster state directory, created when\n" +
			"missing, and cluster/audit-policy.yaml) and writes the kind configuration to\n" +
			"--out, for kind create cluster --config or CI. It fails when the result still\n" +
			"holds a template action.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if out == "" {
				return errors.New("usage: kubedrill profile render [--profile base] --out <file>")
			}
			raw, err := a.renderProfile(profile)
			if err != nil {
				return err
			}
			if err := atomicfile.Write(out, raw, 0o644); err != nil {
				return fmt.Errorf("write rendered profile %s: %w", out, err)
			}
			a.printf("rendered profile %s to %s\n", profile, out)
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", defaultProfile, "kind profile under cluster/profiles")
	cmd.Flags().StringVar(&out, "out", "", "file to write the kind configuration to")
	return cmd
}

// renderProfile renders profile with the ProfileData of ensureCluster
// through the cluster manager, the same code path Ensure uses, so profile
// render never drifts from it.
func (a *app) renderProfile(profile string) ([]byte, error) {
	if profile == "" || profile != filepath.Base(profile) || strings.HasPrefix(profile, ".") {
		return nil, fmt.Errorf("profile %q: want a file name under cluster/profiles without .yaml", profile)
	}
	data, err := a.profileData()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(data.AuditDir, 0o700); err != nil {
		return nil, fmt.Errorf("render profile %s: create audit directory: %w", profile, err)
	}
	cl, err := a.getCluster()
	if err != nil {
		return nil, err
	}
	raw, err := cl.RenderProfile(profile, data)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(raw, []byte("{{")) {
		return nil, fmt.Errorf("render profile %s: the result still holds a template action ({{)", profile)
	}
	return raw, nil
}
