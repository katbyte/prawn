package pr

import (
	"errors"
	"testing"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

func TestLanding(t *testing.T) {
	t.Parallel()
	opened := time.Date(2025, 7, 7, 0, 0, 0, 0, time.UTC)
	resFile := "internal/services/privatedns/private_dns_a_record_resource.go"
	docFile := "website/docs/r/private_dns_a_record.html.markdown"
	diff := []FileDiff{
		{
			Path:    resFile,
			Removed: []string{`			"zone_name": {`, `			"resource_group_name": commonschema.ResourceGroupName(),`},
			// ttl_seconds is reused: it was already in the file, so it is neutral
			Added: []string{`			"private_dns_zone_id": {`, `				ValidateFunc: privatezones.ValidatePrivateDnsZoneID,`, `			"name": {`, `	ttl := d.Get("ttl_seconds")`},
		},
		{
			Path:    docFile,
			Removed: []string{"* `zone_name` - (Required) old"},
			Added:   []string{"* `private_dns_zone_id` - (Required) new"},
		},
		{Path: "internal/services/privatedns/private_dns_a_record_resource_test.go", Added: []string{`"test_only_token"`}},
		{Path: "CHANGELOG.md", Added: []string{"* `azurerm_private_dns_a_record` - now uses `private_dns_zone_id`"}},
	}
	p := &db.PR{Number: 30096, CreatedAt: opened}

	landing := Commit{Hash: "e07564c519", Date: opened.AddDate(1, 0, 13), Author: "sreallymatt", Subject: "5.0: `privatedns` - resolve TODO 4.0s (#32765)", PR: 32765}
	h := &History{
		tags: []releaseTag{{name: "v4.40.0", date: opened.AddDate(0, 11, 0)}, {name: "v5.0.0", date: opened.AddDate(1, 0, 20)}},
		readFile: func(path string) ([]byte, error) {
			switch path {
			case resFile:
				return []byte(`"name": {} "private_dns_zone_id": {} "ttl_seconds": {}`), nil
			case docFile:
				return []byte("* `private_dns_zone_id` - (Required)"), nil
			}
			return nil, errors.New("no such file")
		},
		baseFile: func(_ time.Time, path string) ([]byte, error) {
			switch path {
			case resFile:
				return []byte(`"name": {} "zone_name": {} "ttl_seconds": {}`), nil
			case docFile:
				return []byte("* `zone_name` - (Required)"), nil
			}
			return nil, nil
		},
		attribute: func(_ time.Time, ident, _ string) ([]Commit, error) {
			switch ident {
			case `"private_dns_zone_id"`, "`private_dns_zone_id`", `"zone_name"`, "`zone_name`":
				return []Commit{landing}, nil
			}
			return nil, nil
		},
	}

	got, err := h.Landing(p, diff)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a landing")
	}
	if got.Commit.PR != 32765 || got.ShippedIn != "v5.0.0" {
		t.Errorf("wrong landing: %+v shipped %q", got.Commit, got.ShippedIn)
	}
	// "name" is too short and bare, ttl_seconds predates the PR, test and
	// changelog files are ignored: private_dns_zone_id lands, once per file;
	// zone_name, which the PR deletes, is gone from both files too
	if len(got.Landed) != 1 || got.Landed[0] != "private_dns_zone_id" || len(got.Missing) != 0 {
		t.Errorf("wrong identifiers: landed %v missing %v", got.Landed, got.Missing)
	}
	if len(got.Gone) != 1 || got.Gone[0] != "zone_name" || len(got.Still) != 0 {
		t.Errorf("wrong deletions: gone %v still %v", got.Gone, got.Still)
	}

	// the rename case: the PR chose a different new name than what landed, so
	// its additions are missing, but what it deleted is gone from both files —
	// still a landing
	renamed := []FileDiff{
		{Path: resFile, Removed: []string{`			"zone_name": {`}, Added: []string{`			"private_zone_id": {`}},
		{Path: docFile, Removed: []string{"* `zone_name` - old"}, Added: []string{"* `private_zone_id` - new"}},
	}
	got, err = h.Landing(p, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Gone) != 1 || len(got.Missing) != 1 || got.Commit.PR != 32765 {
		t.Errorf("rename should land on the deletions: %+v", got)
	}

	// one bare deletion is not enough on its own
	if got, _ := h.Landing(p, renamed[:1]); got != nil {
		t.Errorf("a single deletion must not count as landed: %+v", got)
	}

	// the identifier present at HEAD but predating the PR is neutral: no landing
	h.baseFile = func(_ time.Time, path string) ([]byte, error) { return h.readFile(path) }
	if got, _ := h.Landing(p, diff); got != nil {
		t.Errorf("pre-existing identifier must not count as landed: %+v", got)
	}
	h.baseFile = func(time.Time, string) ([]byte, error) { return nil, nil }

	// most identifiers missing from HEAD: still needed
	h.readFile = func(string) ([]byte, error) { return []byte("nothing here"), nil }
	if got, _ := h.Landing(p, diff); got != nil {
		t.Errorf("missing identifiers must not count as landed: %+v", got)
	}
}

func TestLandedIdent(t *testing.T) {
	t.Parallel()
	for tok, want := range map[string]bool{
		"private_dns_zone_id": true, "zone_name": true, "sku": false, "enabled": false, "resource_group_name": false,
		"hyperthreading": true, "dashboards": false, "location": false,
	} {
		if got := landedIdent(tok); got != want {
			t.Errorf("landedIdent(%q) = %v, want %v", tok, got, want)
		}
	}
}

func TestParseDiff(t *testing.T) {
	t.Parallel()
	diff := `diff --git a/internal/services/privatedns/private_dns_a_record_resource.go b/internal/services/privatedns/private_dns_a_record_resource.go
index 1..2 100644
--- a/internal/services/privatedns/private_dns_a_record_resource.go
+++ b/internal/services/privatedns/private_dns_a_record_resource.go
@@ -50,7 +50,7 @@ func resourcePrivateDnsARecord() *pluginsdk.Resource {
 			"name": {
-			"zone_name": {
+			"private_dns_zone_id": {
 				Type:     pluginsdk.TypeString,
diff --git a/website/docs/r/new_thing.html.markdown b/website/docs/r/new_thing.html.markdown
new file mode 100644
--- /dev/null
+++ b/website/docs/r/new_thing.html.markdown
@@ -0,0 +1,2 @@
+* ` + "`zone_name`" + ` - (Required) old
+* ` + "`private_dns_zone_id`" + ` - (Required) new
`
	got := ParseDiff(diff)
	if len(got) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(got), got)
	}
	if got[0].Path != "internal/services/privatedns/private_dns_a_record_resource.go" || got[0].IsNew {
		t.Errorf("wrong first file: %+v", got[0])
	}
	if len(got[0].Removed) != 1 || len(got[0].Added) != 1 || got[0].Removed[0] != `			"zone_name": {` {
		t.Errorf("wrong changed lines: %+v", got[0])
	}
	if !got[1].IsNew || len(got[1].Added) != 2 || len(got[1].Removed) != 0 {
		t.Errorf("wrong new file: %+v", got[1])
	}
}
