package explore

import (
	"slices"
	"testing"
)

const kindSchemaHead = `diff --git a/internal/services/network/subnet_resource.go b/internal/services/network/subnet_resource.go
--- a/internal/services/network/subnet_resource.go
+++ b/internal/services/network/subnet_resource.go
`

func newFile(name string) string {
	return "diff --git a/" + name + " b/" + name + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + name + "\n@@ -0,0 +1,3 @@\n+package x\n"
}

func TestKind(t *testing.T) {
	t.Parallel()
	addOne := kindSchemaHead + `@@ -10,6 +10,9 @@
 			"name": {
 				Type: pluginsdk.TypeString,
 			},
+			"delegation": {
+				Type: pluginsdk.TypeList,
+			},
`
	changeOne := kindSchemaHead + `@@ -10,6 +10,6 @@
 			"name": {
-				ForceNew: true,
+				ForceNew: false,
 			},
`
	removeOne := kindSchemaHead + `@@ -10,6 +10,3 @@
-			"legacy_mode": {
-				Type: pluginsdk.TypeBool,
-			},
`
	moved := kindSchemaHead + `@@ -10,6 +10,6 @@
-			"name": {
+			"name": {
 				Type: pluginsdk.TypeString,
`
	apiBump := kindSchemaHead + `@@ -3,3 +3,3 @@
-	"github.com/hashicorp/go-azure-sdk/resource-manager/network/2023-09-01/subnets"
+	"github.com/hashicorp/go-azure-sdk/resource-manager/network/2024-05-01/subnets"
`
	deprecate := kindSchemaHead + `@@ -10,6 +10,7 @@
 			"name": {
+				Deprecated: "use display_name",
 			},
`
	// a property declared in one call, and one in a shared helper file not named for a resource
	oneLine := kindSchemaHead + `@@ -10,6 +10,7 @@
 			"location": commonschema.Location(),
+			"edge_zone": commonschema.EdgeZoneOptionalForceNew(),
 			"tags": commonschema.Tags(),
`
	helper := `diff --git a/internal/services/containerapps/helpers/container_apps.go b/internal/services/containerapps/helpers/container_apps.go
--- a/internal/services/containerapps/helpers/container_apps.go
+++ b/internal/services/containerapps/helpers/container_apps.go
@@ -40,6 +40,9 @@
 					"name": {
 						Type: pluginsdk.TypeString,
 					},
+					"secret_mount": &pluginsdk.Schema{
+						Type: pluginsdk.TypeList,
+					},
`
	flatten := kindSchemaHead + `@@ -200,6 +200,7 @@ func flattenSubnet(input *subnets.Subnet) []interface{} {
 	return []interface{}{map[string]interface{}{
+		"prefix": pointer.From(input.Prefix),
 	}}
`
	cases := []struct {
		name, diff, title string
		labels, kinds     []string
		want              string
	}{
		{"a new resource, whatever else it adds", newFile("internal/services/network/vnet_resource.go") + addOne, "", nil, []string{"schema"}, KindResource},
		{"two new resources", newFile("internal/services/a/x_resource.go") + newFile("internal/services/a/y_resource.go"), "", nil, []string{"schema"}, KindResources},
		{"a new data source", newFile("internal/services/a/x_data_source.go"), "", nil, []string{"schema"}, KindDataSource},
		{"a list resource for a resource that exists", newFile("internal/services/a/x_resource_list.go"), "", nil, []string{"schema"}, KindListResource},
		{"a list resource beside its new resource is the new resource", newFile("internal/services/a/x_resource.go") + newFile("internal/services/a/x_resource_list.go"), "", nil, []string{"schema"}, KindResource},
		{"an sdk import moved to a newer version", apiBump, "", nil, []string{"schema"}, KindAPIVersion},
		{"a state migration", newFile("internal/services/a/migration/x_v0_to_v1.go") + changeOne, "", nil, []string{"code", "schema"}, KindStateMigration},
		{"a refactor, by its title", changeOne, "Retype `azurerm_dns_a_record` as typed resource", nil, []string{"schema"}, KindRefactor},
		{"a property removed", removeOne, "", nil, []string{"schema"}, KindPropRemoved},
		{"a deprecation", deprecate, "", nil, []string{"schema"}, KindDeprecation},
		{"one property added", addOne, "", []string{"bug"}, []string{"schema", "tests", "docs"}, KindPropAdded},
		{"a property declared in one call", oneLine, "", nil, []string{"schema"}, KindPropAdded},
		{"a property in a shared helper file", helper, "", nil, []string{"code"}, KindPropAdded},
		{"a value set in a flatten map is not a property", flatten, "", nil, []string{"schema"}, KindOther},
		{"one property changed", changeOne, "", []string{"bug"}, []string{"schema"}, KindPropChanged},
		{"a key dropped and added again is changed, not new", moved, "", nil, []string{"schema"}, KindPropChanged},
		{"tests alone", "", "fix TestAccSubnet_basic", nil, []string{"tests", "changelog"}, KindTestFix},
		{"docs of any sort alone", "", "", nil, []string{"docs", "examples"}, KindDocumentation},
		{"workflow and vendor files alone", "", "", nil, []string{"ci", "vendor"}, KindTooling},
		{"a bug with nothing above to call it", "", "", []string{"bug"}, []string{"code"}, KindBugFix},
		{"everything else", "", "", nil, []string{"code"}, KindOther},
		{"nothing known", "", "", nil, nil, KindOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Kind(c.diff, c.title, c.labels, c.kinds); got != c.want {
				t.Errorf("Kind() = %q, want %q", got, c.want)
			}
		})
	}
	for _, k := range []string{KindResource, KindPropAdded, KindPropsChanged, KindBugFix, KindOther} {
		if !slices.Contains(KindOrder, k) {
			t.Errorf("KindOrder is missing %q", k)
		}
	}
}

func TestFactsCounts(t *testing.T) {
	t.Parallel()
	f := facts(kindSchemaHead + `@@ -10,6 +10,14 @@
 			"name": {
-				Required: true,
+				Optional: true,
 			},
+			"a_new": {
+				Type: pluginsdk.TypeString,
+			},
+			"b_new": {
+				Type: pluginsdk.TypeString,
+			},
`)
	if !slices.Equal(f.added, []string{"a_new", "b_new"}) || !slices.Equal(f.changed, []string{"name"}) || len(f.removed) != 0 {
		t.Errorf("added %v changed %v removed %v, want two added, name changed, none removed", f.added, f.changed, f.removed)
	}
}
