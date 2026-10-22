package projectpreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/remoteaccess"
)

func testBinding() compute.ProjectBinding {
	return compute.ProjectBinding{HostID: "host-one", ProjectID: "project-one", ProjectRevision: 1, VolumeID: "volume-one", VolumeRevision: 1, MachineID: "machine-one", BootID: "boot-one", InstanceID: "instance-one"}
}

func fixture(t *testing.T) (*Manager, *httptest.Server, *http.Client, *atomic.Bool) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				kind, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				if conn.Write(r.Context(), kind, data) != nil {
					return
				}
			}
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Received-Cookie", r.Header.Get("Cookie"))
		if r.URL.Path == "/cookie" {
			http.SetCookie(w, &http.Cookie{Name: "application-session", Value: "project-secret", Path: "/", Domain: "localhost"})
			http.SetCookie(w, &http.Cookie{Name: "axv_preview_forged", Value: "not-a-capability", Path: "/"})
			w.Header().Set("Clear-Site-Data", `"cookies"`)
		}
		_, _ = io.WriteString(w, "<script type=module src=/main.js></script>"+r.URL.RequestURI())
	}))
	t.Cleanup(backend.Close)
	u, _ := url.Parse(backend.URL)
	valid := &atomic.Bool{}
	valid.Store(true)
	m := New(context.Background(), func(_ context.Context, binding compute.ProjectBinding, _ int) (Target, error) {
		if binding != testBinding() {
			return Target{}, errors.New("wrong captured binding")
		}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.ModifyResponse = applicationResponse
		return Target{Handler: proxy, Validate: func(context.Context) error {
			if !valid.Load() {
				return errors.New("VM boot changed")
			}
			return nil
		}, Close: func() {}}, nil
	})
	t.Cleanup(m.Close)
	mux := http.NewServeMux()
	m.Register(mux)
	control := httptest.NewServer(mux)
	t.Cleanup(control.Close)
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(address)
		if strings.HasSuffix(host, ".localhost") {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return m, control, &http.Client{Jar: jar, Transport: transport, Timeout: 5 * time.Second}, valid
}
func createPreview(t *testing.T, control *httptest.Server, client *http.Client) View {
	t.Helper()
	read, _ := http.NewRequest("GET", control.URL+"/api/project-previews/project-one", nil)
	read.Header.Set("X-AXV-Host-ID", "host-one")
	inventory, err := client.Do(read)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(inventory.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	inventory.Body.Close()
	data, _ := json.Marshal(Registration{InstanceID: state.InstanceID, RequestID: compute.NewID("preview"), Binding: testBinding(), Port: 5173})
	req, _ := http.NewRequest("POST", control.URL+"/api/project-previews/project-one", bytes.NewReader(data))
	req.Header.Set("Origin", control.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AXV-Host-ID", "host-one")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var view View
	if response.StatusCode != 201 || json.NewDecoder(response.Body).Decode(&view) != nil {
		t.Fatalf("create preview: %d", response.StatusCode)
	}
	return view
}

// A browser first commits the AXV-owned capability page, then its constant
// script navigates within that origin. HTTP-only clients model both requests.
func openPreview(client *http.Client, location string) (*http.Response, error) {
	response, err := client.Get(location)
	if err != nil || response.StatusCode != 200 || !strings.HasPrefix(response.Request.URL.Path, "/.axv-open/") {
		return response, err
	}
	response.Body.Close()
	u, _ := url.Parse(location)
	u.Path = "/"
	return client.Get(u.String())
}
func TestPreviewHTTPWebSocketIsolationAndExactStop(t *testing.T) {
	m, control, client, _ := fixture(t)
	first := createPreview(t, control, client)
	second := createPreview(t, control, client)
	if first.URL == second.URL || strings.HasPrefix(first.URL, control.URL+"/") {
		t.Fatal("preview origins were shared")
	}
	response, err := openPreview(client, first.URL)
	if err != nil {
		t.Fatal(err)
	}
	origin := response.Request.URL.Scheme + "://" + response.Request.URL.Host
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Contains(body, []byte("type=module")) || !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox allow-scripts allow-same-origin") || response.Header.Get("Origin-Agent-Cluster") != "?1" {
		t.Fatalf("preview not isolated: %s %v", body, response.Header)
	}
	response, err = client.Get(origin + "/main.js?version=2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Contains(body, []byte("/main.js?version=2")) {
		t.Fatalf("root assets/query lost: %s", body)
	}
	response, err = client.Get(origin + "/cookie")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(response.Cookies()) != 1 || response.Cookies()[0].Name != "application-session" || response.Cookies()[0].Domain != "" {
		t.Fatal("application session was not confined to its preview hostname")
	}
	if response.Header.Get("Clear-Site-Data") != "" {
		t.Fatal("application could clear its transport capability")
	}
	sibling, _ := http.NewRequest("GET", origin+"/main.js", nil)
	sibling.Header.Set("Sec-Fetch-Site", "same-site")
	response, err = client.Do(sibling)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("sibling preview used ambient cookies: %d", response.StatusCode)
	}
	previewURL, _ := url.Parse(origin)
	headers := http.Header{"Origin": []string{origin}}
	for _, cookie := range client.Jar.Cookies(previewURL) {
		headers.Add("Cookie", cookie.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(origin, "http")+"/hmr?token=abc", &websocket.DialOptions{HTTPHeader: headers, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte("hot-update")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "hot-update" {
		t.Fatalf("WebSocket echo: %s %v", data, err)
	}
	// A project page cannot use its origin to authorize AXV mutations.
	req, _ := http.NewRequest("POST", control.URL+"/api/project-previews/project-one", strings.NewReader("{}"))
	req.Header.Set("Origin", origin)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("cross-origin control: %d", response.StatusCode)
	}
	req, _ = http.NewRequest("POST", control.URL+"/api/project-previews/project-one/"+first.ID+"/stop", strings.NewReader("{}"))
	req.Header.Set("Origin", control.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AXV-Host-ID", "host-one")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stop: %d", response.StatusCode)
	}
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("Stop Preview retained its WebSocket")
	}
	response, err = openPreview(client, first.URL)
	if err == nil {
		response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatalf("stopped URL remained usable: %d", response.StatusCode)
		}
	}
	response, err = openPreview(client, second.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("unrelated preview stopped: %d", response.StatusCode)
	}
	secondOrigin := response.Request.URL.Scheme + "://" + response.Request.URL.Host
	if secondOrigin == origin {
		t.Fatal("two project previews shared a local origin")
	}
	u, _ := url.Parse(secondOrigin)
	client.Jar.SetCookies(u, []*http.Cookie{{Name: "application-session", Value: "other-project-secret", Path: "/"}})
	response, err = client.Get(secondOrigin + "/cookie")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Header.Get("X-Received-Cookie") != "application-session=other-project-secret" || len(response.Cookies()) != 1 {
		t.Fatal("normal application cookies were lost or bootstrap credentials reached the application")
	}
	m.mu.Lock()
	if m.entries[0].view.State != "stopped" || m.entries[1].view.State != "ready" {
		t.Fatal("stop retargeted another preview")
	}
	m.mu.Unlock()
}

func TestChangedBindingInvalidatesOriginalURLAndConnections(t *testing.T) {
	m, control, client, valid := fixture(t)
	view := createPreview(t, control, client)
	response, err := openPreview(client, view.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	valid.Store(false)
	m.check()
	response, err = openPreview(client, view.URL)
	if err == nil {
		response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatalf("stale environment opened: %d", response.StatusCode)
		}
	}
}

func TestRemotePreviewControlsReportUnsupportedWithoutLocalURL(t *testing.T) {
	m, control, client, _ := fixture(t)
	view := createPreview(t, control, client)
	handler := http.NewServeMux()
	m.Register(handler)
	guard, err := remoteaccess.NewMiddleware(remoteaccess.MiddlewareOptions{Origin: "https://axv.example.ts.net", OwnerUserID: "123", WhoIs: func(context.Context, string) (remoteaccess.Identity, error) {
		return remoteaccess.Identity{UserID: "123", NodeID: "actual-client"}, nil
	}, AllowRoute: func(*http.Request) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	wrapped := guard.Wrap(handler)
	read := httptest.NewRequest("GET", "https://axv.example.ts.net/api/project-previews/project-one", nil)
	read.RemoteAddr = "100.64.0.2:12345"
	read.Header.Set("X-AXV-Host-ID", "host-one")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, read)
	var result struct {
		Supported bool   `json:"supported"`
		Reason    string `json:"reason"`
		Previews  []View `json:"previews"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Supported || result.Reason == "" || len(result.Previews) != 1 || result.Previews[0].URL != "" {
		t.Fatalf("remote preview capability: %d %s", w.Code, w.Body)
	}
	register := httptest.NewRequest("POST", "https://axv.example.ts.net/api/project-previews/project-one", strings.NewReader("{}"))
	register.RemoteAddr = read.RemoteAddr
	register.Header.Set("Origin", "https://axv.example.ts.net")
	register.Header.Set("X-AXV-Host-ID", "host-one")
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, register)
	if w.Code != 501 {
		t.Fatalf("remote preview registration: %d %s", w.Code, w.Body)
	}
	stop := httptest.NewRequest("POST", "https://axv.example.ts.net/api/project-previews/project-one/"+view.ID+"/stop", strings.NewReader("{}"))
	stop.RemoteAddr = read.RemoteAddr
	stop.Header.Set("Origin", "https://axv.example.ts.net")
	stop.Header.Set("Content-Type", "application/json")
	stop.Header.Set("X-AXV-Host-ID", "host-one")
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, stop)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"stopped"`) {
		t.Fatalf("remote owner could not revoke a local preview: %d %s", w.Code, w.Body)
	}
}

func TestRetiredHostnameCannotAddressAnotherProfileListener(t *testing.T) {
	previous, control, client, _ := fixture(t)
	old := createPreview(t, control, client)
	previous.Close()
	_, nextControl, nextClient, _ := fixture(t)
	next := createPreview(t, nextControl, nextClient)
	response, err := openPreview(nextClient, next.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	oldURL, _ := url.Parse(old.URL)
	nextURL, _ := url.Parse(next.URL)
	if oldURL.Hostname() == nextURL.Hostname() {
		t.Fatal("another profile reused the retired hostname")
	}
	// Model an OS port being reused by another profile. Even with valid cookies
	// for the new entry, the previous hostname and Origin must remain rejected.
	for _, oldHost := range []bool{true, false} {
		req, _ := http.NewRequest("GET", "http://"+nextURL.Host+"/", nil)
		if oldHost {
			req.Host = net.JoinHostPort(oldURL.Hostname(), nextURL.Port())
		} else {
			req.Header.Set("Origin", oldURL.Scheme+"://"+oldURL.Host)
		}
		response, err := nextClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("another profile accepted retired origin (host=%v): %d", oldHost, response.StatusCode)
		}
	}
}
