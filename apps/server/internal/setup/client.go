package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
)

type desktopClient struct {
	base, instance string
	http           *http.Client
}

// connect accepts only a verified loopback desktop and never follows a redirect
// or an environment proxy. Every mutation also checks the captured instance ID.
func connect(ctx context.Context, state launch.State) (*desktopClient, error) {
	u, err := url.Parse(state.URL)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" || state.InstanceID == "" {
		return nil, fmt.Errorf("invalid local desktop state")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("desktop state must refer to a loopback listener")
	}
	c := &desktopClient{base: strings.TrimSuffix(state.URL, "/"), instance: state.InstanceID, http: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var actual struct {
		InstanceID string `json:"instanceId"`
	}
	if err = c.request(ctx, "GET", "/api/runtime", nil, &actual); err != nil {
		c.Close()
		return nil, err
	}
	if actual.InstanceID != state.InstanceID {
		c.Close()
		return nil, fmt.Errorf("desktop listener belongs to another instance")
	}
	return c, nil
}

func (c *desktopClient) Close() { c.http.CloseIdleConnections() }
func (c *desktopClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set("Origin", c.base)
		req.Header.Set("X-AXV-Instance-ID", c.instance)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
		var detail struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &detail)
		if detail.Error == "" {
			detail.Error = http.StatusText(res.StatusCode)
		}
		return fmt.Errorf("desktop returned HTTP %d: %s", res.StatusCode, detail.Error)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(output)
}

func (c *desktopClient) Check(ctx context.Context) (prepare.Check, error) {
	var value prepare.Check
	err := c.request(ctx, "GET", "/api/compute/preparation/check", nil, &value)
	return value, err
}

func (c *desktopClient) Plan(ctx context.Context, r prepare.Request) (prepare.Plan, error) {
	var value prepare.Plan
	err := c.request(ctx, "POST", "/api/compute/preparation/plan", r, &value)
	return value, err
}

func (c *desktopClient) Start(ctx context.Context, r prepare.StartRequest) (prepare.Job, error) {
	var value prepare.Job
	err := c.request(ctx, "POST", "/api/compute/preparation/jobs", r, &value)
	return value, err
}

func (c *desktopClient) Job(ctx context.Context, id string) (prepare.Job, error) {
	var value prepare.Job
	err := c.request(ctx, "GET", "/api/compute/preparation/jobs/"+url.PathEscape(id), nil, &value)
	return value, err
}

func (c *desktopClient) Retry(ctx context.Context, id string, r prepare.ActionRequest) (prepare.Job, error) {
	var value prepare.Job
	err := c.request(ctx, "POST", "/api/compute/preparation/jobs/"+url.PathEscape(id)+"/retry", r, &value)
	return value, err
}
