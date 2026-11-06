package webpush

import (
	"net/http"
	"time"
)

const maxAuthorizations = 256

type authorizationScope struct {
	origin    string
	publicKey string
	subject   string
}

type authorization struct {
	value     string
	createdAt time.Time
}

type authorizedClient struct {
	sender *Sender
	client *http.Client
	scope  authorizationScope
}

func (c authorizedClient) Do(request *http.Request) (*http.Response, error) {
	// Apple requests hourly JWT reuse. Only an already-generated, signed
	// authorization is cached; no private key or notification payload enters
	// this bounded, process-local cache.
	c.sender.authMu.Lock()
	now := time.Now()
	if c.sender.authorizations == nil {
		c.sender.authorizations = make(map[authorizationScope]authorization)
	}
	cached, ok := c.sender.authorizations[c.scope]
	if ok && now.Sub(cached.createdAt) < time.Hour {
		request.Header.Set("Authorization", cached.value)
	} else {
		if len(c.sender.authorizations) >= maxAuthorizations {
			var oldestScope authorizationScope
			var oldest time.Time
			for scope, entry := range c.sender.authorizations {
				if oldest.IsZero() || entry.createdAt.Before(oldest) {
					oldest, oldestScope = entry.createdAt, scope
				}
			}
			delete(c.sender.authorizations, oldestScope)
		}
		c.sender.authorizations[c.scope] = authorization{value: request.Header.Get("Authorization"), createdAt: now}
	}
	c.sender.authMu.Unlock()
	return c.client.Do(request)
}
