package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
	"github.com/cofy-x/axv/apps/server/internal/tailnet/access"
)

// startRemote owns a second transport for the same workstation. Its failure
// remains visible through local status without taking down the local desktop.
func startRemote(ctx context.Context, home string, settings profile.Remote, handler http.Handler) (remoteService, error) {
	logger := diagnostics.FromContext(ctx).With("component", "remote")
	service, err := tailnet.Start(ctx, tailnet.Options{
		StateDir:    filepath.Join(home, "tailscale"),
		Hostname:    settings.Hostname,
		OwnerUserID: settings.OwnerUserID,
		Handler: func(endpoint tailnet.Endpoint) (http.Handler, func(), error) {
			middleware, err := access.NewMiddleware(access.MiddlewareOptions{
				Origin:         endpoint.Origin,
				OwnerUserID:    endpoint.OwnerUserID,
				WhoIs:          endpoint.WhoIs,
				AllowRoute:     remoteRoute,
				LocalOnlyRoute: remoteLocalOnlyRoute,
			})
			if err != nil {
				return nil, nil, err
			}
			return middleware.Wrap(handler), func() {
				middleware.Close()
			}, nil
		},
		Update: func(status tailnet.Status) {
			// Enrollment URLs are bearer capabilities. AXV's operational log
			// records only the state; local status provides the enrollment URL.
			logger.Info("remote.state", "state", status.State)
		},
	})
	if err != nil {
		logger.Error("remote.unavailable", err)
		return nil, err
	}
	return service, nil
}

func remoteStatusHandler(instanceID string, status func() remoteSnapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !httpaccess.LocalRequest(r, false) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "remote_local_only", "error": "Check remote access on the computer running AXV."})
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			InstanceID string `json:"instanceId"`
			remoteSnapshot
		}{InstanceID: instanceID, remoteSnapshot: status()})
	})
}

func remoteSettingsHandler(controller *remoteController) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !httpaccess.LocalRequest(r, true) {
			remoteWriteError(w, http.StatusForbidden, "remote_local_only", "Change remote access on the computer running AXV.")
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil || decoder.Decode(new(any)) != io.EOF {
			remoteWriteError(w, http.StatusBadRequest, "invalid_remote_request", "Provide only an enabled boolean to change remote access.")
			return
		}
		if err := controller.SetEnabled(r.Context(), *input.Enabled); err != nil {
			var failure *remoteControlError
			if errors.As(err, &failure) {
				remoteWriteError(w, failure.status, failure.code, failure.message)
			} else {
				remoteWriteError(w, http.StatusServiceUnavailable, "remote_stopping", "The remote access change could not complete. Refresh its status.")
			}
			return
		}
		_ = json.NewEncoder(w).Encode(remoteConnection(controller.Status()))
	})
}

func remoteWriteError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
}

func remoteLocalOnlyRoute(r *http.Request) bool {
	if pathWithin(r.URL.Path, "/api/extensions") {
		return extensionsLocalOnlyRoute(r)
	}
	if pathWithin(r.URL.Path, "/api/cloudflare") {
		return !cloudflareSafeRead(r)
	}
	return pathWithin(r.URL.Path, "/api/remote") || pathWithin(r.URL.Path, "/api/compute/preparation")
}

func cloudflareSafeRead(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/api/cloudflare/health" || r.URL.Path == "/api/cloudflare/traffic" || r.URL.Path == "/api/cloudflare/capabilities"
}

func extensionsLocalOnlyRoute(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/extensions"), "/")
	if r.URL.Path == "/api/extensions/install" {
		return true
	}
	return (len(parts) == 2 && (r.Method == http.MethodPut || r.Method == http.MethodDelete)) ||
		(len(parts) == 3 && parts[2] == "data" && r.Method == http.MethodDelete)
}

func remoteExtensionRoute(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/extensions"), "/")
	if r.URL.Path == "/api/extensions" {
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	if len(parts) < 3 || parts[0] != "" || parts[1] == "" {
		return false
	}
	switch {
	case len(parts) == 3 && parts[2] == "data":
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	case len(parts) == 5 && parts[2] == "content" && parts[3] != "" && parts[4] != "":
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	case len(parts) == 3 && parts[2] == "sessions":
		return r.Method == http.MethodPost
	case len(parts) == 4 && parts[2] == "sessions" && parts[3] != "":
		return r.Method == http.MethodDelete
	case len(parts) == 5 && parts[2] == "sessions" && parts[3] != "" && parts[4] == "rpc":
		return r.Method == http.MethodPost
	default:
		return false
	}
}

// Keep new API families private until their owner explicitly opts them into the
// remote transport. Feature-level identifiers and mutation guards still apply.
func remoteRoute(r *http.Request) bool {
	if remoteLocalOnlyRoute(r) || pathWithin(r.URL.Path, "/v1") {
		return false
	}
	if pathWithin(r.URL.Path, "/api") {
		if cloudflareSafeRead(r) {
			return true
		}
		if pathWithin(r.URL.Path, "/api/extensions") {
			return remoteExtensionRoute(r)
		}
		for _, prefix := range []string{
			"/api/health", "/api/bootstrap", "/api/connection", "/api/entries", "/api/import",
			"/api/preferences", "/api/layout", "/api/trash", "/api/terminal",
			"/api/projects", "/api/documents", "/api/document-links", "/api/document-snapshots",
			"/api/host/directories", "/api/host/codex", "/api/compute",
			"/api/project-previews",
			"/api/cloudflare-publications",
		} {
			if pathWithin(r.URL.Path, prefix) {
				return true
			}
		}
		return false
	}
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func pathWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
