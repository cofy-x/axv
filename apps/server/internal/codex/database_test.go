package codex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func requireUnreservedRequest(t *testing.T, manager *Manager, requestID string) {
	t.Helper()
	_, err := manager.store.CodexRequest(context.Background(), requestID)
	var missing *storage.Error
	if !errors.As(err, &missing) || missing.Code != "not_found" {
		t.Fatalf("rejected source-store request was reserved: request=%s error=%v", requestID, err)
	}
}

func TestLaunchDatabaseFencePrecedesReplayValidationAndReservation(t *testing.T) {
	original := testManager(t)
	replacement := testManager(t)
	rejectServiceHostWork(t, replacement)
	replacement.settings.ResolveVMProject = func(context.Context, compute.ProjectBinding) (VMProjectResolution, error) {
		t.Fatal("stale database request inspected a live VM environment")
		return VMProjectResolution{}, nil
	}
	if original.databaseID == "" || replacement.databaseID == "" || original.databaseID == replacement.databaseID {
		t.Fatal("independent fixtures share a database identity")
	}
	known := serviceRunningRun(t, replacement, "request-present-in-replacement")
	ctx := context.Background()
	for _, request := range []StartInput{
		{RequestID: "unknown-create-request", ExpectedDatabaseID: original.databaseID},
		{RequestID: "unknown-vm-create-request", ExpectedDatabaseID: original.databaseID, VMProject: &VMProjectInput{}},
		{RequestID: known.RequestID, ExpectedDatabaseID: original.databaseID, ProjectPath: known.ProjectPath},
		{ExpectedDatabaseID: original.databaseID},
	} {
		_, err := replacement.Create(ctx, request)
		requireServiceError(t, err, Conflict, "database_changed")
	}
	for _, request := range []ResumeInput{
		{RequestID: "unknown-resume-request", ExpectedDatabaseID: original.databaseID, ExpectedRunID: known.RunID},
		{RequestID: "unknown-vm-resume-request", ExpectedDatabaseID: original.databaseID, ExpectedRunID: known.RunID, VMProject: &VMProjectInput{}},
		{RequestID: known.RequestID, ExpectedDatabaseID: original.databaseID, ExpectedRunID: known.RunID},
		{ExpectedDatabaseID: original.databaseID},
	} {
		_, err := replacement.Resume(ctx, known.ID, request)
		requireServiceError(t, err, Conflict, "database_changed")
	}
	for _, requestID := range []string{"unknown-create-request", "unknown-vm-create-request", "unknown-resume-request", "unknown-vm-resume-request"} {
		requireUnreservedRequest(t, replacement, requestID)
	}
	sessions, err := replacement.store.CodexSessions(ctx)
	if err != nil || len(sessions) != 1 || !reflect.DeepEqual(sessions[0], known) {
		t.Fatalf("stale source-store request changed replacement runs: %+v %v", sessions, err)
	}
	if _, err := os.Stat(replacement.settings.WorkspaceDir); !os.IsNotExist(err) {
		t.Fatalf("stale source-store request created a host workspace: %v", err)
	}
	if sessions, err := original.store.CodexSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("database fencing modified the original store: %+v %v", sessions, err)
	}
}

func TestLaunchHTTPDatabaseFenceIsRequiredAndPreservesRejectedRequests(t *testing.T) {
	manager := testManager(t)
	rejectServiceHostWork(t, manager)
	manager.settings.ResolveVMProject = func(context.Context, compute.ProjectBinding) (VMProjectResolution, error) {
		t.Fatal("stale HTTP request inspected a VM environment")
		return VMProjectResolution{}, nil
	}
	mux := http.NewServeMux()
	manager.Register(mux)
	for _, endpoint := range []string{"create", "resume"} {
		path := "/api/host/codex/sessions"
		if endpoint == "resume" {
			path += "/unknown-session/resume"
		}
		for _, databaseID := range []string{"", manager.databaseID, "previous-desktop-database"} {
			body := `{"requestId":""}`
			if databaseID == "previous-desktop-database" {
				body = `{"requestId":"http-` + endpoint + `-stale","vmProject":{"binding":{}}}`
				if endpoint == "resume" {
					body = `{"requestId":"http-resume-stale","expectedRunId":"original-run","vmProject":{"binding":{}}}`
				}
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.RemoteAddr, request.Host = "127.0.0.1:44", "localhost:3001"
			request.Header.Set("Origin", "http://localhost:3001")
			request.Header.Set("Content-Type", "application/json")
			if databaseID != "" {
				request.Header.Set("X-AXV-Database-ID", databaseID)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			status, code := http.StatusBadRequest, "invalid_session"
			if databaseID == "" {
				status, code = http.StatusPreconditionRequired, "database_identity_required"
			} else if databaseID == "previous-desktop-database" {
				status, code = http.StatusConflict, "database_changed"
			}
			if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
				t.Fatalf("unexpected %s database fence response for %q: %d %s", endpoint, databaseID, response.Code, response.Body.String())
			}
		}
		requireUnreservedRequest(t, manager, "http-"+endpoint+"-stale")
	}
	sessions, err := manager.store.CodexSessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("rejected HTTP requests reserved runs: %+v %v", sessions, err)
	}
}
