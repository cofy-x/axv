package vmproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandlerInjectsHostKeyAndRejectsInvalidGuestToken(t *testing.T) {
	var gotAuth, gotPath, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler, err := NewHandler(Config{
		APIKey:                "sk-real-host-secret",
		BaseURL:               upstream.URL + "/v1",
		AllowLoopbackUpstream: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Health check succeeds without auth
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}

	// Wrong guest token is rejected
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for invalid guest token, got %d", rec.Code)
	}

	// Valid guest placeholder token gets replaced with real host API key and strips cookies
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	req.Header.Set("Authorization", "Bearer "+ProxyToken)
	req.Header.Set("Cookie", "session=should-be-stripped")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer sk-real-host-secret" {
		t.Fatalf("expected injected host key, got %q", gotAuth)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("expected /v1/responses, got %q", gotPath)
	}
	if gotCookie != "" {
		t.Fatalf("expected cookie stripped, got %q", gotCookie)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestValidateUpstreamRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://api.openai.com/v1",
		"https://user:pass@api.openai.com/v1",
		"https://127.0.0.1:8080/v1",
		"https://169.254.169.254/latest",
	} {
		if _, err := NewHandler(Config{APIKey: "sk-test", BaseURL: raw}); err == nil {
			t.Fatalf("expected error for unsafe upstream %q", raw)
		}
	}
}

func TestStatusRedactsKey(t *testing.T) {
	cfg := Config{APIKey: "sk-super-secret-value", BaseURL: "https://api.openai.com/v1?key=secret", Source: "axv_env"}
	status := cfg.Status()
	if !status.Enabled || status.BaseURL != "https://api.openai.com/v1" || status.Source != "axv_env" {
		t.Fatalf("unexpected status: %+v", status)
	}
}
