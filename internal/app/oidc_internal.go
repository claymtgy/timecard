package app

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	internalClientOnce sync.Once
	internalClient     *http.Client
)

// httpClient returns a client that sends requests for the public issuer host to
// OIDCInternalURL instead (nil when not configured). Behind a reverse proxy this keeps
// app→IdP traffic on the private Docker network while tokens still carry the public issuer.
func (c *oidcClient) httpClient() *http.Client {
	if c.cfg.OIDCInternalURL == "" {
		return nil
	}
	internalClientOnce.Do(func() {
		pub, err1 := url.Parse(c.cfg.OIDCIssuer)
		internal, err2 := url.Parse(c.cfg.OIDCInternalURL)
		if err1 != nil || err2 != nil {
			return
		}
		internalClient = &http.Client{
			Timeout:   15 * time.Second,
			Transport: &rewriteTransport{public: pub, internal: internal, base: http.DefaultTransport},
		}
	})
	return internalClient
}

type rewriteTransport struct {
	public, internal *url.URL
	base             http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.public.Host {
		return t.base.RoundTrip(req)
	}
	r := req.Clone(req.Context())
	r.URL.Scheme = t.internal.Scheme
	r.URL.Host = t.internal.Host
	r.Host = t.public.Host
	r.Header.Set("X-Forwarded-Proto", t.public.Scheme)
	return t.base.RoundTrip(r)
}
