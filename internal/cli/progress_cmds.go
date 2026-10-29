package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/web"
)

func (a *app) progressCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Reset the progress of one scenario or of everything, or restore it",
		Long: "progress reset makes the runs of a scenario (or of every scenario with\n" +
			"--all) stop counting toward clean solves, mastery, badges, the\n" +
			"recommendation, list, path, exam and stats, and clears the stage progress\n" +
			"of the browser workspace. Nothing is deleted: kubedrill journal and\n" +
			"kubedrill debrief still show every run, marked (reset), and progress\n" +
			"restore counts them again. A scenario with the active run cannot be\n" +
			"reset; end the run first.",
	}
	cmd.AddCommand(a.progressResetCommand(), a.progressRestoreCommand())
	return cmd
}

func (a *app) progressResetCommand() *cobra.Command {
	var all, yes bool
	cmd := &cobra.Command{
		Use:   "reset <ref> | --all",
		Short: "Stop counting the runs of one scenario (or every one) toward progress and clear its stage progress",
		Args:  scopeArgs(&all, "usage: kubedrill progress reset <ref> | --all [--yes]"),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runProgressReset(firstArg(args), all, yes)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "reset every scenario")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation")
	return cmd
}

func (a *app) progressRestoreCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "restore <ref> | --all",
		Short: "Undo a progress reset: its runs count again and its stage progress comes back",
		Long: "restore removes the reset marker of one scenario, or with --all the marker\n" +
			"of the reset of everything; scenarios reset one by one keep their own\n" +
			"marker until restored by name.",
		Args: scopeArgs(&all, "usage: kubedrill progress restore <ref> | --all"),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runProgressRestore(firstArg(args))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "undo the reset of every scenario")
	return cmd
}

// scopeArgs accepts one scenario ref or the --all flag, not both.
func scopeArgs(all *bool, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if (len(args) == 1) == *all || len(args) > 1 {
			return errors.New(usage)
		}
		return nil
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// scopeLabel names a reset scope in the output: the scenario id, or every
// scenario.
func scopeLabel(id string) string {
	if id == "" {
		return "every scenario"
	}
	return id
}

// scopeID resolves ref to a scenario id of the catalog; an empty ref is
// every scenario.
func (a *app) scopeID(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	cat, err := a.getCatalog()
	if err != nil {
		return "", err
	}
	sc, err := cat.Find(ref)
	if err != nil {
		return "", err
	}
	return sc.Meta.ID, nil
}

// refuseActive is the refusal of a reset whose scope (id, every scenario
// when empty) holds the active episode cur, or nil.
func refuseActive(cur *journal.Episode, id string) error {
	if cur == nil || (id != "" && cur.ScenarioID != id) {
		return nil
	}
	return fmt.Errorf("%s is active (episode %s); %w", cur.ScenarioID, cur.ID, web.ErrRunActive)
}

// resetProgress records the reset of id (every scenario when empty) after
// refusing it while the active episode is in scope; stages is what the
// marker saves (see journal.Store.ResetProgress).
func resetProgress(j Journal, id string, stages func(prev json.RawMessage) (json.RawMessage, error)) (journal.ResetResult, error) {
	cur, err := j.Current()
	if err != nil {
		return journal.ResetResult{}, fmt.Errorf("read current episode: %w", err)
	}
	if err := refuseActive(cur, id); err != nil {
		return journal.ResetResult{}, err
	}
	res, err := j.ResetProgress(id, stages)
	if errors.Is(err, journal.ErrActive) {
		if cur, cerr := j.Current(); cerr == nil {
			if rerr := refuseActive(cur, id); rerr != nil {
				return journal.ResetResult{}, rerr
			}
		}
	}
	return res, err
}

// restoreProgress removes the reset marker of id (every scenario when
// empty); without one, the error names the markers there are.
func restoreProgress(j Journal, id string) (journal.ResetResult, error) {
	res, err := j.RestoreProgress(id)
	if !errors.Is(err, journal.ErrNoReset) {
		return res, err
	}
	r, rerr := j.Resets()
	if rerr != nil {
		return res, err
	}
	var ids []string
	for sid := range r.Scenarios {
		ids = append(ids, sid)
	}
	sort.Strings(ids)
	msg := fmt.Sprintf("no reset of %s is recorded; nothing to restore", scopeLabel(id))
	switch {
	case id == "" && len(ids) > 0:
		msg += fmt.Sprintf("; scenarios reset one by one: %s (kubedrill progress restore <ref>)", strings.Join(ids, ", "))
	case id != "" && r.All != nil:
		msg += "; every scenario was reset: kubedrill progress restore --all"
	}
	return res, errors.New(msg)
}

// runsCounted counts the finished episodes in scope among eps.
func runsCounted(eps []journal.Episode, id string) int {
	n := 0
	for _, e := range eps {
		if e.EndedAt != nil && (id == "" || e.ScenarioID == id) {
			n++
		}
	}
	return n
}

// runsLine is "1 run no longer counts" or "3 runs count again".
func runsLine(n int, singular, plural string) string {
	verb := plural
	if n == 1 {
		verb = singular
	}
	return render.Plural(n, "run") + " " + verb
}

// stagesFile is the stage progress file of the browser workspace in the
// journal directory.
func stagesFile(j Journal) *web.StageFile {
	return web.NewStageFile(filepath.Join(j.Dir(), webProgressFile))
}

func (a *app) runProgressReset(ref string, all, yes bool) error {
	id, err := a.scopeID(ref)
	if err != nil {
		return err
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	cur, err := j.Current()
	if err != nil {
		return fmt.Errorf("read current episode: %w", err)
	}
	if err := refuseActive(cur, id); err != nil {
		return err
	}
	eps, err := countedEpisodes(j)
	if err != nil {
		return err
	}
	n := runsCounted(eps, id)
	undo := "kubedrill progress restore " + id
	question := fmt.Sprintf("Reset the progress of %s? %s stop counting toward clean solves, mastery and badges and its stage progress is cleared; kubedrill journal keeps the history and %s undoes it.", id, render.Plural(n, "run"), undo)
	if all {
		undo = "kubedrill progress restore --all"
		question = fmt.Sprintf("Reset all progress? %s stop counting toward clean solves, mastery and badges and every stage checkmark is cleared; kubedrill journal keeps the history and %s undoes it.", render.Plural(n, "run"), undo)
	}
	ok, err := a.confirm(question, yes)
	if err != nil {
		return err
	}
	if !ok {
		a.println("progress kept")
		return nil
	}
	stages := stagesFile(j)
	res, err := resetProgress(j, id, func(prev json.RawMessage) (json.RawMessage, error) {
		return stages.Snapshot(id, prev)
	})
	if err != nil {
		return err
	}
	cleared, err := stages.Clear(id)
	if err != nil {
		return fmt.Errorf("the reset of %s is recorded, but the stage progress was not cleared: %w", scopeLabel(id), err)
	}
	line := fmt.Sprintf("%s: %s", scopeLabel(id), runsLine(res.Runs, "no longer counts", "no longer count"))
	if cleared {
		line += "; stage progress cleared"
	} else {
		line += "; no stage progress to clear"
	}
	a.println(line)
	a.println("undo with: " + undo)
	return nil
}

func (a *app) runProgressRestore(ref string) error {
	id, err := a.scopeID(ref)
	if err != nil {
		return err
	}
	j, err := a.getJournal()
	if err != nil {
		return err
	}
	res, err := restoreProgress(j, id)
	if err != nil {
		return err
	}
	back, err := stagesFile(j).Restore(res.Reset.Stages)
	if err != nil {
		return fmt.Errorf("the runs of %s count again, but the stage progress did not come back: %w", scopeLabel(id), err)
	}
	line := fmt.Sprintf("%s: %s", scopeLabel(id), runsLine(res.Runs, "counts again", "count again"))
	if back {
		line += "; stage progress restored"
	} else {
		line += "; no stage progress to restore"
	}
	a.println(line)
	return nil
}
