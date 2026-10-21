package idp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookie = "idp_session"
	sessionTTL    = 14 * 24 * time.Hour
)

// browserSession is an authenticated SSO session at the IdP.
type browserSession struct {
	ID       string
	UserID   string
	AuthTime time.Time
}

type sessionStore struct {
	db     *pgxpool.Pool
	secure bool
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b
}

func randomToken(n int) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(n))
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// create stores a new session and sets the cookie.
func (s *sessionStore) create(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string) (*browserSession, error) {
	token := randomToken(32)
	now := time.Now()
	sess := &browserSession{ID: hashToken(token), UserID: userID, AuthTime: now}
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions (id, user_id, auth_time, expires_at, user_agent, ip)
		VALUES ($1, $2::uuid, $3, $4, $5, $6)`,
		sess.ID, userID, now, now.Add(sessionTTL), truncate(r.UserAgent(), 300), clientIP(r))
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, Expires: now.Add(sessionTTL),
	})
	return sess, nil
}

// current returns the valid session for the request's cookie, or nil.
func (s *sessionStore) current(ctx context.Context, r *http.Request) (*browserSession, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	sess := &browserSession{}
	err = s.db.QueryRow(ctx, `
		SELECT s.id, s.user_id::text, s.auth_time
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now() AND NOT u.disabled`, hashToken(c.Value)).
		Scan(&sess.ID, &sess.UserID, &sess.AuthTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// destroy deletes the request's session (if any) and clears the cookie.
func (s *sessionStore) destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, hashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
