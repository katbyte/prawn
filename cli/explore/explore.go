// Package explore is prawn explore: the interactive page over every PR that
// was open at any point in the period — a global filter, then tabs: the
// queue to work, the trends, the people, the areas, and the checks' live
// candidates. One self-contained html file, no server, state in the url.
package explore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/prawn/assets"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/cli/close"
	"github.com/katbyte/prawn/lib/explore"
)

// Command returns prawn explore.
func Command() *cobra.Command {
	c := &cobra.Command{
		Use:   "explore",
		Short: "writes the interactive explore page: every PR open in the period, with a global filter over data, trends, suggested, areas, people, and checks tabs",
		Long: `Writes one self-contained html page over every PR that was open at any point since --since (or PRAWN_SINCE): a global filter bar
(period, state, group, author, service, kind, label, court, effort, and a query language), and under it the tabs — the data: every
matching PR sorted by whose court it sits in and how cheap it is to review, the trends (daily open PRs by state, opened vs
merged vs closed, response and merge times, age), the suggested (the easy wins by category), the people (authors and reviewers matching the filter, with their trend),
the areas (services and kinds of change), and the checks (every close candidate the checks see, restricted to the filter).
Groups of logins come from PRAWN_GROUP_<name>=login,login in the environment or .prawn; PRAWN_MAINTAINERS names the group
that counts as maintainers (default: members) for the response and ball-in-court stats. The checks tab needs the provider
checkout (--src-dir / PRAWN_SRC_DIR) and is skipped with a note without it; --with-ai scores the candidates as the report does.`,
		Aliases:       []string{"x", "ui"},
		Args:          cobra.NoArgs,
		PreRunE:       cli.ValidateParams([]string{cli.ParamTokenGH, cli.ParamRepo, "db"}),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return run(cli.GetFlags())
		},
	}
	c.Flags().String("explore-out", filepath.Join("report", "explore.html"), "the html file to write (overwritten each run — the page's state lives in its url)")
	c.Flags().String("since", "", "the period's start (yyyy-mm-dd): every PR open at any point since — or set PRAWN_SINCE")
	c.Flags().String("view-from", "", "the period the page opens on (yyyy-mm-dd; default: january 1st of last year) — the data still reaches back to --since; or set PRAWN_VIEW_FROM")
	c.Flags().String("src-dir", "", "a local checkout of the provider, for the release markers and the checks tab (or PRAWN_SRC_DIR)")
	c.Flags().Bool("checks", true, "run the close checks for the checks tab (needs --src-dir)")
	c.Flags().Bool("with-ai", false, "AI-score every check candidate, as prawn close report --with-ai does (cached verdicts are reused)")
	c.Flags().Int("limit", 0, "cap candidates per check when scoring, for cheap test runs (0 = all)")
	return c
}

func run(f *cli.FlagData) error {
	since, err := f.SinceTime()
	if err != nil {
		return err
	}
	if since.IsZero() {
		return errors.New("explore needs the period's start: set --since or PRAWN_SINCE (yyyy-mm-dd)")
	}
	if f.Cmd.Report.WithAI {
		if err := f.RequireAI(); err != nil {
			return err
		}
	}
	if !f.NoAutoFetch {
		if err := f.AutoFetch(); err != nil {
			return err
		}
	}

	d, err := f.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	now := time.Now().UTC()
	cout.Printf("building the explore page of %s since <yellow>%s</>...\n", f.RepoTag(), since.Format("2006-01-02"))

	in := explore.Input{}
	if in.PRs, err = d.PRsSince(since); err != nil {
		return err
	}
	if in.Events, err = d.AllEvents(); err != nil {
		return err
	}
	if in.Commits, err = d.AllCommits(); err != nil {
		return err
	}
	if in.Verdicts, err = d.AllVerdicts(); err != nil {
		return err
	}
	if in.Closes, err = d.AllCloses(); err != nil {
		return err
	}
	withEvents := 0
	for _, p := range in.PRs {
		if len(in.Events[p.Number]) > 0 {
			withEvents++
		}
	}
	cout.Printf("  <gray>%d PRs in the period, %d with timelines</>\n", len(in.PRs), withEvents)
	if withEvents < len(in.PRs) {
		cout.Printf("  <yellow>%d PRs have no timeline yet — run</> <cyan>prawn fetch --full --since %s</> <yellow>to fetch them</>\n",
			len(in.PRs)-withEvents, since.Format("2006-01-02"))
	}

	groups := cli.LoadGroups()
	cfg := explore.Config{Groups: groups, MaintainerGroup: cli.MaintainersGroup(), Now: now}
	cfg.Maintainers = groups.Maintainers()
	cfg.Partners = groups.Partners()
	cfg.PartnerGroup = cli.PartnersGroup()
	if len(cfg.Maintainers) == 0 {
		cout.Printf("  <gray>no PRAWN_GROUP_%s set — maintainers are whoever github marks member/owner/collaborator</>\n", strings.ToUpper(cfg.MaintainerGroup))
		if cfg.Maintainers, err = d.MaintainerLogins(); err != nil {
			return err
		}
	}
	if len(groups) > 0 {
		var parts []string
		for _, n := range groups.Names() {
			parts = append(parts, fmt.Sprintf("%s (%d)", n, len(groups[n])))
		}
		cout.Printf("  <gray>groups: %s · maintainers: %s</>\n", strings.Join(parts, ", "), cfg.MaintainerGroup)
	}

	data := explore.Build(in, cfg)
	data.Repo = f.GH.Repo
	data.Since = since.Format("2006-01-02")
	data.ViewFrom = viewFrom(f.Cmd.ViewFrom, since, now)
	data.Version = version.Version

	if f.Cmd.SrcDir != "" {
		if data.Releases, err = releases(f.Cmd.SrcDir, since); err != nil {
			cout.Printf("  <yellow>release markers skipped: %v</>\n", err)
		} else {
			cout.Printf("  <gray>%d release tags since %s for the markers</>\n", len(data.Releases), data.Since)
		}
	}

	switch {
	case !f.Cmd.Explore.Checks:
		data.ChecksNote = "the checks were not run (--checks=false)"
	case f.Cmd.SrcDir == "":
		data.ChecksNote = "the checks need a provider checkout: rerun with --src-dir or PRAWN_SRC_DIR set"
	default:
		cout.Printf("running the close checks for the checks tab...\n")
		o := cli.FlagsReport{WithAI: f.Cmd.Report.WithAI, Limit: f.Cmd.Report.Limit}
		sections, serr := close.NewFlags(f).ReportSections(d, o, now)
		if serr != nil {
			return serr
		}
		data.Checks = checkItems(sections)
		cout.Printf("  <gray>%d close candidates across %d checks</>\n", len(data.Checks), len(sections))
	}

	out := f.Cmd.Explore.Out
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(out), err)
	}
	if err := write(out, data); err != nil {
		return err
	}
	st, _ := os.Stat(out)
	size := ""
	if st != nil {
		size = fmt.Sprintf(" (%.1f MB)", float64(st.Size())/(1<<20))
	}
	cout.Printf("\nwrote <cyan>%s</>%s — <yellow>%d</> PRs since %s\n", out, size, len(data.PRs), data.Since)
	if abs, aerr := filepath.Abs(out); aerr == nil {
		cout.Printf("<gray>open:</> <cyan>file://%s</>\n", abs)
	}
	return nil
}

// viewFrom is the period the page opens on: the flag when set, else January
// 1st of last year, never earlier than the data's start.
func viewFrom(flag string, since, now time.Time) string {
	from := time.Date(now.Year()-1, 1, 1, 0, 0, 0, 0, time.UTC)
	if flag != "" {
		if t, err := time.Parse("2006-01-02", flag); err == nil {
			from = t
		} else {
			cout.Printf("  <yellow>--view-from %q is not yyyy-mm-dd — using the default</>\n", flag)
		}
	}
	if from.Before(since) {
		from = since
	}
	return from.Format("2006-01-02")
}

// checkItems flattens the report sections into the page's check rows.
func checkItems(sections []cli.ReportSection) []explore.CheckItem {
	var out []explore.CheckItem
	for _, s := range sections {
		name := s.Name
		if name == "" {
			name = s.Slug
		}
		name = strings.TrimPrefix(name, "close ")
		for _, it := range s.Items {
			ci := explore.CheckItem{Check: name, Number: it.Number, Score: it.AIScore, Reason: it.AIReason}
			for _, line := range it.Evidence {
				var parts []string
				for _, sp := range line {
					parts = append(parts, sp.Text)
				}
				ci.Evidence = append(ci.Evidence, strings.Join(parts, " "))
			}
			out = append(out, ci)
		}
	}
	return out
}

// releases lists the provider's v* tags since the date, from the checkout.
func releases(srcDir string, since time.Time) ([]explore.Release, error) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", srcDir, "tag", "-l", "v*", "--format=%(refname:short) %(creatordate:unix)") //nolint:gosec // srcDir is the user's own checkout
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git tag in %s: %w", srcDir, err)
	}
	var out []explore.Release
	for line := range strings.SplitSeq(buf.String(), "\n") {
		tag, ts, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		u, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || u < since.Unix() {
			continue
		}
		out = append(out, explore.Release{Tag: tag, Date: u})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// pageData is what the template renders: the styles partial's fields plus
// the data set as a JS literal.
type pageData struct {
	Repo     string
	Since    string
	Count    int
	DataJSON template.JS // our own json, with </ escaped in write
	Script   template.JS // the embedded page script
}

// write renders the page, the whole data set embedded as json so it opens
// from disk.
func write(path string, data *explore.Data) error {
	js, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding explore data: %w", err)
	}
	// the one thing json inside a <script> must never contain
	js = bytes.ReplaceAll(js, []byte("</"), []byte(`<\/`))
	js = bytes.ReplaceAll(js, []byte(" "), []byte(` `))
	js = bytes.ReplaceAll(js, []byte(" "), []byte(` `))

	tmpl, err := template.New("explore").Parse(assets.Styles() + assets.ExploreHTML())
	if err != nil {
		return fmt.Errorf("parsing explore template: %w", err)
	}
	out, err := os.Create(path) //nolint:gosec // G304: user-chosen output path is the point
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() { _ = out.Close() }()

	pd := pageData{Repo: data.Repo, Since: data.Since, Count: len(data.PRs), DataJSON: template.JS(js), Script: template.JS(assets.ExploreJS())} //nolint:gosec // G203: see above
	if err := tmpl.Execute(out, pd); err != nil {
		return fmt.Errorf("rendering explore page: %w", err)
	}
	return nil
}
