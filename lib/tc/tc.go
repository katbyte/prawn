// Package tc reads acceptance test results from TeamCity: the builds a
// project ran on pull request branches, and the tests that failed in one.
// Read-only — prawn looks results up and never starts a build.
package tc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Client talks to one TeamCity server with an access token.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for server — a host name or a url — and token.
func New(server, token string) *Client {
	base := strings.TrimRight(strings.TrimSpace(server), "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return &Client{base: base, token: token, http: &http.Client{Timeout: 2 * time.Minute}}
}

// Build is one TeamCity build on a pull request's branch.
type Build struct {
	ID         int
	PR         int
	Branch     string // refs/pull/N/merge, or …/head
	BuildType  string // the build configuration's id: one per service
	State      string // queued | running | finished
	Status     string // SUCCESS | FAILURE | UNKNOWN (cancelled, or not yet decided)
	StatusText string // TeamCity's one-line summary: "Tests failed: 4 (4 new), passed: 38"
	Started    time.Time
	Finished   time.Time
	Passed     int
	Failed     int
	Ignored    int
	Revision   string // the commit it built: the merge commit for a …/merge branch
	URL        string
}

// timeLayout is how TeamCity writes times: 20261005T225212+0000.
const timeLayout = "20060102T150405-0700"

// prBranch reads the pull request number off a branch name.
var prBranch = regexp.MustCompile(`^(?:refs/)?pull/(\d+)(?:/(?:merge|head))?$`)

const buildFields = "nextHref,build(id,status,state,branchName,buildTypeId,startDate,finishDate,statusText,webUrl," +
	"testOccurrences(passed,failed,ignored),revisions(revision(version)))"

// pageSize is how many builds one request asks for.
const pageSize = 100

// PRBuilds returns the builds project ran on pull request branches, newest
// first: every one TeamCity still keeps, or only those started since a time
// when since is not zero. Queued, running, cancelled and failed-to-start
// builds are included — a PR's last run is its last run, however it ended.
// progress (nil ok) is called after every page with the running count.
func (c *Client) PRBuilds(ctx context.Context, project string, since time.Time, progress func(fetched int)) ([]Build, error) {
	locator := fmt.Sprintf("affectedProject:(id:%s),branch:(default:false),state:any,canceled:any,failedToStart:any,count:%d", project, pageSize)
	if !since.IsZero() {
		locator += ",sinceDate:" + since.UTC().Format(timeLayout)
	}
	next := "/app/rest/builds?" + url.Values{"locator": {locator}, "fields": {buildFields}}.Encode()

	var out []Build
	for next != "" {
		var page struct {
			NextHref string `json:"nextHref"`
			Build    []struct {
				ID              int    `json:"id"`
				Status          string `json:"status"`
				State           string `json:"state"`
				BranchName      string `json:"branchName"`
				BuildTypeID     string `json:"buildTypeId"`
				StartDate       string `json:"startDate"`
				FinishDate      string `json:"finishDate"`
				StatusText      string `json:"statusText"`
				WebURL          string `json:"webUrl"`
				TestOccurrences struct {
					Passed  int `json:"passed"`
					Failed  int `json:"failed"`
					Ignored int `json:"ignored"`
				} `json:"testOccurrences"`
				Revisions struct {
					Revision []struct {
						Version string `json:"version"`
					} `json:"revision"`
				} `json:"revisions"`
			} `json:"build"`
		}
		if err := c.get(ctx, next, &page); err != nil {
			return nil, fmt.Errorf("listing %s's pull request builds: %w", project, err)
		}
		for i := range page.Build {
			b := &page.Build[i]
			m := prBranch.FindStringSubmatch(b.BranchName)
			if m == nil {
				continue // a feature branch someone tested, not a pull request
			}
			pr, _ := strconv.Atoi(m[1])
			build := Build{
				ID: b.ID, PR: pr, Branch: b.BranchName, BuildType: b.BuildTypeID, State: b.State, Status: b.Status,
				StatusText: b.StatusText, URL: b.WebURL,
				Passed: b.TestOccurrences.Passed, Failed: b.TestOccurrences.Failed, Ignored: b.TestOccurrences.Ignored,
			}
			build.Started, _ = time.Parse(timeLayout, b.StartDate)   // unset while queued
			build.Finished, _ = time.Parse(timeLayout, b.FinishDate) // unset until it ends
			if len(b.Revisions.Revision) > 0 {
				build.Revision = b.Revisions.Revision[0].Version
			}
			out = append(out, build)
		}
		if progress != nil {
			progress(len(out))
		}
		next = page.NextHref
	}
	return out, nil
}

// FailedTests names the tests that failed in a build, up to limit of them.
func (c *Client) FailedTests(ctx context.Context, buildID, limit int) ([]string, error) {
	q := url.Values{
		"locator": {fmt.Sprintf("build:(id:%d),status:FAILURE,count:%d", buildID, limit)},
		"fields":  {"testOccurrence(name)"},
	}
	var page struct {
		TestOccurrence []struct {
			Name string `json:"name"`
		} `json:"testOccurrence"`
	}
	if err := c.get(ctx, "/app/rest/testOccurrences?"+q.Encode(), &page); err != nil {
		return nil, fmt.Errorf("listing the failed tests of build %d: %w", buildID, err)
	}
	names := make([]string, 0, len(page.TestOccurrence))
	for _, t := range page.TestOccurrence {
		names = append(names, TestName(t.Name))
	}
	return names, nil
}

// TestName shortens TeamCity's test name to the test itself: it prefixes the
// suite and package ("…/internal/services/network: TestAccSubnet_basic").
func TestName(full string) string {
	if i := strings.LastIndex(full, ": "); i >= 0 {
		full = full[i+2:]
	}
	if i := strings.LastIndexAny(full, " /"); i >= 0 && strings.HasPrefix(full[i+1:], "Test") {
		full = full[i+1:]
	}
	return full
}

// get fetches a path (with its query) off the server and decodes the json.
func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reaching teamcity: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("reading teamcity's answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("teamcity refused the token (%d): check TC_TOKEN and that it can view the project", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("teamcity returned %d: %.200s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding teamcity's answer: %w", err)
	}
	return nil
}
