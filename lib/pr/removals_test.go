package pr

import (
	"testing"
)

const testGuide = `---
page_title: guide
---

# v5.0

## Removed Resources

### ` + "`azurerm_old_thing`" + `

This resource has been removed, use ` + "`azurerm_new_thing`" + ` instead.

## Removed Data Sources

### ` + "`azurerm_old_data`" + `

This data source has been removed.

## Behaviour changes and removed properties in Resources

### ` + "`azurerm_widget`" + `

* The deprecated ` + "`old_prop`" + ` property has been removed in favour of the ` + "`new_prop`" + ` property.
* The ` + "`some_default`" + ` property now defaults to true.
`

func TestParseUpgradeGuide(t *testing.T) {
	t.Parallel()
	rs := ParseUpgradeGuide(testGuide, 5)
	if len(rs) != 3 {
		t.Fatalf("expected 3 removals, got %d: %+v", len(rs), rs)
	}
	if rs[0].Resource != "azurerm_old_thing" || rs[0].Kind != RemovalKindResource || rs[0].Action != RemovalRemoved || rs[0].Successor != "azurerm_new_thing" {
		t.Errorf("removed resource row wrong: %+v", rs[0])
	}
	if rs[1].Resource != "azurerm_old_data" || rs[1].Kind != RemovalKindDataSource {
		t.Errorf("removed data source row wrong: %+v", rs[1])
	}
	if rs[2].Property != "old_prop" || rs[2].Resource != "azurerm_widget" || rs[2].Successor != "new_prop" || rs[2].Kind != RemovalKindProperty {
		t.Errorf("removed property row wrong: %+v", rs[2])
	}
}

const testChangelog = `## 5.3.0 (August 21, 2026)

DEPRECATIONS:

* ` + "`azurerm_dying_thing`" + ` - deprecated in favour of ` + "`azurerm_shiny_thing`" + ` [GH-1]
* ` + "`azurerm_widget`" + ` - the ` + "`old_field`" + ` property has been superseded by ` + "`new_field`" + ` [GH-2]

BUG FIXES:

* ` + "`azurerm_widget`" + ` - fix a crash [GH-3]

## 5.2.0 (August 14, 2026)

ENHANCEMENTS:

* ` + "`azurerm_widget`" + ` - support ` + "`shiny`" + ` [GH-4]
`

func TestParseChangelogDeprecations(t *testing.T) {
	t.Parallel()
	rs := ParseChangelogDeprecations(testChangelog)
	if len(rs) != 2 {
		t.Fatalf("expected 2 removals, got %d: %+v", len(rs), rs)
	}
	if rs[0].Resource != "azurerm_dying_thing" || rs[0].Action != RemovalDeprecated || rs[0].Successor != "azurerm_shiny_thing" || rs[0].Source != "changelog v5.3.0" {
		t.Errorf("deprecated resource row wrong: %+v", rs[0])
	}
	if rs[1].Resource != "azurerm_widget" || rs[1].Property != "old_field" || rs[1].Successor != "new_field" {
		t.Errorf("deprecated property row wrong: %+v", rs[1])
	}
}

// TestLoadInventoryAzurerm parses a real provider checkout when one is present
// (skipped otherwise) — a smoke test that the guide/changelog/source formats
// still match.
func TestLoadInventoryAzurerm(t *testing.T) {
	t.Parallel()
	src := "../../../../azure/azurerm"
	inv, err := LoadInventory(src)
	if err != nil {
		t.Skipf("no provider checkout at %s: %v", src, err)
	}
	if len(inv.Removals) < 100 {
		t.Errorf("suspiciously few removals from a real checkout: %d", len(inv.Removals))
	}
	if !inv.Live["azurerm_resource_group"] {
		t.Errorf("live set missing azurerm_resource_group")
	}
	if inv.CurrentMajor < 5 {
		t.Errorf("current major %d < 5", inv.CurrentMajor)
	}
	counts := map[string]int{}
	for _, r := range inv.Removals {
		counts[r.Action+" "+r.Kind]++
	}
	t.Logf("inventory: %d removals %v, current major %d, %d live names", len(inv.Removals), counts, inv.CurrentMajor, len(inv.Live))
}
