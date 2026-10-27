package access

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
)

// Options identifies one owner and one Access application. It contains no
// API token, connector credential or browser bearer token.
type Options struct {
	Issuer, Audience, OwnerSubject string
	// HTTPClient is a trusted composition/testing dependency. Requests remain confined to the configured signing-key endpoint.
	HTTPClient *http.Client
}

// Verifier shares the desktop's strict, at-most-fifteen-minute signed
// lease with independently isolated application origins. Verify returns that
// token's original expiry, never a new lease measured from the current request.
// Callers still enforce their own peer, Host, Origin, route, connection-expiry
// and local revocation boundaries. It does not introspect provider revocation.
type Verifier struct {
	ctx      context.Context
	verifier *tokenVerifier
	keys     *jwksTransport
	cancel   context.CancelFunc
}

func New(ctx context.Context, options Options) (*Verifier, error) {
	return newVerifier(ctx, options, dependencies{client: options.HTTPClient})
}

func newVerifier(ctx context.Context, options Options, deps dependencies) (*Verifier, error) {
	if ctx == nil || options.Issuer == "" || options.Audience == "" || options.OwnerSubject == "" {
		return nil, errors.New("an explicit Access issuer, audience and owner are required")
	}
	if err := policy.Validate(policy.Configuration{Issuer: options.Issuer, Audience: options.Audience, OwnerSubject: options.OwnerSubject}); err != nil {
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
	return &Verifier{ctx: lifetime, verifier: newTokenVerifier(lifetime, options, client, deps.now), keys: keys, cancel: cancel}, nil
}

func (v *Verifier) Verify(ctx context.Context, token string) (time.Time, error) {
	if v.ctx.Err() != nil || ctx.Err() != nil {
		return time.Time{}, errUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	return v.verifier.verify(ctx, token)
}

func (v *Verifier) Close() { v.cancel(); v.keys.close() }

type dependencies struct {
	client *http.Client
	now    func() time.Time
}
