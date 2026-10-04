package explore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestViewerAndFrom(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.0.7:4242"
	if got := viewer(r); got != "-" {
		t.Errorf("viewer without a proxy header = %q, want -", got)
	}
	if got := from(r); got != "10.0.0.7" {
		t.Errorf("from without a proxy = %q, want 10.0.0.7", got)
	}

	r.Header.Set("X-Forwarded-User", "katbyte")
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := viewer(r); got != "katbyte" {
		t.Errorf("viewer = %q, want katbyte", got)
	}
	if got := from(r); got != "203.0.113.9" {
		t.Errorf("from behind a proxy = %q, want the first hop", got)
	}
}

func TestStatusWriter(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	sw.WriteHeader(http.StatusNotFound)
	if _, err := sw.Write([]byte("nope")); err != nil {
		t.Fatal(err)
	}
	if sw.status != http.StatusNotFound || sw.bytes != 4 {
		t.Errorf("recorded %d/%d bytes, want 404/4", sw.status, sw.bytes)
	}
}
