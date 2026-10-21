// Package idp is an OpenID Connect provider (authorization code + PKCE, refresh tokens,
// RP-initiated logout, single sign-on) built on github.com/zitadel/oidc, backed by Postgres.
package idp

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations returns the embedded SQL migrations (file names at the root).
func Migrations() embed.FS { return migrationsFS }

// MigrationsDir is the directory inside Migrations() holding the .sql files.
const MigrationsDir = "migrations"

type Config struct {
	Issuer        string
	CryptoKey     string
	AllowSignup   bool
	SecureCookies bool
	// TrustProxy takes the client IP from X-Forwarded-For (set it only behind a reverse proxy
	// such as Caddy that overwrites the header). Used for login rate limiting and session records.
	TrustProxy bool
	Clients    []ClientConfig
}

// NewHandler builds the IdP HTTP handler: OIDC endpoints plus the login UI.
func NewHandler(ctx context.Context, db *pgxpool.Pool, cfg Config) (http.Handler, *Storage, error) {
	if len(cfg.CryptoKey) < 32 {
		return nil, nil, fmt.Errorf("IDP_CRYPTO_KEY must be at least 32 characters")
	}
	storage, err := NewStorage(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range cfg.Clients {
		if err := storage.UpsertClient(ctx, c); err != nil {
			return nil, nil, fmt.Errorf("register client %s: %w", c.ID, err)
		}
		slog.Info("registered OIDC client", "client_id", c.ID, "redirect_uris", c.RedirectURIs)
	}

	opConfig := &op.Config{
		CryptoKey:                sha256.Sum256([]byte(cfg.CryptoKey)),
		DefaultLogoutRedirectURI: "/logged-out",
		CodeMethodS256:           true,
		AuthMethodPost:           true,
		GrantTypeRefreshToken:    true,
		SupportedUILocales:       []language.Tag{language.English},
	}
	var opts []op.Option
	if strings.HasPrefix(cfg.Issuer, "http://") {
		opts = append(opts, op.WithAllowInsecure())
	}
	provider, err := op.NewOpenIDProvider(cfg.Issuer, opConfig, storage, opts...)
	if err != nil {
		return nil, nil, err
	}

	sessions := &sessionStore{db: db, secure: cfg.SecureCookies}
	ui := &loginUI{
		storage:     storage,
		users:       storage.users,
		sessions:    sessions,
		callbackURL: op.AuthCallbackURL(provider),
		allowSignup: cfg.AllowSignup,
		secure:      cfg.SecureCookies,
		limiter:     newFailureLimiter(10, 15*time.Minute),
	}
	issuer := op.NewIssuerInterceptor(provider.IssuerFromRequest)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", issuer.HandlerFunc(ui.loginPage))
	mux.HandleFunc("POST /login", issuer.HandlerFunc(ui.loginSubmit))
	mux.HandleFunc("GET /signup", issuer.HandlerFunc(ui.signupPage))
	mux.HandleFunc("POST /signup", issuer.HandlerFunc(ui.signupSubmit))
	mux.HandleFunc("GET /account", ui.accountPage)
	mux.HandleFunc("POST /account/logout", ui.accountLogout)
	mux.HandleFunc("GET /logged-out", ui.loggedOut)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/account", http.StatusFound)
	})

	// /authorize needs the SSO session in context so prompt=none can be answered.
	mux.Handle("/authorize", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := sessions.current(r.Context(), r)
		if err != nil {
			slog.Error("load session", "err", err)
		}
		provider.ServeHTTP(w, r.WithContext(withSession(r.Context(), sess)))
	}))
	// RP-initiated logout also ends the IdP browser session (single sign-out for this browser).
	mux.Handle("/end_session", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := sessions.destroy(r.Context(), w, r); err != nil {
			slog.Error("destroy session", "err", err)
		}
		provider.ServeHTTP(w, r)
	}))
	mux.Handle("/", provider)

	var handler http.Handler = logRequests(mux)
	if cfg.TrustProxy {
		handler = realIP(handler)
	}
	return handler, storage, nil
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
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

// realIP replaces RemoteAddr with the proxy-reported client address (the rightmost
// X-Forwarded-For entry, which the proxy itself appended).
func realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				r.RemoteAddr = net.JoinHostPort(ip, "0")
			}
		}
		next.ServeHTTP(w, r)
	})
}
