package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type API struct{ store *storage.Store }

func New(store *storage.Store, ui http.Handler) http.Handler {
	a := &API{store: store}
	mux := http.NewServeMux()
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, a.bound(handler, pattern == "GET /api/entries/{id}/content"))
	}
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(r.Context()); err != nil {
			fail(w, err)
			return
		}
		write(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		b, err := store.Bootstrap(r.Context(), r.URL.Query().Get("clientId"))
		respond(w, b, err)
	})
	register("GET /api/entries", func(w http.ResponseWriter, r *http.Request) {
		entries, err := store.List(r.Context())
		respond(w, map[string]any{"entries": entries}, err)
	})
	register("POST /api/entries", a.create)
	register("GET /api/entries/{id}", a.get)
	register("PATCH /api/entries/{id}", a.update)
	register("GET /api/entries/{id}/content", a.content)
	register("POST /api/entries/{id}/duplicate", a.duplicate)
	register("POST /api/entries/{id}/trash", func(w http.ResponseWriter, r *http.Request) {
		entry, err := store.Trash(r.Context(), r.PathValue("id"))
		respond(w, map[string]any{"entry": entry}, err)
	})
	register("POST /api/entries/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		entry, err := store.Restore(r.Context(), r.PathValue("id"))
		respond(w, map[string]any{"entry": entry}, err)
	})
	register("DELETE /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, store.Delete(r.Context(), r.PathValue("id")))
	})
	register("DELETE /api/trash", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, store.EmptyTrash(r.Context()))
	})
	register("POST /api/import", a.importFiles)
	register("PUT /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		var p storage.Preferences
		if !decode(w, r, &p) {
			return
		}
		respond(w, p, store.SavePreferences(r.Context(), p))
	})
	register("PUT /api/layout", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ClientID string          `json:"clientId"`
			Layout   json.RawMessage `json:"layout"`
		}
		if !decode(w, r, &in) {
			return
		}
		respond(w, map[string]bool{"ok": true}, store.SaveLayout(r.Context(), in.ClientID, in.Layout))
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
	})
	mux.Handle("/", ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		// Browsers may send cross-origin form posts to loopback services. Accept only
		// same-origin or loopback development origins; no CORS credentials are exposed.
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Host != r.Host && !loopback(u.Hostname())) {
					writeError(w, http.StatusForbidden, "invalid_origin", "Cross-origin changes are not allowed.")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *API) bound(next http.HandlerFunc, allowQuery bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := a.store.DatabaseID(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if httpaccess.RequireDatabaseIdentity(w, r, identity, allowQuery) {
			next(w, r)
		}
	}
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
