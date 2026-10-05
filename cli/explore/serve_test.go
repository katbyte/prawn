package explore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestRefresher(t *testing.T) {
	t.Parallel()
	release, ran := make(chan struct{}), make(chan struct{}, 2)
	rf := &refresher{run: func() error { ran <- struct{}{}; <-release; return nil }}
	do := func(method string, header bool) (int, refreshStatus) {
		r := httptest.NewRequestWithContext(t.Context(), method, "/refresh", http.NoBody)
		if header {
			r.Header.Set("X-Prawn", "1")
			r.Header.Set("X-Forwarded-User", "katbyte")
		}
		rec := httptest.NewRecorder()
		rf.ServeHTTP(rec, r)
		var st refreshStatus
		_ = json.Unmarshal(rec.Body.Bytes(), &st)
		return rec.Code, st
	}

	if code, _ := do(http.MethodPost, false); code != http.StatusForbidden {
		t.Errorf("POST without the page's header = %d, want 403", code)
	}
	if code, st := do(http.MethodPost, true); code != http.StatusOK || !st.Running || st.By != "katbyte" {
		t.Errorf("POST = %d %+v, want 200 and running by katbyte", code, st)
	}
	<-ran
	do(http.MethodPost, true) // a second press while it runs joins it, starting nothing
	close(release)
	for range 200 {
		if _, st := do(http.MethodGet, false); !st.Running {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, st := do(http.MethodGet, false); st.Running || st.Finished == "" || st.Error != "" {
		t.Errorf("after the run: %+v, want finished without error", st)
	}
	do(http.MethodPost, true) // moments after finishing: too soon, starts nothing
	if len(ran) != 0 {
		t.Errorf("the rebuild ran %d more times, want it to have run once", len(ran))
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
