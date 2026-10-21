// Package app is the timesheet / project management web app: a JSON API plus the
// built Vue front end, authenticating users through the OIDC provider.
package app

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrations() embed.FS { return migrationsFS }

const MigrationsDir = "migrations"

type Config struct {
	AppURL           string // public base URL, no trailing slash
	SecureCookies    bool
	WebDist          string // directory of the built SPA; "" = API only
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	// OIDCInternalURL, if set (e.g. http://idp:8081), is where server-to-server OIDC calls
	// (discovery, token exchange, JWKS) are sent instead of the public issuer host.
	OIDCInternalURL string
	TimeZone        *time.Location // company time zone for clock-in dates; nil = server local
	Mail            MailConfig
}

type Server struct {
	cfg  Config
	db   *pgxpool.Pool
	oidc *oidcClient
	loc  *time.Location
}

func New(cfg Config, db *pgxpool.Pool) *Server {
	cfg.AppURL = strings.TrimRight(cfg.AppURL, "/")
	loc := cfg.TimeZone
	if loc == nil {
		loc = time.Local
	}
	return &Server{cfg: cfg, db: db, oidc: &oidcClient{cfg: cfg}, loc: loc}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/me", h(s.handleMe))

	api.HandleFunc("GET /api/workers", h(s.listWorkers))
	api.HandleFunc("POST /api/workers", manager(s.createWorker))
	api.HandleFunc("PUT /api/workers/{id}", manager(s.updateWorker))
	api.HandleFunc("DELETE /api/workers/{id}", manager(s.deleteWorker))

	api.HandleFunc("GET /api/clients", manager(s.listClients))
	api.HandleFunc("POST /api/clients", manager(s.createClient))
	api.HandleFunc("GET /api/clients/{id}", manager(s.getClient))
	api.HandleFunc("PUT /api/clients/{id}", manager(s.updateClient))
	api.HandleFunc("DELETE /api/clients/{id}", manager(s.deleteClient))

	api.HandleFunc("GET /api/projects", h(s.listProjects))
	api.HandleFunc("POST /api/projects", manager(s.createProject))
	api.HandleFunc("GET /api/projects/{id}", h(s.getProject))
	api.HandleFunc("PUT /api/projects/{id}", manager(s.updateProject))
	api.HandleFunc("DELETE /api/projects/{id}", manager(s.deleteProject))
	api.HandleFunc("POST /api/projects/{id}/tasks", manager(s.createTask))
	api.HandleFunc("POST /api/projects/{id}/apply-template", manager(s.applyTemplateToProject))
	api.HandleFunc("POST /api/projects/{id}/save-as-template", manager(s.saveProjectAsTemplate))
	api.HandleFunc("POST /api/projects/{id}/profit-share", manager(s.postProfitShare))

	api.HandleFunc("GET /api/templates", manager(s.listTemplates))
	api.HandleFunc("POST /api/templates", manager(s.createTemplate))
	api.HandleFunc("PUT /api/templates/{id}", manager(s.updateTemplate))
	api.HandleFunc("DELETE /api/templates/{id}", manager(s.deleteTemplate))

	api.HandleFunc("GET /api/clock", h(s.getClock))
	api.HandleFunc("GET /api/clock/active", manager(s.activeClocks))
	api.HandleFunc("POST /api/clock/in", h(s.clockIn))
	api.HandleFunc("POST /api/clock/out", h(s.clockOut))
	api.HandleFunc("POST /api/clock/switch", h(s.clockSwitch))
	api.HandleFunc("DELETE /api/clock", h(s.cancelClock))

	api.HandleFunc("GET /api/tasks", h(s.listTasks))
	api.HandleFunc("PATCH /api/tasks/{id}", h(s.updateTask))
	api.HandleFunc("DELETE /api/tasks/{id}", manager(s.deleteTask))

	api.HandleFunc("GET /api/time-entries", h(s.listTimeEntries))
	api.HandleFunc("POST /api/time-entries", h(s.createTimeEntry))
	api.HandleFunc("PUT /api/time-entries/{id}", h(s.updateTimeEntry))
	api.HandleFunc("DELETE /api/time-entries/{id}", h(s.deleteTimeEntry))

	api.HandleFunc("GET /api/invoices", manager(s.listInvoices))
	api.HandleFunc("POST /api/invoices", manager(s.createInvoice))
	api.HandleFunc("GET /api/invoices/{id}", manager(s.getInvoice))
	api.HandleFunc("PATCH /api/invoices/{id}", manager(s.updateInvoice))
	api.HandleFunc("DELETE /api/invoices/{id}", manager(s.deleteInvoice))
	api.HandleFunc("POST /api/invoices/{id}/lines", manager(s.addInvoiceLine))
	api.HandleFunc("DELETE /api/invoices/{id}/lines/{lineID}", manager(s.deleteInvoiceLine))
	api.HandleFunc("GET /api/invoices/{id}/pdf", manager(s.invoicePDF))
	api.HandleFunc("POST /api/invoices/{id}/send", manager(s.sendInvoice))
	api.HandleFunc("GET /api/settings", manager(s.getSettings))
	api.HandleFunc("PUT /api/settings", admin(s.updateSettings))
	api.HandleFunc("GET /api/billing/unbilled", manager(s.unbilledSummary))

	api.HandleFunc("GET /api/payroll/unpaid", manager(s.unpaidSummary))
	api.HandleFunc("GET /api/pay-runs", manager(s.listPayRuns))
	api.HandleFunc("POST /api/pay-runs", manager(s.createPayRun))
	api.HandleFunc("GET /api/pay-runs/{id}", manager(s.getPayRun))
	api.HandleFunc("PATCH /api/pay-runs/{id}", manager(s.updatePayRun))
	api.HandleFunc("DELETE /api/pay-runs/{id}", manager(s.deletePayRun))
	api.HandleFunc("GET /api/adjustments", manager(s.listAdjustments))
	api.HandleFunc("POST /api/adjustments", manager(s.createAdjustment))
	api.HandleFunc("DELETE /api/adjustments/{id}", manager(s.deleteAdjustment))

	api.HandleFunc("GET /api/reports/projects", manager(s.projectProfitReport))
	api.HandleFunc("GET /api/reports/dashboard", h(s.dashboard))

	api.HandleFunc("GET /api/users", admin(s.listUsers))
	api.HandleFunc("PATCH /api/users/{id}", admin(s.updateUser))

	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
	})

	root := http.NewServeMux()
	root.Handle("/api/", requireAuth(api))
	root.HandleFunc("GET /auth/login", h(s.handleLogin))
	root.HandleFunc("GET /auth/callback", h(s.handleCallback))
	root.Handle("POST /auth/logout", requireAuth(h(s.handleLogout)))
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	if s.cfg.WebDist != "" {
		root.Handle("/", spaHandler(s.cfg.WebDist))
	}
	return securityHeaders(logRequests(s.withSession(root)))
}

// spaHandler serves static files and falls back to index.html for client-side routes.
func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if info, err := os.Stat(filepath.Join(dir, clean)); err == nil && !info.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Cleanup periodically removes expired sessions and login states.
func (s *Server) Cleanup(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, q := range []string{
			`DELETE FROM sessions WHERE expires_at < now()`,
			`DELETE FROM login_states WHERE expires_at < now()`,
		} {
			if _, err := s.db.Exec(ctx, q); err != nil && ctx.Err() == nil {
				slog.Warn("cleanup", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- query helpers ------------------------------------------------------------

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// collect runs a query and maps rows onto T by `db` struct tags. Never returns a nil slice.
func collect[T any](ctx context.Context, q querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []T{}
	}
	return out, nil
}

// one runs a query expected to return exactly one row (pgx.ErrNoRows -> 404).
func one[T any](ctx context.Context, q querier, sql string, args ...any) (T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByNameLax[T])
}
