// Package httpapi adapts local host-directory selection to HTTP clients.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/cofy-x/axv/apps/server/internal/hostdir"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

// Register adds the local host-directory endpoint independently of any agent.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/host/directories", directories)
}

func directories(w http.ResponseWriter, r *http.Request) {
	if !httpaccess.AllowRequest(r, false) {
		write(w, http.StatusForbidden, map[string]string{"code": "host_forbidden", "error": "Host directories require a same-origin connection from this computer."})
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || (len(query) == 1 && len(query["path"]) != 1) {
		write(w, http.StatusBadRequest, map[string]string{"code": "invalid_directory", "error": "Use one optional path parameter."})
		return
	}
	result, err := hostdir.List(query.Get("path"))
	respond(w, result, err)
}

func respond(w http.ResponseWriter, value any, err error) {
	status := http.StatusOK
	if err != nil {
		var directoryError *hostdir.Error
		if errors.As(err, &directoryError) {
			switch directoryError.Kind {
			case hostdir.Unreadable:
				status = http.StatusForbidden
			case hostdir.HomeUnavailable:
				status = http.StatusServiceUnavailable
			default:
				status = http.StatusBadRequest
			}
			value = map[string]string{"code": directoryError.Code, "error": directoryError.Message}
		} else {
			log.Printf("Host directory operation failed: %v", err)
			status = http.StatusInternalServerError
			value = map[string]string{"code": "internal_error", "error": "The host directory operation could not be completed. Try again."}
		}
	}
	write(w, status, value)
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
