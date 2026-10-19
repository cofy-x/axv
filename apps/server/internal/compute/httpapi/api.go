// Package httpapi exposes the compute service over its private host socket.
package httpapi

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func Handler(service *control.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/runtime", func(w http.ResponseWriter, r *http.Request) {
		runtime := service.Runtime()
		status := http.StatusOK
		if !runtime.Ready {
			status = http.StatusServiceUnavailable
		}
		write(w, status, runtime)
	})
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		info, err := service.Info(r.Context())
		reply(w, info, err)
	})
	mux.HandleFunc("GET /v1/machines", func(w http.ResponseWriter, r *http.Request) {
		machines, err := service.Machines(r.Context())
		reply(w, map[string]any{"machines": machines}, err)
	})
	mux.HandleFunc("GET /v1/machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		machine, err := service.Machine(r.Context(), r.PathValue("id"))
		reply(w, machine, err)
	})
	mux.HandleFunc("POST /v1/machines", func(w http.ResponseWriter, r *http.Request) {
		req := compute.CreateRequest{Start: true}
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.Create(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /v1/machines/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var req compute.ActionRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.Action(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("PATCH /v1/machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req compute.UpdateRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.Update(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("GET /v1/operations", func(w http.ResponseWriter, r *http.Request) {
		operations, err := service.Operations(r.Context())
		reply(w, map[string]any{"operations": operations}, err)
	})
	mux.HandleFunc("GET /v1/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		operation, err := service.Operation(r.Context(), r.PathValue("id"))
		reply(w, operation, err)
	})
	mux.HandleFunc("GET /v1/operations/by-request/{requestId}", func(w http.ResponseWriter, r *http.Request) {
		operation, err := service.OperationByRequestID(r.Context(), r.PathValue("requestId"))
		reply(w, operation, err)
	})
	mux.HandleFunc("GET /v1/volumes", func(w http.ResponseWriter, r *http.Request) {
		volumes, err := service.Volumes(r.Context())
		reply(w, map[string]any{"volumes": volumes}, err)
	})
	mux.HandleFunc("POST /v1/volumes", func(w http.ResponseWriter, r *http.Request) {
		var req compute.CreateVolumeRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.CreateVolume(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /v1/volumes/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var req compute.VolumeRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.VolumeAction(r.Context(), r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /v1/volumes/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RequestID string `json:"requestId"`
			Revision  int64  `json:"revision"`
		}
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		op, err := service.DeleteVolume(r.Context(), r.PathValue("id"), req.RequestID, req.Revision)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, op)
	})
	mux.HandleFunc("/v1/machines/{id}/guest/{path...}", func(w http.ResponseWriter, r *http.Request) { proxyGuest(service, w, r) })
	registerImages(mux, service)
	registerGuestSSH(mux, service)
	registerProjects(mux, service)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return compute.Problem("invalid_request", "Invalid request: %v", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return compute.Problem("invalid_request", "Expected one JSON object.")
	}
	return nil
}

func reply(w http.ResponseWriter, value any, err error) {
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, value)
}

func failure(w http.ResponseWriter, err error) {
	status, code, message := 500, compute.ErrorCode(err), err.Error()
	switch code {
	case "invalid_request", "invalid_path", "invalid_content", "invalid_directory", "invalid_cursor", "not_file":
		status = 400
	case "not_found":
		status = 404
	case "binding_changed", "boot_changed", "instance_changed", "project_changed", "file_changed", "file_conflict", "revision_conflict", "operation_conflict", "idempotency_conflict", "volume_conflict", "image_conflict", "configuration_conflict", "name_conflict", "unavailable":
		status = 409
	case "capacity_exceeded":
		status = 429
	case "unsupported", "host_unavailable", "guest_unavailable", "guest_identity_mismatch", "storage_unavailable", "project_unavailable", "file_unavailable":
		status = 503
	case "file_too_large":
		status = 413
	case "insufficient_storage", "disk_full":
		status = 507
	case "file_forbidden":
		status = 403
	case "not_text":
		status = 415
	default:
		log.Printf("compute API: %v", err)
		message = "The compute operation could not be completed. Check the host service log."
	}
	write(w, status, map[string]string{"code": code, "error": message})
}

var terminalPath = regexp.MustCompile(`^terminals/[a-f0-9]{32}(/attach)?$`)

func proxyGuest(service *control.Service, w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	allowed := ((path == "health" || path == "terminals") && r.Method == "GET") || ((path == "exec" || path == "terminals") && r.Method == "POST") || (terminalPath.MatchString(path) && (r.Method == "GET" || (r.Method == "DELETE" && !strings.HasSuffix(path, "/attach"))))
	if !allowed {
		http.NotFound(w, r)
		return
	}
	capability := ""
	if path == "exec" {
		capability = "exec"
	} else if strings.HasPrefix(path, "terminals") {
		capability = "terminals"
	}
	access, err := service.GuestAccess(r.Context(), r.PathValue("id"), capability)
	if err != nil {
		failure(w, err)
		return
	}
	m, d := access.Machine, access.Driver
	token, err := d.GuestToken(m)
	if err != nil {
		failure(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	transport := compute.GuestTransport(m, d)
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "guest", "guest"
			p.Out.URL.Path, p.Out.URL.RawPath = "/v1/"+path, ""
			p.Out.Header.Set("Authorization", "Bearer "+token)
			p.Out.Header.Del("Cookie")
		}, Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			service.Logger.Error("guest.transport_failed", err, "machineId", m.ID)
			write(w, 503, map[string]string{"code": "guest_unavailable", "error": "The guest connection is unavailable. The machine or terminal was not replaced."})
		},
	}
	proxy.ServeHTTP(w, r)
}
