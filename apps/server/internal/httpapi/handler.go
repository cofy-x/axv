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

func New(store *storage.Store, ui http.Handler) http.Handler {
	mux := http.NewServeMux()
	register := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, bindDatabase(store, handler))
	}
	health := func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(r.Context()); err != nil {
			fail(w, err)
			return
		}
		write(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	boundHealth := bindDatabase(store, health)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		// Launchers probe before bootstrap. A mounted desktop probes the
		// database captured when its page opened.
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || query.Has("databaseId") || len(r.Header.Values(httpaccess.DatabaseIdentityHeader)) != 0 {
			boundHealth(w, r)
			return
		}
		health(w, r)
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		b, err := store.Bootstrap(r.Context(), r.URL.Query().Get("clientId"))
		respond(w, b, err)
	})
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

func bindDatabase(store *storage.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := store.DatabaseID(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if httpaccess.RequireDatabaseIdentity(w, r, identity, false) {
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
