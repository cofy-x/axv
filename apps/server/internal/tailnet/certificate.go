package tailnet

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"
)

// Certificates are immutable after parsing; renewal publishes a new pointer.
type certificateCache struct {
	mu    sync.RWMutex
	value *tls.Certificate
}

func (c *certificateCache) current(domain string, now time.Time) (*tls.Certificate, error) {
	c.mu.RLock()
	value := c.value
	c.mu.RUnlock()
	if value == nil || value.Leaf == nil || value.Leaf.VerifyHostname(domain) != nil || now.Before(value.Leaf.NotBefore) || !now.Before(value.Leaf.NotAfter) {
		return nil, errors.New("a valid cached HTTPS certificate is unavailable")
	}
	return value, nil
}

func (c *certificateCache) needsRefresh(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value == nil || c.value.Leaf == nil || c.value.Leaf.NotAfter.Sub(now) < 24*time.Hour
}

func refreshCertificates(ctx context.Context, node backend, domain string, cache *certificateCache, limits timing, result func(error)) {
	for {
		if ctx.Err() != nil {
			return
		}
		if cache.needsRefresh(time.Now()) {
			fetchCtx, cancel := context.WithTimeout(ctx, limits.certificate)
			value, err := certificate(node, fetchCtx, domain, 24*time.Hour)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				cache.mu.Lock()
				cache.value = value
				cache.mu.Unlock()
			}
			result(err)
		}
		if pause(ctx, limits.refresh) != nil {
			return
		}
	}
}

func (s *Server) renewalStatus(err error) {
	s.mu.Lock()
	if s.ctx.Err() != nil || s.status.State != StateReady {
		s.mu.Unlock()
		return
	}
	s.status.Error = ""
	if err != nil {
		s.status.Error = "HTTPS certificate renewal is pending; the current certificate is still in use until it expires."
	}
	s.mu.Unlock()
	s.notify()
}
