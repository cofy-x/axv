package guest

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *service) handler() http.Handler {
	mux := http.NewServeMux()
	s.access.Register(mux)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		health := protocol.Health{Version: protocol.Version, MachineID: s.config.MachineID, BootID: s.bootID, InstanceID: s.instanceID}
		health.SSHAvailable = image.Has(s.capabilities, "ssh") && s.access.Info(r.Context()).Available
		taskError := s.taskAvailabilityError()
		health.Capabilities = []string{}
		for _, capability := range image.Capabilities() {
			if image.Has(s.capabilities, capability) && (capability != "ssh" || health.SSHAvailable) && (capability != "project-tasks" || taskError == nil) {
				health.Capabilities = append(health.Capabilities, capability)
			}
		}
		if err := s.projectReady(); err != nil {
			health.Reason = err.Error()
		} else {
			health.Ready = true
		}
		if health.Reason == "" && image.Has(s.capabilities, "project-tasks") && taskError != nil {
			health.Reason = "Project tasks unavailable: " + taskError.Error()
		}
		respond(w, http.StatusOK, health)
	})
	mux.HandleFunc("POST /v1/exec", s.execute)
	mux.HandleFunc("POST /v1/project-files/inspect", s.inspectProjectFiles)
	mux.HandleFunc("POST /v1/project-files/restore-check", s.checkRestoredProject)
	mux.HandleFunc("POST /v1/project-files/list", s.listProjectFiles)
	mux.HandleFunc("POST /v1/project-files/read", s.readProjectFile)
	mux.HandleFunc("POST /v1/project-files/write", s.writeProjectFile)
	mux.HandleFunc("POST /v1/project-files/mkdir", s.mkdirProjectFile)
	mux.HandleFunc("/v1/project-preview", s.projectPreview)
	for _, action := range []string{"submit", "inspect", "logs", "cancel"} {
		mux.HandleFunc("POST /v1/project-tasks/"+action, func(w http.ResponseWriter, r *http.Request) { s.projectTaskRequest(w, r, action) })
	}
	mux.HandleFunc("GET /v1/terminals", s.terminals.list)
	mux.HandleFunc("POST /v1/terminals", s.terminals.create)
	mux.HandleFunc("GET /v1/terminals/{id}", s.terminals.info)
	mux.HandleFunc("DELETE /v1/terminals/{id}", s.terminals.end)
	mux.HandleFunc("GET /v1/terminals/{id}/attach", s.terminals.attach)
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.shutdown(ctx); err != nil {
			fail(w, http.StatusServiceUnavailable, "shutdown_failed", err)
			return
		}
		respond(w, http.StatusAccepted, map[string]bool{"accepted": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.config.Token)) != 1 {
			fail(w, http.StatusForbidden, "forbidden", fmt.Errorf("guest authentication failed"))
			return
		}
		capability := ""
		switch {
		case r.URL.Path == "/v1/project-files/restore-check":
			capability = "project-restore"
		case r.URL.Path == "/v1/project-preview":
			capability = "project-previews"
		case r.URL.Path == "/v1/exec":
			capability = "exec"
		case strings.HasPrefix(r.URL.Path, "/v1/project-files"):
			capability = "project-files"
		case strings.HasPrefix(r.URL.Path, "/v1/project-tasks"):
			capability = "project-tasks"
		case strings.HasPrefix(r.URL.Path, "/v1/terminals"):
			capability = "terminals"
		case strings.HasPrefix(r.URL.Path, "/v1/ssh"):
			capability = "ssh"
		case strings.HasPrefix(r.URL.Path, "/v1/network"):
			capability = "network"
		}
		if capability != "" && !image.Has(s.capabilities, capability) {
			fail(w, http.StatusNotImplemented, "unsupported", fmt.Errorf("guest capability %s is not declared by this image", capability))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code string, err error) {
	respond(w, status, map[string]string{"code": code, "error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
