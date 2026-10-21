package idp

import (
	"crypto/rsa"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// authRequest is an in-flight /authorize request. It is persisted as JSON in
// auth_requests.data, with user/done/code tracked in their own columns.
type authRequest struct {
	ID                  string            `json:"id"`
	CreatedAt           time.Time         `json:"created_at"`
	ClientID            string            `json:"client_id"`
	RedirectURI         string            `json:"redirect_uri"`
	State               string            `json:"state"`
	Prompt              []string          `json:"prompt"`
	LoginHint           string            `json:"login_hint"`
	MaxAgeSeconds       *int64            `json:"max_age,omitempty"`
	Scopes              []string          `json:"scopes"`
	ResponseType        oidc.ResponseType `json:"response_type"`
	ResponseMode        oidc.ResponseMode `json:"response_mode"`
	Nonce               string            `json:"nonce"`
	CodeChallenge       string            `json:"code_challenge"`
	CodeChallengeMethod string            `json:"code_challenge_method"`

	// Filled from table columns, not the JSON blob.
	UserID   string    `json:"-"`
	AuthTime time.Time `json:"-"`
	IsDone   bool      `json:"-"`
}

var _ op.AuthRequest = (*authRequest)(nil)

func newAuthRequest(req *oidc.AuthRequest, id string) *authRequest {
	ar := &authRequest{
		ID:                  id,
		CreatedAt:           time.Now(),
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		State:               req.State,
		LoginHint:           req.LoginHint,
		Scopes:              req.Scopes,
		ResponseType:        req.ResponseType,
		ResponseMode:        req.ResponseMode,
		Nonce:               req.Nonce,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: string(req.CodeChallengeMethod),
	}
	for _, p := range req.Prompt {
		switch p {
		case oidc.PromptNone, oidc.PromptLogin, oidc.PromptConsent, oidc.PromptSelectAccount:
			ar.Prompt = append(ar.Prompt, p)
		}
	}
	if req.MaxAge != nil {
		s := int64(*req.MaxAge)
		ar.MaxAgeSeconds = &s
	}
	return ar
}

func (a *authRequest) hasPrompt(p string) bool {
	for _, x := range a.Prompt {
		if x == p {
			return true
		}
	}
	return false
}

func (a *authRequest) GetID() string         { return a.ID }
func (a *authRequest) GetACR() string        { return "" }
func (a *authRequest) GetAudience() []string { return []string{a.ClientID} }
func (a *authRequest) GetAuthTime() time.Time {
	return a.AuthTime
}
func (a *authRequest) GetClientID() string                { return a.ClientID }
func (a *authRequest) GetNonce() string                   { return a.Nonce }
func (a *authRequest) GetRedirectURI() string             { return a.RedirectURI }
func (a *authRequest) GetResponseType() oidc.ResponseType { return a.ResponseType }
func (a *authRequest) GetResponseMode() oidc.ResponseMode { return a.ResponseMode }
func (a *authRequest) GetScopes() []string                { return a.Scopes }
func (a *authRequest) GetState() string                   { return a.State }
func (a *authRequest) GetSubject() string                 { return a.UserID }
func (a *authRequest) Done() bool                         { return a.IsDone }

func (a *authRequest) GetAMR() []string {
	if a.IsDone {
		return []string{"pwd"}
	}
	return nil
}

func (a *authRequest) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" {
		return nil
	}
	method := oidc.CodeChallengeMethodPlain
	if a.CodeChallengeMethod == string(oidc.CodeChallengeMethodS256) {
		method = oidc.CodeChallengeMethodS256
	}
	return &oidc.CodeChallenge{Challenge: a.CodeChallenge, Method: method}
}

// refreshTokenRequest implements op.RefreshTokenRequest from a refresh_tokens row.
type refreshTokenRequest struct {
	ID       string
	ClientID string
	UserID   string
	Audience []string
	Scopes   []string
	AMR      []string
	AuthTime time.Time
}

func (r *refreshTokenRequest) GetAMR() []string            { return r.AMR }
func (r *refreshTokenRequest) GetAudience() []string       { return r.Audience }
func (r *refreshTokenRequest) GetAuthTime() time.Time      { return r.AuthTime }
func (r *refreshTokenRequest) GetClientID() string         { return r.ClientID }
func (r *refreshTokenRequest) GetScopes() []string         { return r.Scopes }
func (r *refreshTokenRequest) GetSubject() string          { return r.UserID }
func (r *refreshTokenRequest) SetCurrentScopes(s []string) { r.Scopes = s }

// client implements op.Client from a clients row.
type client struct {
	id                     string
	name                   string
	secretHash             string
	redirectURIs           []string
	postLogoutRedirectURIs []string
	applicationType        op.ApplicationType
	authMethod             oidc.AuthMethod
	grantTypes             []oidc.GrantType
	devMode                bool
}

var _ op.Client = (*client)(nil)

func (c *client) GetID() string                    { return c.id }
func (c *client) RedirectURIs() []string           { return c.redirectURIs }
func (c *client) PostLogoutRedirectURIs() []string { return c.postLogoutRedirectURIs }
func (c *client) ApplicationType() op.ApplicationType {
	return c.applicationType
}
func (c *client) AuthMethod() oidc.AuthMethod { return c.authMethod }
func (c *client) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c *client) GrantTypes() []oidc.GrantType { return c.grantTypes }
func (c *client) LoginURL(id string) string    { return "/login?authRequestID=" + id }
func (c *client) AccessTokenType() op.AccessTokenType {
	return op.AccessTokenTypeBearer
}
func (c *client) IDTokenLifetime() time.Duration { return time.Hour }
func (c *client) DevMode() bool                  { return c.devMode }
func (c *client) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *client) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}

// IsScopeAllowed is consulted for non-standard scopes only; we have none.
func (c *client) IsScopeAllowed(string) bool { return false }

// IDTokenUserinfoClaimsAssertion puts profile/email claims directly in the ID token,
// so relying parties don't need a separate userinfo round-trip.
func (c *client) IDTokenUserinfoClaimsAssertion() bool { return true }
func (c *client) ClockSkew() time.Duration             { return 0 }

// signingKey / publicKey adapt an RSA key to op.SigningKey and op.Key.
type signingKey struct {
	id  string
	alg jose.SignatureAlgorithm
	key *rsa.PrivateKey
}

func (s *signingKey) SignatureAlgorithm() jose.SignatureAlgorithm { return s.alg }
func (s *signingKey) Key() any                                    { return s.key }
func (s *signingKey) ID() string                                  { return s.id }

type publicKey struct{ *signingKey }

func (p publicKey) Algorithm() jose.SignatureAlgorithm { return p.alg }
func (p publicKey) Use() string                        { return "sig" }
func (p publicKey) Key() any                           { return &p.key.PublicKey }
