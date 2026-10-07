// Package explore is prawn explore: the interactive page over every PR that
// was open at any point in the period — a global filter, then tabs: the
// queue to work, the trends, the people, the areas, and the checks' live
// candidates. One self-contained html file, no server, state in the url.
package explore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	"github.com/katbyte/prawn/lib/pr"
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
		PreRunE:       cli.ValidateParams([]string{cli.ParamRepo, "db"}), // no token: the page from the db as it is
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
	c.Flags().String("admins", "", "with --serve: the logins (comma-separated) that may refresh the page and upload a database, as the login proxy in front names them — or set PRAWN_ADMINS; empty lets anyone")
	c.Flags().String("serve", "", "after writing, serve the page on this address until ctrl-c (a port like 8765, or host:port) so other machines on the network can open it")
	return c
}

func run(f *cli.FlagData) error {
	// without a token there is nothing to sync with: the page is built and served from the
	// database as it stands (a copy brought from elsewhere), and refresh only rebuilds it
	if f.GH.Token == "" && !f.NoAutoFetch {
		cout.Printf("<fg=208>no GITHUB_TOKEN:</> using the database as it is, without syncing\n")
		f.NoAutoFetch = true
	}
	if f.Cmd.Explore.Serve == "" {
		return build(f, syncIfStale)
	}
	// serving: the page goes up at once from what the db holds, and the sync a plain run would do
	// first happens behind it as the first refresh — a slow or failing github delays fresh numbers,
	// not the page. The refresh button syncs whatever the db's age.
	if err := build(f, syncNever); err != nil {
		return err
	}
	return serve(f.Cmd.Explore.Out, f.Cmd.Explore.Serve, f.DBPath, newAdmins(f.Cmd.Explore.Admins),
		func() error { return build(f, syncAlways) },
		func() error { return build(f, syncIfStale) },
		func() error { return build(f, syncNever) })
}

// when build syncs the database before writing the page
const (
	syncIfStale = iota // when the last sync is over an hour old: a plain run
	syncAlways         // regardless: the refresh button
	syncNever          // not at all: the page from what is there
)

// build syncs the database as sync says (never with --no-auto-fetch) and
// writes the page.
func build(f *cli.FlagData, sync int) error {
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
	switch {
	case f.NoAutoFetch || sync == syncNever: // offline, or asked not to: the db as it is
	case sync == syncAlways:
		if err := f.Fetch(false); err != nil {
			return err
		}
	default:
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
	if in.Diffs, err = d.AllDiffs(); err != nil {
		return err
	}
	if in.CI, err = d.AllCI(); err != nil {
		return err
	}
	if in.Tests, err = d.AllTCBuilds(); err != nil {
		return err
	}
	if in.TestCmds, err = d.TestComments(); err != nil {
		return err
	}
	withEvents := 0
	for _, p := range in.PRs {
		if len(in.Events[p.Number]) > 0 {
			withEvents++
		}
	}
	cout.Printf("  <yellow>%d</><gray> PRs in the period, </><yellow>%d</><gray> with timelines</>\n", len(in.PRs), withEvents)
	if withEvents < len(in.PRs) {
		cout.Printf("  <fg=208>%d PRs have no timeline yet — run</> <cyan>prawn fetch --full --since %s</> <fg=208>to fetch them</>\n",
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
	if t, serr := cli.LastSync(d); serr == nil && !t.IsZero() {
		data.Synced = t.Unix()
	}
	data.NoSync = f.NoAutoFetch

	// the page goes up without the checkout rather than not at all: its markers and checks tab need it, nothing
	// else. The build a server starts with does not clone (minutes, before anyone can open the page); the
	// sync that follows it does
	srcDir := f.Cmd.SrcDir
	if _, err := os.Stat(filepath.Join(srcDir, ".git")); srcDir != "" && err != nil && sync == syncNever {
		cout.Printf("  <gray>no provider checkout at %s yet: the sync after this clones it</>\n", srcDir)
		srcDir = ""
	} else if err := f.EnsureSrcDir(); err != nil {
		cout.Printf("  <fg=208>provider checkout skipped: %v</>\n", err)
		srcDir = ""
	}
	if srcDir != "" {
		if data.Releases, err = releases(srcDir, since); err != nil {
			cout.Printf("  <fg=208>release markers skipped: %v</>\n", err)
		} else {
			cout.Printf("  <yellow>%d</><gray> release tags since %s for the markers</>\n", len(data.Releases), data.Since)
		}
	}

	switch {
	case !f.Cmd.Explore.Checks:
		data.ChecksNote = "the checks were not run (--checks=false)"
	case srcDir == "" && f.Cmd.SrcDir != "":
		data.ChecksNote = "the provider checkout is not ready yet: the checks run on the next build once it is"
	case srcDir == "":
		data.ChecksNote = "the checks need a provider checkout: rerun with --src-dir or PRAWN_SRC_DIR set"
	default:
		cout.Printf("running the close checks for the checks tab...\n")
		o := cli.FlagsReport{WithAI: f.Cmd.Report.WithAI, Limit: f.Cmd.Report.Limit}
		// a check that fails costs the checks tab, not the page: the rest of it is built and served either way
		sections, serr := close.NewFlags(f).ReportSections(d, o, now)
		if serr != nil {
			cout.Printf("  <fg=208>the checks failed, so the checks tab is empty: %v</>\n", serr)
			data.ChecksNote = "the checks failed when this page was built: " + serr.Error()
		} else {
			data.Checks, data.CheckSections = checkItems(sections)
			cout.Printf("  <yellow>%d</><gray> close candidates across </><yellow>%d</><gray> checks</>\n", len(data.Checks), len(sections))
		}
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
			cout.Printf("  <fg=208>--view-from %q is not yyyy-mm-dd — using the default</>\n", flag)
		}
	}
	if from.Before(since) {
		from = since
	}
	return from.Format("2006-01-02")
}

// checkItems flattens the report sections into the page's check rows.
func checkItems(sections []cli.ReportSection) ([]explore.CheckItem, []explore.CheckSection) {
	var out []explore.CheckItem
	heads := make([]explore.CheckSection, 0, len(sections))
	for _, s := range sections {
		name := s.Name
		if name == "" {
			name = s.Slug
		}
		name = strings.TrimPrefix(name, "close ")
		head := explore.CheckSection{Check: name, Question: s.Question, Command: s.Command, Total: s.Total}
		for _, c := range s.Classes {
			if c.Count > 0 {
				head.Classes = append(head.Classes, explore.CheckClass{Name: c.Name, Kind: c.Kind, Count: c.Count})
			}
		}
		heads = append(heads, head)
		for _, it := range s.Items {
			ci := explore.CheckItem{Check: name, Number: it.Number, Meta: it.Meta, Score: it.AIScore, ScoreOf: it.AIKind, Reason: it.AIReason}
			for _, line := range it.Evidence {
				bits := make([]explore.CheckBit, 0, len(line))
				for _, sp := range line {
					bits = append(bits, explore.CheckBit{Text: sp.Text, URL: sp.URL, Kind: sp.Kind})
				}
				ci.Evidence = append(ci.Evidence, bits)
			}
			out = append(out, ci)
		}
	}
	return out, heads
}

// releases lists the provider's v* tags since the date, from the checkout.
func releases(srcDir string, since time.Time) ([]explore.Release, error) {
	cmd := exec.CommandContext(context.Background(), "git", "-C", srcDir, "tag", "-l", "v*", "--format=%(refname:short) %(creatordate:unix)") //nolint:gosec // srcDir is the user's own checkout
	tags, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git tag in %s: %s%w", srcDir, pr.GitSaid(err), err)
	}
	var out []explore.Release
	for line := range strings.SplitSeq(string(tags), "\n") {
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
	slices.SortFunc(out, func(a, b explore.Release) int { return cmp.Compare(a.Date, b.Date) })
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
	// rendered beside the page under a name of its own and swapped in whole,
	// so a page being served (--serve, or the container's cron refresh) is
	// never read half-written, and two writers never share a draft
	out, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	tmp := out.Name()
	if err := tmpl.Execute(out, pageData{Repo: data.Repo, Since: data.Since, Count: len(data.PRs), DataJSON: template.JS(js), Script: template.JS(assets.ExploreJS())}); err != nil { //nolint:gosec // G203: see above
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("rendering explore page: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
