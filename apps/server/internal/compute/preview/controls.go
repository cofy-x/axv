package preview

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/localaccess"
)

const remoteUnsupported = "Development previews currently require a browser on the computer running AXV. Remote previews require an independently isolated HTTPS origin and are unavailable."

func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/project-previews/{projectId}", m.list)
	mux.HandleFunc("POST /api/project-previews/{projectId}", m.create)
	mux.HandleFunc("POST /api/project-previews/{projectId}/{id}/stop", m.stop)
}

func allowed(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || !localaccess.AllowRequest(r, r.Method != http.MethodGet) {
		fail(w, 403, "preview_forbidden", "Open preview controls from the AXV desktop.")
		return false
	}
	if r.Header.Get("X-AXV-Host-ID") == "" {
		fail(w, 409, "binding_changed", "The original AXV host identity is required for preview controls.")
		return false
	}
	return true
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, map[string]string{"code": code, "error": message})
}

func decodeControl(w http.ResponseWriter, r *http.Request, result any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return compute.Problem("invalid_request", "Preview controls require application/json.")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil || !jsonunicode.Valid(data) {
		return compute.Problem("invalid_request", "Preview controls require bounded valid JSON.")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return compute.Problem("invalid_request", "Expected one preview request object.")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(result); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return compute.Problem("invalid_request", "Expected one preview request object.")
	}
	return nil
}

func (e *entry) snapshot(r *http.Request) View {
	view := e.view
	view.URL = ""
	if !localaccess.Request(r, false) {
		view.RemoteError = remoteUnsupported
	} else if view.State == "ready" {
		view.URL = e.local.origin + "/.axv-open/" + e.token
	}
	return view
}
func (m *Manager) list(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	views := []View{}
	retired := 0
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.view.ProjectID != r.PathValue("projectId") || e.view.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
			continue
		}
		if e.view.State != "ready" {
			retired++
			if retired > retainedPreviewHistory {
				continue
			}
		}
		views = append(views, e.snapshot(r))
	}
	supported := localaccess.Request(r, false)
	result := map[string]any{"instanceId": m.instanceID, "supported": supported, "previews": views}
	if !supported {
		result["reason"] = remoteUnsupported
	}
	reply(w, 200, result)
}

func (m *Manager) create(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	if !localaccess.Request(r, true) {
		fail(w, 501, "preview_remote_unsupported", remoteUnsupported)
		return
	}
	var input Registration
	if err := decodeControl(w, r, &input); err != nil || compute.ValidateRequestID(input.RequestID) != nil || input.Port < 1024 || input.Port > 65535 || input.Binding.ProjectID != r.PathValue("projectId") || input.Binding.HostID == "" || input.Binding.HostID != r.Header.Get("X-AXV-Host-ID") {
		fail(w, 400, "invalid_request", "Provide the original preview instance, request ID, complete binding and a guest TCP port from 1024 to 65535.")
		return
	}
	for _, value := range []string{input.Binding.HostID, input.Binding.ProjectID, input.Binding.VolumeID, input.Binding.MachineID, input.Binding.BootID, input.Binding.InstanceID} {
		if value == "" || len(value) > 128 {
			fail(w, 400, "invalid_request", "A complete bounded original preview binding is required.")
			return
		}
	}
	if input.Binding.ProjectRevision < 1 || input.Binding.VolumeRevision < 1 {
		fail(w, 400, "invalid_request", "Positive original project and volume revisions are required.")
		return
	}
	m.mu.Lock()
	if input.InstanceID != m.instanceID {
		m.mu.Unlock()
		fail(w, 409, "preview_instance_changed", "The original preview manager is unavailable. Inspect current previews before explicitly releasing its recovery request.")
		return
	}
	receipt := m.requests[input.RequestID]
	if receipt != nil && receipt.input != input {
		m.mu.Unlock()
		fail(w, 409, "idempotency_conflict", "This preview request ID already belongs to a different payload.")
		return
	}
	if receipt == nil {
		if m.closed || m.ctx.Err() != nil {
			m.mu.Unlock()
			fail(w, 503, "preview_stopping", "The preview manager is stopping.")
			return
		}
		if m.active >= maxActivePreviews {
			m.mu.Unlock()
			fail(w, 429, "preview_capacity", "Stop an active preview before registering another (64 active previews).")
			return
		}
		if len(m.requests) >= maxRegistrationReceipts {
			m.mu.Unlock()
			fail(w, 429, "preview_receipt_capacity", "Preview receipt capacity is full (4096 requests). Existing receipts were preserved; restart AXV before explicitly registering new previews.")
			return
		}
		receipt = &registration{input: input, done: make(chan struct{})}
		m.requests[input.RequestID] = receipt
		m.active++
		m.wg.Add(1)
		go m.initialize(receipt)
	}
	m.mu.Unlock()
	select {
	case <-r.Context().Done():
		return
	case <-receipt.done:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if receipt.err != nil {
		fail(w, receipt.err.status, receipt.err.code, receipt.err.message)
		return
	}
	reply(w, http.StatusCreated, receipt.entry.snapshot(r))
}

func (m *Manager) initialize(receipt *registration) {
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(m.ctx, 25*time.Second)
	defer cancel()
	input := receipt.input
	target, err := m.resolve(ctx, input.Binding, input.Port)
	var e *entry
	if err == nil {
		probe := &probeWriter{header: http.Header{}, status: 200}
		probeContext, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
		request, _ := http.NewRequestWithContext(probeContext, "HEAD", "http://preview/", nil)
		target.Handler.ServeHTTP(probe, request)
		cancelProbe()
		if probe.status >= 500 || ctx.Err() != nil {
			err = compute.Problem("preview_unavailable", "The development service is not ready. Start it in the VM and check the port.")
		}
		if err == nil {
			var listener net.Listener
			listener, err = net.Listen("tcp", "127.0.0.1:0")
			if err == nil {
				entryContext, entryCancel := context.WithCancel(m.ctx)
				e = &entry{owner: m, token: rand.Text(), target: target, ctx: entryContext, cancel: entryCancel, view: View{ID: rand.Text(), ProjectID: input.Binding.ProjectID, Port: input.Port, Binding: input.Binding, State: "ready"}}
				port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
				e.local = serve(listener, "http://"+net.JoinHostPort(strings.ToLower(rand.Text())+".localhost", port), e)
			}
		}
	}
	m.mu.Lock()
	if err == nil && (m.closed || m.ctx.Err() != nil) {
		err = context.Canceled
	}
	if err == nil {
		receipt.entry = e
		m.entries = append(m.entries, e)
	} else {
		m.active--
		status, code := 503, "preview_unavailable"
		switch compute.ErrorCode(err) {
		case "binding_changed", "project_changed", "revision_conflict", "boot_changed", "instance_changed":
			status, code = 409, compute.ErrorCode(err)
		}
		receipt.err = &registrationError{status, code, "The development service could not be registered in its original environment. Inspect the original target before explicitly registering again."}
	}
	m.mu.Unlock()
	if err != nil {
		if e != nil {
			e.shutdown()
		} else if target.Close != nil {
			target.Close()
		}
	}
	close(receipt.done)
}

func (m *Manager) stop(w http.ResponseWriter, r *http.Request) {
	if !allowed(w, r) {
		return
	}
	var body struct{}
	if err := decodeControl(w, r, &body); err != nil {
		fail(w, 400, "invalid_request", "Stop Preview requires an empty JSON object.")
		return
	}
	m.mu.Lock()
	var selected *entry
	for _, e := range m.entries {
		if e.view.ID == r.PathValue("id") && e.view.ProjectID == r.PathValue("projectId") && e.view.Binding.HostID == r.Header.Get("X-AXV-Host-ID") {
			selected = e
			break
		}
	}
	m.mu.Unlock()
	if selected == nil {
		http.NotFound(w, r)
		return
	}
	selected.retire("stopped", "")
	m.mu.Lock()
	view := selected.snapshot(r)
	m.mu.Unlock()
	reply(w, 200, view)
}

type probeWriter struct {
	header http.Header
	status int
}

func (p *probeWriter) Header() http.Header { return p.header }
func (p *probeWriter) WriteHeader(status int) {
	if status >= 200 {
		p.status = status
	}
}
func (p *probeWriter) Write(data []byte) (int, error) { return len(data), nil }
