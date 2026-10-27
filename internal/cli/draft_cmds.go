package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

func (a *app) draftGuideCommand() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "draft-guide <ref>",
		Short: "Draft the guide of simulation.yaml from solution/README.md, or check them against each other",
		Long: "draft-guide prints a guide: block for simulation.yaml with one step per command\n" +
			"of the Observation, Change and Verify sections of solution/README.md, rendered\n" +
			"for the first variant (the one the guided practice runs), without the\n" +
			"namespace flag. The bodies are placeholders. --check instead lists the guide\n" +
			"commands whose kubectl verb and resource the walkthrough never runs; it is\n" +
			"advice and always exits 0.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			sc, err := cat.Find(args[0])
			if err != nil {
				return err
			}
			seeds := sc.VariantSeeds()
			if len(seeds) == 0 {
				return fmt.Errorf("scenario %s has no variant seed", sc.Meta.ID)
			}
			rd, err := sc.Render(seeds[0])
			if err != nil {
				return err
			}
			if check {
				if rd.Simulation == nil {
					a.printf("%s has no simulation.yaml; draft one with: kubedrill draft-guide %s\n", sc.Meta.ID, sc.Meta.ID)
					return nil
				}
				drift := scenario.GuideDrift(rd)
				if len(drift) == 0 {
					a.printf("%s: every guide command appears in the solution walkthrough\n", sc.Meta.ID)
					return nil
				}
				for _, d := range drift {
					a.printf("%s: %s\n", sc.Meta.ID, d)
				}
				return nil
			}
			a.printf("%s", draftGuideYAML(sc.Meta.ID, scenario.DraftGuide(rd)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "compare the existing guide with the walkthrough instead of drafting")
	return cmd
}

// draftGuideYAML renders steps as the guide: block of simulation.yaml.
func draftGuideYAML(id string, steps []scenario.GuideStep) string {
	q := func(s string) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(s)
		return strings.TrimSpace(buf.String())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# kubedrill draft-guide %s: paste under simulation.yaml, write each body,\n", id)
	b.WriteString("# keep the steps a newcomer needs, and add check: <id> to the steps that\n")
	b.WriteString("# change the cluster, so they turn green on their own.\n")
	if len(steps) == 0 {
		b.WriteString("# The solution has no command in Observation, Change or Verify.\n")
		return b.String()
	}
	b.WriteString("guide:\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "  - title: %s\n    body: %s\n    command: %s\n", q(s.Title), q(s.Body), q(s.Command))
	}
	return b.String()
}
