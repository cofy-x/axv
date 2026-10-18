package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func TestRuntimeReadinessRequiresControllerRecovery(t *testing.T) {
	store, err := control.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ready := make(chan struct{})
	service := control.NewService(control.Options{Store: store, OnReady: func() { close(ready) }})
	handler := Handler(service)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/v1/runtime", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("unrecovered controller reported ready")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-ready
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/v1/runtime", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if service.Ready() {
		t.Fatal("stopped controller remained ready")
	}
}
