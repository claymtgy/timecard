package idp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const (
	authRequestTTL  = 10 * time.Minute
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
)

// Storage implements op.Storage on Postgres.
type Storage struct {
	db    *pgxpool.Pool
	users *Users

	mu   sync.RWMutex
	keys []*signingKey // newest first
}

var (
	_ op.Storage                   = (*Storage)(nil)
	_ op.CanSetUserinfoFromRequest = (*Storage)(nil)
)

func NewStorage(ctx context.Context, db *pgxpool.Pool) (*Storage, error) {
	keys, err := loadSigningKeys(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load signing keys: %w", err)
	}
	return &Storage{db: db, users: NewUsers(db), keys: keys}, nil
}

type notFoundError struct{ what string }

func (e notFoundError) Error() string { return e.what + " not found" }
func (notFoundError) IsNotFound()     {}

// ---- context: the IdP browser session is attached by middleware ------------

type ctxKey int

const sessionCtxKey ctxKey = 1

func withSession(ctx context.Context, s *browserSession) context.Context {
	return context.WithValue(ctx, sessionCtxKey, s)
}

func sessionFrom(ctx context.Context) *browserSession {
	s, _ := ctx.Value(sessionCtxKey).(*browserSession)
	return s
}

// ---- auth requests -----------------------------------------------------------

func (s *Storage) CreateAuthRequest(ctx context.Context, req *oidc.AuthRequest, hintUserID string) (op.AuthRequest, error) {
	ar := newAuthRequest(req, randomUUID())
	if ar.hasPrompt(oidc.PromptNone) {
		// prompt=none can only succeed with an existing SSO session (for the hinted user, if any).
		sess := sessionFrom(ctx)
		if sess == nil || (hintUserID != "" && hintUserID != sess.UserID) {
			return nil, oidc.ErrLoginRequired()
		}
	}
	data, err := json.Marshal(ar)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO auth_requests (id, client_id, data, expires_at)
		VALUES ($1::uuid, $2, $3, $4)`, ar.ID, ar.ClientID, data, time.Now().Add(authRequestTTL))
	if err != nil {
		return nil, err
	}
	return ar, nil
}

func (s *Storage) loadAuthRequest(ctx context.Context, where string, arg any) (*authRequest, error) {
	var (
		data     []byte
		userID   *string
		authTime *time.Time
		done     bool
	)
	err := s.db.QueryRow(ctx, `
		SELECT data, user_id::text, auth_time, done FROM auth_requests
		WHERE `+where+` AND expires_at > now()`, arg).Scan(&data, &userID, &authTime, &done)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFoundError{"auth request"}
	}
	if err != nil {
		return nil, err
	}
	ar := &authRequest{}
	if err := json.Unmarshal(data, ar); err != nil {
		return nil, err
	}
	if userID != nil {
		ar.UserID = *userID
	}
	if authTime != nil {
		ar.AuthTime = *authTime
	}
	ar.IsDone = done
	return ar, nil
}

func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	return s.authRequestByID(ctx, id)
}

func (s *Storage) authRequestByID(ctx context.Context, id string) (*authRequest, error) {
	return s.loadAuthRequest(ctx, "id::text = $1", id)
}

func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	return s.loadAuthRequest(ctx, "code = $1", code)
}

func (s *Storage) SaveAuthCode(ctx context.Context, id, code string) error {
	_, err := s.db.Exec(ctx, `UPDATE auth_requests SET code = $2 WHERE id::text = $1`, id, code)
	return err
}

func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth_requests WHERE id::text = $1`, id)
	return err
}

// completeAuthRequest marks the request as authenticated by userID (called by the login UI).
func (s *Storage) completeAuthRequest(ctx context.Context, id, userID string, authTime time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth_requests SET user_id = $2::uuid, auth_time = $3, done = true
		WHERE id::text = $1 AND expires_at > now()`, id, userID, authTime)
	if err == nil && tag.RowsAffected() == 0 {
		return notFoundError{"auth request"}
	}
	return err
}

// ---- tokens --------------------------------------------------------------------

type clientIDer interface{ GetClientID() string }

func requestInfo(req op.TokenRequest) (clientID string, authTime time.Time, amr []string) {
	if c, ok := req.(clientIDer); ok {
		clientID = c.GetClientID()
	}
	if r, ok := req.(interface{ GetAuthTime() time.Time }); ok {
		authTime = r.GetAuthTime()
	}
	if r, ok := req.(interface{ GetAMR() []string }); ok {
		amr = r.GetAMR()
	}
	if authTime.IsZero() {
		authTime = time.Now()
	}
	return
}

func (s *Storage) CreateAccessToken(ctx context.Context, req op.TokenRequest) (string, time.Time, error) {
	clientID, _, _ := requestInfo(req)
	id := randomUUID()
	exp := time.Now().Add(accessTokenTTL)
	_, err := s.db.Exec(ctx, `
		INSERT INTO access_tokens (id, client_id, user_id, audience, scopes, expires_at)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6)`,
		id, clientID, req.GetSubject(), nonNil(req.GetAudience()), nonNil(req.GetScopes()), exp)
	if err != nil {
		return "", time.Time{}, err
	}
	return id, exp, nil
}

func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, req op.TokenRequest, currentRefreshToken string) (string, string, time.Time, error) {
	clientID, authTime, amr := requestInfo(req)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer tx.Rollback(ctx)

	if currentRefreshToken != "" {
		// Rotation: the presented token is single-use.
		var expires time.Time
		var ownerClient string
		err := tx.QueryRow(ctx, `
			DELETE FROM refresh_tokens WHERE token_hash = $1
			RETURNING expires_at, client_id, auth_time, amr`, hashToken(currentRefreshToken)).
			Scan(&expires, &ownerClient, &authTime, &amr)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", time.Time{}, op.ErrInvalidRefreshToken
		}
		if err != nil {
			return "", "", time.Time{}, err
		}
		if expires.Before(time.Now()) || ownerClient != clientID {
			return "", "", time.Time{}, op.ErrInvalidRefreshToken
		}
	}

	refreshID := randomUUID()
	refreshToken := randomToken(32)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, token_hash, client_id, user_id, audience, scopes, amr, auth_time, expires_at)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6, $7, $8, $9)`,
		refreshID, hashToken(refreshToken), clientID, req.GetSubject(), nonNil(req.GetAudience()),
		nonNil(req.GetScopes()), nonNil(amr), authTime, time.Now().Add(refreshTokenTTL)); err != nil {
		return "", "", time.Time{}, err
	}
	accessID := randomUUID()
	exp := time.Now().Add(accessTokenTTL)
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_tokens (id, client_id, user_id, refresh_token_id, audience, scopes, expires_at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7)`,
		accessID, clientID, req.GetSubject(), refreshID, nonNil(req.GetAudience()), nonNil(req.GetScopes()), exp); err != nil {
		return "", "", time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", time.Time{}, err
	}
	return accessID, refreshToken, exp, nil
}

func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, token string) (op.RefreshTokenRequest, error) {
	r := &refreshTokenRequest{}
	err := s.db.QueryRow(ctx, `
		SELECT id::text, client_id, user_id::text, audience, scopes, amr, auth_time
		FROM refresh_tokens WHERE token_hash = $1 AND expires_at > now()`, hashToken(token)).
		Scan(&r.ID, &r.ClientID, &r.UserID, &r.Audience, &r.Scopes, &r.AMR, &r.AuthTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, op.ErrInvalidRefreshToken
	}
	return r, err
}

func (s *Storage) TerminateSession(ctx context.Context, userID, clientID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id::text = $1 AND client_id = $2`, userID, clientID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `DELETE FROM access_tokens WHERE user_id::text = $1 AND client_id = $2`, userID, clientID)
	return err
}

func (s *Storage) GetRefreshTokenInfo(ctx context.Context, clientID, token string) (string, string, error) {
	var userID, id string
	err := s.db.QueryRow(ctx, `
		SELECT user_id::text, id::text FROM refresh_tokens
		WHERE token_hash = $1 AND client_id = $2`, hashToken(token), clientID).Scan(&userID, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", op.ErrInvalidRefreshToken
	}
	return userID, id, err
}

func (s *Storage) RevokeToken(ctx context.Context, tokenOrID, userID, clientID string) *oidc.Error {
	var owner string
	var err error
	if userID != "" {
		// Access token: tokenOrID is our token ID.
		err = s.db.QueryRow(ctx, `DELETE FROM access_tokens WHERE id::text = $1 RETURNING client_id`, tokenOrID).Scan(&owner)
	} else {
		// Refresh token: tokenOrID is the raw token. Deleting it cascades to its access tokens.
		err = s.db.QueryRow(ctx, `DELETE FROM refresh_tokens WHERE token_hash = $1 RETURNING client_id`, hashToken(tokenOrID)).Scan(&owner)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // RFC 7009: unknown tokens are not an error
	}
	if err != nil {
		return oidc.ErrServerError().WithParent(err)
	}
	if owner != clientID {
		return oidc.ErrInvalidClient().WithDescription("token was not issued for this client")
	}
	return nil
}

// ---- keys ------------------------------------------------------------------------

func (s *Storage) SigningKey(context.Context) (op.SigningKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keys[0], nil
}

func (s *Storage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.RS256}, nil
}

func (s *Storage) KeySet(context.Context) ([]op.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]op.Key, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, publicKey{k})
	}
	return out, nil
}

// ---- clients -----------------------------------------------------------------------

func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	return s.client(ctx, clientID)
}

func (s *Storage) client(ctx context.Context, clientID string) (*client, error) {
	c := &client{}
	var (
		appType    int16
		authMethod string
		grants     []string
		secretHash *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, name, secret_hash, redirect_uris, post_logout_redirect_uris,
		       application_type, auth_method, grant_types, dev_mode
		FROM clients WHERE id = $1`, clientID).
		Scan(&c.id, &c.name, &secretHash, &c.redirectURIs, &c.postLogoutRedirectURIs, &appType, &authMethod, &grants, &c.devMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFoundError{"client"}
	}
	if err != nil {
		return nil, err
	}
	if secretHash != nil {
		c.secretHash = *secretHash
	}
	c.applicationType = op.ApplicationType(appType)
	c.authMethod = oidc.AuthMethod(authMethod)
	for _, g := range grants {
		c.grantTypes = append(c.grantTypes, oidc.GrantType(g))
	}
	return c, nil
}

func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, secret string) error {
	c, err := s.client(ctx, clientID)
	if err != nil {
		return err
	}
	if c.secretHash == "" || subtle.ConstantTimeCompare([]byte(c.secretHash), []byte(hashSecret(secret))) != 1 {
		return errors.New("invalid client secret")
	}
	return nil
}

// ClientConfig describes a relying party to register.
type ClientConfig struct {
	ID                     string
	Name                   string
	Secret                 string // empty for public (PKCE-only) clients
	RedirectURIs           []string
	PostLogoutRedirectURIs []string
	DevMode                bool // allows http:// redirect URIs
}

// UpsertClient registers or updates a confidential web client.
func (s *Storage) UpsertClient(ctx context.Context, c ClientConfig) error {
	var secretHash *string
	authMethod := string(oidc.AuthMethodNone)
	appType := op.ApplicationTypeUserAgent
	if c.Secret != "" {
		h := hashSecret(c.Secret)
		secretHash = &h
		authMethod = string(oidc.AuthMethodBasic)
		appType = op.ApplicationTypeWeb
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO clients (id, name, secret_hash, redirect_uris, post_logout_redirect_uris, application_type, auth_method, grant_types, dev_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
		  name = EXCLUDED.name, secret_hash = EXCLUDED.secret_hash, redirect_uris = EXCLUDED.redirect_uris,
		  post_logout_redirect_uris = EXCLUDED.post_logout_redirect_uris, application_type = EXCLUDED.application_type,
		  auth_method = EXCLUDED.auth_method, grant_types = EXCLUDED.grant_types, dev_mode = EXCLUDED.dev_mode,
		  updated_at = now()`,
		c.ID, c.Name, secretHash, nonNil(c.RedirectURIs), nonNil(c.PostLogoutRedirectURIs), int16(appType), authMethod,
		[]string{string(oidc.GrantTypeCode), string(oidc.GrantTypeRefreshToken)}, c.DevMode)
	return err
}

// isRegisteredPostLogoutURI reports whether uri is a post-logout redirect of any client.
func (s *Storage) isRegisteredPostLogoutURI(ctx context.Context, uri string) bool {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM clients WHERE $1 = ANY(post_logout_redirect_uris))`, uri).Scan(&ok)
	return err == nil && ok
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ---- userinfo / introspection ---------------------------------------------------------

func (s *Storage) SetUserinfoFromScopes(context.Context, *oidc.UserInfo, string, string, []string) error {
	return nil // deprecated; SetUserinfoFromRequest is used instead
}

func (s *Storage) SetUserinfoFromRequest(ctx context.Context, info *oidc.UserInfo, req op.IDTokenRequest, scopes []string) error {
	return s.setUserinfo(ctx, info, req.GetSubject(), scopes)
}

func (s *Storage) SetUserinfoFromToken(ctx context.Context, info *oidc.UserInfo, tokenID, subject, origin string) error {
	var userID string
	var scopes []string
	err := s.db.QueryRow(ctx, `
		SELECT user_id::text, scopes FROM access_tokens
		WHERE id::text = $1 AND expires_at > now()`, tokenID).Scan(&userID, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("token is invalid or has expired")
	}
	if err != nil {
		return err
	}
	return s.setUserinfo(ctx, info, userID, scopes)
}

func (s *Storage) SetIntrospectionFromToken(ctx context.Context, resp *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	var (
		userID, owner string
		audience      []string
		scopes        []string
		expires       time.Time
	)
	err := s.db.QueryRow(ctx, `
		SELECT user_id::text, client_id, audience, scopes, expires_at FROM access_tokens
		WHERE id::text = $1 AND expires_at > now()`, tokenID).Scan(&userID, &owner, &audience, &scopes, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("token is invalid or has expired")
	}
	if err != nil {
		return err
	}
	for _, aud := range audience {
		if aud == clientID {
			info := new(oidc.UserInfo)
			if err := s.setUserinfo(ctx, info, userID, scopes); err != nil {
				return err
			}
			resp.SetUserInfo(info)
			resp.Scope = scopes
			resp.ClientID = owner
			resp.Expiration = oidc.FromTime(expires)
			return nil
		}
	}
	return errors.New("token is not valid for this client")
}

func (s *Storage) setUserinfo(ctx context.Context, info *oidc.UserInfo, userID string, scopes []string) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Disabled {
		return errors.New("user is disabled")
	}
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeOpenID:
			info.Subject = u.ID
		case oidc.ScopeEmail:
			info.Email = u.Email
			info.EmailVerified = oidc.Bool(u.EmailVerified)
		case oidc.ScopeProfile:
			info.Name = u.Name
			info.GivenName = u.GivenName
			info.FamilyName = u.FamilyName
			info.PreferredUsername = u.Email
		}
	}
	return nil
}

func (s *Storage) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, nil
}

func (s *Storage) GetKeyByIDAndClientID(context.Context, string, string) (*jose.JSONWebKey, error) {
	return nil, errors.New("JWT profile client authentication is not supported")
}

func (s *Storage) ValidateJWTProfileScopes(_ context.Context, _ string, scopes []string) ([]string, error) {
	var allowed []string
	for _, sc := range scopes {
		if sc == oidc.ScopeOpenID {
			allowed = append(allowed, sc)
		}
	}
	return allowed, nil
}

func (s *Storage) Health(ctx context.Context) error { return s.db.Ping(ctx) }

// Cleanup periodically deletes expired rows until ctx is cancelled.
func (s *Storage) Cleanup(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, q := range []string{
			`DELETE FROM auth_requests WHERE expires_at < now()`,
			`DELETE FROM access_tokens WHERE expires_at < now()`,
			`DELETE FROM refresh_tokens WHERE expires_at < now()`,
			`DELETE FROM sessions WHERE expires_at < now()`,
		} {
			if _, err := s.db.Exec(ctx, q); err != nil && ctx.Err() == nil {
				slog.Warn("cleanup failed", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func randomUUID() string {
	b := randomBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
