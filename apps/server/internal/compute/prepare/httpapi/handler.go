// Package httpapi adapts the shared preparation engine to the local desktop
// API. It does not own compute processes or the desktop listener.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

const prefix = "/api/compute/preparation/"

type preparation interface {
	Check(context.Context) (prepare.Check, error)
	Plan(context.Context, prepare.Request) (prepare.Plan, error)
	Start(context.Context, prepare.StartRequest) (prepare.Job, error)
	Job(context.Context, string) (prepare.Job, error)
	Retry(context.Context, string, prepare.ActionRequest) (prepare.Job, error)
	Cancel(context.Context, string, prepare.ActionRequest) (prepare.Job, error)
}

func New(service preparation, instanceID string) http.Handler {
	return guarded(instanceID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if path == r.URL.Path {
			respond(w, 404, map[string]string{"code": "not_found", "error": "Preparation endpoint was not found."})
			return
		}
		var value any
		var err error
		status := http.StatusOK
		switch {
		case path == "check" && r.Method == http.MethodGet:
			var check prepare.Check
			check, err = service.Check(r.Context())
			value = struct {
				prepare.Check
				InstanceID string `json:"instanceId"`
			}{check, instanceID}
		case path == "plan" && r.Method == http.MethodPost:
			var request prepare.Request
			if err = decode(r, &request); err == nil {
				value, err = service.Plan(r.Context(), request)
			}
		case path == "jobs" && r.Method == http.MethodPost:
			var request prepare.StartRequest
			if err = decode(r, &request); err == nil {
				value, err = service.Start(r.Context(), request)
				status = http.StatusAccepted
			}
		case strings.HasPrefix(path, "jobs/"):
			parts := strings.Split(strings.TrimPrefix(path, "jobs/"), "/")
			if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodGet {
				value, err = service.Job(r.Context(), parts[0])
			} else if len(parts) == 2 && parts[0] != "" && r.Method == http.MethodPost && (parts[1] == "retry" || parts[1] == "cancel") {
				var request prepare.ActionRequest
				if err = decode(r, &request); err == nil {
					if parts[1] == "retry" {
						value, err = service.Retry(r.Context(), parts[0], request)
						status = http.StatusAccepted
					} else {
						value, err = service.Cancel(r.Context(), parts[0], request)
					}
				}
			} else {
				err = &responseError{status: 404, code: "not_found", message: "Preparation endpoint was not found."}
			}
		default:
			err = &responseError{status: 405, code: "method_not_allowed", message: "This preparation endpoint does not support that method."}
		}
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, status, value)
	}))
}

func Unavailable(instanceID string, cause error) http.Handler {
	return guarded(instanceID, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 503, map[string]string{"code": "preparation_unavailable", "error": fmt.Sprintf("VM preparation is unavailable; existing data was preserved: %v", cause)})
	}))
}

func guarded(instanceID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mutation := r.Method != http.MethodGet && r.Method != http.MethodHead
		if !localaccess.Request(r, mutation) {
			respond(w, 403, map[string]string{"code": "preparation_forbidden", "error": "VM preparation requires a same-origin connection from this computer."})
			return
		}
		if mutation && (instanceID == "" || r.Header.Get("X-AXV-Instance-ID") != instanceID) {
			respond(w, 409, map[string]string{"code": "instance_changed", "error": "The AXV desktop instance changed. Refresh preparation before continuing."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decode(r *http.Request, value any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return &responseError{status: 415, code: "invalid_request", message: "Use an application/json request body."}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return &responseError{status: 400, code: "invalid_request", message: "The preparation request exceeds 64 KiB or could not be read."}
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return &responseError{status: 400, code: "invalid_request", message: "The preparation request must contain a JSON object."}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return &responseError{status: 400, code: "invalid_request", message: "Invalid preparation request: " + err.Error()}
	}
	if d.Decode(new(any)) != io.EOF {
		return &responseError{status: 400, code: "invalid_request", message: "The preparation request must contain one JSON object."}
	}
	return nil
}

type responseError struct {
	status        int
	code, message string
}

func (e *responseError) Error() string { return e.message }

func writeError(w http.ResponseWriter, err error) {
	var protocol *responseError
	if errors.As(err, &protocol) {
		respond(w, protocol.status, map[string]string{"code": protocol.code, "error": protocol.message})
		return
	}
	var typed *prepare.Error
	if errors.As(err, &typed) {
		status := http.StatusInternalServerError
		switch typed.Kind {
		case prepare.KindInvalidInput:
			status = http.StatusBadRequest
		case prepare.KindNotFound:
			status = http.StatusNotFound
		case prepare.KindConflict:
			status = http.StatusConflict
		case prepare.KindUnavailable:
			status = http.StatusServiceUnavailable
		}
		respond(w, status, typed)
		return
	}
	respond(w, 500, map[string]string{"code": "preparation_failed", "error": err.Error()})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
