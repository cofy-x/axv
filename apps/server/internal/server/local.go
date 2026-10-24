package server

import (
	"encoding/json"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

// localDesktopHandler wraps only the ordinary desktop listener. Explicit remote
// transports authenticate independently before reaching the shared handler.
func localDesktopHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localaccess.PeerHost(r) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code": "local_only", "error": "Use the local AXV address or a configured authenticated remote entrypoint.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
