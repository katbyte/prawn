package label

import "testing"

func TestTitleHeuristics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		label string
		want  bool
	}{
		{"azurerm_storage_account - fix crash when soft delete is enabled", LabelBug, true},
		{"Fixes #12345: correct diff on tags", LabelBug, true},
		{"azurerm_kusto_cluster - support for zones", LabelEnhancement, true},
		{"New Resource: azurerm_widget_frobulator", LabelEnhancement, true},
		{"add `identity` block to azurerm_thing", LabelEnhancement, true},
		{"Update CHANGELOG.md", LabelBug, false},
		{"Update CHANGELOG.md", LabelEnhancement, false},
		{"dependencies: bump go-azure-sdk", LabelEnhancement, false},
	}
	for _, c := range cases {
		if got := reTitleByLabel[c.label].MatchString(c.title); got != c.want {
			t.Errorf("%s heuristic on %q = %v, want %v", c.label, c.title, got, c.want)
		}
	}
}
