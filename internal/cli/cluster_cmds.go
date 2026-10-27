package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/render"
)

// recreateOfferFile, under the journal directory, records the cluster
// whose recreation the learner declined, so the offer is made once per
// cluster.
const recreateOfferFile = ".recreate-offer"

// offerRecreate offers to delete a cluster created with an older audit
// policy, so the default run creates it again with the repository's
// policy, when no episode is active (an active episode keeps the cluster
// and gets the notice of start instead). A declined offer is not repeated
// for the same cluster.
func (a *app) offerRecreate(ctx context.Context, cl Cluster) error {
	exists, err := cl.Exists(ctx)
	if err != nil || !exists {
		return err
	}
	if stale, err := a.auditPolicyStale(ctx); err != nil || !stale {
		return nil
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	if cur, err := j.Current(); err != nil || cur != nil {
		return nil
	}
	stamp, err := a.clusterStamp(ctx)
	if err != nil {
		return nil
	}
	marker := filepath.Join(j.Dir(), recreateOfferFile)
	if prev, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(prev)) == stamp {
		return nil
	}
	a.println(a.printer().WrapLine("the cluster was created with an older audit policy, so the debrief misses part of what you change; no episode is active, so recreating it loses nothing and takes about 2 minutes", "  "))
	ans, err := a.ask("Recreate the cluster now? [Y/n]")
	if err != nil {
		return err
	}
	switch strings.ToLower(ans) {
	case "", "y", "yes":
		if err := cl.Delete(ctx); err != nil {
			return err
		}
		a.forgetFaults(ctx)
		a.printf("cluster %s deleted\n", cluster.Name)
		return nil
	}
	if err := os.MkdirAll(j.Dir(), 0o700); err == nil {
		_ = os.WriteFile(marker, []byte(stamp+"\n"), 0o600)
	}
	a.println("kept the cluster; kubedrill down --yes && kubedrill up recreates it")
	return nil
}

// preparation is a workstation Prepare running in the background while the
// cluster is ensured; its output waits in out until the terminal is free.
type preparation struct {
	cancel context.CancelFunc
	done   chan error
	out    bytes.Buffer
	err    error
	waited bool
}

// startPreparation starts Workstation.Prepare in a goroutine, so the image
// and linux binary builds overlap with the cluster creation; it returns nil
// when there is no workstation to prepare. The caller owns the goroutine
// and ends it with stop.
func (a *app) startPreparation(ctx context.Context) *preparation {
	j, err := a.getJournal()
	if err != nil {
		return nil
	}
	ws, err := a.getWorkstation(j)
	if err != nil || ws == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &preparation{cancel: cancel, done: make(chan error, 1)}
	go func() {
		p.done <- ws.Prepare(ctx, &p.out)
	}()
	return p
}

// ready reports, without blocking, whether the preparation has ended.
func (p *preparation) ready() bool {
	if p.waited {
		return true
	}
	select {
	case p.err = <-p.done:
		p.waited = true
		return true
	default:
		return false
	}
}

// wait blocks until the preparation ends and returns its error.
func (p *preparation) wait() error {
	if !p.waited {
		p.err = <-p.done
		p.waited = true
	}
	return p.err
}

// stop cancels the preparation and waits for its goroutine; a nil
// preparation is a no-op.
func (p *preparation) stop() {
	if p == nil {
		return
	}
	p.cancel()
	_ = p.wait()
}

// finishPreparation waits for p, printing its output; a failure is a
// warning, because Shell builds the same things again and reports the
// error where the learner needs it.
func (a *app) finishPreparation(ctx context.Context, p *preparation) {
	if p == nil {
		return
	}
	if !p.ready() {
		a.progress.Step("finishing the workstation build")
	}
	err := p.wait()
	a.progress.Finish()
	if out := strings.TrimSpace(p.out.String()); out != "" {
		a.println(out)
	}
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(a.stderr, "warning: %v; kubedrill tries again when the page opens a terminal\n", err)
	}
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
				if !t.Found && !t.Optional {
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
		switch {
		case !t.Found && t.Optional:
			detail = "optional, the workstation has its own; for your own terminal, smoke and scenario hooks: " + t.Install
		case !t.Found && t.Install != "":
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

// forgetFaults drops the fault records of a cluster that was deleted or
// recreated; a failure is a warning, kubedrill heal clears them later.
func (a *app) forgetFaults(ctx context.Context) {
	fm, err := a.getFaults()
	if err != nil || fm == nil {
		return
	}
	if err := fm.Forget(ctx); err != nil {
		fmt.Fprintf(a.stderr, "warning: %v; kubedrill heal clears them\n", err)
	}
}

// ensureCluster ensures the cluster exists (audit log under
// <cluster state>/audit), forgets the fault records when it had to create
// it, installs the base addons plus extra, waits for
// the API services and preloads cluster.DebugImage and the images of every
// scenario (scenario.Scenario.Images) on the nodes, so practice needs no
// registry afterwards; a failed preload is a warning.
func (a *app) ensureCluster(ctx context.Context, cl Cluster, profile string, extra []string) error {
	data, err := a.profileData()
	if err != nil {
		return err
	}
	existed, err := cl.Exists(ctx)
	if err != nil {
		return err
	}
	if err := cl.Ensure(ctx, profile, data); err != nil {
		return err
	}
	if !existed {
		a.forgetFaults(ctx)
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
	if err := a.waitAPIServices(ctx, conn, apiServiceWait); err != nil {
		return err
	}
	images := a.preloadImages()
	a.progress.Step(fmt.Sprintf("preloading %s on the nodes", render.Plural(len(images), "image")))
	err = cl.PullImages(ctx, images)
	a.progress.Finish()
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		fmt.Fprintf(a.stderr, "warning: %v; the nodes pull these images from the registry when a pod first needs them\n", err)
	}
	return nil
}

// preloadImages is cluster.DebugImage plus the images of every scenario in
// the catalog; a scenario that does not render is reported on stderr and
// contributes what it could.
func (a *app) preloadImages() []string {
	images := []string{cluster.DebugImage}
	cat, err := a.getCatalog()
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: preload only the debug image: %v\n", err)
		return images
	}
	seen := map[string]bool{cluster.DebugImage: true}
	for _, sc := range sortedScenarios(cat) {
		found, err := sc.Images()
		if err != nil {
			fmt.Fprintf(a.stderr, "warning: %v\n", err)
		}
		for _, image := range found {
			if !seen[image] {
				seen[image] = true
				images = append(images, image)
			}
		}
	}
	return images
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
			a.forgetFaults(cmd.Context())
			a.printf("cluster %s deleted\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	return cmd
}
