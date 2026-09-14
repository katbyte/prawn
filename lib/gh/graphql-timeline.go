// The timeline: every event on a PR — label changes, review requests, draft
// flips, commits, reviews, comments, closes, merges — as GitHub's typed
// timeline items. The explore page derives its stats (ball in court, waiting
// cycles, response times) from these rather than from the PR's current state.

package gh

import (
	"encoding/json"
	"fmt"
	"time"
)

// timelineTypes is every item type the fetch asks for; anything else on the
// timeline (subscriptions, mentions, locks...) says nothing about review flow.
//
//nolint:misspell // github's enum names
const timelineTypes = `LABELED_EVENT, UNLABELED_EVENT, CLOSED_EVENT, REOPENED_EVENT, MERGED_EVENT,
REVIEW_REQUESTED_EVENT, REVIEW_REQUEST_REMOVED_EVENT, READY_FOR_REVIEW_EVENT, CONVERT_TO_DRAFT_EVENT,
MILESTONED_EVENT, DEMILESTONED_EVENT, ASSIGNED_EVENT, UNASSIGNED_EVENT, HEAD_REF_FORCE_PUSHED_EVENT,
RENAMED_TITLE_EVENT, BASE_REF_CHANGED_EVENT, REVIEW_DISMISSED_EVENT, CROSS_REFERENCED_EVENT,
PULL_REQUEST_COMMIT, PULL_REQUEST_REVIEW, ISSUE_COMMENT, HEAD_REF_DELETED_EVENT`

// timelineNodeFields is the per-type selection. Bodies are deliberately
// absent — comments and reviews carry theirs in their own tables for the
// open set; here the who and when are what matter.
const timelineNodeFields = `
__typename
... on Node { id }
... on LabeledEvent { createdAt actor { login } label { name } }
... on UnlabeledEvent { createdAt actor { login } label { name } }
... on ClosedEvent { createdAt actor { login } }
... on ReopenedEvent { createdAt actor { login } }
... on MergedEvent { createdAt actor { login } }
... on ReviewRequestedEvent { createdAt actor { login } requestedReviewer { ... on User { login } ... on Team { name } } }
... on ReviewRequestRemovedEvent { createdAt actor { login } requestedReviewer { ... on User { login } ... on Team { name } } }
... on ReadyForReviewEvent { createdAt actor { login } }
... on ConvertToDraftEvent { createdAt actor { login } }
... on MilestonedEvent { createdAt actor { login } milestoneTitle }
... on DemilestonedEvent { createdAt actor { login } milestoneTitle }
... on AssignedEvent { createdAt actor { login } assignee { ... on User { login } } }
... on UnassignedEvent { createdAt actor { login } assignee { ... on User { login } } }
... on HeadRefForcePushedEvent { createdAt actor { login } afterCommit { oid } }
... on RenamedTitleEvent { createdAt actor { login } previousTitle currentTitle }
... on BaseRefChangedEvent { createdAt actor { login } }
... on ReviewDismissedEvent { createdAt actor { login } review { author { login } } }
... on CrossReferencedEvent { createdAt actor { login } willCloseTarget source { __typename ... on PullRequest { number } ... on Issue { number } } }
... on PullRequestCommit { commit { oid committedDate authoredDate additions deletions messageHeadline author { name user { login } } } }
... on PullRequestReview { createdAt submittedAt author { login } state comments { totalCount } }
... on IssueComment { createdAt author { login } }
... on HeadRefDeletedEvent { createdAt actor { login } }`

// timelineFields selects one page of the timeline. after is the cursor
// variable name to continue from, "" for the first page.
func timelineFields(after string) string {
	cursor := ""
	if after != "" {
		cursor = ", after: $" + after
	}
	return fmt.Sprintf(`timelineItems(first: 100%s, itemTypes: [%s]) {
  pageInfo { endCursor hasNextPage }
  nodes {%s}
}`, cursor, timelineTypes, timelineNodeFields)
}

// typeCommit is the timeline item type that carries a commit.
const typeCommit = "PullRequestCommit"

// TimelinePage is one page of a PR's timeline, nodes still raw so the
// caller keeps the whole node beside the typed view.
type TimelinePage struct {
	PageInfo PageInfo          `json:"pageInfo"`
	Nodes    []json.RawMessage `json:"nodes"`
}

// TimelineNode is the typed view of one timeline item: every type's fields
// live side by side, zero where the type has none.
type TimelineNode struct {
	Typename  string    `json:"__typename"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Actor     Actor     `json:"actor"`
	Author    Actor     `json:"author"` // reviews and comments

	Label struct {
		Name string `json:"name"`
	} `json:"label"`
	MilestoneTitle    string `json:"milestoneTitle"`
	RequestedReviewer struct {
		Login string `json:"login"`
		Name  string `json:"name"` // a team
	} `json:"requestedReviewer"`
	Assignee struct {
		Login string `json:"login"`
	} `json:"assignee"`
	AfterCommit struct {
		Oid string `json:"oid"`
	} `json:"afterCommit"`
	PreviousTitle string `json:"previousTitle"`
	CurrentTitle  string `json:"currentTitle"`
	Review        struct {
		Author Actor `json:"author"`
	} `json:"review"`
	WillCloseTarget bool `json:"willCloseTarget"`
	Source          struct {
		Typename string `json:"__typename"`
		Number   int    `json:"number"`
	} `json:"source"`
	SubmittedAt time.Time `json:"submittedAt"`
	State       string    `json:"state"`
	Comments    struct {
		TotalCount int `json:"totalCount"` // a review's inline comments
	} `json:"comments"`
	Commit struct {
		Oid             string    `json:"oid"`
		CommittedDate   time.Time `json:"committedDate"`
		AuthoredDate    time.Time `json:"authoredDate"`
		Additions       int       `json:"additions"`
		Deletions       int       `json:"deletions"`
		MessageHeadline string    `json:"messageHeadline"`
		Author          struct {
			Name string `json:"name"`
			User Actor  `json:"user"`
		} `json:"author"`
	} `json:"commit"`
}

// ParseTimelineNode decodes one raw timeline node into its typed view.
func ParseTimelineNode(raw json.RawMessage) (TimelineNode, error) {
	var n TimelineNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return n, fmt.Errorf("decoding timeline node: %w", err)
	}
	return n, nil
}

// When returns the item's timestamp: createdAt for events, submittedAt for
// reviews, committedDate for commits.
func (n *TimelineNode) When() time.Time {
	switch {
	case n.Typename == typeCommit:
		return n.Commit.CommittedDate
	case n.Typename == "PullRequestReview" && !n.SubmittedAt.IsZero():
		return n.SubmittedAt
	default:
		return n.CreatedAt
	}
}

// Who returns the login behind the item: the actor for events, the author
// for reviews, comments, and commits.
func (n *TimelineNode) Who() string {
	switch n.Typename {
	case typeCommit:
		return n.Commit.Author.User.Login
	case "PullRequestReview", "IssueComment":
		return n.Author.Login
	default:
		return n.Actor.Login
	}
}

// Subject returns the item's object as one string: the reviewer or team
// requested, the assignee, the commit oid, the referencing #number, the new
// title.
func (n *TimelineNode) Subject() string {
	switch n.Typename {
	case "ReviewRequestedEvent", "ReviewRequestRemovedEvent":
		if n.RequestedReviewer.Login != "" {
			return n.RequestedReviewer.Login
		}
		return "team:" + n.RequestedReviewer.Name
	case "AssignedEvent", "UnassignedEvent":
		return n.Assignee.Login
	case "HeadRefForcePushedEvent":
		return n.AfterCommit.Oid
	case typeCommit:
		return n.Commit.Oid
	case "RenamedTitleEvent":
		return n.CurrentTitle
	case "ReviewDismissedEvent":
		return n.Review.Author.Login
	case "CrossReferencedEvent":
		if n.Source.Number == 0 {
			return ""
		}
		kind := "issue"
		if n.Source.Typename == "PullRequest" {
			kind = "pr"
		}
		return fmt.Sprintf("%s:%d", kind, n.Source.Number)
	default:
		return ""
	}
}

var prTimelineQuery = `
query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {` + timelineFields("cursor") + `}
  }
}`

// PRTimeline fetches one further page of a PR's timeline from cursor — the
// follow-up for PRs whose first page (fetched with the PR) had more.
func (c *Client) PRTimeline(owner, name string, number int, cursor string) (*TimelinePage, RateLimit, error) {
	vars := repoVars(owner, name, cursor)
	vars["number"] = number

	var resp struct {
		RateLimit  RateLimit `json:"rateLimit"`
		Repository struct {
			PullRequest struct {
				TimelineItems TimelinePage `json:"timelineItems"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := c.Do(prTimelineQuery, vars, &resp); err != nil {
		return nil, RateLimit{}, fmt.Errorf("fetching timeline page for #%d: %w", number, err)
	}
	return &resp.Repository.PullRequest.TimelineItems, resp.RateLimit, nil
}
