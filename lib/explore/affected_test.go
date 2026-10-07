package explore

import (
	"slices"
	"testing"
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
