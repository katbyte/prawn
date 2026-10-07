package explore

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

// TestRun is the latest /test on a PR — who asked, when, and for which
// services — and the latest results the bot posted back.
type TestRun struct {
	By       string      `json:"by,omitempty"`
	At       int64       `json:"at,omitempty"`
	URL      string      `json:"u,omitempty"`
	Services []string    `json:"sv,omitempty"`
	Since    *int        `json:"cs,omitempty"`   // commits pushed since the /test, when the PR's commits are known
	Failed   string      `json:"fail,omitempty"` // why it started nothing, in the workflow's words, when it said so
	FailURL  string      `json:"fu,omitempty"`
	Result   *TestResult `json:"r,omitempty"`
}

// TestResult is one results comment: the build and what it came to.
type TestResult struct {
	At       int64  `json:"at"`
	URL      string `json:"u"`
	Build    int    `json:"b,omitempty"`
	Total    int    `json:"t"`
	Passed   int    `json:"p"`
	Failed   int    `json:"f"`
	Skipped  int    `json:"s"`
	NewFail  bool   `json:"nf,omitempty"` // the bot said one or more tests newly failed
	Duration string `json:"d,omitempty"`
}

const testResultsMarker = "<!-- teamcity-test-results -->"

var (
	testCmd       = regexp.MustCompile(`^/test\b`)
	testService   = regexp.MustCompile("(?m)^- `([a-z0-9]+)`:")
	resultBuild   = regexp.MustCompile(`Build: \[(\d+)\]`)
	resultField   = regexp.MustCompile(`\*\*(Total|Passed|Failed|Skipped|Test Duration):\*\*\s*([^\n]+)`)
	resultNewFail = regexp.MustCompile(`(?i)newly failed`)
)

// testRun reads a PR's /test comments and the bot's results, oldest first,
// into the latest of each. nil when there is neither.
func testRun(comments []db.Comment, commits []db.Commit) *TestRun {
	var run *TestRun
	var res *TestResult
	for _, c := range comments {
		body := strings.TrimSpace(c.Body)
		switch {
		case testCmd.MatchString(body):
			run = &TestRun{By: c.Author, At: c.CreatedAt.Unix(), URL: c.URL}
			for _, m := range testService.FindAllStringSubmatch(body, -1) {
				run.Services = append(run.Services, m[1])
			}
		case strings.Contains(body, testResultsMarker):
			res = testResult(c)
		case strings.HasPrefix(body, "❌") && run != nil:
			// the workflow's word that the latest /test started nothing: its first line, the ❌ off
			run.Failed = strings.TrimSpace(strings.TrimPrefix(strings.SplitN(body, "\n", 2)[0], "❌"))
			run.FailURL = c.URL
		}
	}
	if run == nil && res == nil {
		return nil
	}
	if run == nil {
		run = &TestRun{}
	}
	run.Result = res
	if run.At != 0 && len(commits) > 0 {
		n := 0
		for _, c := range commits {
			if c.CommittedAt.After(time.Unix(run.At, 0)) {
				n++
			}
		}
		run.Since = &n
	}
	return run
}

func testResult(c db.Comment) *TestResult {
	r := &TestResult{At: c.CreatedAt.Unix(), URL: c.URL, NewFail: resultNewFail.MatchString(c.Body)}
	if m := resultBuild.FindStringSubmatch(c.Body); m != nil {
		r.Build, _ = strconv.Atoi(m[1])
	}
	for _, m := range resultField.FindAllStringSubmatch(c.Body, -1) {
		v := strings.TrimSpace(m[2])
		n, _ := strconv.Atoi(v)
		switch m[1] {
		case "Total":
			r.Total = n
		case "Passed":
			r.Passed = n
		case "Failed":
			r.Failed = n
		case "Skipped":
			r.Skipped = n
		case "Test Duration":
			r.Duration = v
		}
	}
	return r
}
