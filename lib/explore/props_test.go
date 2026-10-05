package explore

import (
	"slices"
	"testing"
)

func TestProperties(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		diff string
		want []string
	}{
		{
			name: "a new schema key, its model tag and its docs bullet are one property",
			diff: `diff --git a/internal/services/network/public_ip_resource.go b/internal/services/network/public_ip_resource.go
--- a/internal/services/network/public_ip_resource.go
+++ b/internal/services/network/public_ip_resource.go
@@ -10,6 +10,7 @@ type PublicIPModel struct {
 	Name string ` + "`tfschema:\"name\"`" + `
+	IPAddress string ` + "`tfschema:\"ip_address\"`" + `
@@ -80,6 +81,12 @@ func (r PublicIPResource) Arguments() map[string]*pluginsdk.Schema {
 			"sku": {
 				Type: pluginsdk.TypeString,
 			},
+			"ip_address": {
+				Type:     pluginsdk.TypeString,
+				Optional: true,
+			},
diff --git a/website/docs/r/public_ip.html.markdown b/website/docs/r/public_ip.html.markdown
--- a/website/docs/r/public_ip.html.markdown
+++ b/website/docs/r/public_ip.html.markdown
@@ -60,6 +60,8 @@
+* ` + "`ip_address`" + ` - (Optional) The address to use.
`,
			want: []string{"ip_address"},
		},
		{
			name: "a line changed inside a block names the block, not its neighbours",
			diff: `diff --git a/internal/services/storage/storage_account_resource.go b/internal/services/storage/storage_account_resource.go
--- a/internal/services/storage/storage_account_resource.go
+++ b/internal/services/storage/storage_account_resource.go
@@ -100,9 +100,9 @@ func resourceStorageAccount() *pluginsdk.Resource {
 			"account_tier": {
 				Type:     pluginsdk.TypeString,
 			},
 			"min_tls_version": {
 				Type:     pluginsdk.TypeString,
-				Default:  "TLS1_0",
+				Default:  "TLS1_2",
 			},
`,
			want: []string{"min_tls_version"},
		},
		{
			name: "a key added inside a block is its own property, the parent untouched",
			diff: `diff --git a/internal/services/compute/vm_resource.go b/internal/services/compute/vm_resource.go
--- a/internal/services/compute/vm_resource.go
+++ b/internal/services/compute/vm_resource.go
@@ -50,6 +50,9 @@ func resourceVM() *pluginsdk.Resource {
 			"os_disk": {
 				Elem: &pluginsdk.Resource{
 					Schema: map[string]*pluginsdk.Schema{
+						"placement": {
+							Type: pluginsdk.TypeString,
+						},
`,
			want: []string{"placement"},
		},
		{
			name: "new files, tests and the docs' timeouts are not property changes",
			diff: `diff --git a/internal/services/lustre/job_resource.go b/internal/services/lustre/job_resource.go
new file mode 100644
--- /dev/null
+++ b/internal/services/lustre/job_resource.go
@@ -0,0 +1,9 @@
+			"prefixes": {
+				Type: pluginsdk.TypeList,
+			},
diff --git a/internal/services/lustre/job_resource_test.go b/internal/services/lustre/job_resource_test.go
--- a/internal/services/lustre/job_resource_test.go
+++ b/internal/services/lustre/job_resource_test.go
@@ -1,3 +1,6 @@
+			"fixture": {
+				Type: pluginsdk.TypeString,
+			},
diff --git a/website/docs/r/lustre.html.markdown b/website/docs/r/lustre.html.markdown
--- a/website/docs/r/lustre.html.markdown
+++ b/website/docs/r/lustre.html.markdown
@@ -40,3 +40,4 @@
+* ` + "`create`" + ` - (Defaults to 30 minutes) Used when creating the job.
`,
			want: []string{},
		},
		{
			name: "rewording the docs alone changes no property",
			diff: `diff --git a/website/docs/r/vm.html.markdown b/website/docs/r/vm.html.markdown
--- a/website/docs/r/vm.html.markdown
+++ b/website/docs/r/vm.html.markdown
@@ -40,3 +40,3 @@
-* ` + "`size`" + ` - (Required) The size.
+* ` + "`size`" + ` - (Required) The SKU of the virtual machine.
`,
			want: []string{},
		},
		{name: "no diff", diff: "", want: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Properties(c.diff); !slices.Equal(got, c.want) {
				t.Errorf("Properties() = %v, want %v", got, c.want)
			}
		})
	}
}
