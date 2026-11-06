package webpush

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type authorizationTransport func(*http.Request) (*http.Response, error)

func (f authorizationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAuthorizationCacheExpiryAndBound(t *testing.T) {
	sender := NewSender()
	var observed string
	client := &http.Client{Transport: authorizationTransport(func(request *http.Request) (*http.Response, error) {
		observed = request.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
	})}
	send := func(scope authorizationScope, value string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, "https://fcm.googleapis.com/wp/token", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", value)
		response, err := (authorizedClient{sender: sender, client: client, scope: scope}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	scope := authorizationScope{origin: "https://fcm.googleapis.com", publicKey: "reviewed-public-key", subject: "owner@example.test"}
	send(scope, "first signed token")
	send(scope, "second signed token")
	if observed != "first signed token" {
		t.Fatal("Fresh authorization was not reused")
	}
	sender.authorizations[scope] = authorization{value: "old signed token", createdAt: time.Now().Add(-time.Hour - time.Second)}
	send(scope, "fresh signed token")
	if observed != "fresh signed token" {
		t.Fatal("Expired authorization was reused")
	}
	for i := 0; i < maxAuthorizations+1; i++ {
		send(authorizationScope{origin: fmt.Sprintf("https://node%d.push.apple.com", i), publicKey: "reviewed-public-key", subject: "owner@example.test"}, "other signed token")
	}
	if len(sender.authorizations) != maxAuthorizations {
		t.Fatal("Authorization cache exceeded its bound")
	}
}
