package guest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func TestGuestPreviewHTTPAndWebSocketPreservePathsWithoutControlHeaders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("project roots are enforced inside Linux guests")
	}
	s := testService(t, "project-files", "project-previews")
	scope := projectFileScope(t, s, "")
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if r.Header.Get("Authorization") != "Bearer application-session" || r.Header.Get(protocol.PreviewScopeHeader) != "" || r.Header.Get(protocol.PreviewAuthorizationHeader) != "" {
			t.Error("private metadata leaked into the project application")
		}
		_, _ = io.WriteString(w, r.URL.RequestURI())
	}))
	defer app.Close()
	address, _ := url.Parse(app.URL)
	guest := httptest.NewServer(s.handler())
	defer guest.Close()
	encoded, _ := json.Marshal(scope)
	headers := http.Header{"Authorization": []string{"Bearer " + s.config.Token}, protocol.PreviewScopeHeader: []string{base64.RawURLEncoding.EncodeToString(encoded)}, protocol.PreviewPortHeader: []string{address.Port()}, protocol.PreviewPathHeader: []string{"/src/a%20b.js"}}
	headers.Set(protocol.PreviewAuthorizationHeader, "Bearer application-session")
	r, _ := http.NewRequest("GET", guest.URL+"/v1/project-preview?version=7", nil)
	r.Header = headers.Clone()
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(data) != "/src/a%20b.js?version=7" {
		t.Fatalf("guest preview path: %d %s", response.StatusCode, data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers.Set(protocol.PreviewPathHeader, "/hmr")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(guest.URL, "http")+"/v1/project-preview?token=test", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte("refresh")); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil || string(data) != "refresh" {
		t.Fatalf("guest WebSocket: %s %v", data, err)
	}
	s.close()
	if _, _, err = conn.Read(ctx); err == nil {
		t.Fatal("guest shutdown retained its preview WebSocket")
	}
}

func TestGuestPreviewRejectsStaleScopeAndUnsafePorts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("project roots are enforced inside Linux guests")
	}
	s := testService(t, "project-files", "project-previews")
	scope := projectFileScope(t, s, "")
	for _, tc := range []struct {
		name       string
		scope      protocol.ProjectFileScope
		port, path string
		status     int
	}{
		{"boot", protocol.ProjectFileScope{Root: scope.Root, RootIdentity: scope.RootIdentity, BootID: "old", InstanceID: scope.InstanceID}, "5173", "/", 409},
		{"guest", protocol.ProjectFileScope{Root: scope.Root, RootIdentity: scope.RootIdentity, BootID: scope.BootID, InstanceID: "old"}, "5173", "/", 409},
		{"root", protocol.ProjectFileScope{Root: scope.Root, RootIdentity: "other-root", BootID: scope.BootID, InstanceID: scope.InstanceID}, "5173", "/", 409},
		{"privileged port", scope, "22", "/", 400},
		{"invalid port", scope, strconv.Itoa(65536), "/", 400},
		{"nonabsolute path", scope, "5173", "http://other-host/", 400},
		{"invalid path", scope, "5173", "/%00", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(tc.scope)
			r := httptest.NewRequest("GET", "/v1/project-preview", nil)
			r.Header.Set("Authorization", "Bearer "+s.config.Token)
			r.Header.Set(protocol.PreviewScopeHeader, base64.RawURLEncoding.EncodeToString(encoded))
			r.Header.Set(protocol.PreviewPortHeader, tc.port)
			r.Header.Set(protocol.PreviewPathHeader, tc.path)
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
			}
		})
	}
	noCapability := testService(t, "project-files")
	assertProblem(t, guestRequest(noCapability, "GET", "/v1/project-preview", ""), 501, "unsupported")
}
