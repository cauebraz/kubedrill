package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

const (
	layoutTmux          = workstation.LayoutTmux
	layoutShell         = workstation.LayoutShell
	backOnHostLine      = "back on the host; kubedrill check grades from here too"
	ticketInWorkstation = "ticket shown in the workstation (kubedrill ticket reprints it)"
)

var errNoWorkstation = errors.New("no workstation is wired into this build")

// checkLayout rejects a --layout value workstation.ValidLayout does not know.
func checkLayout(layout string) error {
	if workstation.ValidLayout(layout) {
		return nil
	}
	return fmt.Errorf("--layout must be %s or %s, got %q", layoutTmux, layoutShell, layout)
}

func (a *app) shellCommand() *cobra.Command {
	var layout string
	var exam bool
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Open the workstation again: kubectl and the tools pinned to the training cluster",
		Long: "shell opens the containerized workstation against the training cluster: kubectl,\n" +
			"helm, k9s, stern and kubedrill, with KUBECONFIG pinned to the cluster. With an\n" +
			"active episode it is the shell start opened; exit it to return to the host.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkLayout(layout); err != nil {
				return err
			}
			return a.runShell(cmd.Context(), layout, exam)
		},
	}
	addLayoutFlag(cmd, &layout)
	cmd.Flags().BoolVar(&exam, "exam", false, "exam workstation: a rehearsal with PATH limited to the exam tools (kubectl, helm, yq, jq, vim), no watch pane, the allowed docs listed (automatic for an exam episode)")
	return cmd
}

// runShell opens the workstation for the active episode, or for the cluster
// alone without one; exam forces the exam workstation.
func (a *app) runShell(ctx context.Context, layout string, exam bool) error {
	if a.insideWorkstation() {
		return errors.New("already inside the workstation (KUBEDRILL_WORKSTATION=1); exit this shell to return to the host")
	}
	if !a.term.IsTerminal || !a.isInputTerminal {
		return errors.New("shell needs a terminal on stdin and stdout; run it from an interactive terminal")
	}
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
	if err := ws.DockerAvailable(ctx); err != nil {
		return fmt.Errorf("the workstation needs docker: %w", err)
	}
	cl, err := a.getCluster()
	if err != nil {
		return err
	}
	exists, err := cl.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("cluster %s: missing; the workstation needs its kubeconfig, create it with: kubedrill up", cluster.Name)
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if exam && (cur == nil || cur.Mode != journal.ModeExam) {
		cur = &journal.Episode{Mode: journal.ModeExam}
	}
	return a.openWorkstation(ctx, j, ws, layout, cur)
}

// openWorkstation writes the internal kubeconfig and drops the learner into
// the toolbox; Shell builds the binary and the image itself. An exam
// episode (ep may be nil) opens the exam workstation with its deadline. The
// return line is printed when the shell exits normally and an episode is
// still active, the one check would grade.
func (a *app) openWorkstation(ctx context.Context, j Journal, ws Workstation, layout string, ep *journal.Episode) error {
	cl, err := a.getCluster()
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stderr, "preparing the workstation...")
	state, err := a.stateDir()
	if err != nil {
		return fmt.Errorf("workstation kubeconfig path: %w", err)
	}
	kubeconfig := cluster.InternalKubeconfigPathIn(state)
	if err := cl.WriteInternalKubeconfig(ctx, kubeconfig); err != nil {
		return fmt.Errorf("write workstation kubeconfig: %w", err)
	}
	opts := workstation.ShellOptions{Kubeconfig: kubeconfig, Layout: layout, ClusterName: cluster.Name}
	if ep != nil && ep.Mode == journal.ModeExam {
		opts.Exam = true
		if ep.Deadline != nil {
			opts.Deadline = *ep.Deadline
		}
	}
	if err := ws.Shell(ctx, opts); err != nil {
		return fmt.Errorf("workstation shell: %w", err)
	}
	if cur, err := j.Current(); err == nil && cur != nil {
		a.println(backOnHostLine)
	}
	return nil
}

func (a *app) nodeCommand() *cobra.Command {
	names := strings.Join(cluster.NodeNames(), "|")
	usage := "usage: kubedrill node <" + names + "> (kubectl get nodes lists them as " + cluster.Name + "-<node>)"
	return &cobra.Command{
		Use:   "node <" + names + ">",
		Short: "Open a root shell on one kind node",
		Long: "node runs docker exec into the kind node container " + cluster.Name + "-<node> as root.\n" +
			"Changes made there (files, processes, kubelet state) persist until kubedrill\n" +
			"down; exit the shell to return to where node was run.",
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
