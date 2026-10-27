package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

var errNoWorkstation = errors.New("no workstation is wired into this build")

func (a *app) nodeCommand() *cobra.Command {
	names := strings.Join(cluster.NodeNames(), "|")
	usage := "usage: kubedrill node <" + names + "> (kubectl get nodes lists them as " + cluster.Name + "-<node>)"
	return &cobra.Command{
		Use:   "node <" + names + ">",
		Short: "Open a root shell on one kind node",
		Long: "node runs docker exec into the kind node container " + cluster.Name + "-<node> as root.\n" +
			"Inside the workstation, which has no docker, it runs kubectl debug\n" +
			"node/" + cluster.Name + "-<node> --profile=sysadmin and chroots into the node filesystem,\n" +
			"the way a real cluster without ssh allows it; the debugging pod lives in the\n" +
			workstation.DebugNamespace + " namespace and is deleted when the shell ends. Changes made on the\n" +
			"node (files, processes, kubelet state) persist until kubedrill down; exit the\n" +
			"shell to return to where node was run.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 || !slices.Contains(cluster.NodeNames(), args[0]) {
				return errors.New(usage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			ws, err := a.getWorkstation(j)
			if err != nil {
				return err
			}
			if ws == nil {
				return errNoWorkstation
			}
			if a.insideWorkstation() {
				name := cluster.Name + "-" + args[0]
				a.printf("kubedrill node: root on %s through kubectl debug node/%s --profile=sysadmin -- chroot /host (changes persist until kubedrill down)\n", name, name)
				if err := ws.NodeDebug(cmd.Context(), cluster.Name, args[0], cluster.DebugImage); err != nil {
					return fmt.Errorf("node shell %s: %w", args[0], err)
				}
				return nil
			}
			if err := ws.DockerAvailable(cmd.Context()); err != nil {
				return fmt.Errorf("a node shell needs docker: %w", err)
			}
			a.printf("kubedrill node: root on %s-%s (kind node; changes persist until kubedrill down)\n", cluster.Name, args[0])
			if err := ws.NodeShell(cmd.Context(), cluster.Name, args[0]); err != nil {
				return fmt.Errorf("node shell %s: %w", args[0], err)
			}
			return nil
		},
	}
}
