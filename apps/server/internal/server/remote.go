package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
	"github.com/cofy-x/axv/apps/server/internal/remoteaccess"
	"github.com/cofy-x/axv/apps/server/internal/tailnet"
)

// startRemote owns a second transport for the same workstation. Its failure
// remains visible through local status without taking down the local desktop.
func startRemote(ctx context.Context, settings config.Config, handler http.Handler) (func() tailnet.Status, func() error) {
	if !settings.Remote.Enabled {
		return func() tailnet.Status { return tailnet.Status{State: "disabled"} }, func() error { return nil }
	}
	logger := diagnostics.FromContext(ctx).With("component", "remote")
	service, err := tailnet.Start(ctx, tailnet.Options{
		StateDir:    filepath.Join(settings.Home, "tailscale"),
		Hostname:    settings.Remote.Hostname,
		OwnerUserID: settings.Remote.OwnerUserID,
		Handler: func(endpoint tailnet.Endpoint) (http.Handler, func(), error) {
			middleware, err := remoteaccess.NewMiddleware(remoteaccess.MiddlewareOptions{
				Origin:         endpoint.Origin,
				OwnerUserID:    endpoint.OwnerUserID,
				WhoIs:          endpoint.WhoIs,
				AllowRoute:     remoteRoute,
				LocalOnlyRoute: remoteLocalOnlyRoute,
			})
			if err != nil {
				return nil, nil, err
			}
			return middleware.Wrap(handler), middleware.Close, nil
		},
		Update: func(status tailnet.Status) {
			// Enrollment URLs are bearer capabilities. AXV's operational log
			// records only the state; local status provides the enrollment URL.
			logger.Info("remote.state", "state", status.State)
		},
	})
	if err != nil {
		logger.Error("remote.unavailable", err)
		return func() tailnet.Status { return tailnet.Status{State: "error", Error: err.Error()} }, func() error { return nil }
	}
	return service.Status, service.Close
}

func remoteStatusHandler(instanceID string, status func() tailnet.Status) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !localaccess.Request(r, false) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "remote_local_only", "error": "Check remote access on the computer running AXV."})
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			InstanceID string `json:"instanceId"`
			tailnet.Status
		}{InstanceID: instanceID, Status: status()})
	})
}

func remoteLocalOnlyRoute(r *http.Request) bool {
	return pathWithin(r.URL.Path, "/api/remote") || pathWithin(r.URL.Path, "/api/compute/preparation")
}

// Keep new API families private until their owner explicitly opts them into the
// remote transport. Feature-level identifiers and mutation guards still apply.
func remoteRoute(r *http.Request) bool {
	if remoteLocalOnlyRoute(r) || pathWithin(r.URL.Path, "/v1") {
		return false
	}
	if pathWithin(r.URL.Path, "/api") {
		for _, prefix := range []string{
			"/api/health", "/api/bootstrap", "/api/connection", "/api/entries", "/api/import",
			"/api/preferences", "/api/layout", "/api/trash", "/api/terminal",
			"/api/projects", "/api/documents", "/api/document-links", "/api/document-snapshots",
			"/api/host/directories", "/api/host/codex", "/api/compute",
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
