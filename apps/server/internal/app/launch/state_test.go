package launch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessRequiresInstanceIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "other"})
	}))
	defer srv.Close()
	if Verify(context.Background(), State{URL: srv.URL, InstanceID: "expected"}) == nil {
		t.Fatal("accepted an unrelated healthy HTTP service")
	}
	if err := Verify(context.Background(), State{URL: srv.URL, InstanceID: "other"}); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessRejectsRedirectsAndMismatchedLaunchOwnership(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "desktop", "launchId": "launch-owned"})
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirect.Close()
	for _, state := range []State{
		{URL: redirect.URL, InstanceID: "desktop", LaunchID: "launch-owned"},
		{URL: target.URL, InstanceID: "desktop", LaunchID: "another-launch"},
		{URL: target.URL, InstanceID: "desktop"},
	} {
		if err := Verify(context.Background(), state); err == nil {
			t.Fatalf("accepted unverifiable ownership: %#v", state)
		}
	}
	if err := Verify(context.Background(), State{URL: target.URL, InstanceID: "desktop", LaunchID: "launch-owned"}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalDesktopURLRequiresLiteralLoopbackOrigin(t *testing.T) {
	for _, value := range []string{"https://127.0.0.1:3001", "http://127.0.0.1", "http://localhost:3001", "http://example.com:3001", "http://192.0.2.1:3001", "http://0.0.0.0:3001", "http://user@127.0.0.1:3001", "http://127.0.0.1:3001/path", "http://127.0.0.1:3001?query", "http://127.0.0.1:3001?", "http://127.0.0.1:3001#fragment"} {
		if _, err := LocalDesktopURL(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"http://127.0.0.1:3001", "http://127.0.0.1:3001/", "http://[::1]:3001"} {
		if _, err := LocalDesktopURL(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
}
