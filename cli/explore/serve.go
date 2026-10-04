package explore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/katbyte/go-kt/cout"
)

// serve keeps the written page available over http until interrupted, so
// other machines on the network can open it — the page is one file with
// its data embedded, so this is a file server for exactly that file.
func serve(path, addr string) error {
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
	srv := &http.Server{Addr: addr, Handler: logged(mux), ReadHeaderTimeout: 10 * time.Second}

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
	for _, h := range reachableHosts(addr) {
		cout.Printf("  <cyan>http://%s:%s/</>\n", h, port)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
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

// userHeaders are where a login-aware proxy in front (traefik's github auth
// plugins, oauth2-proxy, authelia) puts the viewer's login; the first one set
// names the viewer in the log. They mean nothing without such a proxy — any
// client can send them — so they are a label, never a decision.
var userHeaders = []string{"X-Forwarded-User", "X-Auth-Request-User", "X-Auth-User", "Remote-User"}

// logged writes one line per request — who, from where, what, how it went —
// so the container's log (or the terminal) shows who is looking at what.
func logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		cout.Printf("<gray>%s</> %s <cyan>%s</> %s %s %s <gray>%s %s</>\n",
			start.Format("2006-01-02 15:04:05"), viewer(r), from(r), r.Method, r.URL.Path, statusColour(sw.status), humanSize(sw.bytes), time.Since(start).Round(time.Millisecond))
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
