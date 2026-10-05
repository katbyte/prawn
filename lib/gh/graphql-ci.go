package gh

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ciBatch is how many PRs one aliased query asks about: enough to keep a
// fetch to a few dozen requests, few enough that a page stays quick.
const (
	failingChecksBatch = 10
	baseDistanceBatch  = 40
)

// helperCheck matches the jobs a workflow runs about another job's result —
// "unit-tests-on-fail / comment-failure", the ones that save a job's output —
// which fail or skip with it and say nothing of their own.
var helperCheck = regexp.MustCompile(`-(on-fail|on-success|save-artifacts)( / .*)?$`) //nolint:misspell // the jobs' own spelling

// failedConclusion is every check-run conclusion and status-context state
// that turns the head commit's rollup red.
var failedConclusion = map[string]bool{
	"FAILURE": true, "TIMED_OUT": true, "STARTUP_FAILURE": true, "ACTION_REQUIRED": true, "CANCELLED": true, "ERROR": true,
}

// FailingChecks names the checks failing on each PR's head commit, sorted,
// helper jobs left out. It asks only about the PRs given — the ones whose
// rollup is red — as a page of every PR's forty-odd checks is most of a
// fetch's budget for a handful of answers.
func (c *Client) FailingChecks(owner, name string, numbers []int) (map[int][]string, error) {
	out := map[int][]string{}
	for batch := range slices.Chunk(numbers, failingChecksBatch) {
		var q strings.Builder
		q.WriteString("query($owner: String!, $name: String!) {\n  rateLimit { cost remaining resetAt }\n  repository(owner: $owner, name: $name) {\n")
		for _, n := range batch {
			fmt.Fprintf(&q, `    p%d: pullRequest(number: %d) { commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
      __typename ... on CheckRun { name conclusion } ... on StatusContext { context state } } } } } } } }
`, n, n)
		}
		q.WriteString("  }\n}")

		var resp struct {
			RateLimit  RateLimit `json:"rateLimit"`
			Repository map[string]struct {
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup struct {
								Contexts struct {
									Nodes []struct {
										Name       string `json:"name"`
										Conclusion string `json:"conclusion"`
										Context    string `json:"context"`
										State      string `json:"state"`
									} `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"repository"`
		}
		if err := c.DoTolerant(q.String(), repoVars(owner, name, ""), &resp); err != nil {
			return nil, fmt.Errorf("fetching failing checks: %w", err)
		}
		for _, n := range batch {
			pr := resp.Repository[fmt.Sprintf("p%d", n)]
			if len(pr.Commits.Nodes) == 0 {
				continue
			}
			seen := map[string]bool{}
			for _, ctx := range pr.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts.Nodes {
				check, result := ctx.Name, ctx.Conclusion
				if check == "" {
					check, result = ctx.Context, ctx.State // a commit status, not a check run
				}
				if check == "" || !failedConclusion[result] || helperCheck.MatchString(check) || seen[check] {
					continue
				}
				seen[check] = true
				out[n] = append(out[n], check)
			}
			slices.Sort(out[n])
		}
		resp.RateLimit.WaitIfLow()
	}
	return out, nil
}

// BaseDistance is how far a PR's branch and its CI result have fallen
// behind the branch it merges into.
type BaseDistance struct {
	Behind int // commits on the base branch the PR's branch lacks
	Ahead  int // commits on the PR's branch the base lacks
	Drift  int // commits the base branch has had since the PR's CI last ran, -1 when it never did
}

// BaseDistances measures each PR against base (a branch name): how many
// commits behind and ahead its head is, and — for the PRs given a time their
// CI last ran — how many commits base has had since. A PR whose head GitHub
// cannot compare is left out.
func (c *Client) BaseDistances(owner, name, base string, ciRanAt map[int]time.Time) (map[int]BaseDistance, error) {
	numbers := make([]int, 0, len(ciRanAt))
	for n := range ciRanAt {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)

	out := map[int]BaseDistance{}
	for batch := range slices.Chunk(numbers, baseDistanceBatch) {
		var compares, histories strings.Builder
		for _, n := range batch {
			fmt.Fprintf(&compares, "      c%d: compare(headRef: \"refs/pull/%d/head\") { aheadBy behindBy }\n", n, n)
			if at := ciRanAt[n]; !at.IsZero() {
				fmt.Fprintf(&histories, "        h%d: history(since: %q) { totalCount }\n", n, at.UTC().Format(time.RFC3339))
			}
		}
		target := ""
		if histories.Len() > 0 {
			target = "      target { ... on Commit {\n" + histories.String() + "      } }\n"
		}
		q := "query($owner: String!, $name: String!, $ref: String!) {\n  rateLimit { cost remaining resetAt }\n  repository(owner: $owner, name: $name) {\n    ref(qualifiedName: $ref) {\n" +
			compares.String() + target + "    }\n  }\n}"

		var resp struct {
			RateLimit  RateLimit `json:"rateLimit"`
			Repository struct {
				Ref map[string]json.RawMessage `json:"ref"`
			} `json:"repository"`
		}
		vars := repoVars(owner, name, "")
		vars["ref"] = "refs/heads/" + base
		if err := c.DoTolerant(q, vars, &resp); err != nil {
			return nil, fmt.Errorf("measuring PRs against %s: %w", base, err)
		}
		var since map[string]struct {
			TotalCount int `json:"totalCount"`
		}
		if raw, ok := resp.Repository.Ref["target"]; ok {
			if err := json.Unmarshal(raw, &since); err != nil {
				return nil, fmt.Errorf("decoding %s's history counts: %w", base, err)
			}
		}
		for _, n := range batch {
			var cmp *struct {
				AheadBy  int `json:"aheadBy"`
				BehindBy int `json:"behindBy"`
			}
			if raw, ok := resp.Repository.Ref[fmt.Sprintf("c%d", n)]; ok {
				_ = json.Unmarshal(raw, &cmp) // null for a head github cannot compare
			}
			if cmp == nil {
				continue
			}
			d := BaseDistance{Behind: cmp.BehindBy, Ahead: cmp.AheadBy, Drift: -1}
			if h, ok := since[fmt.Sprintf("h%d", n)]; ok {
				d.Drift = h.TotalCount
			}
			out[n] = d
		}
		resp.RateLimit.WaitIfLow()
	}
	return out, nil
}
