package explore

import (
	"testing"

	"github.com/katbyte/prawn/lib/db"
)

func TestTests(t *testing.T) {
	t.Parallel()
	const prefix = "TF_AzureRM_AZURERM_SERVICE_PUBLIC_"
	builds := []db.TCBuild{
		// network: failed, then re-run and passed — the re-run is what counts
		{ID: 1, BuildType: prefix + "NETWORK", State: "finished", Status: "FAILURE", Failed: 3, StartedAt: day(1), FinishedAt: day(1), FailedTests: []string{"TestAccOld"}},
		{ID: 5, BuildType: prefix + "NETWORK", State: "finished", Status: "SUCCESS", Passed: 12, StartedAt: day(4), FinishedAt: day(4)},
		// compute: its latest run failed two tests
		{ID: 7, BuildType: prefix + "COMPUTE", State: "finished", Status: "FAILURE", Passed: 38, Failed: 2, Ignored: 3, StartedAt: day(6), FinishedAt: day(6), FailedTests: []string{"TestAccVM_b", "TestAccVM_a"}},
	}
	commits := []db.Commit{{CommittedAt: day(2)}, {CommittedAt: day(7)}, {CommittedAt: day(8)}}
	service := serviceNames(map[int][]db.TCBuild{1: builds})

	got := tests(builds, commits, service)
	if got.Status != "failing" || got.Passed != 50 || got.Failed != 2 || got.Ignored != 3 || got.Runs != 3 {
		t.Errorf("summary = %s %d/%d/%d over %d runs, want failing 50/2/3 over 3", got.Status, got.Passed, got.Failed, got.Ignored, got.Runs)
	}
	if got.Ran != day(6).Unix() {
		t.Errorf("ran = %d, want the most recent build's finish", got.Ran)
	}
	if got.Since == nil || *got.Since != 2 {
		t.Errorf("commits since = %v, want the 2 pushed after the last build began", got.Since)
	}
	if len(got.Failing) != 2 || got.Failing[0] != "TestAccVM_a" {
		t.Errorf("failing tests = %v, want the latest compute build's two, sorted, and not the superseded network run's", got.Failing)
	}
	if len(got.Builds) != 2 || got.Builds[0].Service != "compute" || got.Builds[1].Service != "network" || got.Builds[1].Status != "passing" {
		t.Errorf("builds = %+v, want compute then network, network passing", got.Builds)
	}

	if tests(nil, commits, service) != nil {
		t.Error("a PR with no builds has a test picture")
	}
	if one := tests(builds[1:2], nil, service); one.Status != "passing" || one.Since != nil {
		t.Errorf("one passing build, commits unknown = %s since %v, want passing and no count", one.Status, one.Since)
	}
	running := tests([]db.TCBuild{{ID: 9, BuildType: prefix + "STORAGE", State: "running", Status: "SUCCESS", StartedAt: day(9)}}, nil, service)
	if running.Status != "running" || running.Ran != day(9).Unix() {
		t.Errorf("a running build = %s at %d, want running, dated by its start", running.Status, running.Ran)
	}
}

func TestServiceNames(t *testing.T) {
	t.Parallel()
	const prefix = "TF_AzureRM_AZURERM_SERVICE_PUBLIC_"
	name := serviceNames(map[int][]db.TCBuild{
		1: {{BuildType: prefix + "COMPUTE"}, {BuildType: prefix + "CONTAINERS"}},
		2: {{BuildType: prefix + "COSMOS"}, {BuildType: "TF_AzureRM_AZURERM_PR_PUBLIC"}},
	})
	for bt, want := range map[string]string{
		prefix + "COMPUTE":             "compute",    // the shared prefix, not one cut inside the names
		prefix + "CONTAINERS":          "containers", //
		"TF_AzureRM_AZURERM_PR_PUBLIC": "pr_public",  // the odd one out keeps what sets it apart
	} {
		if got := name(bt); got != want {
			t.Errorf("name(%s) = %q, want %q", bt, got, want)
		}
	}
	if got := serviceNames(map[int][]db.TCBuild{1: {{BuildType: "ONLY_ONE"}}})("ONLY_ONE"); got != "only_one" {
		t.Errorf("a single build type = %q, want it whole", got)
	}
}
