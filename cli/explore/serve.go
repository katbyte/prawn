package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/cout"
)

// serve keeps the written page available over http until interrupted, so
// other machines on the network can open it — the page is one file with
// its data embedded, so this is a file server for exactly that file, plus
// /refresh: the page's button to sync and rebuild it. rebuild is what that
// button runs; startup, when not nil, runs once as the first refresh as soon
// as the page is up. /db hands out the database at dbPath and takes a
// replacement, after which page rewrites the page without syncing. Only
// admins may refresh or replace the database; anyone may when it is empty.
func serve(path, addr, dbPath string, admins admins, rebuild, startup, page func() error) error {
	if !strings.Contains(addr, ":") {
		addr = ":" + addr // a bare port
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/explore.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache") // a regenerated page must win over a cached one
		http.ServeFile(w, r, path)
	})
	// what a build prints, and what it warns of, goes where it always did and — while a refresh
	// runs — into that refresh's log, for the page's status window. Set once, before anything serves.
	rf := &refresher{run: rebuild, admins: admins}
	stdout, logOut := cout.Out, clog.Log.Out
	cout.Out = io.MultiWriter(stdout, &rf.log)
	clog.Log.SetOutput(io.MultiWriter(logOut, &rf.log))
	defer func() { cout.Out = stdout; clog.Log.SetOutput(logOut) }()
	mux.Handle("/refresh", rf)
	mux.Handle("/db", &dbFile{path: dbPath, rf: rf, rebuild: page, admins: admins})
	// the request log goes straight to the terminal: who loaded the page is not part of a refresh
	srv := &http.Server{Addr: addr, Handler: logged(mux, stdout), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	port := ""
	if _, p, perr := net.SplitHostPort(ln.Addr().String()); perr == nil {
		port = p // the real port, when 0 asked for a free one
	}
	cout.Printf("\nserving <cyan>%s</> — ctrl-c stops\n", path)
	if len(admins) > 0 {
		cout.Printf("  refresh and upload: <yellow>%s</> only, by the login the proxy in front sets\n", admins)
	}
	for _, h := range reachableHosts(addr) {
		cout.Printf("  <cyan>http://%s:%s/</>\n", h, port)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	if startup != nil {
		rf.begin("startup", "", startup, false)
	}
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cout.Printf("\nstopping\n")
		return srv.Shutdown(shutdownCtx)
	}
}

// refreshGap is how soon after a refresh another may start: the button is
// for "it moved since I opened this", not for holding down.
const refreshGap = 30 * time.Second

// refresher is the page's refresh button: POST starts a sync and rebuild
// unless one is running or only just finished, GET reports how it is going.
// One runs at a time, whoever asked; the page polls and reloads when it ends.
type refresher struct {
	run    func() error
	admins admins // who may start one; anyone may watch

	mu       sync.Mutex
	running  bool
	started  time.Time
	finished time.Time
	by       string
	what     string // "" for a sync and rebuild, "upload" for a database swapped in
	err      string

	log runLog // what the running refresh has printed; the last one's once it ends
}

// refreshStatus is what the page polls. Log is only sent when asked for
// (?from=<offset>): the text printed since that offset, LogEnd the offset to
// ask from next.
type refreshStatus struct {
	Running  bool   `json:"running"`
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
	By       string `json:"by,omitempty"`
	What     string `json:"what,omitempty"`
	Error    string `json:"error,omitempty"`
	Admin    bool   `json:"admin"` // whether the one asking may start a refresh
	Log      string `json:"log,omitempty"`
	LogEnd   int    `json:"logEnd,omitempty"`
}

// runLogMax bounds a refresh's kept output: a first walk of a big repo prints
// a line every few PRs, and the status window needs the story, not all of it.
const runLogMax = 1 << 20

// ansi matches the colour codes the terminal output carries.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// runLog collects what is printed while a refresh runs, colour codes
// stripped. It is written to by everything that prints, from any goroutine,
// and ignores what arrives between refreshes.
type runLog struct {
	mu  sync.Mutex
	on  bool
	buf []byte
}

func (l *runLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.on && len(l.buf) < runLogMax {
		l.buf = append(l.buf, ansi.ReplaceAll(p, nil)...)
	}
	return len(p), nil
}

// begin starts a fresh log; end stops collecting and keeps what there is.
func (l *runLog) begin() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on, l.buf = true, nil
}

func (l *runLog) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = false
}

// since returns what was printed after offset from, and the offset it ends at.
func (l *runLog) since(from int) (text string, end int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from < 0 || from > len(l.buf) {
		from = 0 // a new refresh since the page last asked: from the top
	}
	return string(l.buf[from:]), len(l.buf)
}

func (rf *refresher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		// a header a form on another site cannot send, so only this page's own script starts a refresh
		if r.Header.Get("X-Prawn") == "" {
			http.Error(w, "refresh is the page's own button", http.StatusForbidden)
			return
		}
		if !rf.admins.allow(r) {
			http.Error(w, "only an admin may refresh (PRAWN_ADMINS)", http.StatusForbidden)
			return
		}
		rf.start(viewer(r))
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
		return
	}
	st := rf.status()
	st.Admin = rf.admins.allow(r)
	if from, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil {
		st.Log, st.LogEnd = rf.log.since(from)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}

// start kicks off a refresh unless one is running or one finished moments ago.
func (rf *refresher) start(by string) { rf.begin(by, "", rf.run, false) }

// begin is start running something other than the button's rebuild — the sync
// a server does once it is up, an uploaded database going in — and says
// whether it started. Nothing starts while another runs; force skips the wait
// after one has finished, for what somebody did rather than pressed.
func (rf *refresher) begin(by, what string, run func() error, force bool) bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.running || (!force && !rf.finished.IsZero() && time.Since(rf.finished) < refreshGap) {
		return false
	}
	rf.running, rf.started, rf.by, rf.what, rf.err = true, time.Now(), by, what, ""
	rf.log.begin()
	go func() {
		err := run()
		if err != nil {
			cout.Printf("<red>refresh failed:</> %v\n", err)
		}
		rf.log.end()
		rf.mu.Lock()
		defer rf.mu.Unlock()
		rf.running, rf.finished = false, time.Now()
		if err != nil {
			rf.err = err.Error()
		}
	}()
	return true
}

func (rf *refresher) status() refreshStatus {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	st := refreshStatus{Running: rf.running, By: rf.by, What: rf.what, Error: rf.err}
	if !rf.started.IsZero() {
		st.Started = rf.started.UTC().Format(time.RFC3339)
	}
	if !rf.finished.IsZero() {
		st.Finished = rf.finished.UTC().Format(time.RFC3339)
	}
	return st
}

// userHeaders are where a login-aware proxy in front (traefik's github auth
// plugins, oauth2-proxy, authelia) puts the viewer's login; the first one set
// names the viewer in the log, and decides who is an admin. Any client can
// send them, so PRAWN_ADMINS means something only when that proxy is the one
// way in (it replaces whatever the client sent) and the port is not open
// to anyone else.
var userHeaders = []string{"X-Forwarded-User", "X-Auth-Request-User", "X-Auth-User", "Remote-User"}

// logged writes one line per request — who, from where, what, how it went —
// so the container's log (or the terminal) shows who is looking at what.
func logged(next http.Handler, out io.Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.Method == http.MethodGet && (r.URL.Path == "/refresh" || (r.URL.Path == "/db" && r.URL.Query().Has("info"))) {
			return // the page polling a refresh, or sizing up the database: nothing anyone did
		}
		_, _ = fmt.Fprint(out, cout.Sprintf("<gray>%s</> %s <cyan>%s</> %s %s %s <gray>%s %s</>\n",
			start.Format("2006-01-02 15:04:05"), viewer(r), from(r), r.Method, r.URL.Path, statusColour(sw.status), humanSize(sw.bytes), time.Since(start).Round(time.Millisecond)))
	})
}

// viewer is the login the proxy in front vouched for, "-" without one.
func viewer(r *http.Request) string {
	for _, h := range userHeaders {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return "-"
}

// admins is who may change things on a served page — start a refresh, upload
// a database — by login, as the proxy in front names the viewer. Empty lets
// anyone: a server without a login in front has no one to tell apart.
type admins map[string]bool

// newAdmins reads a comma-separated list of logins; github's are case-blind.
func newAdmins(list string) admins {
	a := admins{}
	for login := range strings.SplitSeq(list, ",") {
		if login = strings.ToLower(strings.TrimSpace(login)); login != "" {
			a[login] = true
		}
	}
	return a
}

func (a admins) allow(r *http.Request) bool {
	return len(a) == 0 || a[strings.ToLower(viewer(r))]
}

func (a admins) String() string {
	logins := make([]string, 0, len(a))
	for login := range a {
		logins = append(logins, login)
	}
	slices.Sort(logins)
	return strings.Join(logins, ", ")
}

// from is the client's address: the first hop in X-Forwarded-For when a
// proxy set it, else the connection's own.
func from(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func statusColour(status int) string {
	if status >= 400 {
		return fmt.Sprintf("<red>%d</>", status)
	}
	return fmt.Sprintf("<green>%d</>", status)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%dKB", n/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

// statusWriter remembers the status and the bytes sent for the log line.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// reachableHosts names the addresses the page can be opened on: localhost,
// the machine's hostname, and each non-loopback IPv4 address — or just the
// bound address when the listener is pinned to one.
func reachableHosts(addr string) []string {
	host, _, _ := net.SplitHostPort(addr)
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{host}
	}
	hosts := []string{"localhost"}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		hn = strings.ToLower(hn)
		if !strings.Contains(hn, ".") {
			hn += ".local" // the name other machines resolve over mdns
		}
		hosts = append(hosts, hn)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return hosts
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			hosts = append(hosts, ipn.IP.String())
		}
	}
	return hosts
}
