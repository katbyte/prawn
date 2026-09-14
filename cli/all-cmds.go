// The prawn root command and the root-level commands: fetch, reopen, cache,
// and stats. The command groups (close, label) are added by main.go; the
// flag-free shared helpers live in cli/

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/go-kt/version"
)

// Make builds the prawn root command with its persistent flags and the
// root-level commands; main.go adds the close/label groups on top.
func Make() (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "prawn [command]",
		Short: "🦐 prawn — PRs: Attention Where Needed. assisted bulk triage, clustering, and review of a repo's open PRs",
		Long: `prawn fetches every open pull request on a repository into a local sqlite
database — comments, reviews, changed files, and linked issues included — then
runs evidence checks over them: deterministic sweeps surface the candidates,
an AI judge reads each one's actual substance, and a human approves actions
one evidence-packed card at a time. Nothing touches GitHub without an
approved action.`,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cout.SetLevelFromFlags(viper.GetBool("silent"), viper.GetBool("quiet"), viper.GetBool("verbose"))
			return BindCommandFlags(cmd)
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Printf("Run \"prawn help\" for more information about available prawn commands.\n")
			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "displays the version",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			cout.Printf("🦐 prawn %s\n", version.Version)
			return nil
		},
	})

	fetchCmd := &cobra.Command{
		Use:           "fetch",
		Short:         "fetches all open PRs (with comments, reviews, files, linked issues, and diffs) into the database",
		Long:          `Fetches every open pull request — title, body, all comments, reviews, changed files, labels, and the issues its closing keywords reference — via the GraphQL API into the local database, then each open PR's unified diff via REST (one call per new or updated PR, so the checks can see what a PR actually changes). The first run walks everything (resumable); later runs sync incrementally and reconcile the open set against GitHub.`,
		Aliases:       []string{"f"},
		Args:          cobra.NoArgs,
		PreRunE:       ValidateParams([]string{ParamTokenGH, ParamRepo, "db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			f := GetFlags()
			return f.Fetch(f.Cmd.FetchFull)
		},
	}
	addFetchFlags(fetchCmd)
	root.AddCommand(fetchCmd)

	reopenCmd := &cobra.Command{
		Use:           "reopen #",
		Short:         "reopens a closed PR (mistake recovery), with an optional comment",
		Args:          cobra.ExactArgs(1),
		PreRunE:       ValidateParams([]string{ParamTokenGH, ParamRepo, "db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			number, err := ParseNumber(args[0])
			if err != nil {
				return err
			}
			return GetFlags().Reopen(number)
		},
	}
	addReopenFlags(reopenCmd)
	root.AddCommand(reopenCmd)

	cacheCmd := &cobra.Command{
		Use:           "cache",
		Short:         "lists the local db's clearable caches and their sizes",
		Args:          cobra.NoArgs,
		PreRunE:       ValidateParams([]string{"db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return GetFlags().Cache("")
		},
	}
	cacheCmd.AddCommand(&cobra.Command{
		Use:           "clear ai|prs|all",
		Short:         "empties one cache domain — the next fetch/judge rebuilds it (actions are never touched)",
		Args:          cobra.ExactArgs(1),
		PreRunE:       ValidateParams([]string{"db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return GetFlags().Cache(args[0])
		},
	})
	root.AddCommand(cacheCmd)

	root.AddCommand(&cobra.Command{
		Use:           "stats",
		Short:         "shows what the local db holds: open PRs by review state",
		Aliases:       []string{"s"},
		Args:          cobra.NoArgs,
		PreRunE:       ValidateParams([]string{"db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return GetFlags().Stats()
		},
	})

	if err := ConfigureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}

	return root, nil
}

// Per-command flag registration, kept beside the commands that own them.

func addFetchFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("full", false, "force a full re-walk instead of an incremental sync")
}

func addReopenFlags(cmd *cobra.Command) {
	cmd.Flags().String("comment", "", "comment to post when reopening")
}
