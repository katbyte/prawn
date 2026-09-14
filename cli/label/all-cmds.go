// Package label holds the labellers: add-only labelling of open PRs on
// evidence — bug and enhancement.
package label

import (
	"github.com/spf13/cobra"

	"github.com/katbyte/prawn/cli"
)

// Flags wraps the shared flag data so the labellers keep their method form.
type Flags struct{ *cli.FlagData }

// flags is every labeller RunE's entry point to the fully populated Flags.
func flags() *Flags { return &Flags{cli.GetFlags()} }

// labelPreRun is the PreRunE every labeller shares.
func labelPreRun() func(*cobra.Command, []string) error {
	return cli.ValidateParams([]string{cli.ParamTokenGH, cli.ParamRepo, "db"})
}

// Command returns the label command group: one subcommand per label prawn can
// add. Labels are only ever added, never removed.
func Command() *cobra.Command {
	c := &cobra.Command{
		Use:   "label",
		Short: "the labellers: add the labels the evidence supports (bug, enhancement) — add-only",
		Long: `Each labeller finds open PRs whose evidence supports a label they don't carry
— a linked issue wearing it, a title reading like one — has the AI judge
whether the label actually fits the change, and adds it. Labels are only ever
added, never removed. All labellers share the tri-mode applies: --apply acts
on the evidence alone, --apply-with-ai has the AI score while you confirm
each one, and --apply-with-ai-auto acts unattended at or above the confidence
threshold.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	c.AddCommand(labelCmd(LabelBug), labelCmd(LabelEnhancement))
	return c
}

func labelCmd(label string) *cobra.Command {
	runE := func(link string) func(cmd *cobra.Command, _ []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Label(label, link)
		}
	}
	c := &cobra.Command{
		Use:           label,
		Short:         "these open PRs read as " + article(label) + " their labels don't record — label them? AI-scored, add-only",
		Long:          `Open PRs lacking both the bug and enhancement labels whose evidence supports ` + label + `: an issue the PR's closing keywords reference carries the label (the linked-issue class, strongest), or the title reads like one (the title class). The AI judges whether the label fits what the change actually does — cosmetic fixes, test-only changes, and dependency bumps score low. --apply labels everything listed, --apply-with-ai asks per PR with the score advising, --apply-with-ai-auto labels at or above the threshold. Labels are only ever added, never removed.`,
		Args:          cobra.NoArgs,
		PreRunE:       labelPreRun(),
		SilenceErrors: true,
		RunE:          runE(""),
	}
	for _, sub := range []struct{ use, short string }{
		{classLinkedIssue, "only PRs whose linked issue carries the " + label + " label (strongest evidence)"},
		{classTitle, "only PRs whose title reads like " + article(label) + " (the AI earns its keep here)"},
	} {
		c.AddCommand(&cobra.Command{
			Use:           sub.use,
			Short:         sub.short,
			Args:          cobra.NoArgs,
			PreRunE:       labelPreRun(),
			SilenceErrors: true,
			RunE:          runE(sub.use),
		})
	}
	return c
}
