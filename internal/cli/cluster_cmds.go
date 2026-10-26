package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/render"
)

func (a *app) runDefault(ctx context.Context) error {
	tools, err := a.runDoctor(ctx)
	if err != nil {
		return err
	}
	for _, t := range tools {
		if t.Name == "docker" && !t.Found {
			return fmt.Errorf("docker is not available: %s; install it, start the daemon and run kubedrill again", t.Install)
		}
	}
	cl, err := a.getCluster()
	if err != nil {
		return err
	}
	a.printf("ensuring cluster %s (profile %s)...\n", cluster.Name, defaultProfile)
	if err := a.ensureCluster(ctx, cl, defaultProfile, nil); err != nil {
		return err
	}
	a.println()
	if err := a.runList("", true); err != nil {
		return err
	}
	if !a.interactive() || a.insideWorkstation() || a.getenv("KUBEDRILL_NO_SHELL") == "1" {
		return nil
	}
	return a.continuePractice(ctx)
}

// continuePractice takes the learner from the default run into practice:
// the workstation of the active episode, or the recommended scenario after
// a [Y/n] prompt, so ./start.sh is the one command to remember.
func (a *app) continuePractice(ctx context.Context) error {
	a.expireIfDue(ctx)
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if cur != nil {
		a.printf("\nresuming %s; opening the workstation\n", cur.ScenarioID)
		return a.runShell(ctx, layoutTmux, false)
	}
	pick, ok := a.nextPick()
	if !ok {
		return nil
	}
	a.println()
	switch strings.ToLower(a.ask(fmt.Sprintf("Start %s now? [Y/n]", pick.Ref))) {
	case "", "y", "yes":
		return a.runStart(ctx, pick.Ref, startOptions{seed: pick.Seed, layout: layoutTmux})
	}
	a.println(a.printer().WrapLine(fmt.Sprintf("not started; run ./start.sh again or kubedrill start %s when you are ready", pick.Ref), "  "))
	return nil
}

func (a *app) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report on docker, kubectl and kind",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tools, err := a.runDoctor(cmd.Context())
			if err != nil {
				return err
			}
			for _, t := range tools {
				if !t.Found {
					return fmt.Errorf("%s is missing: %s", t.Name, t.Install)
				}
			}
			return nil
		},
	}
}

func (a *app) runDoctor(ctx context.Context) ([]cluster.Tool, error) {
	cl, err := a.getCluster()
	if err != nil {
		return nil, err
	}
	tools, err := cl.Doctor(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(tools))
	for _, t := range tools {
		detail := t.Detail
		if !t.Found && t.Install != "" {
			detail = "install: " + t.Install
		}
		rows = append(rows, []string{t.Name, yesNo(t.Found), render.OrDash(t.Version), render.OrDash(detail)})
	}
	table(a.stdout, []string{"TOOL", "FOUND", "VERSION", "DETAIL"}, rows)
	return tools, nil
}

func (a *app) upCommand() *cobra.Command {
	var profile, addons string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create the kind cluster when missing, wait for it and install the addons",
		Long: "up creates the kind cluster from cluster/profiles/<profile>.yaml when it is\n" +
			"missing, with the API audit log written to <state>/audit, then installs\n" +
			"metrics-server and every addon --addons names (cluster/addons/<name>). A\n" +
			"profile or audit policy change applies only to a new cluster: kubedrill down\n" +
			"--yes && kubedrill up; up on an existing cluster reports a changed policy.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := a.getCluster()
			if err != nil {
				return err
			}
			extra, err := parseAddons(cl, addons)
			if err != nil {
				return err
			}
			exists, err := cl.Exists(cmd.Context())
			if err != nil {
				return err
			}
			if exists {
				a.printf("cluster %s: present (profile %s)\n", cluster.Name, profile)
			} else {
				a.printf("creating cluster %s (profile %s), about 2 minutes...\n", cluster.Name, profile)
			}
			if err := a.ensureCluster(cmd.Context(), cl, profile, extra); err != nil {
				return err
			}
			if !exists {
				a.printf("cluster %s is ready (profile %s)\n", cluster.Name, profile)
				return nil
			}
			if stale, err := a.auditPolicyStale(cmd.Context()); err == nil && stale {
				fmt.Fprintln(a.stderr, auditPolicyOld)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", defaultProfile, "kind profile under cluster/profiles")
	cmd.Flags().StringVar(&addons, "addons", "", "comma-separated addons to install besides metrics-server, for example gateway,kwok")
	return cmd
}

// metricsServer is the addon every up installs when the cluster names no
// base addons.
const metricsServer = "metrics-server"

// parseAddons splits the --addons value and rejects names the cluster does
// not ship, listing the valid ones.
func parseAddons(cl Cluster, value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	known := map[string]bool{}
	for _, n := range cl.Addons() {
		known[n] = true
	}
	var out, unknown []string
	for _, n := range strings.Split(value, ",") {
		n = strings.TrimSpace(n)
		switch {
		case n == "":
		case !known[n]:
			unknown = append(unknown, n)
		default:
			out = append(out, n)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown addon(s) %s; valid: %s", strings.Join(unknown, ", "), strings.Join(cl.Addons(), ", "))
	}
	return out, nil
}

// ensureCluster ensures the cluster exists (audit log under
// <cluster state>/audit), then installs the base addons plus extra.
func (a *app) ensureCluster(ctx context.Context, cl Cluster, profile string, extra []string) error {
	data, err := a.profileData()
	if err != nil {
		return err
	}
	if err := cl.Ensure(ctx, profile, data); err != nil {
		return err
	}
	names := cl.BaseAddons()
	if len(names) == 0 {
		names = []string{metricsServer}
	}
	for _, n := range extra {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	conn, err := a.getConn()
	if err != nil {
		return err
	}
	if err := cl.EnsureAddons(ctx, conn, names); err != nil {
		return err
	}
	a.printf("addons: %s\n", strings.Join(names, ", "))
	return a.waitAPIServices(ctx, conn, apiServiceWait)
}

// profileData is the template input of every profile: the audit directory
// under the cluster state directory and the repository's audit policy.
func (a *app) profileData() (cluster.ProfileData, error) {
	root, err := a.repoRoot()
	if err != nil {
		return cluster.ProfileData{}, err
	}
	state, err := a.stateDir()
	if err != nil {
		return cluster.ProfileData{}, err
	}
	return cluster.ProfileData{AuditDir: cluster.AuditDirIn(state), AuditPolicy: filepath.Join(root, "cluster", "audit-policy.yaml")}, nil
}

func (a *app) waitAPIServices(ctx context.Context, conn *lazyConn, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(apiServicePoll)
	defer ticker.Stop()
	announced := false
	for {
		down, err := conn.UnavailableAPIServices(ctx)
		if err == nil && len(down) == 0 {
			if announced {
				a.printf("API services available\n")
			}
			return nil
		}
		if !announced && err == nil {
			a.printf("waiting for API services: %s\n", strings.Join(down, ", "))
			announced = true
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("wait for API services: %w", err)
			}
			return fmt.Errorf("API services still unavailable after %s: %s; check: kubectl get apiservices", limit, strings.Join(down, ", "))
		case <-ticker.C:
		}
	}
}

func (a *app) downCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Delete the kind cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := a.confirm(fmt.Sprintf("Delete cluster %s and every scenario namespace?", cluster.Name), yes)
			if err != nil {
				return err
			}
			if !ok {
				a.println("kept the cluster")
				return nil
			}
			cl, err := a.getCluster()
			if err != nil {
				return err
			}
			if err := cl.Delete(cmd.Context()); err != nil {
				return err
			}
			a.printf("cluster %s deleted\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	return cmd
}
