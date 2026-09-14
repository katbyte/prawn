// The PR walks: every open pull request with its comments, reviews, changed
// files, and the issues it says it closes — the evidence every check reads.

package gh

import (
	"fmt"
	"time"
)

type PRNode struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	State             string    `json:"state"` // OPEN | CLOSED | MERGED
	IsDraft           bool      `json:"isDraft"`
	Author            Actor     `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	ClosedAt          time.Time `json:"closedAt"`
	MergedAt          time.Time `json:"mergedAt"`
	URL               string    `json:"url"`

	Mergeable      string `json:"mergeable"`      // MERGEABLE | CONFLICTING | UNKNOWN
	ReviewDecision string `json:"reviewDecision"` // APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | ""

	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`

	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`

	Files struct {
		Nodes []struct {
			Path string `json:"path"`
		} `json:"nodes"`
	} `json:"files"`

	Thumbs struct {
		TotalCount int `json:"totalCount"`
	} `json:"thumbs"`

	Comments struct {
		TotalCount int           `json:"totalCount"`
		Nodes      []CommentNode `json:"nodes"`
	} `json:"comments"`

	Reviews struct {
		Nodes []ReviewNode `json:"nodes"`
	} `json:"reviews"`

	Commits struct {
		Nodes []struct {
			Commit struct {
				CommittedDate     time.Time `json:"committedDate"`
				StatusCheckRollup struct {
					State string `json:"state"` // SUCCESS | FAILURE | ERROR | PENDING | EXPECTED, "" when the commit has no checks
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`

	ClosingIssuesReferences struct {
		Nodes []LinkedIssueNode `json:"nodes"`
	} `json:"closingIssuesReferences"`

	MergedBy  Actor `json:"mergedBy"`
	Milestone struct {
		Title string `json:"title"`
	} `json:"milestone"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`

	// the first page of the timeline; PageInfo says whether more follow
	TimelineItems TimelinePage `json:"timelineItems"`
}

type CommentNode struct {
	ID                string    `json:"id"`
	URL               string    `json:"url"`
	Author            Actor     `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	CreatedAt         time.Time `json:"createdAt"`
	Body              string    `json:"body"`
}

type ReviewNode struct {
	ID                string    `json:"id"`
	URL               string    `json:"url"`
	Author            Actor     `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	State             string    `json:"state"` // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED | PENDING
	SubmittedAt       time.Time `json:"submittedAt"`
	Body              string    `json:"body"`
}

// LinkedIssueNode is one issue a PR's closing keywords reference, with enough
// of the issue (state, reason, labels) for the fixed and label checks to work
// without a second fetch.
type LinkedIssueNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"` // OPEN | CLOSED
	StateReason string `json:"stateReason"`
	Labels      struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

// prFields is the shared selection for a PR node. Comments, reviews, files,
// linked issues, and the first page of the timeline come along in the same
// request — on an old PR the review thread is where the story is. Pages are
// small (10) because a page that takes GitHub over ~10s to assemble comes
// back as a gateway 502, and old open PRs carry long timelines.
var prFields = `
number title body state isDraft
author { login }
authorAssociation
createdAt updatedAt closedAt mergedAt url
mergeable reviewDecision
additions deletions changedFiles
mergedBy { login } milestone { title } baseRefName headRefName
labels(first: 30) { nodes { name } }
files(first: 100) { nodes { path } }
thumbs: reactions(content: THUMBS_UP) { totalCount }
comments(first: 50) {
  totalCount
  nodes { id url author { login } authorAssociation createdAt body }
}
reviews(first: 30) {
  nodes { id url author { login } authorAssociation state submittedAt body }
}
commits(last: 1) { nodes { commit { committedDate statusCheckRollup { state } } } }
closingIssuesReferences(first: 10) {
  nodes { number title state stateReason labels(first: 10) { nodes { name } } }
}
` + timelineFields("")

// prFieldsLite is the selection for the closed and merged PRs the explore
// backfill walks: no comment bodies (the timeline carries who commented and
// when, which is what the stats read), and smaller connections — the
// point cost of a page is the sum of its connection sizes, and the backfill
// is thousands of PRs.
var prFieldsLite = `
number title body state isDraft
author { login }
authorAssociation
createdAt updatedAt closedAt mergedAt url
mergeable reviewDecision
additions deletions changedFiles
mergedBy { login } milestone { title } baseRefName headRefName
labels(first: 20) { nodes { name } }
files(first: 60) { nodes { path } }
thumbs: reactions(content: THUMBS_UP) { totalCount }
comments(first: 1) {
  totalCount
  nodes { id url author { login } authorAssociation createdAt body }
}
reviews(first: 20) {
  nodes { id url author { login } authorAssociation state submittedAt body }
}
commits(last: 1) { nodes { commit { committedDate statusCheckRollup { state } } } }
closingIssuesReferences(first: 5) {
  nodes { number title state stateReason labels(first: 10) { nodes { name } } }
}
` + timelineFields("")

var openPRsQuery = `
query($owner: String!, $name: String!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  repository(owner: $owner, name: $name) {
    pullRequests(first: 10, after: $cursor, states: [OPEN], orderBy: { field: CREATED_AT, direction: ASC }) {
      totalCount
      pageInfo { endCursor hasNextPage }
      nodes {` + prFields + `}
    }
  }
}`

// OpenPRsPage is one page of the full walk over open pull requests.
type OpenPRsPage struct {
	PRs        []PRNode
	PageInfo   PageInfo
	TotalCount int
	RateLimit  RateLimit
}

// OpenPRs fetches one page of open PRs, oldest first, from cursor ("" = start).
func (c *Client) OpenPRs(owner, name, cursor string) (*OpenPRsPage, error) {
	vars := repoVars(owner, name, cursor)

	var resp struct {
		RateLimit  RateLimit `json:"rateLimit"`
		Repository struct {
			PullRequests struct {
				TotalCount int      `json:"totalCount"`
				PageInfo   PageInfo `json:"pageInfo"`
				Nodes      []PRNode `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := c.DoTolerant(openPRsQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("fetching PRs page: %w", err)
	}

	return &OpenPRsPage{
		PRs:        resp.Repository.PullRequests.Nodes,
		PageInfo:   resp.Repository.PullRequests.PageInfo,
		TotalCount: resp.Repository.PullRequests.TotalCount,
		RateLimit:  resp.RateLimit,
	}, nil
}

const openPRNumbersQuery = `
query($owner: String!, $name: String!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  repository(owner: $owner, name: $name) {
    pullRequests(first: 100, after: $cursor, states: [OPEN]) {
      totalCount
      pageInfo { endCursor hasNextPage }
      nodes { number mergeable commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } }
    }
  }
}`

// OpenPRStatus is what the open-set walk carries per PR besides its number:
// the two things that change without the PR's updatedAt moving, so an
// incremental sync would never refresh them.
type OpenPRStatus struct {
	Mergeable  string // MERGEABLE | CONFLICTING | UNKNOWN
	CheckState string // the head commit's combined CI state, "" without checks
}

// OpenPRNumbers pages every open PR's number, mergeability, and CI state. The
// repository connection is ground truth — unlike search, whose index lags —
// so this is what the local open set reconciles against. progress (nil ok)
// is called after every page with the running and total counts.
func (c *Client) OpenPRNumbers(owner, name string, progress func(fetched, total int)) (map[int]OpenPRStatus, error) {
	open := map[int]OpenPRStatus{}
	cursor := ""
	for {
		var resp struct {
			RateLimit  RateLimit `json:"rateLimit"`
			Repository struct {
				PullRequests struct {
					TotalCount int      `json:"totalCount"`
					PageInfo   PageInfo `json:"pageInfo"`
					Nodes      []struct {
						Number    int    `json:"number"`
						Mergeable string `json:"mergeable"`
						Commits   struct {
							Nodes []struct {
								Commit struct {
									StatusCheckRollup struct {
										State string `json:"state"`
									} `json:"statusCheckRollup"`
								} `json:"commit"`
							} `json:"nodes"`
						} `json:"commits"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		}
		if err := c.Do(openPRNumbersQuery, repoVars(owner, name, cursor), &resp); err != nil {
			return nil, fmt.Errorf("fetching open PR numbers: %w", err)
		}
		for _, n := range resp.Repository.PullRequests.Nodes {
			st := OpenPRStatus{Mergeable: n.Mergeable}
			if len(n.Commits.Nodes) > 0 {
				st.CheckState = n.Commits.Nodes[0].Commit.StatusCheckRollup.State
			}
			open[n.Number] = st
		}
		if progress != nil {
			progress(len(open), resp.Repository.PullRequests.TotalCount)
		}
		resp.RateLimit.WaitIfLow()
		if !resp.Repository.PullRequests.PageInfo.HasNextPage {
			return open, nil
		}
		cursor = resp.Repository.PullRequests.PageInfo.EndCursor
	}
}

var updatedPRsQuery = `
query($query: String!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  search(query: $query, type: ISSUE, first: 10, after: $cursor) {
    issueCount
    pageInfo { endCursor hasNextPage }
    nodes { ... on PullRequest {` + prFields + `} }
  }
}`

// UpdatedPRsPage is one page of an incremental sync search.
type UpdatedPRsPage struct {
	PRs       []PRNode
	PageInfo  PageInfo
	PRCount   int
	RateLimit RateLimit
}

// UpdatedPRs fetches one page of PRs updated since the given time (any state).
// The search API caps results at 1000; the caller falls back to a full walk beyond that.
func (c *Client) UpdatedPRs(owner, name string, since time.Time, cursor string) (*UpdatedPRsPage, error) {
	q := fmt.Sprintf("repo:%s/%s is:pr updated:>%s sort:updated-asc", owner, name, since.UTC().Format(time.RFC3339))
	vars := searchVars(q, cursor)

	var resp struct {
		RateLimit RateLimit `json:"rateLimit"`
		Search    struct {
			IssueCount int      `json:"issueCount"`
			PageInfo   PageInfo `json:"pageInfo"`
			Nodes      []PRNode `json:"nodes"`
		} `json:"search"`
	}
	if err := c.DoTolerant(updatedPRsQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("fetching updated PRs page: %w", err)
	}

	return &UpdatedPRsPage{
		PRs:       resp.Search.Nodes,
		PageInfo:  resp.Search.PageInfo,
		PRCount:   resp.Search.IssueCount,
		RateLimit: resp.RateLimit,
	}, nil
}

var closedPRsQuery = `
query($query: String!, $cursor: String, $first: Int!) {
  rateLimit { cost remaining resetAt }
  search(query: $query, type: ISSUE, first: $first, after: $cursor) {
    issueCount
    pageInfo { endCursor hasNextPage }
    nodes { ... on PullRequest {` + prFieldsLite + `} }
  }
}`

// ClosedPRsPageSize is the backfill's normal page; a page GitHub's gateway
// keeps timing out on (a PR with an enormous timeline in it) is retried at
// ClosedPRsSmallPage.
const (
	ClosedPRsPageSize  = 20
	ClosedPRsSmallPage = 4
)

// ClosedPRs fetches one page of the PRs closed (or merged) in [from, to],
// oldest close first, with the lighter selection. The search API caps a
// query at 1000 results; the caller windows the range so no window exceeds it.
func (c *Client) ClosedPRs(owner, name string, from, to time.Time, cursor string, pageSize int) (*UpdatedPRsPage, error) {
	q := fmt.Sprintf("repo:%s/%s is:pr closed:%s..%s sort:updated-asc", owner, name,
		from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"))
	vars := searchVars(q, cursor)
	vars["first"] = pageSize

	var resp struct {
		RateLimit RateLimit `json:"rateLimit"`
		Search    struct {
			IssueCount int      `json:"issueCount"`
			PageInfo   PageInfo `json:"pageInfo"`
			Nodes      []PRNode `json:"nodes"`
		} `json:"search"`
	}
	if err := c.DoTolerant(closedPRsQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("fetching closed PRs page: %w", err)
	}

	return &UpdatedPRsPage{
		PRs:       resp.Search.Nodes,
		PageInfo:  resp.Search.PageInfo,
		PRCount:   resp.Search.IssueCount,
		RateLimit: resp.RateLimit,
	}, nil
}

// ClosedPRCount returns how many PRs closed in [from, to] — the windowing
// probe, one cheap request.
func (c *Client) ClosedPRCount(owner, name string, from, to time.Time) (int, error) {
	q := fmt.Sprintf("repo:%s/%s is:pr closed:%s..%s", owner, name,
		from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"))
	var resp struct {
		Search struct {
			IssueCount int `json:"issueCount"`
		} `json:"search"`
	}
	if err := c.Do(`query($query: String!) { search(query: $query, type: ISSUE, first: 1) { issueCount } }`,
		map[string]any{varQuery: q}, &resp); err != nil {
		return 0, fmt.Errorf("counting closed PRs: %w", err)
	}
	return resp.Search.IssueCount, nil
}
