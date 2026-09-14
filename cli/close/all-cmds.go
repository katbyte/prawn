package close

import (
	"github.com/spf13/cobra"

	"github.com/katbyte/prawn/cli"
)

// checkPreRun is the PreRunE every check shares.
func checkPreRun() func(*cobra.Command, []string) error {
	return cli.ValidateParams([]string{cli.ParamTokenGH, cli.ParamRepo, "db"})
}

// Command returns the close command group: every check that closes PRs on
// evidence, one subcommand per check.
func Command() *cobra.Command {
	c := &cobra.Command{
		Use:   "close",
		Short: "the checks: close PRs the evidence says are done (resolved, duplicate, stale, deprecated), and the report of every candidate",
		Long: `Each check finds open pull requests one kind of evidence says are done with,
has the AI judge every candidate, and closes them with a comment citing the
evidence. All checks share the tri-mode applies: --apply acts on the evidence
alone, --apply-with-ai has the AI score while you confirm each one, and
--apply-with-ai-auto acts unattended at or above the confidence threshold.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	c.AddCommand(resolvedCmd(), duplicateCmd(), staleCmd(), deprecatedCmd(), reportCmd())
	return c
}

// subs builds one class-scoped subcommand per entry.
func subs(c *cobra.Command, runE func(string) func(*cobra.Command, []string) error, entries []struct{ use, link, short string }) {
	for _, sub := range entries {
		c.AddCommand(&cobra.Command{
			Use:           sub.use,
			Short:         sub.short,
			Args:          cobra.NoArgs,
			PreRunE:       checkPreRun(),
			SilenceErrors: true,
			RunE:          runE(sub.link),
		})
	}
}

func resolvedCmd() *cobra.Command {
	runE := func(link string) func(cmd *cobra.Command, _ []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Resolved(link)
		}
	}
	c := &cobra.Command{
		Use:           "resolved",
		Aliases:       []string{"fixed"},
		Short:         "the change these open PRs propose has already been resolved — it landed in another commit, the issues they fix closed, or the thread says superseded. AI-scored, closeable",
		Long:          `Every open PR whose purpose appears already served, from three evidence sources judged together. Landed: the identifiers the PR's diff introduces (schema and doc properties) already exist in the provider source and git log shows them arriving after the PR was opened — somebody else made the change; the commit that introduced them is cited. Needs a local checkout (--src-dir or PRAWN_SRC_DIR; without one this class is skipped and says so). Issue-closed: every issue the PR's closing keywords reference ("fixes #N") has since been closed — the problem was dealt with some other way, or dismissed. Superseded: a maintainer's comment on the thread says the change has been covered elsewhere ("superseded by #N", "replaced by", "handled in"). Subcommands scope to one class. The AI reads each candidate exhaustively — the full body, every changed file, every review, and the whole thread — and weighs the evidence against the PR's actual substance: a PR that does more than its linked issues cover scores low. --apply closes everything listed, --apply-with-ai asks per PR with the score advising, --apply-with-ai-auto closes at or above the threshold; every close comments citing the evidence and records an action for prawn reopen.`,
		Args:          cobra.NoArgs,
		PreRunE:       checkPreRun(),
		SilenceErrors: true,
		RunE:          runE(""),
	}
	c.PersistentFlags().String("src-dir", "", "path to a local checkout of the provider — checks whether what each PR adds is already there (or PRAWN_SRC_DIR)")
	subs(c, runE, []struct{ use, link, short string }{
		{classLanded, classLanded, "only PRs whose additions are already in the provider source, arrived after they opened (strongest evidence; needs --src-dir)"},
		{classIssueClosed, classIssueClosed, "only PRs whose every linked issue has since been closed"},
		{classSuperseded, classSuperseded, "only PRs whose thread says the change was covered elsewhere (the AI earns its keep here)"},
	})
	return c
}

func duplicateCmd() *cobra.Command {
	runE := func(link string) func(cmd *cobra.Command, _ []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Duplicate(link)
		}
	}
	c := &cobra.Command{
		Use:           "duplicate",
		Aliases:       []string{"duplicates", "dupes"},
		Short:         "another open PR makes the same change — close towards the one carrying the review. AI-scored, closeable",
		Long:          `Every open PR that another open PR in the same repository duplicates. Same-issue: both PRs' closing keywords reference the same issue. Similar: near-identical titles over overlapping changed files, where nobody linked the pair. The survivor — the PR the close points at — is the one carrying more of the review (approved first, then review and comment weight, then the older on a tie); approved PRs are never the closed side. Subcommands scope to one class. The AI compares the substance of both changes before blessing a close. --apply closes everything listed, --apply-with-ai asks per PR, --apply-with-ai-auto closes at or above the threshold; every close comments pointing at the surviving PR.`,
		Args:          cobra.NoArgs,
		PreRunE:       checkPreRun(),
		SilenceErrors: true,
		RunE:          runE(""),
	}
	subs(c, runE, []struct{ use, link, short string }{
		{classSameIssue, classSameIssue, "only PRs sharing a closing issue with another open PR (strongest evidence)"},
		{classSimilar, classSimilar, "only pairs with near-identical titles over overlapping files (the AI earns its keep here)"},
	})
	return c
}

func deprecatedCmd() *cobra.Command {
	runE := func(link string) func(cmd *cobra.Command, _ []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Deprecated(link)
		}
	}
	c := &cobra.Command{
		Use:           "deprecated",
		Aliases:       []string{"removed", "moot"},
		Short:         "these open PRs change something the provider has removed or deprecated — moot as proposed. AI-scored, closeable",
		Long:          `Every open PR whose change targets a resource, data source, or property that no longer exists in the provider — or is formally on the way out — judged against the removals inventory read from a local provider checkout (--src-dir): the major-version upgrade guides and the changelog's DEPRECATIONS sections name what went and what replaced it, and DeprecationMessage markers in the source catch deprecations announced in code before any guide mentions them; for removed resources the source corroborates (the name appears nowhere any more). Candidates surface from the PR's changed files and its title/body mentions; subcommands scope to resource-level or property-level evidence. The AI reads each PR's actual substance — a PR that equally changes a living resource is not moot, and deprecated things still work, so those score low unless the change extends the dying thing itself. --apply closes everything listed, --apply-with-ai asks per PR with the score advising, --apply-with-ai-auto closes at or above the threshold; every close comments citing the removal and its successor.`,
		Args:          cobra.NoArgs,
		PreRunE:       checkPreRun(),
		SilenceErrors: true,
		RunE:          runE(""),
	}
	c.PersistentFlags().String("src-dir", "", "path to a local checkout of the provider — supplies the upgrade guides, changelog, and deprecation markers")
	subs(c, runE, []struct{ use, link, short string }{
		{"resource", "resource", "only PRs targeting removed/deprecated resources and data sources (strongest evidence)"},
		{"property", "property", "only PRs word-matching removed/deprecated properties of the resources they touch (the AI earns its keep here)"},
	})
	return c
}

func staleCmd() *cobra.Command {
	runE := func(link string) func(cmd *cobra.Command, _ []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Stale(link)
		}
	}
	c := &cobra.Command{
		Use:           "stale",
		Short:         "these open PRs look abandoned — waiting on an author who never came back. AI-scored, closeable",
		Long:          `Open PRs whose author appears to have walked away, classed by the shape of the evidence: waiting (the PR carries the waiting-response label and the author has not come back — 90 days of silence is enough) or changes-requested (a maintainer's review requested changes and the author neither pushed nor replied since — 180 days); subcommands scope to one class. The AI reads the thread before blessing a close — an author who addressed the feedback and is waiting on the maintainers scores low, so the ball stays with the maintainers where it belongs. Approved PRs are never touched. --apply closes everything listed, --apply-with-ai asks per PR with the score advising, --apply-with-ai-auto closes at or above the threshold; every close comments inviting a fresh rebase and records an action for prawn reopen.`,
		Args:          cobra.NoArgs,
		PreRunE:       checkPreRun(),
		SilenceErrors: true,
		RunE:          runE(""),
	}
	subs(c, runE, []struct{ use, link, short string }{
		{classStaleWaiting, classStaleWaiting, "only PRs labelled waiting-response whose author never came back (strongest evidence)"},
		{classStaleChanges, classStaleChanges, "only PRs where changes were requested and the author never engaged"},
	})
	return c
}

// reportCmd returns prawn close report: the HTML page of every close candidate
// the checks see. Like koi, the report lives under its command group.
func reportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "report",
		Short:         "writes an HTML report of every close candidate the checks see (resolved, duplicate, stale, deprecated)",
		Long:          `Writes close-<yyyymmdd-hhmm>.html: every close candidate each check sees, grouped by check with the evidence for why it is listed — the closed issues, the superseding claim, the surviving duplicate, the unanswered review, the removed resource and its successor — everything linked; the top of the page describes each check and jumps to its section. --with-ai scores every candidate with the check's own judge (cached verdicts are reused) and sorts surest first; --limit N caps each check for a cheap test run. The deprecated check needs the provider checkout (--src-dir or PRAWN_SRC_DIR), and the report refuses to run without it rather than silently leave a check out.`,
		Args:          cobra.NoArgs,
		PreRunE:       checkPreRun(),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return flags().Report()
		},
	}
	c.Flags().String("out", "report", "directory to write the report files into")
	c.Flags().Bool("with-ai", false, "AI-score every candidate in every check (cached verdicts reused) and sort surest first")
	c.Flags().Int("limit", 0, "cap candidates per check for a cheap test run (0 = all)")
	c.Flags().String("src-dir", "", "path to a local checkout of the provider for the deprecated check (or PRAWN_SRC_DIR)")
	return c
}
