package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

const (
	sessionCookie = "crm_session"
	stateCookie   = "crm_login_state"
	sessionTTL    = 14 * 24 * time.Hour
)

// User is the signed-in app user.
type User struct {
	ID       int64  `json:"id"`
	Subject  string `json:"-"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	WorkerID *int64 `json:"worker_id"`
}

func (u *User) isManager() bool { return u.Role == "admin" || u.Role == "manager" }
func (u *User) isAdmin() bool   { return u.Role == "admin" }

type session struct {
	ID        string
	CSRFToken string
	IDToken   string
	User      *User
}

type ctxKey int

const sessionKey ctxKey = 1

func currentSession(ctx context.Context) *session {
	s, _ := ctx.Value(sessionKey).(*session)
	return s
}

func currentUser(r *http.Request) *User {
	if s := currentSession(r.Context()); s != nil {
		return s.User
	}
	return nil
}

// oidcClient is initialised lazily so the app can start before the IdP is reachable.
type oidcClient struct {
	mu         sync.Mutex
	cfg        Config
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      *oauth2.Config
	endSession string
}

func (c *oidcClient) get(ctx context.Context) (*oidcClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if hc := c.httpClient(); hc != nil {
		ctx = oidc.ClientContext(ctx, hc) // also used later by the JWKS key set
	}
	p, err := oidc.NewProvider(ctx, c.cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery at %s: %w", c.cfg.OIDCIssuer, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := p.Claims(&meta); err != nil {
		return nil, err
	}
	c.provider = p
	c.endSession = meta.EndSession
	c.verifier = p.Verifier(&oidc.Config{ClientID: c.cfg.OIDCClientID})
	c.oauth = &oauth2.Config{
		ClientID:     c.cfg.OIDCClientID,
		ClientSecret: c.cfg.OIDCClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  c.cfg.AppURL + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return c, nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// safeReturnTo keeps post-login redirects on this site.
func safeReturnTo(p string) string {
	u, err := url.Parse(p)
	if err != nil || p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return p
}

func (s *Server) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode}
	if maxAge < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(maxAge.Seconds())
	}
	return c
}

// GET /auth/login?return_to=/path
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	oc, err := s.oidc.get(r.Context())
	if err != nil {
		slog.Error("oidc unavailable", "err", err)
		http.Error(w, "The sign-in service is unavailable. Please try again shortly.", http.StatusServiceUnavailable)
		return nil
	}
	state, nonce, verifier := randomString(24), randomString(24), oauth2.GenerateVerifier()
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO login_states (state, nonce, verifier, return_to, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '10 minutes')`,
		state, nonce, verifier, safeReturnTo(r.URL.Query().Get("return_to"))); err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(stateCookie, state, 10*time.Minute))
	http.Redirect(w, r, oc.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
	return nil
}

// GET /auth/callback
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	fail := func(msg string, err error) error {
		if err != nil {
			slog.Warn("login failed", "reason", msg, "err", err)
		}
		http.SetCookie(w, s.cookie(stateCookie, "", -1))
		http.Error(w, "Sign-in failed: "+msg+". Go back and try again.", http.StatusBadRequest)
		return nil
	}
	if e := q.Get("error"); e != "" {
		return fail(e, errors.New(q.Get("error_description")))
	}
	c, err := r.Cookie(stateCookie)
	state := q.Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		return fail("state mismatch", err)
	}
	var nonce, verifier, returnTo string
	err = s.db.QueryRow(ctx, `
		DELETE FROM login_states WHERE state = $1 AND expires_at > now()
		RETURNING nonce, verifier, return_to`, state).Scan(&nonce, &verifier, &returnTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail("login expired", nil)
	}
	if err != nil {
		return err
	}
	oc, err := s.oidc.get(ctx)
	if err != nil {
		return err
	}
	exCtx := ctx
	if hc := oc.httpClient(); hc != nil {
		exCtx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	}
	token, err := oc.oauth.Exchange(exCtx, q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		return fail("code exchange failed", err)
	}
	rawID, _ := token.Extra("id_token").(string)
	idToken, err := oc.verifier.Verify(ctx, rawID)
	if err != nil {
		return fail("invalid ID token", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return fail("nonce mismatch", nil)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fail("unreadable claims", err)
	}

	userID, err := s.upsertUser(ctx, idToken.Subject, claims.Email, claims.Name)
	if err != nil {
		return err
	}
	sessToken := randomString(32)
	if _, err := s.db.Exec(ctx, `
		INSERT INTO sessions (id, user_id, csrf_token, id_token, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		sha256Hex(sessToken), userID, randomString(24), rawID, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(stateCookie, "", -1))
	http.SetCookie(w, s.cookie(sessionCookie, sessToken, sessionTTL))
	http.Redirect(w, r, safeReturnTo(returnTo), http.StatusFound)
	return nil
}

// upsertUser records the login. The very first user becomes admin; a new user is
// linked to the worker with the same email if one exists and is unlinked.
func (s *Server) upsertUser(ctx context.Context, subject, email, name string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	var id int64
	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO users (subject, email, name, role, last_login_at)
		VALUES ($1, $2, $3, CASE WHEN EXISTS (SELECT 1 FROM users) THEN 'worker' ELSE 'admin' END, now())
		ON CONFLICT (subject) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name, last_login_at = now()
		RETURNING id, (xmax = 0)`, subject, email, name).Scan(&id, &inserted)
	if err != nil {
		return 0, err
	}
	if inserted && email != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET worker_id = (
			  SELECT w.id FROM workers w
			  WHERE lower(w.email) = lower($2) AND NOT EXISTS (SELECT 1 FROM users u WHERE u.worker_id = w.id)
			  ORDER BY w.id LIMIT 1)
			WHERE id = $1`, id, email); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

// POST /auth/logout — ends the app session and returns the IdP logout URL.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	sess := currentSession(r.Context())
	redirect := "/"
	if sess != nil {
		if _, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE id = $1`, sess.ID); err != nil {
			return err
		}
		if oc, err := s.oidc.get(r.Context()); err == nil && oc.endSession != "" {
			v := url.Values{
				"id_token_hint":            {sess.IDToken},
				"post_logout_redirect_uri": {s.cfg.AppURL + "/"},
				"client_id":                {s.cfg.OIDCClientID},
			}
			redirect = oc.endSession + "?" + v.Encode()
		}
	}
	http.SetCookie(w, s.cookie(sessionCookie, "", -1))
	return ok(w, map[string]string{"redirect": redirect})
}

// withSession loads the session (if any) into the request context.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess := &session{User: &User{}}
		err = s.db.QueryRow(r.Context(), `
			SELECT s.id, s.csrf_token, s.id_token, u.id, u.subject, u.email, u.name, u.role, u.worker_id
			FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.id = $1 AND s.expires_at > now()`, sha256Hex(c.Value)).
			Scan(&sess.ID, &sess.CSRFToken, &sess.IDToken, &sess.User.ID, &sess.User.Subject, &sess.User.Email,
				&sess.User.Name, &sess.User.Role, &sess.User.WorkerID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				slog.Error("load session", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

// requireAuth rejects anonymous API calls and enforces the CSRF header on writes.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := currentSession(r.Context())
		if sess == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": errUnauthorized.Msg})
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRFToken)) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid CSRF token; reload the page"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// manager wraps a handler that only admins and managers may call.
func manager(fn handlerFunc) http.HandlerFunc {
	return h(func(w http.ResponseWriter, r *http.Request) error {
		if u := currentUser(r); u == nil || !u.isManager() {
			return errForbidden
		}
		return fn(w, r)
	})
}

func admin(fn handlerFunc) http.HandlerFunc {
	return h(func(w http.ResponseWriter, r *http.Request) error {
		if u := currentUser(r); u == nil || !u.isAdmin() {
			return errForbidden
		}
		return fn(w, r)
	})
}

// GET /api/me
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	sess := currentSession(r.Context())
	return ok(w, map[string]any{"user": sess.User, "csrf_token": sess.CSRFToken})
}
