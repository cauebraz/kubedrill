package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/telemetry"
)

const (
	hintNudgeWindow = 10 * time.Second
	hintNudge       = "tip: hints are spaced; try one more observation before the next one"
	diagnoseUsage   = `kubedrill diagnose "<what you think is wrong>"`
	examSubmitLine  = "kubedrill submit grades once"
	telemetryOff    = "telemetry: audit log off; recreate the cluster to enable it (kubedrill down --yes && kubedrill up)"
	auditPolicyOld  = "telemetry: the audit policy changed since the cluster was created; recreate it to apply (kubedrill down --yes && kubedrill up)"
)

var errNoFaults = errors.New("no fault manager is wired into this build")

// hintLadder names each rung of the hint ladder by what it gives away.
var hintLadder = []string{"where to look", "what to observe", "what it means", "what to change"}

// hintLabel is "hint 2 of 4 (what to observe)".
func hintLabel(index, total int) string {
	label := fmt.Sprintf("hint %d of %d", index, total)
	if index >= 1 && index <= len(hintLadder) {
		label += " (" + hintLadder[index-1] + ")"
	}
	return label
}

// diagnosisNeeded is the refusal shown when what (the last hint, the
// solution) waits for a recorded diagnosis; it matches
// env.ErrDiagnosisRequired.
func diagnosisNeeded(what string) error {
	return refusal{msg: what + " needs your diagnosis first: " + diagnoseUsage, cause: env.ErrDiagnosisRequired}
}

// examRefusal is the error of a practice-only command run in exam mode.
func examRefusal(cmd string) error {
	return refusal{msg: cmd + " is not available in exam mode; " + examSubmitLine, cause: env.ErrExamMode}
}

// refusal is a learner-facing error whose text stands alone and which
// still matches its cause with errors.Is.
type refusal struct {
	msg   string
	cause error
}

func (r refusal) Error() string { return r.msg }
func (r refusal) Unwrap() error { return r.cause }

// interactive reports whether prompts can be answered: stdin and stdout are
// terminals.
func (a *app) interactive() bool {
	return a.term.IsTerminal && a.isInputTerminal
}

// ask prints question and returns the trimmed line typed; end of input is an
// empty answer and an interruption is errInterrupted.
func (a *app) ask(question string) (string, error) {
	fmt.Fprintf(a.stdout, "%s ", question)
	line, err := a.readLine()
	if errors.Is(err, errInterrupted) {
		a.println()
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// expireIfDue finishes an exam episode whose time box ended before running
// any command that reads the journal, and prints the outcome.
func (a *app) expireIfDue(ctx context.Context) {
	j, err := a.getJournal()
	if err != nil {
		return
	}
	cur, err := j.Current()
	if err != nil || cur == nil || cur.Mode != journal.ModeExam || cur.Deadline == nil || a.now().Before(*cur.Deadline) {
		return
	}
	s, err := a.learnerSession(ctx)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: the exam time box of %s expired; the final grade failed: %v\n", cur.ScenarioID, err)
		return
	}
	ep, err := s.Expire(ctx)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: the exam time box of %s expired; the final grade failed: %v\n", cur.ScenarioID, err)
	}
	if ep == nil {
		return
	}
	a.printf("exam time box of %s expired at %s: %s\n", ep.ScenarioID, cur.Deadline.Local().Format("15:04"), outcomeLine(ep))
}

// outcomeLine names the outcome and the last grade of an ended episode.
func outcomeLine(ep *journal.Episode) string {
	line := "outcome " + string(ep.Outcome)
	if n := len(ep.Grades); n > 0 {
		passed, total := ep.Grades[n-1].Summary()
		line += fmt.Sprintf(" (%d of %d checks passed)", passed, total)
	}
	return line
}

// afterSolve prints the solved banner, the recommendation and the next
// steps, then the debrief: prompts on a terminal, the summary otherwise. It
// reports whether the debrief questions were answered. Every line goes
// through p.
func (a *app) afterSolve(ctx context.Context, p render.Printer, ep *journal.Episode) (bool, error) {
	if ep == nil {
		return false, nil
	}
	cat, err := a.getCatalog()
	if err != nil {
		return false, err
	}
	sc, err := cat.Find(ep.ScenarioID)
	if err != nil {
		return false, err
	}
	p.Println()
	p.Println(p.WrapLine(p.Style(render.StylePass.Bold(true), render.SolvedBanner(sc.Meta.Title, ep)), "  "))
	if j, err := a.getJournal(); err == nil {
		if eps, err := allEpisodes(j); err == nil {
			if rec, ok := a.recommend(cat, eps); ok {
				for _, l := range recommendationLines(p, cat, rec, "Recommended") {
					p.Println(l)
				}
			}
		}
	}
	p.Println(p.WrapLine(solvedNextLine(p, sc.Meta.ID), "  "))
	p.Println()
	return a.debrief(ctx, p, ep, sc, a.interactive())
}

// debriefLater points at the command that answers the debrief questions
// when they could not be asked.
const debriefLater = "kubedrill debrief --redo answers the questions later"

// debrief prints the summary of ep and, when prompt is set, asks the
// debrief questions and records the answers. Enter keeps a previous
// answer, shown in brackets. It ends with one line: "debrief recorded"
// when a question has an answer, the debrief --redo pointer otherwise (also
// without prompts on a non-terminal). It reports whether any question has
// an answer.
func (a *app) debrief(ctx context.Context, p render.Printer, ep *journal.Episode, sc *scenario.Scenario, prompt bool) (bool, error) {
	a.writeDebriefSummary(p, ep, sc)
	if !prompt {
		if !a.interactive() {
			p.Println(p.WrapLine(debriefLater, "  "))
		}
		return false, nil
	}
	prev := journal.Debrief{}
	if ep.Debrief != nil {
		prev = *ep.Debrief
	}
	matched, err := a.askMatched(ep.Diagnosis)
	if err != nil {
		return false, err
	}
	d := journal.Debrief{At: a.now(), Principle: prev.Principle}
	if d.RuledOutEarlier, err = a.askKeeping("Which observation would have ruled out your first hypothesis earlier?", prev.RuledOutEarlier); err != nil {
		return false, err
	}
	if d.NextTimeFirst, err = a.askKeeping("Next time I check first:", prev.NextTimeFirst); err != nil {
		return false, err
	}
	s, err := a.learnerSession(ctx)
	if err != nil {
		return false, err
	}
	if err := s.RecordDebrief(ep.ID, d, matched); err != nil {
		return false, fmt.Errorf("record debrief of %s: %w", ep.ID, err)
	}
	answered := d.RuledOutEarlier != "" || d.NextTimeFirst != ""
	if answered {
		p.Println(p.WrapLine("debrief recorded; kubedrill debrief shows it again", "  "))
	} else {
		p.Println(p.WrapLine(debriefLater, "  "))
	}
	return answered, nil
}

// askMatched asks whether the diagnosis matched the root cause, showing a
// previous answer as [y] or [n], which Enter keeps; without a diagnosis it
// asks nothing and returns nil.
func (a *app) askMatched(diag *journal.Diagnosis) (*bool, error) {
	if diag == nil {
		return nil, nil
	}
	matched := diag.Matched
	q := "Did your diagnosis match? [y/n]"
	if matched != nil {
		prev := "n"
		if *matched {
			prev = "y"
		}
		q = fmt.Sprintf("Did your diagnosis match? [y/n] [%s]", prev)
	}
	ans, err := a.ask(q)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(ans) {
	case "y", "yes":
		matched = boolPtr(true)
	case "n", "no":
		matched = boolPtr(false)
	}
	return matched, nil
}

// askKeeping asks question; with a previous answer the prompt shows it in
// brackets and an empty line keeps it, otherwise enter skips.
func (a *app) askKeeping(question, previous string) (string, error) {
	if previous == "" {
		return a.ask(question + " (enter to skip)")
	}
	ans, err := a.ask(fmt.Sprintf("%s [%s]", question, previous))
	if err != nil || ans != "" {
		return ans, err
	}
	return previous, nil
}

func boolPtr(b bool) *bool { return &b }

// rootCause is the reference root cause of the variant ep ran.
func rootCause(sc *scenario.Scenario, ep *journal.Episode) string {
	for _, v := range sc.Variants {
		if v.Name == ep.Variant {
			return v.RootCause
		}
	}
	return ""
}

// writeDebriefSummary prints the facts of an ended episode: duration,
// hints, the trajectory from telemetry, the diagnosis next to the root
// cause, and the stored answers.
func (a *app) writeDebriefSummary(p render.Printer, ep *journal.Episode, sc *scenario.Scenario) {
	p.Println(p.WrapLine(p.Style(render.StyleBold, fmt.Sprintf("Debrief: %s (episode %s, %s)", sc.Meta.ID, ep.ID, ep.Outcome)), "  "))
	detail(p, fmt.Sprintf("  duration %s, %s", render.Minutes(ep.DurationAt(a.now())), render.Plural(ep.HintsUsed, "hint")))
	t := ep.Telemetry
	switch {
	case t == nil || t.Source == "" || t.Source == "none":
		detail(p, "  telemetry: none recorded (no audit log or shell log for this episode)")
	default:
		detail(p, fmt.Sprintf("  observations before the first write: %d (telemetry: %s)", t.ReadsBeforeFirstWrite, t.Source))
		switch {
		case t.FirstWriteTarget == "":
			detail(p, "  first write: none recorded")
		case telemetry.MatchTarget(t.FirstWriteTarget, sc.Meta.FixTargets):
			detail(p, fmt.Sprintf("  first write: %s (a fix target)", t.FirstWriteTarget))
		default:
			detail(p, fmt.Sprintf("  first write: %s (not a fix target; the fix targets are %s)", t.FirstWriteTarget, render.OrDash(strings.Join(sc.Meta.FixTargets, ", "))))
		}
		detail(p, fmt.Sprintf("  verdict: %s", verdictLabel(t.Verdict)))
	}
	diag := "none recorded"
	if ep.Diagnosis != nil {
		diag = ep.Diagnosis.Text
	}
	detail(p, "  your diagnosis: "+diag)
	detail(p, "  root cause: "+render.OrDash(rootCause(sc, ep)))
	if ep.Diagnosis != nil && ep.Diagnosis.Matched != nil {
		detail(p, fmt.Sprintf("  diagnosis matched: %s", yesNo(*ep.Diagnosis.Matched)))
	}
	if d := ep.Debrief; d != nil {
		if d.RuledOutEarlier != "" {
			detail(p, fmt.Sprintf("  would have ruled it out earlier: %s", d.RuledOutEarlier))
		}
		if d.NextTimeFirst != "" {
			detail(p, fmt.Sprintf("  next time I check first: %s", d.NextTimeFirst))
		}
		if d.Principle != "" {
			detail(p, fmt.Sprintf("  principle: %s", d.Principle))
		}
	}
}

// detail prints one indented debrief line, wrapped with a four space
// hanging indent.
func detail(p render.Printer, line string) {
	p.Println(p.WrapLine(line, "    "))
}

// verdictLabel names the telemetry verdict; "unknown" means the audit log
// was off.
func verdictLabel(v journal.Verdict) string {
	switch v {
	case "":
		return "-"
	case journal.VerdictUnknown:
		return "unknown (telemetry off)"
	}
	return string(v)
}

func (a *app) debriefCommand() *cobra.Command {
	var redo bool
	cmd := &cobra.Command{
		Use:   "debrief [episode-id]",
		Short: "Show the debrief of an ended episode, or answer it again",
		Long: "debrief prints the summary of the most recent ended episode (or the one named):\n" +
			"duration, hints, observations before the first write, the first write target,\n" +
			"the verdict, your diagnosis next to the root cause and the answers recorded.\n" +
			"On a terminal it asks the questions when none are recorded, or with --redo.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			ep, err := findEpisode(j, id, false)
			if err != nil {
				return err
			}
			cat, err := a.getCatalog()
			if err != nil {
				return err
			}
			sc, err := cat.Find(ep.ScenarioID)
			if err != nil {
				return err
			}
			if redo && !a.interactive() {
				return errors.New("debrief --redo asks questions and needs a terminal on stdin and stdout")
			}
			_, err = a.debrief(cmd.Context(), a.printer(), ep, sc, a.interactive() && (redo || ep.Debrief == nil))
			return err
		},
	}
	cmd.Flags().BoolVar(&redo, "redo", false, "answer the debrief questions again")
	return cmd
}

// findEpisode returns the episode with id (or an id prefix), or without an
// id the most recent ended episode; withActive also accepts the active one.
func findEpisode(j Journal, id string, withActive bool) (*journal.Episode, error) {
	eps, err := allEpisodes(j)
	if err != nil {
		return nil, err
	}
	if id != "" {
		var found []journal.Episode
		for _, e := range eps {
			if e.ID == id {
				found = []journal.Episode{e}
				break
			}
			if strings.HasPrefix(e.ID, id) {
				found = append(found, e)
			}
		}
		switch {
		case len(found) == 0:
			return nil, fmt.Errorf("episode %q not found; kubedrill journal lists them", id)
		case len(found) > 1:
			return nil, fmt.Errorf("episode %q is ambiguous (%d matches); kubedrill journal lists them", id, len(found))
		}
		e := found[0]
		if e.EndedAt == nil && !withActive {
			return nil, fmt.Errorf("episode %s is still active; the debrief comes after kubedrill check passes or the episode ends", e.ID)
		}
		return &e, nil
	}
	for i := len(eps) - 1; i >= 0; i-- {
		if eps[i].EndedAt != nil || withActive {
			e := eps[i]
			return &e, nil
		}
	}
	return nil, errors.New("no ended episode yet; kubedrill journal lists the episodes")
}

func (a *app) replayCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "replay [episode-id]",
		Short: "Play the terminal recording of an episode in the workstation image",
		Long: "replay plays the latest asciinema recording of the most recent episode (or the\n" +
			"one named), as recorded by the workstation under <journal>/recordings/. A path\n" +
			"recorded inside the workstation (/data/recordings/...) is read by its file name\n" +
			"under the host journal. Space pauses, q quits.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			j, err := a.getJournal()
			if err != nil {
				return err
			}
			ep, err := findEpisode(j, id, true)
			if err != nil {
				return err
			}
			cast := latestRecording(j.Dir(), ep)
			if cast != "" {
				cast = filepath.Join(journal.RecordingsDir(j.Dir()), filepath.Base(cast))
			}
			if cast == "" {
				return fmt.Errorf("episode %s (%s) has no recording; the workstation records the shell under %s when an episode is active", ep.ID, ep.ScenarioID, journal.RecordingsDir(j.Dir()))
			}
			ws, err := a.getWorkstation(j)
			if err != nil {
				return err
			}
			if ws == nil {
				return errNoWorkstation
			}
			if err := ws.DockerAvailable(cmd.Context()); err != nil {
				return fmt.Errorf("replay needs docker: %w", err)
			}
			if !a.interactive() {
				return errors.New("replay needs a terminal on stdin and stdout")
			}
			name := filepath.Base(cast)
			a.printf("replaying %s (space pause, q quit)\n", name)
			return ws.Replay(cmd.Context(), name)
		},
	}
}

// latestRecording returns the newest recording of ep: the last one the
// telemetry lists, or the newest <namespace>-<unix>.cast in the journal's
// recordings directory within the episode window.
func latestRecording(dir string, ep *journal.Episode) string {
	if t := ep.Telemetry; t != nil && len(t.Recordings) > 0 {
		return t.Recordings[len(t.Recordings)-1]
	}
	if ep.Namespace == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(journal.RecordingsDir(dir), ep.Namespace+"-*.cast"))
	best, bestAt := "", int64(-1)
	for _, m := range matches {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), ep.Namespace+"-"), ".cast")
		at, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || at < ep.StartedAt.Unix()-1 {
			continue
		}
		if ep.EndedAt != nil && at > ep.EndedAt.Unix() {
			continue
		}
		if at > bestAt {
			best, bestAt = m, at
		}
	}
	return best
}

// faultActionNames lists faults.Actions as strings, in help order.
func faultActionNames() []string {
	acts := faults.Actions()
	names := make([]string, 0, len(acts))
	for _, act := range acts {
		names = append(names, string(act))
	}
	return names
}

func (a *app) faultCommand() *cobra.Command {
	var delay time.Duration
	var loss float64
	names := faultActionNames()
	nodes := strings.Join(cluster.NodeNames(), "|")
	usage := "usage: kubedrill fault <" + strings.Join(names, "|") + "> <" + nodes + "> [--delay 200ms --loss 5]"
	cmd := &cobra.Command{
		Use:   "fault <action> <node>",
		Short: "Inject a node fault (a tool for scenario hooks and practice); kubedrill heal undoes it",
		Long: "fault breaks one kind node: pause/unpause freezes the node container, stop-kubelet/\n" +
			"start-kubelet stops the kubelet, netem adds delay and loss on the node's network\n" +
			"(--delay, --loss in percent) and clear-netem removes it. Faults are recorded in\n" +
			"the cluster state directory; kubedrill heal and every kubedrill start undo them.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New(usage)
			}
			if !slices.Contains(names, args[0]) {
				return fmt.Errorf("unknown action %q; valid: %s", args[0], strings.Join(names, ", "))
			}
			if !slices.Contains(cluster.NodeNames(), args[1]) {
				return unknownNode(args[1])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if loss < 0 || loss > 100 {
				return fmt.Errorf("--loss must be between 0 and 100, got %g", loss)
			}
			if delay < 0 {
				return fmt.Errorf("--delay must be >= 0, got %s", delay)
			}
			fm, err := a.faultManager()
			if err != nil {
				return err
			}
			action := faults.Action(args[0])
			if err := fm.Apply(cmd.Context(), action, args[1], faults.NetemOptions{Delay: delay, Loss: loss}); err != nil {
				switch {
				case errors.Is(err, faults.ErrAlreadyActive):
					return refusal{msg: alreadyActive(action, args[1]), cause: err}
				case errors.Is(err, faults.ErrUnknownNode):
					return refusal{msg: unknownNode(args[1]).Error(), cause: err}
				}
				return err
			}
			a.printf("fault %s applied to %s; kubedrill heal undoes every recorded fault\n", action, args[1])
			return nil
		},
	}
	cmd.Flags().DurationVar(&delay, "delay", 0, "netem: added latency, for example 200ms")
	cmd.Flags().Float64Var(&loss, "loss", 0, "netem: packet loss in percent, for example 5")
	return cmd
}

// alreadyActive names the state a repeated fault finds the node in.
func alreadyActive(action faults.Action, node string) string {
	switch action {
	case "pause":
		return node + " is already paused"
	case "stop-kubelet":
		return node + " already has its kubelet stopped"
	}
	return fmt.Sprintf("%s already has %s applied", node, action)
}

// unknownNode is the error for a node name outside cluster.NodeNames.
func unknownNode(node string) error {
	return fmt.Errorf("unknown node %q; valid: %s", node, strings.Join(cluster.NodeNames(), ", "))
}

func (a *app) faultManager() (Faults, error) {
	fm, err := a.getFaults()
	if err != nil {
		return nil, err
	}
	if fm == nil {
		return nil, errNoFaults
	}
	return fm, nil
}

func (a *app) healCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "heal",
		Short: "Undo every fault kubedrill fault recorded",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fm, err := a.faultManager()
			if err != nil {
				return err
			}
			active, err := fm.Active(cmd.Context())
			if err != nil {
				return err
			}
			if len(active) == 0 {
				a.println("no recorded faults")
				return nil
			}
			if err := fm.Heal(cmd.Context()); err != nil {
				return err
			}
			for _, f := range active {
				a.printf("healed %s on %s\n", f.Action, f.Node)
			}
			return nil
		},
	}
}

// telemetryNoticeFile is the marker under the journal directory that
// records the notice was shown, holding the creation time of the cluster it
// was shown for when the cluster reports one.
const telemetryNoticeFile = ".telemetry-notice"

// clusterCreated is the optional part of Cluster that reports when the kind
// cluster was created, so the notice comes back once for a new cluster.
type clusterCreated interface {
	Created(ctx context.Context) (time.Time, error)
}

// auditPolicyNoticeFile is the marker under the journal directory that
// records the audit policy drift notice was shown, with the same stamp as
// telemetryNoticeFile.
const auditPolicyNoticeFile = ".audit-policy-notice"

// telemetryNotice tells the learner, once per journal (and once more per
// recreated cluster when the cluster reports its creation time), that the
// audit log is off, so the debrief has no API trajectory, and, in the same
// way, that the audit policy changed since the cluster was created. Nothing
// is printed when the audit log is on and the policy is current, or when a
// question fails.
func (a *app) telemetryNotice(ctx context.Context, j Journal) {
	if on, known := a.auditState(ctx); known && !on {
		a.noticeOnce(ctx, j, telemetryNoticeFile, telemetryOff)
	}
	if stale, err := a.auditPolicyStale(ctx); err == nil && stale {
		a.noticeOnce(ctx, j, auditPolicyNoticeFile, auditPolicyOld)
	}
}

// auditPolicyStale asks the cluster, under ctx with a 5 s timeout, whether
// the audit policy changed since it was created.
func (a *app) auditPolicyStale(ctx context.Context) (bool, error) {
	cl, err := a.getCluster()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, auditCheckTimeout)
	defer cancel()
	return cl.AuditPolicyStale(ctx)
}

// noticeOnce prints text on stderr unless the marker file under the journal
// directory already records the current cluster (its creation time when
// the cluster reports one, "shown" otherwise), then records it.
func (a *app) noticeOnce(ctx context.Context, j Journal, file, text string) {
	cl, err := a.getCluster()
	if err != nil {
		return
	}
	stamp := "shown"
	if cc, ok := cl.(clusterCreated); ok {
		cctx, cancel := context.WithTimeout(ctx, auditCheckTimeout)
		created, err := cc.Created(cctx)
		cancel()
		if err == nil && !created.IsZero() {
			stamp = created.UTC().Format(time.RFC3339)
		}
	}
	marker := filepath.Join(j.Dir(), file)
	if err := os.MkdirAll(j.Dir(), 0o700); err != nil {
		fmt.Fprintln(a.stderr, text)
		return
	}
	if prev, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(prev)) == stamp {
		return
	}
	fmt.Fprintln(a.stderr, text)
	if err := os.WriteFile(marker, []byte(stamp+"\n"), 0o600); err != nil {
		fmt.Fprintf(a.stderr, "warning: record the notice in %s: %v\n", marker, err)
	}
}
