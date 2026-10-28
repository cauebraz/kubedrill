package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/pack"
)

func (a *app) packCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Add, list or remove scenario packs from other repositories",
		Long: "A pack is a directory or git repository with a pack.yaml and a scenarios/ tree.\n" +
			"kubedrill pack add copies it under the journal directory, pins it to a commit,\n" +
			"lints it as kubedrill lint would, shows every script it would run on this\n" +
			"machine and asks before enabling it. An enabled pack's scenarios appear in\n" +
			"kubedrill list, the ladder and the browser workspace under the modules its\n" +
			"pack.yaml names.",
	}
	cmd.AddCommand(a.packAddCommand(), a.packListCommand(), a.packRemoveCommand())
	return cmd
}

func (a *app) packAddCommand() *cobra.Command {
	var ref string
	var yes, allowPrivileged bool
	cmd := &cobra.Command{
		Use:   "add <directory|git url>",
		Short: "Fetch a pack, lint it, show its scripts and enable it after confirmation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			var existing []string
			for _, s := range cat.All() {
				existing = append(existing, s.Meta.ID)
			}
			opts := pack.FetchOptions{Ref: ref, AllowPrivileged: allowPrivileged}
			c, err := a.loadCurriculum()
			if err != nil {
				return err
			}
			var planned map[string]string
			opts.ModuleIDs, opts.ExamDomains, planned = curriculumRefs(c)
			for id := range planned {
				existing = append(existing, id)
			}
			opts.ExistingIDs = existing
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			if lib, err := a.f.Concepts(root); err == nil {
				opts.Concepts = lib.IDs()
			}
			st, err := pack.Fetch(cmd.Context(), j.Dir(), args[0], opts)
			if err != nil {
				return err
			}
			m := st.Manifest
			a.printf("pack %s (%s) from %s", m.Name, m.Title, args[0])
			if st.Record.Commit != "" {
				a.printf(" at %s", st.Record.Commit[:min(12, len(st.Record.Commit))])
			}
			a.println()
			a.printf("  %d scenario(s):\n", len(st.Scenarios))
			for _, s := range st.Scenarios {
				a.printf("    %s  %s (%s, %d min)\n", s.Meta.ID, s.Meta.Title, s.Meta.Level, s.Meta.Minutes)
			}
			if len(st.Scripts) > 0 {
				a.println("  scripts that run on this machine when a scenario is set up, solved or checked:")
				for _, p := range st.Scripts {
					a.printf("    %s\n", p)
				}
			}
			ok, err := a.confirm(fmt.Sprintf("Enable pack %s?", m.Name), yes)
			if err != nil || !ok {
				_ = st.Discard()
				if err != nil {
					return err
				}
				a.println("pack not enabled")
				return nil
			}
			inst, err := st.Enable()
			if err != nil {
				_ = st.Discard()
				return err
			}
			a.catalog = nil
			a.printf("pack %s enabled in %s; kubedrill list shows its scenarios\n", inst.Name, inst.Dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "tag, branch or commit to pin a git source to (default: the default branch head)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "enable without asking")
	cmd.Flags().BoolVar(&allowPrivileged, "allow-privileged", false, "accept scenarios whose scenario.toml sets allow_privileged = true")
	return cmd
}

func (a *app) packListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the enabled packs",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			packs, lerr := pack.List(j.Dir())
			if len(packs) == 0 && lerr == nil {
				a.printf("no packs; kubedrill pack add <directory|git url> enables one (%s)\n", pack.Dir(j.Dir()))
				return nil
			}
			for _, p := range packs {
				n := 0
				for _, m := range p.Manifest.Modules {
					n += len(m.Scenarios)
				}
				pin := p.Record.Source
				if p.Record.Commit != "" {
					pin += " @ " + p.Record.Commit[:min(12, len(p.Record.Commit))]
				}
				a.printf("%s  %s  %d scenario(s)  %s\n", p.Name, p.Manifest.Title, n, pin)
			}
			return lerr
		},
	}
}

func (a *app) packRemoveCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an enabled pack; the journal keeps the episodes played from it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			ok, err := a.confirm(fmt.Sprintf("Remove pack %s? Its scenario files are deleted; the journal keeps the episodes.", args[0]), yes)
			if err != nil || !ok {
				return err
			}
			if err := pack.Remove(j.Dir(), args[0]); err != nil {
				return err
			}
			a.catalog = nil
			a.printf("pack %s removed\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "remove without asking")
	return cmd
}

// packRoots returns the directories of the enabled packs, read from the
// journal directory; a broken pack is reported on stderr and skipped.
func (a *app) packRoots() ([]string, error) {
	j, err := a.getJournal()
	if err != nil {
		return nil, err
	}
	roots, err := pack.Roots(j.Dir())
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: %v\n", err)
	}
	return roots, nil
}

// mergePacks appends the modules of every enabled pack to the ladder.
func (a *app) mergePacks(c *curriculum.Curriculum) error {
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	packs, lerr := pack.List(j.Dir())
	var errs []error
	if lerr != nil {
		fmt.Fprintf(a.stderr, "warning: %v\n", lerr)
	}
	for _, p := range packs {
		for _, m := range p.Manifest.Modules {
			if err := c.Merge(m.Level, p.Name, []curriculum.Module{m.Module}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("load curriculum with packs: %w", errors.Join(errs...))
	}
	return nil
}
