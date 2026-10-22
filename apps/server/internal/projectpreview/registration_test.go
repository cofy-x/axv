package projectpreview

import (
	"context"
	"encoding/json"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The readiness probe must inspect HTTP status, not just environment validity.
func TestHTTP503DoesNotBecomeReadyPreview(t *testing.T) {
	m := New(context.Background(), func(_ context.Context, _ compute.ProjectBinding, _ int) (Target, error) {
		return Target{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }), Validate: func(context.Context) error { return nil }, Close: func() {}}, nil
	})
	defer m.Close()
	mux := http.NewServeMux()
	m.Register(mux)
	data, _ := json.Marshal(Registration{InstanceID: m.instanceID, RequestID: "original-probe-request", Binding: testBinding(), Port: 5173})
	r := httptest.NewRequest("POST", "http://127.0.0.1:3000/api/project-previews/project-one", strings.NewReader(string(data)))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://127.0.0.1:3000")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-AXV-Host-ID", "host-one")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 503 || len(m.entries) != 0 {
		t.Fatalf("unready service registered: %d %d", w.Code, len(m.entries))
	}
}
