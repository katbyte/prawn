package explore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/prawn/lib/db"
)

// dbFile is the page's db button: GET hands out a copy of the database, POST
// takes one in its place and rebuilds the page from it — how a database
// fetched on one machine gets into a server (a container) on another without
// walking the whole repo again.
type dbFile struct {
	path    string       // the database
	rf      *refresher   // the swap runs as a refresh: one thing at a time, and the page watches it
	rebuild func() error // writes the page from the database as it is
	admins  admins       // who may upload; anyone may download
}

// dbInfo is what the button's window says about the database (GET ?info).
type dbInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
	Admin    bool   `json:"admin"` // whether the one asking may upload
}

// uploadMax bounds an uploaded database: several times what years of a big repo come to.
const uploadMax = 8 << 30

func (d *dbFile) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Has("info") {
			d.info(w, r)
			return
		}
		d.download(w, r)
	case http.MethodPost:
		// as with refresh: a header a form on another site cannot send
		if r.Header.Get("X-Prawn") == "" {
			http.Error(w, "upload is the page's own button", http.StatusForbidden)
			return
		}
		if !d.admins.allow(r) {
			http.Error(w, "only an admin may upload a database (PRAWN_ADMINS)", http.StatusForbidden)
			return
		}
		d.upload(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func (d *dbFile) info(w http.ResponseWriter, r *http.Request) {
	in := dbInfo{Name: filepath.Base(d.path), Admin: d.admins.allow(r)}
	if st, err := os.Stat(d.path); err == nil {
		in.Size, in.Modified = st.Size(), st.ModTime().UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(in)
}

// downloadName is the database's name with the day in it: prs.db on the 5th
// of october 2026 is prs.20261005.db.
func downloadName(path string, now time.Time) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "." + now.Format("20060102") + ext
}

// download sends a snapshot: the file itself may be mid-write, with its
// latest rows still in the journal beside it.
func (d *dbFile) download(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(d.path); err != nil {
		http.Error(w, "there is no database yet", http.StatusNotFound)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.path), ".prawn-download-*.db")
	if err != nil {
		http.Error(w, "making room for the copy: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()

	src, err := db.Open(d.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = src.Snapshot(tmp.Name())
	_ = src.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName(d.path, now)))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", now, tmp)
}

// upload takes the request's body as the new database: written beside the
// real one, checked, and only then swapped in, as a refresh the page watches.
func (d *dbFile) upload(w http.ResponseWriter, r *http.Request) {
	if d.rf.status().Running {
		http.Error(w, "a refresh is running: upload once it is done", http.StatusConflict)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.path), ".prawn-upload-*.db")
	if err != nil {
		http.Error(w, "making room for the upload: "+err.Error(), http.StatusInternalServerError)
		return
	}
	name := tmp.Name()
	discard := func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(name + suffix)
		}
	}
	_, err = io.Copy(tmp, http.MaxBytesReader(w, r.Body, uploadMax))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		discard()
		http.Error(w, "receiving the upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	prs, err := db.Inspect(name)
	if err != nil {
		discard()
		http.Error(w, "that file cannot be used: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !d.rf.begin(viewer(r), "upload", func() error { return d.swap(name, prs) }, true) {
		discard()
		http.Error(w, "a refresh started meanwhile: upload once it is done", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(d.rf.status())
}

// replacedSuffix names the one copy kept of a database an upload replaced.
const replacedSuffix = ".replaced"

// swap puts the uploaded file in the database's place, keeps the one it
// replaces beside it, and rebuilds the page.
func (d *dbFile) swap(upload string, prs int) error {
	cout.Printf("replacing <cyan>%s</> with an uploaded database of <yellow>%d</> PRs\n", d.path, prs)
	if _, err := os.Stat(d.path); err == nil {
		// opening and closing it folds its journal into the file, so the copy kept is whole
		if old, oerr := db.Open(d.path); oerr == nil {
			_ = old.Close()
		}
		if err := os.Rename(d.path, d.path+replacedSuffix); err != nil {
			return fmt.Errorf("setting the old database aside: %w", err)
		}
		cout.Printf("the one it replaces is kept as <cyan>%s</>\n", d.path+replacedSuffix)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		// the old database's journal must not be read as the new one's
		if err := os.Remove(d.path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clearing the old database's journal: %w", err)
		}
		_ = os.Remove(upload + suffix)
	}
	if err := os.Rename(upload, d.path); err != nil {
		return fmt.Errorf("moving the upload into place: %w", err)
	}
	_ = os.Chmod(d.path, 0o644) //nolint:gosec // a temporary file is private; the database is as readable as one sqlite made
	return d.rebuild()
}
