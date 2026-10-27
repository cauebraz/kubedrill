package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultProfile     = "base"
	defaultJournalLast = 20
	apiServiceWait     = 3 * time.Minute
	apiServicePoll     = 2 * time.Second
)

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "kubedrill",
		Short: "Local Kubernetes training: reset a scenario, fix it, get graded",
		Long: "kubedrill drives a local kind cluster, deploys one scenario per episode and\n" +
			"grades the end state. Without arguments it checks the tools, creates the\n" +
			"cluster and prints the scenario list with the next recommended one; on a\n" +
			"terminal it then reopens the active episode or offers to start that one.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runDefault(cmd.Context())
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !noExpiry[cmd.Name()] {
				a.expireIfDue(cmd.Context())
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&a.rootFlag, "root", "", "repository root (default: KUBEDRILL_ROOT or the checkout above the working directory)")
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		a.doctorCommand(),
		a.upCommand(),
		a.downCommand(),
		a.listCommand(),
		a.startCommand(),
		a.ticketCommand(),
		a.checkCommand(),
		a.watchCommand(),
		a.hintCommand(),
		a.namespaceCommand(),
		a.learnCommand(),
		a.storyCommand(),
		a.conceptsCommand(),
		a.solutionCommand(),
		a.diagnoseCommand(),
		a.debriefCommand(),
		a.submitCommand(),
		a.statsCommand(),
		a.replayCommand(),
		a.faultCommand(),
		a.healCommand(),
		a.resetCommand(),
		a.shellCommand(),
		a.nodeCommand(),
		a.statusCommand(),
		a.journalCommand(),
		a.pathCommand(),
		a.examCommand(),
		a.lintCommand(),
		a.newCommand(),
		a.smokeCommand(),
		a.profileCommand(),
		a.versionCommand(),
		a.webCommand(),
		a.draftGuideCommand(),
	)
	return root
}

// noExpiry names the commands that never finish an expired exam episode:
// they do not read the journal or they manage the cluster itself.
var noExpiry = map[string]bool{"kubedrill": true, "doctor": true, "up": true, "down": true, "lint": true, "new": true, "smoke": true, "version": true, "help": true, "fault": true, "heal": true, "profile": true, "render": true}

// usageArgs returns a cobra args validator that reports usage when the
// argument count is not n.
func usageArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return errors.New(usage)
		}
		return nil
	}
}
