package explore

import (
	"testing"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

func TestTestRun(t *testing.T) {
	t.Parallel()
	at := func(h int) time.Time { return time.Date(2026, 10, 7, h, 0, 0, 0, time.UTC) }
	comments := []db.Comment{
		{Author: "teowa", CreatedAt: at(1), URL: "u1", Body: "/test\n\n---\n**TeamCity build triggered:**\n- `loadbalancer`: [#1](x)"},
		{Author: "hc-github-team-tf-azure", CreatedAt: at(2), URL: "r1", Body: "<!-- teamcity-test-results -->\nBuild: [769898](x)\n\n**Total:** 4\n**Passed:** 4\n**Failed:** 0\n**Skipped:** 0\n**Test Duration:** 0h 3m 24s\n"},
		{Author: "sreallymatt", CreatedAt: at(3), URL: "u2", Body: "/test -s storage\n\n---\n**TeamCity build triggered:**\n- `storage`: [#2](x)\n- `network`: [#3](x)"},
		{Author: "hc-github-team-tf-azure", CreatedAt: at(4), URL: "r2", Body: "<!-- teamcity-test-results -->\n@sreallymatt - One or more tests newly failed in this PR.\nBuild: [769918](x)\n**Total:** 125\n**Passed:** 120\n**Failed:** 4\n**Skipped:** 1\n"},
	}
	commits := []db.Commit{{CommittedAt: at(2)}, {CommittedAt: at(5)}}

	r := testRun(comments, commits)
	if r == nil || r.By != "sreallymatt" || r.URL != "u2" || len(r.Services) != 2 || r.Services[0] != "storage" {
		t.Fatalf("the latest /test = %+v, want sreallymatt's for storage and network", r)
	}
	if r.Since == nil || *r.Since != 1 {
		t.Errorf("commits since the /test = %v, want 1", r.Since)
	}
	res := r.Result
	if res == nil || res.URL != "r2" || res.Build != 769918 || res.Total != 125 || res.Passed != 120 || res.Failed != 4 || res.Skipped != 1 || !res.NewFail {
		t.Errorf("the latest result = %+v, want build 769918: 125, 120 passed, 4 failed, 1 skipped, newly failed", res)
	}
	if testRun(nil, nil) != nil {
		t.Error("no comments: want nil")
	}
}
