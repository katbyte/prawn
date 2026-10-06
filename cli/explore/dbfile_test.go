package explore

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/prawn/lib/db"
)

func TestDownloadName(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for path, want := range map[string]string{"prs.db": "prs.20260101.db", "/data/prs.db": "prs.20260101.db", "cache": "cache.20260101"} {
		if got := downloadName(path, day); got != want {
			t.Errorf("downloadName(%q) = %q, want %q", path, got, want)
		}
	}
}

// a database goes down from one server and up into another
func TestDBFile(t *testing.T) {
	t.Parallel()
	newDB := func(dir, marker string) string {
		path := filepath.Join(dir, "prs.db")
		d, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SetMeta("marker", marker); err != nil {
			t.Fatal(err)
		}
		_ = d.Close()
		return path
	}
	marker := func(path string) string {
		d, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = d.Close() }()
		v, _ := d.GetMeta("marker")
		return v
	}
	do := func(h http.Handler, method, target string, body []byte, header bool) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
		if header {
			r.Header.Set("X-Prawn", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	from := &dbFile{path: newDB(t.TempDir(), "from"), rf: &refresher{}}
	down := do(from, http.MethodGet, "/db", nil, false)
	if down.Code != http.StatusOK || !strings.Contains(down.Header().Get("Content-Disposition"), downloadName("prs.db", time.Now())) {
		t.Fatalf("download = %d %q, want 200 and a dated name", down.Code, down.Header().Get("Content-Disposition"))
	}
	copied := down.Body.Bytes()

	rebuilt := make(chan struct{}, 1)
	to := &dbFile{path: newDB(t.TempDir(), "to"), rf: &refresher{}, rebuild: func() error { rebuilt <- struct{}{}; return nil }}
	if rec := do(to, http.MethodPost, "/db", copied, false); rec.Code != http.StatusForbidden {
		t.Errorf("upload without the page's header = %d, want 403", rec.Code)
	}
	if rec := do(to, http.MethodPost, "/db", []byte("not a database at all"), true); rec.Code != http.StatusBadRequest {
		t.Errorf("uploading junk = %d, want 400", rec.Code)
	}
	if got := marker(to.path); got != "to" {
		t.Fatalf("after the refused uploads the database holds %q, want it untouched", got)
	}
	if rec := do(to, http.MethodPost, "/db", copied, true); rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %s, want 200", rec.Code, rec.Body)
	}
	select {
	case <-rebuilt:
	case <-time.After(10 * time.Second):
		t.Fatal("the page was not rebuilt after the upload")
	}
	if got := marker(to.path); got != "from" {
		t.Errorf("after the upload the database holds %q, want the uploaded one", got)
	}
	if got := marker(to.path + replacedSuffix); got != "to" {
		t.Errorf("the copy kept holds %q, want the database that was replaced", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(to.path), ".prawn-*"))
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	if _, err := os.Stat(from.path); err != nil {
		t.Errorf("downloading disturbed the source: %v", err)
	}
}
