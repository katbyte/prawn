package explore

import (
	"slices"
	"strings"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

// Tests is a PR's acceptance test picture: the latest build TeamCity ran for
// each service the PR was tested against, and what they add up to.
type Tests struct {
	Status  string      `json:"s"`            // failing | running | passing | cancelled: the worst of the latest builds
	Passed  int         `json:"p"`            // tests passed, over the latest builds
	Failed  int         `json:"f"`            // tests failed
	Ignored int         `json:"i"`            // tests skipped
	Ran     int64       `json:"at"`           // when the most recent of them finished (started, while it runs)
	Since   *int        `json:"cs,omitempty"` // commits pushed to the PR since it started; left off when the PR's commits are not known
	Failing []string    `json:"ft,omitempty"` // the failed tests' names, as far as they were looked up
	Builds  []TestBuild `json:"b"`            // the latest build of each service, most recent first
	Runs    int         `json:"n"`            // every build teamcity still keeps for the PR, re-runs included
}

// TestBuild is the latest build of one service.
type TestBuild struct {
	Service string `json:"sv"`
	Status  string `json:"st"` // failing | running | passing | cancelled
	Passed  int    `json:"p"`
	Failed  int    `json:"f"`
	At      int64  `json:"at"`
	URL     string `json:"u"`
}

// how a set of checks or a test build came out, as the page words it: ci and
// the tests share these three
const (
	outcomePassing = "passing"
	outcomeFailing = "failing"
	outcomeRunning = "running"
)

// testsFailingMax bounds the failed test names a PR carries onto the page.
const testsFailingMax = 40

// buildStatus reads one build's outcome the way the page words it.
func buildStatus(b *db.TCBuild) string {
	switch {
	case b.State != "finished":
		return outcomeRunning // queued counts: it is on its way
	case b.Status == "SUCCESS":
		return outcomePassing
	case b.Status == "FAILURE":
		return outcomeFailing
	default:
		return "cancelled" // UNKNOWN: stopped, or never started
	}
}

// tests sums a PR's builds up: the latest per service (a re-run replaces the
// run before it), their worst outcome, and how many commits the PR has had
// since the most recent of them began. service is what serviceNames returns: it
// names a build by its service. nil without builds.
func tests(builds []db.TCBuild, commits []db.Commit, service func(buildType string) string) *Tests {
	if len(builds) == 0 {
		return nil
	}
	latest := map[string]*db.TCBuild{}
	for i := range builds {
		b := &builds[i]
		if cur := latest[b.BuildType]; cur == nil || b.ID > cur.ID {
			latest[b.BuildType] = b
		}
	}

	t := &Tests{Runs: len(builds), Builds: make([]TestBuild, 0, len(latest))}
	worst := map[string]bool{}
	var began time.Time
	for _, b := range latest {
		status := buildStatus(b)
		worst[status] = true
		at := b.FinishedAt
		if at.IsZero() {
			at = b.StartedAt
		}
		if b.StartedAt.After(began) {
			began = b.StartedAt
		}
		t.Passed, t.Failed, t.Ignored = t.Passed+b.Passed, t.Failed+b.Failed, t.Ignored+b.Ignored
		if at.Unix() > t.Ran && !at.IsZero() {
			t.Ran = at.Unix()
		}
		tb := TestBuild{Service: service(b.BuildType), Status: status, Passed: b.Passed, Failed: b.Failed, URL: b.URL}
		if !at.IsZero() {
			tb.At = at.Unix()
		}
		t.Builds = append(t.Builds, tb)
		if status == outcomeFailing {
			for _, name := range b.FailedTests {
				if len(t.Failing) < testsFailingMax && !slices.Contains(t.Failing, name) {
					t.Failing = append(t.Failing, name)
				}
			}
		}
	}
	slices.SortFunc(t.Builds, func(a, b TestBuild) int {
		if a.At != b.At {
			return int(b.At - a.At)
		}
		return strings.Compare(a.Service, b.Service)
	})
	slices.Sort(t.Failing)

	for _, s := range []string{outcomeFailing, outcomeRunning, outcomePassing, "cancelled"} {
		if worst[s] {
			t.Status = s
			break
		}
	}
	// commits since the tests began: only countable when the PR's commits were fetched
	if len(commits) > 0 && !began.IsZero() {
		n := 0
		for _, c := range commits {
			if c.CommittedAt.After(began) {
				n++
			}
		}
		t.Since = &n
	}
	return t
}

// serviceNames returns how a build type id is shown: with the prefix most
// build types share cut off and lowercased — TF_AzureRM_AZURERM_SERVICE_PUBLIC_NETWORK
// is "network". The odd one out (a build that is not per-service) keeps what
// sets it apart from the rest: only what every id starts with is cut.
func serviceNames(all map[int][]db.TCBuild) func(buildType string) string {
	seen := map[string]bool{}
	for _, builds := range all {
		for i := range builds {
			seen[builds[i].BuildType] = true
		}
	}
	// each id's own prefix is everything up to its last underscore; the commonest wins
	votes, common, first := map[string]int{}, "", true
	for bt := range seen {
		votes[bt[:strings.LastIndex(bt, "_")+1]]++
		if first {
			common, first = bt, false
			continue
		}
		for !strings.HasPrefix(bt, common) {
			common = common[:len(common)-1]
		}
	}
	common = common[:strings.LastIndex(common, "_")+1]
	most, n := "", 1 // a prefix has to be shared to count
	for prefix, v := range votes {
		if v > n || (v == n && most != "" && prefix < most) {
			most, n = prefix, v
		}
	}
	return func(bt string) string {
		switch {
		case most != "" && strings.HasPrefix(bt, most):
			bt = bt[len(most):]
		case len(seen) > 1:
			bt = strings.TrimPrefix(bt, common)
		}
		return strings.ToLower(bt)
	}
}
