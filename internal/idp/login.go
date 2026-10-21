package idp

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

const csrfCookie = "idp_csrf"

// loginUI serves the interactive pages: login, sign-up, account and logout.
type loginUI struct {
	storage     *Storage
	users       *Users
	sessions    *sessionStore
	callbackURL func(context.Context, string) string
	allowSignup bool
	secure      bool
	limiter     *failureLimiter
}

type pageData struct {
	Title         string
	Message       string
	Error         string
	CSRF          string
	AuthRequestID string
	Email         string
	Name          string
	AllowSignup   bool
	User          *User
}

func (l *loginUI) render(w http.ResponseWriter, status int, name string, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, d); err != nil {
		slog.Error("render template", "name", name, "err", err)
	}
}

func (l *loginUI) message(w http.ResponseWriter, status int, title, msg string) {
	l.render(w, status, "message", pageData{Title: title, Message: msg})
}

func (l *loginUI) serverError(w http.ResponseWriter, err error) {
	slog.Error("idp request failed", "err", err)
	l.message(w, http.StatusInternalServerError, "Something went wrong", "Please try again in a moment.")
}

// csrfToken returns the double-submit token for this browser, setting the cookie if needed.
func (l *loginUI) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	tok := randomToken(24)
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: tok, Path: "/", HttpOnly: true, Secure: l.secure, SameSite: http.SameSiteStrictMode})
	return tok
}

func (l *loginUI) checkCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.PostFormValue("csrf"))) == 1
}

// GET /login?authRequestID=...
func (l *loginUI) loginPage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	ar, err := l.storage.authRequestByID(r.Context(), id)
	if err != nil {
		l.message(w, http.StatusBadRequest, "Sign-in link expired", "Go back to the app you were using and sign in again.")
		return
	}
	// Single sign-on: reuse an existing browser session unless the client forces a fresh login.
	sess, err := l.sessions.current(r.Context(), r)
	if err != nil {
		l.serverError(w, err)
		return
	}
	if sess != nil && !ar.hasPrompt("login") && withinMaxAge(ar, sess.AuthTime) {
		l.finish(w, r, ar.ID, sess.UserID, sess.AuthTime)
		return
	}
	if ar.hasPrompt("none") {
		// No usable session: send the user back; the OP answers the client with login_required.
		http.Redirect(w, r, l.callbackURL(r.Context(), ar.ID), http.StatusFound)
		return
	}
	l.render(w, http.StatusOK, "login", pageData{
		Title: "Sign in", CSRF: l.csrfToken(w, r), AuthRequestID: ar.ID, Email: ar.LoginHint, AllowSignup: l.allowSignup,
	})
}

// POST /login
func (l *loginUI) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		l.message(w, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	id := r.PostFormValue("authRequestID")
	email := strings.TrimSpace(r.PostFormValue("email"))
	d := pageData{Title: "Sign in", CSRF: l.csrfToken(w, r), AuthRequestID: id, Email: email, AllowSignup: l.allowSignup}
	if !l.checkCSRF(r) {
		d.Error = "Your session timed out. Please try again."
		l.render(w, http.StatusBadRequest, "login", d)
		return
	}
	if _, err := l.storage.authRequestByID(r.Context(), id); err != nil {
		l.message(w, http.StatusBadRequest, "Sign-in link expired", "Go back to the app you were using and sign in again.")
		return
	}
	limitKey := clientIP(r) + "|" + strings.ToLower(email)
	if l.limiter.blocked(limitKey) {
		d.Error = "Too many failed attempts. Wait a few minutes and try again."
		l.render(w, http.StatusTooManyRequests, "login", d)
		return
	}
	u, err := l.users.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, ErrInvalidCredentials) {
		l.limiter.fail(limitKey)
		d.Error = ErrInvalidCredentials.Error()
		l.render(w, http.StatusUnauthorized, "login", d)
		return
	}
	if err != nil {
		l.serverError(w, err)
		return
	}
	l.limiter.reset(limitKey)
	l.startSessionAndFinish(w, r, id, u.ID)
}

// GET /signup
func (l *loginUI) signupPage(w http.ResponseWriter, r *http.Request) {
	if !l.allowSignup {
		l.message(w, http.StatusNotFound, "Sign-up is disabled", "Ask an administrator to create your account.")
		return
	}
	l.render(w, http.StatusOK, "signup", pageData{Title: "Create account", CSRF: l.csrfToken(w, r), AuthRequestID: r.URL.Query().Get("authRequestID")})
}

// POST /signup
func (l *loginUI) signupSubmit(w http.ResponseWriter, r *http.Request) {
	if !l.allowSignup {
		l.message(w, http.StatusNotFound, "Sign-up is disabled", "Ask an administrator to create your account.")
		return
	}
	if err := r.ParseForm(); err != nil {
		l.message(w, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	id := r.PostFormValue("authRequestID")
	d := pageData{Title: "Create account", CSRF: l.csrfToken(w, r), AuthRequestID: id,
		Email: strings.TrimSpace(r.PostFormValue("email")), Name: strings.TrimSpace(r.PostFormValue("name"))}
	if !l.checkCSRF(r) {
		d.Error = "Your session timed out. Please try again."
		l.render(w, http.StatusBadRequest, "signup", d)
		return
	}
	if l.limiter.blocked("signup|" + clientIP(r)) {
		d.Error = "Too many attempts. Wait a few minutes and try again."
		l.render(w, http.StatusTooManyRequests, "signup", d)
		return
	}
	l.limiter.fail("signup|" + clientIP(r)) // counts every sign-up to slow down mass registration
	u, err := l.users.Create(r.Context(), d.Email, d.Name, r.PostFormValue("password"))
	if err != nil {
		if errors.Is(err, ErrEmailTaken) || isValidationError(err) {
			d.Error = err.Error()
			l.render(w, http.StatusBadRequest, "signup", d)
			return
		}
		l.serverError(w, err)
		return
	}
	slog.Info("user signed up", "user_id", u.ID)
	if id == "" {
		if _, err := l.sessions.create(r.Context(), w, r, u.ID); err != nil {
			l.serverError(w, err)
			return
		}
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	l.startSessionAndFinish(w, r, id, u.ID)
}

func isValidationError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "enter ") || strings.HasPrefix(msg, "password ")
}

func (l *loginUI) startSessionAndFinish(w http.ResponseWriter, r *http.Request, authRequestID, userID string) {
	// Replace any existing session (prevents session fixation / account mix-ups).
	if err := l.sessions.destroy(r.Context(), w, r); err != nil {
		l.serverError(w, err)
		return
	}
	sess, err := l.sessions.create(r.Context(), w, r, userID)
	if err != nil {
		l.serverError(w, err)
		return
	}
	l.finish(w, r, authRequestID, userID, sess.AuthTime)
}

func (l *loginUI) finish(w http.ResponseWriter, r *http.Request, authRequestID, userID string, authTime time.Time) {
	if err := l.storage.completeAuthRequest(r.Context(), authRequestID, userID, authTime); err != nil {
		l.message(w, http.StatusBadRequest, "Sign-in link expired", "Go back to the app you were using and sign in again.")
		return
	}
	http.Redirect(w, r, l.callbackURL(r.Context(), authRequestID), http.StatusSeeOther)
}

// GET /account
func (l *loginUI) accountPage(w http.ResponseWriter, r *http.Request) {
	d := pageData{Title: "Account", CSRF: l.csrfToken(w, r)}
	sess, err := l.sessions.current(r.Context(), r)
	if err != nil {
		l.serverError(w, err)
		return
	}
	if sess != nil {
		if d.User, err = l.users.ByID(r.Context(), sess.UserID); err != nil {
			l.serverError(w, err)
			return
		}
	}
	l.render(w, http.StatusOK, "account", d)
}

// POST /account/logout
func (l *loginUI) accountLogout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !l.checkCSRF(r) {
		l.message(w, http.StatusBadRequest, "Bad request", "Please try again.")
		return
	}
	if err := l.sessions.destroy(r.Context(), w, r); err != nil {
		l.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// GET /logged-out[?redirect=...] — default post-logout page. Only redirects to URIs
// that some client has registered, so it can't be used as an open redirect.
func (l *loginUI) loggedOut(w http.ResponseWriter, r *http.Request) {
	if target := r.URL.Query().Get("redirect"); target != "" && l.storage.isRegisteredPostLogoutURI(r.Context(), target) {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	l.message(w, http.StatusOK, "Signed out", "You have been signed out. You can close this tab.")
}

func withinMaxAge(ar *authRequest, authTime time.Time) bool {
	if ar.MaxAgeSeconds == nil {
		return true
	}
	return time.Since(authTime) <= time.Duration(*ar.MaxAgeSeconds)*time.Second
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// failureLimiter is a small in-memory fixed-window counter for failed logins.
type failureLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	count int
	start time.Time
}

func newFailureLimiter(max int, per time.Duration) *failureLimiter {
	return &failureLimiter{max: max, window: per, hits: map[string]*window{}}
}

func (f *failureLimiter) blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.hits[key]
	if !ok {
		return false
	}
	if time.Since(w.start) > f.window {
		delete(f.hits, key)
		return false
	}
	return w.count >= f.max
}

func (f *failureLimiter) fail(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hits) > 100_000 { // crude memory bound
		f.hits = map[string]*window{}
	}
	w, ok := f.hits[key]
	if !ok || time.Since(w.start) > f.window {
		f.hits[key] = &window{count: 1, start: time.Now()}
		return
	}
	w.count++
}

func (f *failureLimiter) reset(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hits, key)
}
