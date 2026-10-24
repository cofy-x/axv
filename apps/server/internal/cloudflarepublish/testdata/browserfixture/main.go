// This executable serves only browser security fixtures. Provider API calls and
// guest content are local fakes; publication admission, journals, browser gates,
// HTTP surfaces and restart/retry behavior use the production manager.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflareapi"
	"github.com/cofy-x/axv/apps/server/internal/cloudflarepublish"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/projectpreview"
)

type command struct {
	ID          int               `json:"id"`
	Op          string            `json:"op"`
	Domain      string            `json:"domain"`
	Name        string            `json:"name"`
	RequestID   string            `json:"requestId"`
	Target      string            `json:"target"`
	Publication string            `json:"publication"`
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
}

type reply struct {
	ID     int    `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
}

type suite struct {
	home     string
	profiles map[string]*fixture
}

type fixture struct {
	manager  *cloudflarepublish.Manager
	settings cloudflarepublish.Settings
	options  cloudflarepublish.Options
	client   *http.Client
	mu       sync.Mutex
	hits     map[string]int
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Provide one invocation-owned fixture directory.")
		os.Exit(2)
	}
	s := &suite{home: os.Args[1], profiles: map[string]*fixture{}}
	defer func() {
		for _, f := range s.profiles {
			_ = f.manager.Close()
			f.client.CloseIdleConnections()
		}
	}()
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 32<<20))
	encoder := json.NewEncoder(os.Stdout)
	for {
		var input command
		if err := decoder.Decode(&input); err != nil {
			if err != io.EOF {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		value, err := s.run(input)
		response := reply{ID: input.ID, Result: value}
		if err != nil {
			response.Result, response.Error = nil, err.Error()
			var problem *cloudflarepublish.Problem
			if errors.As(err, &problem) {
				response.Code = problem.Code
			}
		}
		if encoder.Encode(response) != nil || input.Op == "shutdown" {
			return
		}
	}
}

func (s *suite) run(input command) (any, error) {
	if input.Op == "shutdown" {
		return true, nil
	}
	f := s.profiles[input.Domain]
	if f == nil {
		if input.Op != "publish" {
			return nil, errors.New("the original fixture profile does not exist")
		}
		var err error
		f, err = s.create(input.Domain)
		if err != nil {
			return nil, err
		}
		s.profiles[input.Domain] = f
	}
	switch input.Op {
	case "publish":
		binding := fixtureBinding(input.Target)
		value, err := f.manager.Publish(context.Background(), cloudflarepublish.Registration{InstanceID: f.manager.InstanceID(), RequestID: input.RequestID, Binding: binding, Port: 3000, Name: input.Name, Domain: input.Domain, Visibility: "public"})
		if err != nil {
			return nil, err
		}
		if value.State != "provisioning" && value.State != "published" {
			return value, nil
		}
		return waitFor(f, value.ID, "published")
	case "unpublish":
		value, err := f.manager.Unpublish(context.Background(), input.Publication)
		if err != nil {
			return nil, err
		}
		return waitFor(f, value.ID, "removed")
	case "restart":
		if err := f.manager.Close(); err != nil {
			return nil, err
		}
		f.client.CloseIdleConnections()
		var err error
		f.manager, err = cloudflarepublish.New(context.Background(), f.options)
		if err != nil {
			return nil, err
		}
		return f.manager.List(), nil
	case "resume":
		value, err := f.manager.Retry(context.Background(), input.Publication)
		if err != nil {
			return nil, err
		}
		return waitFor(f, value.ID, "published")
	case "fetch":
		return f.fetch(input)
	case "stats":
		f.mu.Lock()
		defer f.mu.Unlock()
		copy := map[string]int{}
		for path, count := range f.hits {
			copy[path] = count
		}
		return copy, nil
	default:
		return nil, errors.New("unknown fixture command")
	}
}

func fixtureBinding(target string) compute.ProjectBinding {
	if target == "" {
		target = "original-project"
	}
	return compute.ProjectBinding{HostID: "fixture-host", ProjectID: target, ProjectRevision: 1, VolumeID: "fixture-volume", VolumeRevision: 1, MachineID: "fixture-vm", BootID: "original-boot", InstanceID: "fixture-guest"}
}

func (s *suite) create(domain string) (*fixture, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	digest := sha256.Sum256([]byte(domain))
	home := filepath.Join(s.home, hex.EncodeToString(digest[:8]))
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	f := &fixture{settings: cloudflarepublish.Settings{APIToken: "fixture-api-token", AccountID: "fixture-account", ZoneID: "fixture-zone", TunnelID: "fixture-tunnel", Domain: domain, Listen: address, DesktopOrigin: "https://desktop.example.com"}, hits: map[string]int{}}
	provider := &cloud{domain: domain, dns: map[string]cloudflareapi.DNSRecord{}, configuration: map[string]any{"ingress": []any{map[string]any{"service": "http_status:404"}}}}
	f.options = cloudflarepublish.Options{Home: home, Settings: func() (cloudflarepublish.Settings, error) { return f.settings, nil }, Client: func(settings cloudflarepublish.Settings) (*cloudflareapi.Client, error) {
		return cloudflareapi.New(cloudflareapi.Options{Token: settings.APIToken, HTTPClient: &http.Client{Transport: provider}})
	}, Capture: func(_ context.Context, _ compute.ProjectBinding) (cloudflarepublish.CapturedRoute, error) {
		return cloudflarepublish.CapturedRoute{Socket: filepath.Join(home, "captured-host.sock"), ConfigFingerprint: "fixture-original-host"}, nil
	}, Resolve: func(_ context.Context, _ cloudflarepublish.CapturedRoute, binding compute.ProjectBinding, _ int) (projectpreview.Target, error) {
		return projectpreview.Target{Validate: func(context.Context) error { return nil }, Close: func() {}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
				return
			}
			f.mu.Lock()
			f.hits[r.URL.Path]++
			f.mu.Unlock()
			switch r.URL.Path {
			case "/echo":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"cookie": r.Header.Get("Cookie"), "origin": r.Header.Get("Origin"), "target": binding.ProjectID})
			case "/sw.js":
				w.Header().Set("Content-Type", "application/javascript")
				_, _ = io.WriteString(w, `self.addEventListener('install',()=>self.skipWaiting());self.addEventListener('activate',e=>e.waitUntil(self.clients.claim()));self.addEventListener('fetch',e=>{if(new URL(e.request.url).pathname==='/offline-proof')e.respondWith(new Response('retained-worker'));});`)
			default:
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = fmt.Fprintf(w, "<!doctype html><title>Fixture guest</title><h1 data-fixture-application>Guest application: %s</h1>", binding.ProjectID)
			}
		})}, nil
	}}
	f.manager, err = cloudflarepublish.New(context.Background(), f.options)
	if err != nil {
		return nil, err
	}
	f.client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableCompression: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return f, nil
}

func waitFor(f *fixture, id, state string) (cloudflarepublish.Publication, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range f.manager.List() {
			if item.ID != id {
				continue
			}
			if item.State == state {
				return item, nil
			}
			if item.State == "error" {
				return item, fmt.Errorf("fixture publication failed: %s", item.Error)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cloudflarepublish.Publication{}, errors.New("timed out waiting for the original publication")
}

func (f *fixture) fetch(input command) (any, error) {
	var selected bool
	request, err := http.NewRequest(input.Method, input.URL, nil)
	if err != nil || request.URL.Scheme != "https" || request.URL.Port() != "" || request.URL.User != nil {
		return nil, errors.New("the fixture requires an exact HTTPS publication origin")
	}
	for _, item := range f.manager.List() {
		if item.Hostname == request.URL.Host {
			selected = true
		}
	}
	if !selected {
		return nil, errors.New("the fixture refuses unrelated destinations")
	}
	data, err := base64.StdEncoding.DecodeString(input.Body)
	if err != nil {
		return nil, err
	}
	request, err = http.NewRequest(input.Method, "http://"+f.settings.Listen+request.URL.RequestURI(), strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	for key, value := range input.Headers {
		if strings.EqualFold(key, "host") || strings.EqualFold(key, "content-length") || strings.EqualFold(key, "connection") {
			continue
		}
		request.Header.Set(key, value)
	}
	original, _ := http.NewRequest(http.MethodGet, input.URL, nil)
	request.Host = original.URL.Host
	response, err := f.client.Do(request)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return map[string]string{"networkError": "connectionrefused"}, nil
		}
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for name, values := range response.Header {
		if strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Transfer-Encoding") || strings.EqualFold(name, "Connection") {
			continue
		}
		headers[name] = strings.Join(values, "\n")
	}
	return map[string]any{"status": response.StatusCode, "headers": headers, "body": base64.StdEncoding.EncodeToString(body)}, nil
}

type cloud struct {
	mu            sync.Mutex
	domain        string
	dns           map[string]cloudflareapi.DNSRecord
	configuration map[string]any
	version       int
}

func (c *cloud) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	status := 200
	var result any
	switch {
	case path == "/zones/fixture-zone":
		result = cloudflareapi.Zone{ID: "fixture-zone", Name: c.domain}
	case path == "/zones/fixture-zone/dns_records" && r.Method == http.MethodGet:
		items := []cloudflareapi.DNSRecord{}
		for _, record := range c.dns {
			if record.Name == r.URL.Query().Get("name.exact") {
				items = append(items, record)
			}
		}
		result = items
	case path == "/zones/fixture-zone/dns_records" && r.Method == http.MethodPost:
		var record cloudflareapi.DNSRecord
		_ = json.NewDecoder(r.Body).Decode(&record)
		record.ID = "record-" + strings.ReplaceAll(record.Name, ".", "-")
		c.dns[record.ID], result = record, record
	case strings.HasPrefix(path, "/zones/fixture-zone/dns_records/"):
		id := strings.TrimPrefix(path, "/zones/fixture-zone/dns_records/")
		record, exists := c.dns[id]
		if !exists {
			status = 404
		} else if r.Method == http.MethodDelete {
			delete(c.dns, id)
			result = map[string]string{"id": id}
		} else {
			result = record
		}
	case path == "/accounts/fixture-account/cfd_tunnel/fixture-tunnel":
		result = cloudflareapi.Tunnel{ID: "fixture-tunnel", RemoteConfig: true, Status: "healthy"}
	case path == "/accounts/fixture-account/cfd_tunnel/fixture-tunnel/configurations":
		if r.Method == http.MethodPut {
			var body struct {
				Config map[string]any `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			c.configuration = body.Config
			c.version++
		}
		result = map[string]any{"config": c.configuration, "version": c.version}
	default:
		status = 404
	}
	data, _ := json.Marshal(map[string]any{"success": status < 400, "result": result, "errors": []any{}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
}
