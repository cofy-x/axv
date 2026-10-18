//go:build !production

package webui

import "net/http"

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The API is running. Start the Vite development server with pnpm dev, or use make build for a standalone application.\n"))
	})
}
