// Package server serves Kubernetes Job logs from Loki as plain text, in the
// shape Kargo's AnalysisRun log view expects.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/browntech/kargo-loki-logs/internal/loki"
)

// maxNameLen is the Kubernetes limit for a DNS subdomain name.
const maxNameLen = 253

// namePattern is the Kubernetes DNS subdomain name pattern. Only names that
// match it reach a LogQL query.
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// LogSource is the part of the Loki client the server needs.
type LogSource interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]loki.Entry, error)
	VolumeWindow(ctx context.Context, selector string, start, end time.Time, step time.Duration) (loki.Window, error)
}

// Labels are the Loki label names that hold the namespace, pod and container.
type Labels struct {
	Namespace string
	Pod       string
	Container string
}

// Config configures a Server.
type Config struct {
	Source LogSource
	Labels Labels
	// Since is how far back the log query looks when the window lookup gives
	// no window.
	Since time.Duration
	// Lookback is how far back the window lookup searches. It reads only the
	// Loki index, so it can be much longer than Since.
	Lookback time.Duration
	// Limit is the maximum number of lines returned.
	Limit int
	// WindowStep is the bucket size of the volume lookup.
	WindowStep time.Duration
	// Token, when not empty, must be presented in the Authorization header.
	Token string
	// Now returns the current time. Nil means time.Now.
	Now    func() time.Time
	Logger *slog.Logger
}

// Server is the HTTP handler.
type Server struct {
	cfg       Config
	tokenHash [sha256.Size]byte
}

// New returns a Server for cfg.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{cfg: cfg}
	if cfg.Token != "" {
		s.tokenHash = sha256.Sum256([]byte(cfg.Token))
	}
	return s
}

// ServeHTTP routes requests. It does not use http.ServeMux, so that an empty
// path segment gets a 400 and not a redirect to a cleaned path.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	case strings.HasPrefix(r.URL.Path, "/logs/"):
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.serveLogs(w, r)
	default:
		http.NotFound(w, r)
	}
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/logs/"), "/")
	if len(parts) != 3 {
		http.Error(w, "path must be /logs/{jobNamespace}/{jobName}/{container}", http.StatusBadRequest)
		return
	}
	for i, field := range []string{"jobNamespace", "jobName", "container"} {
		if !validName(parts[i]) {
			http.Error(w, "invalid "+field+": must be a Kubernetes name", http.StatusBadRequest)
			return
		}
	}
	ns, job, container := parts[0], parts[1], parts[2]

	selector := Selector(s.cfg.Labels, ns, job, container)
	end := s.cfg.Now()
	start := end.Add(-s.cfg.Since)

	win, err := s.cfg.Source.VolumeWindow(r.Context(), selector, end.Add(-s.cfg.Lookback), end, s.cfg.WindowStep)
	switch {
	case err == nil:
		start, end = win.Start, win.End
	case errors.Is(err, loki.ErrNoData):
		s.cfg.Logger.Debug("volume lookup found no data, using default window", "selector", selector)
	default:
		s.cfg.Logger.Warn("volume lookup failed, using default window", "selector", selector, "err", err)
	}

	entries, err := s.cfg.Source.QueryRange(r.Context(), selector, start, end, s.cfg.Limit)
	if err != nil {
		s.cfg.Logger.Error("loki query failed", "selector", selector, "err", err)
		http.Error(w, "log backend error", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(strings.TrimSuffix(e.Line, "\n"))
		b.WriteByte('\n')
	}
	_, _ = w.Write([]byte(b.String()))
}

// authorized reports whether the request carries the configured token. The
// header may hold the bare token or "Bearer <token>". Both sides are hashed
// first so the comparison takes constant time whatever their lengths.
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	if after, ok := cutPrefixFold(got, "Bearer "); ok {
		got = after
	}
	if got == "" {
		return false
	}
	h := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(h[:], s.tokenHash[:]) == 1
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

func validName(s string) bool {
	return len(s) <= maxNameLen && namePattern.MatchString(s)
}

// Selector builds the LogQL stream selector for a Job's container. The values
// must already have passed validName; jobName is regex-quoted and every value
// is escaped for a LogQL double-quoted string.
func Selector(l Labels, ns, job, container string) string {
	return fmt.Sprintf(`{%s="%s", %s=~"%s", %s="%s"}`,
		l.Namespace, logqlString(ns),
		l.Pod, logqlString(regexp.QuoteMeta(job)+"-.*"),
		l.Container, logqlString(container))
}

// logqlString escapes s for use inside a LogQL double-quoted string.
func logqlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}
