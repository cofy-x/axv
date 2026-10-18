package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(&fakePreparation{check: prepare.Check{Checks: []prepare.Requirement{}}}, "test-instance")
}

type fakePreparation struct {
	check  prepare.Check
	plan   prepare.Plan
	job    prepare.Job
	err    error
	called string
	id     string
	input  any
	ctx    context.Context
}

func (f *fakePreparation) record(ctx context.Context, method, id string, input any) {
	f.ctx, f.called, f.id, f.input = ctx, method, id, input
}

func (f *fakePreparation) Check(ctx context.Context) (prepare.Check, error) {
	f.record(ctx, "check", "", nil)
	return f.check, f.err
}

func (f *fakePreparation) Plan(ctx context.Context, request prepare.Request) (prepare.Plan, error) {
	f.record(ctx, "plan", "", request)
	return f.plan, f.err
}

func (f *fakePreparation) Start(ctx context.Context, request prepare.StartRequest) (prepare.Job, error) {
	f.record(ctx, "start", "", request)
	return f.job, f.err
}

func (f *fakePreparation) Job(ctx context.Context, id string) (prepare.Job, error) {
	f.record(ctx, "job", id, nil)
	return f.job, f.err
}

func (f *fakePreparation) Retry(ctx context.Context, id string, request prepare.ActionRequest) (prepare.Job, error) {
	f.record(ctx, "retry", id, request)
	return f.job, f.err
}

func (f *fakePreparation) Cancel(ctx context.Context, id string, request prepare.ActionRequest) (prepare.Job, error) {
	f.record(ctx, "cancel", id, request)
	return f.job, f.err
}

func TestEngineValidationRetainsHTTPContract(t *testing.T) {
	engine, err := prepare.New(prepare.Options{CacheHome: filepath.Join(t.TempDir(), "cache"), Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	handler := New(engine, "test-instance")
	cases := []struct {
		body    string
		code    string
		message string
		status  int
	}{
		{`{"requestId":"reviewed-request","planId":"missing-plan","confirmed":false}`, "confirmation_required", "Confirm the reviewed preparation plan before starting.", 400},
		{`{"requestId":"reviewed-request","planId":"missing-plan","confirmed":true}`, "plan_not_found", "Review a new preparation plan before continuing.", 404},
	}
	for _, tc := range cases {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request(http.MethodPost, "jobs", tc.body))
		assertErrorResponse(t, response, tc.status, tc.code, tc.message)
	}
}

func request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:3001/api/compute/preparation/"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:44000"
	r.Header.Set("Origin", "http://127.0.0.1:3001")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-AXV-Instance-ID", "test-instance")
	return r
}

func TestCheckExposesInstanceAndStableDirectShape(t *testing.T) {
	handler := testHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request(http.MethodGet, "check", ""))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if string(result["instanceId"]) != `"test-instance"` || result["defaults"] == nil || result["checks"] == nil || result["check"] != nil {
		t.Fatalf("unexpected shape: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}

func TestPreparationPreservesLocalOriginAndInstanceGuards(t *testing.T) {
	handler := testHandler(t)
	cases := []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"other origin", func(r *http.Request) { r.Header.Set("Origin", "https://example.com") }, 403},
		{"other port", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:3002") }, 403},
		{"remote peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.3:4000"; r.Header.Set("X-Forwarded-For", "127.0.0.1") }, 403},
		{"stale instance", func(r *http.Request) { r.Header.Set("X-AXV-Instance-ID", "old-instance") }, 409},
		{"missing instance", func(r *http.Request) { r.Header.Del("X-AXV-Instance-ID") }, 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request(http.MethodPost, "plan", "{}")
			tc.change(r)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPreparationRejectsNonObjectsUnknownFieldsAndOversizedBodies(t *testing.T) {
	handler := testHandler(t)
	for _, body := range []string{"null", "[]", "{} {}", `{"arbitraryCommand":"sh"}`, `{"storageRoot":"` + strings.Repeat("x", 64<<10) + `"}`} {
		r := request(http.MethodPost, "plan", body)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code != 400 {
			t.Fatalf("accepted invalid body with %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestPreparationForwardsRequestsAndResults(t *testing.T) {
	cases := []struct {
		name, method, path, body, operation, id string
		input                                   any
		status                                  int
	}{
		{"plan", http.MethodPost, "plan", `{"storageRoot":"/selected","cpus":3}`, "plan", "", prepare.Request{StorageRoot: "/selected", CPUs: 3}, 200},
		{"start", http.MethodPost, "jobs", `{"requestId":"start-request","planId":"plan-one","confirmed":true}`, "start", "", prepare.StartRequest{RequestID: "start-request", PlanID: "plan-one", Confirmed: true}, 202},
		{"job", http.MethodGet, "jobs/job-one", "", "job", "job-one", nil, 200},
		{"retry", http.MethodPost, "jobs/job-one/retry", `{"requestId":"retry-request","expectedRevision":7}`, "retry", "job-one", prepare.ActionRequest{RequestID: "retry-request", ExpectedRevision: 7}, 202},
		{"cancel", http.MethodPost, "jobs/job-one/cancel", `{"requestId":"cancel-request","expectedRevision":8}`, "cancel", "job-one", prepare.ActionRequest{RequestID: "cancel-request", ExpectedRevision: 8}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakePreparation{plan: prepare.Plan{ID: "plan-result"}, job: prepare.Job{ID: "job-result", Revision: 9}}
			r := request(tc.method, tc.path, tc.body)
			response := httptest.NewRecorder()
			New(service, "test-instance").ServeHTTP(response, r)
			if service.called != tc.operation || service.id != tc.id || !reflect.DeepEqual(service.input, tc.input) || service.ctx != r.Context() {
				t.Fatalf("unexpected call: %+v", service)
			}
			var value any = service.job
			if tc.operation == "plan" {
				value = service.plan
			}
			want, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || strings.TrimSpace(response.Body.String()) != string(want) {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDomainErrorsRetainStatusCodeAndShape(t *testing.T) {
	cases := []struct {
		kind   prepare.ErrorKind
		code   string
		status int
	}{
		{prepare.KindInvalidInput, "confirmation_required", 400},
		{prepare.KindNotFound, "plan_not_found", 404},
		{prepare.KindConflict, "configuration_changed", 409},
		{prepare.KindUnavailable, "preparation_stopping", 503},
		{prepare.KindInternal, "preparation_failed", 500},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			service := &fakePreparation{err: fmt.Errorf("wrapped: %w", &prepare.Error{Kind: tc.kind, Code: tc.code, Message: "Original failure detail."})}
			response := httptest.NewRecorder()
			New(service, "test-instance").ServeHTTP(response, request(http.MethodPost, "plan", "{}"))
			assertErrorResponse(t, response, tc.status, tc.code, "Original failure detail.")
		})
	}
	response := httptest.NewRecorder()
	New(&fakePreparation{err: errors.New("unexpected failure")}, "test-instance").ServeHTTP(response, request(http.MethodGet, "check", ""))
	assertErrorResponse(t, response, 500, "preparation_failed", "unexpected failure")
}

func TestProtocolErrorsRemainInHTTPAdapter(t *testing.T) {
	cases := []struct {
		name, method, path, body, contentType, code, message string
		status                                               int
	}{
		{"media type", http.MethodPost, "plan", "{}", "text/plain", "invalid_request", "Use an application/json request body.", 415},
		{"non-object", http.MethodPost, "plan", "null", "application/json", "invalid_request", "The preparation request must contain a JSON object.", 400},
		{"extra object", http.MethodPost, "plan", "{} {}", "application/json", "invalid_request", "The preparation request must contain one JSON object.", 400},
		{"unknown field", http.MethodPost, "plan", `{"unknown":true}`, "application/json", "invalid_request", `Invalid preparation request: json: unknown field "unknown"`, 400},
		{"method", http.MethodGet, "plan", "", "application/json", "method_not_allowed", "This preparation endpoint does not support that method.", 405},
		{"job route", http.MethodGet, "jobs/job-one/unknown", "", "application/json", "not_found", "Preparation endpoint was not found.", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakePreparation{}
			r := request(tc.method, tc.path, tc.body)
			r.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			New(service, "test-instance").ServeHTTP(response, r)
			assertErrorResponse(t, response, tc.status, tc.code, tc.message)
			if service.called != "" {
				t.Fatalf("protocol failure reached preparation service: %s", service.called)
			}
		})
	}
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || len(result) != 2 || result["code"] != code || result["error"] != message {
		t.Fatalf("unexpected error response: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response headers: %v", response.Header())
	}
}

func TestUnavailableRetainsOriginGuardAndPreservationError(t *testing.T) {
	handler := Unavailable("test-instance", errors.New("unsupported database format"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request(http.MethodGet, "check", ""))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "preparation_unavailable") {
		t.Fatal(response.Code, response.Body.String())
	}
	r := request(http.MethodGet, "check", "")
	r.RemoteAddr = "192.0.2.3:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != 403 {
		t.Fatal(response.Code)
	}
}
