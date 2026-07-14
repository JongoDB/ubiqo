// Package httpapi assembles the HTTP surface: /mcp (streamable, stateless,
// bearer-authenticated), the ops endpoints DevOps review demanded (/healthz,
// /readyz, /metrics, /v1/meta), and the two REST endpoints the session hooks
// use (fail-open clients need plain text, not MCP framing).
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jongodb/ubiqo/internal/authz"
	"github.com/jongodb/ubiqo/internal/core"
	"github.com/jongodb/ubiqo/internal/mcpserver"
	"github.com/jongodb/ubiqo/internal/store"
)

// MinCLIVersion is the oldest ubiqo CLI/hook allowed to talk to this server
// (version-skew handshake; clients check /v1/meta).
const MinCLIVersion = "0.1.0"

var (
	reqs = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ubiqo_http_requests_total", Help: "HTTP requests by route and code.",
	}, []string{"route", "code"})
	authFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ubiqo_auth_failures_total", Help: "Rejected bearer tokens.",
	})
)

type Server struct {
	Svc    *core.Service
	Log    *slog.Logger
	Public string // canonical public URL ("" in --local mode)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := s.Svc.DB.Ping(ctx); err != nil {
			http.Error(w, "postgres: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := s.Svc.Git.HealthCheck(); err != nil {
			http.Error(w, "git data dir: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /v1/meta", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"server":          "ubiqo",
			"server_version":  s.Svc.Version,
			"min_cli_version": MinCLIVersion,
			"mcp_url":         s.Public + "/mcp",
		})
	})

	// MCP: stateless streamable HTTP; every request re-authenticates.
	mcpSrv := mcpserver.New(s.Svc)
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpSrv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	mux.Handle("/mcp", s.auth(mcpHandler))

	// Hook endpoints (plain responses so the CLI can cache them verbatim).
	mux.Handle("GET /v1/context", s.auth(http.HandlerFunc(s.handleContext)))
	mux.Handle("POST /v1/session-end", s.auth(http.HandlerFunc(s.handleSessionEnd)))

	return s.instrument(mux)
}

// auth resolves the bearer token to an Actor in the request context.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || !strings.HasPrefix(token, "ubq_") {
			authFailures.Inc()
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, `{"reason":"missing or malformed bearer token","fix":"Authorization: Bearer ubq_... (ubiqo device create)"}`, http.StatusUnauthorized)
			return
		}
		ti, err := s.Svc.DB.Authenticate(r.Context(), token)
		if err != nil {
			authFailures.Inc()
			code := http.StatusUnauthorized
			msg := `{"reason":"unknown or revoked device token"}`
			if !errors.Is(err, store.ErrNotFound) {
				code = http.StatusServiceUnavailable
				msg = `{"reason":"auth backend unavailable"}`
			}
			http.Error(w, msg, code)
			return
		}
		actor := &core.Actor{
			UserID: ti.UserID, Username: ti.Username,
			OrgID: ti.OrgID, OrgSlug: ti.OrgSlug, OrgRole: ti.OrgRole,
			Identity: ti.Label,
		}
		next.ServeHTTP(w, r.WithContext(core.WithActor(r.Context(), actor)))
	})
}

// handleContext returns the session-start text: status line + instructions +
// digest + memories. The CLI caches this body verbatim for fail-open.
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	actor, _ := core.ActorFrom(r.Context())
	project := r.URL.Query().Get("project")
	if project == "" {
		http.Error(w, "missing ?project=<slug>", http.StatusBadRequest)
		return
	}
	bundle, err := s.Svc.GetContext(r.Context(), actor, project, r.URL.Query().Get("section"))
	if err != nil {
		writeCoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Ubiqo-Bundle-Version", bundle.BundleVersion)
	var b strings.Builder
	b.WriteString(bundle.Instructions)
	if bundle.Digest != "" {
		b.WriteString("\n\n" + bundle.Digest)
	}
	if bundle.Memories != "" {
		b.WriteString("\n\n" + bundle.Memories)
	}
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	actor, _ := core.ActorFrom(r.Context())
	var in struct {
		Project string `json:"project"`
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.Svc.RecordSessionEnd(r.Context(), actor, in.Project, in.Summary); err != nil {
		writeCoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// writeCoreError maps service errors to transport codes: structured denials
// are 403 with the JSON body agents learn from, unknown projects 404, and
// everything else 500 — internal failures must not masquerade as authz.
func writeCoreError(w http.ResponseWriter, err error) {
	var denial *authz.Denial
	switch {
	case errors.As(err, &denial):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(denial)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// instrument wraps everything with structured request logs + metrics.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		route := r.URL.Path
		if len(route) > 40 {
			route = route[:40]
		}
		reqs.WithLabelValues(route, strconv.Itoa(rw.code)).Inc()
		s.Log.Info("http",
			"method", r.Method, "path", r.URL.Path, "code", rw.code,
			"dur_ms", time.Since(start).Milliseconds())
	})
}

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(c int) {
	r.code = c
	r.ResponseWriter.WriteHeader(c)
}

// Flush keeps SSE streaming working through the recorder (MCP GET streams).
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
