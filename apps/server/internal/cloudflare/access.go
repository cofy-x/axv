package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/profile"
)

// AccessOptions identifies one owner and one Access application. It contains no
// API token, connector credential or browser bearer token.
type AccessOptions struct{ Issuer, Audience, OwnerSubject string }

// AccessVerifier shares the desktop's strict, at-most-fifteen-minute signed
// lease with independently isolated application origins. Verify returns that
// token's original expiry, never a new lease measured from the current request.
// Callers still enforce their own peer, Host, Origin, route, connection-expiry
// and local revocation boundaries. It does not introspect provider revocation.
type AccessVerifier struct {
	ctx      context.Context
	verifier *tokenVerifier
	keys     *jwksTransport
	cancel   context.CancelFunc
}

func NewAccessVerifier(ctx context.Context, options AccessOptions) (*AccessVerifier, error) {
	return newAccessVerifier(ctx, options, dependencies{})
}

func newAccessVerifier(ctx context.Context, options AccessOptions, deps dependencies) (*AccessVerifier, error) {
	if ctx == nil || options.Issuer == "" || options.Audience == "" || options.OwnerSubject == "" {
		return nil, errors.New("an explicit Access issuer, audience and owner are required")
	}
	if err := profile.ValidateCloudflare(profile.Cloudflare{Issuer: options.Issuer, Audience: options.Audience, OwnerSubject: options.OwnerSubject}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	lifetime, cancel := context.WithCancel(ctx)
	client, keys := newJWKSClient(lifetime, options.Issuer, deps.client)
	return &AccessVerifier{ctx: lifetime, verifier: newTokenVerifier(lifetime, Options{Issuer: options.Issuer, Audience: options.Audience, OwnerSubject: options.OwnerSubject}, client, deps.now), keys: keys, cancel: cancel}, nil
}

func (v *AccessVerifier) Verify(ctx context.Context, token string) (time.Time, error) {
	if v.ctx.Err() != nil || ctx.Err() != nil {
		return time.Time{}, errUnauthorized
	}
	return v.verifier.verify(ctx, token)
}
func (v *AccessVerifier) Close() { v.cancel(); v.keys.close() }

// StripAccessCredentials removes Access headers and browser authentication
// cookies before forwarding application traffic into a VM.
func StripAccessCredentials(r *http.Request) *http.Request { return withoutAccessCredentials(r) }
