package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GET /api/organization/password (admin) — the password new users sign up with.
// password is null when it can't be recovered and must be replaced with `idp org passwd`.
func (s *Server) getOrgPassword(w http.ResponseWriter, r *http.Request) error {
	u := currentUser(r)
	password, err := s.fetchOrgPassword(r.Context(), u.OrgID)
	if err != nil {
		slog.Error("fetch organization password", "err", err)
		return &apiError{http.StatusBadGateway, "the sign-in service couldn't be reached; try again shortly"}
	}
	slog.Info("organization password viewed", "user_id", u.ID, "org_id", u.OrgID)
	return ok(w, map[string]any{"password": password})
}

// fetchOrgPassword asks the IdP, which owns organizations, authenticating as the OIDC client.
func (s *Server) fetchOrgPassword(ctx context.Context, orgID string) (*string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(s.cfg.OIDCIssuer, "/") + "/internal/org-password?org_id=" + url.QueryEscape(orgID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.cfg.OIDCClientID, s.cfg.OIDCClientSecret)
	client := s.oidc.httpClient()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return nil, err
		}
		password := string(body)
		return &password, nil
	case http.StatusNotFound:
		return nil, nil
	}
	return nil, fmt.Errorf("sign-in service returned %s", resp.Status)
}
