package pr

import (
	"regexp"
	"strings"

	"github.com/katbyte/prawn/lib/db"
)

// rePRBoilerplate strips the PR-template boilerplate sections (community
// note, checklist headings) plus HTML comments, before text is shown or sent
// to the AI.
var (
	reCommunityNote = regexp.MustCompile(`(?is)#+\s*community note.*?(\n#|\z)`)
	reHTMLComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlankRuns     = regexp.MustCompile(`\n{3,}`)
)

// CleanBody removes boilerplate and collapses whitespace runs.
func CleanBody(s string) string {
	s = reHTMLComment.ReplaceAllString(s, "")
	s = reCommunityNote.ReplaceAllString(s, "$1")
	s = reBlankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// Bot reports whether a comment author is automation, whose comments (CLA
// nudges, CI notices, lock warnings) never move a thread forward.
func Bot(author string) bool {
	return strings.HasSuffix(author, "[bot]") || author == "github-actions" || author == "hashibot"
}

// DigestComments picks the informative slice of a thread: the first
// maintainer response plus the newest comments, in chronological order, bots
// dropped first.
func DigestComments(comments []db.Comment, maxN int) []db.Comment {
	human := make([]db.Comment, 0, len(comments))
	for _, c := range comments {
		if !Bot(c.Author) {
			human = append(human, c)
		}
	}
	if len(human) <= maxN {
		return human
	}

	pickedIdx := map[int]bool{}

	// the first maintainer response sets the thread's direction
	for i := range human {
		if human[i].IsMaintainer() {
			pickedIdx[i] = true
			break
		}
	}

	// then the newest comments (thread state now)
	for i := len(human) - 1; i >= 0 && len(pickedIdx) < maxN; i-- {
		pickedIdx[i] = true
	}

	out := make([]db.Comment, 0, len(pickedIdx))
	for i := range human {
		if pickedIdx[i] {
			out = append(out, human[i])
		}
	}
	return out
}
