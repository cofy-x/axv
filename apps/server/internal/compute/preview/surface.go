package preview

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type surface struct {
	origin      string
	server      *http.Server
	mu          sync.Mutex
	connections map[net.Conn]http.ConnState
}
type connectionKey struct{}

func serve(listener net.Listener, origin string, e *entry) *surface {
	s := &surface{origin: origin, connections: map[net.Conn]http.ConnState{}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if conn, ok := r.Context().Value(connectionKey{}).(net.Conn); ok {
				s.mu.Lock()
				if s.connections[conn] == http.StateHijacked {
					delete(s.connections, conn)
				}
				s.mu.Unlock()
			}
		}()
		e.serve(w, r, s)
	})
	s.server = &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10,
		BaseContext: func(net.Listener) context.Context { return e.ctx },
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connectionKey{}, c)
		},
		ConnState: func(c net.Conn, state http.ConnState) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if state == http.StateClosed {
				delete(s.connections, c)
			} else {
				s.connections[c] = state
			}
		},
	}
	go func() { _ = s.server.Serve(listener) }()
	return s
}

func (s *surface) close() {
	_ = s.server.Close()
	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for c := range s.connections {
		connections = append(connections, c)
	}
	s.mu.Unlock()
	// net/http leaves upgraded connections to their handler. Closing them also
	// interrupts any project WebSocket before retirement is acknowledged.
	for _, c := range connections {
		_ = c.Close()
	}
}

func (e *entry) serve(w http.ResponseWriter, r *http.Request, s *surface) {
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin allow-forms allow-downloads; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	u, _ := url.Parse(s.origin)
	if r.Host != u.Host || !localPreviewRequest(r, s.origin) {
		http.Error(w, "This request does not belong to this preview origin.", 403)
		return
	}
	if !e.acquire() {
		http.Error(w, "This preview is no longer active. Register a new preview explicitly.", 410)
		return
	}
	defer e.wg.Done()
	cookieName := "axv_preview_" + e.view.ID
	if r.URL.Path == "/.axv-open/"+e.token && r.Method == "GET" {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: e.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><meta name=\"referrer\" content=\"no-referrer\"><title>Opening preview</title><script>location.replace('/')</script><noscript><a href=\"/\">Open preview</a></noscript>")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "same-site" || site == "cross-site" {
		http.Error(w, "Load project resources from their own preview origin.", 403)
		return
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value != e.token {
		http.Error(w, "Open this preview from AXV.", 403)
		return
	}
	if err := e.target.Validate(r.Context()); err != nil {
		http.Error(w, "The original preview environment is unavailable. Register a new preview explicitly.", 409)
		return
	}
	if e.ctx.Err() != nil || r.Context().Err() != nil {
		return
	}
	r = r.Clone(r.Context())
	// Keep normal application sessions while withholding every bootstrap
	// capability, including a forged cookie bearing another reserved name.
	applicationCookies := []*http.Cookie{}
	for _, c := range r.Cookies() {
		if !strings.HasPrefix(c.Name, "axv_preview_") {
			applicationCookies = append(applicationCookies, c)
		}
	}
	r.Header.Del("Cookie")
	for _, c := range applicationCookies {
		r.AddCookie(c)
	}
	e.target.Handler.ServeHTTP(w, r)
}

// The random hostname is accepted only by its own listener, not by desktop
// APIs. A loopback peer and the exact browser origin remain mandatory.
func localPreviewRequest(r *http.Request, origin string) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return (r.Method == "GET" || r.Method == "HEAD") && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	}
	return len(origins) == 1 && origins[0] == origin
}
