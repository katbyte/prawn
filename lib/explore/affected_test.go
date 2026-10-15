package explore

import (
	"slices"
	"testing"

	"github.com/katbyte/prawn/lib/db"
)

func TestAffected(t *testing.T) {
	t.Parallel()
	res, ds := affected([]string{
		"internal/services/storage/storage_account_resource.go",
		"internal/services/storage/storage_account_resource_test.go",
		"internal/services/storage/storage_management_policy_data_source_test.go",
		"internal/services/storage/client/client.go",
		"website/docs/r/storage_account.html.markdown",
		"website/docs/r/storage_share.html.markdown",
		"website/docs/d/storage_account.html.markdown",
		"CHANGELOG.md",
	})
	if want := []string{"azurerm_storage_account", "azurerm_storage_share"}; !slices.Equal(res, want) {
		t.Errorf("resources = %v, want %v", res, want)
	}
	if want := []string{"azurerm_storage_account", "azurerm_storage_management_policy"}; !slices.Equal(ds, want) {
		t.Errorf("data sources = %v, want %v", ds, want)
	}
}

func TestSharedFiles(t *testing.T) {
	t.Parallel()
	got := sharedFiles([]*db.PR{
		{Number: 1, State: db.PROpen, Files: []string{"internal/services/storage/storage_account_resource.go", "internal/services/storage/registration.go", "go.mod", "vendor/modules.txt"}},
		{Number: 2, State: db.PROpen, Files: []string{"internal/services/storage/storage_account_resource.go", "internal/services/storage/registration.go", "go.mod", "vendor/modules.txt"}},
		{Number: 3, State: db.PROpen, Files: []string{"website/docs/r/storage_account.html.markdown"}},
		{Number: 4, State: db.PRMerged, Files: []string{"website/docs/r/storage_account.html.markdown", "internal/services/compute/registration.go"}},
		{Number: 5, State: db.PRMerged, Files: []string{"internal/services/compute/virtual_machine_resource.go"}},
		{Number: 6, State: db.PRClosed, Files: []string{"internal/services/compute/virtual_machine_resource.go"}},
	})
	want := map[int][]string{
		1: {"internal/services/storage/storage_account_resource.go"},
		2: {"internal/services/storage/storage_account_resource.go"},
		4: {"website/docs/r/storage_account.html.markdown"},
	}
	if len(got) != len(want) {
		t.Fatalf("shared = %v, want %v", got, want)
	}

	for n, files := range want {
		if !slices.Equal(got[n], files) {
			t.Errorf("#%d shares %v, want %v", n, got[n], files)
		}
	}
}
