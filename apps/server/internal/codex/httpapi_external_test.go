package codex_test

import (
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	"github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
)

func init() {
	codex.TestRegisterHTTP = func(mux *http.ServeMux, manager *codex.Manager) {
		handler := httpapi.New(manager)
		handler.Register(mux)
		go func() { <-manager.Done(); handler.Close() }()
	}
}
