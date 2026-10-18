// Package client connects local tools to one verified compute host.
// It never opens a desktop listener or accesses the host database directly.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/hostconn"
)

type Binding struct {
	HostID            string `json:"hostId"`
	ConfigFingerprint string `json:"configFingerprint"`
}

type ResponseError struct {
	StatusCode int
	Err        error
}

func (e *ResponseError) Error() string { return e.Err.Error() }
func (e *ResponseError) Unwrap() error { return e.Err }

type Client struct {
	Binding Binding
	socket  string
	http    *http.Client
}

// Connect captures an identity once. Every later socket connection, including
// an SSH attachment, verifies that identity on the connection it actually uses.
func Connect(ctx context.Context, socket string, expected Binding) (*Client, error) {
	identity, err := Probe(ctx, socket, expected)
	if err != nil {
		return nil, err
	}
	c := &Client{Binding: Binding{HostID: identity.HostID, ConfigFingerprint: identity.ConfigFingerprint}, socket: socket}
	c.http = &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 15 * time.Second, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return c.Dial(ctx) }},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// Probe reports controller recovery and verifies the runtime identity from one
// local connection. It never requests inventory or probes provider availability.
func Probe(ctx context.Context, socket string, expected Binding) (compute.Runtime, error) {
	if !filepath.IsAbs(socket) {
		return compute.Runtime{}, fmt.Errorf("compute socket must be an absolute local path")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return compute.Runtime{}, fmt.Errorf("connect to the local VM service: %w", err)
	}
	defer conn.Close()
	_, identity, err := hostconn.Capture(ctx, conn, hostconn.Binding(expected))
	if err != nil {
		if ctx.Err() != nil {
			return compute.Runtime{}, ctx.Err()
		}
		return compute.Runtime{}, err
	}
	return identity, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Dial(ctx context.Context) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, err
	}
	verified, err := hostconn.Verify(ctx, conn, hostconn.Binding(c.Binding))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return verified, nil
}

// DoJSON never retries a mutation. A transport error can mean the host accepted
// it; callers must retain their request ID and query its operation.
func (c *Client) DoJSON(ctx context.Context, method, path string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://host/v1/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-AXV-Host-ID", c.Binding.HostID)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem compute.Error
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&problem); err != nil || problem.Code == "" {
			return &ResponseError{StatusCode: res.StatusCode, Err: fmt.Errorf("VM service returned HTTP %d", res.StatusCode)}
		}
		return &ResponseError{StatusCode: res.StatusCode, Err: &problem}
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 16<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(output)
}

func (c *Client) Info(ctx context.Context) (compute.Info, error) {
	var result compute.Info
	err := c.DoJSON(ctx, "GET", "info", nil, &result)
	return result, err
}
func (c *Client) Machines(ctx context.Context) ([]compute.Machine, error) {
	var result struct {
		Machines []compute.Machine `json:"machines"`
	}
	err := c.DoJSON(ctx, "GET", "machines", nil, &result)
	return result.Machines, err
}
func (c *Client) Machine(ctx context.Context, id string) (compute.Machine, error) {
	var result compute.Machine
	err := c.DoJSON(ctx, "GET", "machines/"+url.PathEscape(id), nil, &result)
	return result, err
}
func (c *Client) Resolve(ctx context.Context, reference string) (compute.Machine, error) {
	if compute.IsMachineID(reference) {
		return c.Machine(ctx, reference)
	}
	machines, err := c.Machines(ctx)
	if err != nil {
		return compute.Machine{}, err
	}
	var found *compute.Machine
	for _, m := range machines {
		if m.Spec.Name == reference {
			if found != nil {
				return compute.Machine{}, fmt.Errorf("ambiguous VM name %q; use a full machine ID", reference)
			}
			copy := m
			found = &copy
		}
	}
	if found == nil {
		return compute.Machine{}, compute.Problem("not_found", "VM %q was not found", reference)
	}
	return *found, nil
}
func (c *Client) Create(ctx context.Context, request compute.CreateRequest) (compute.Result, error) {
	var result compute.Result
	err := c.DoJSON(ctx, "POST", "machines", request, &result)
	return result, err
}
func (c *Client) Action(ctx context.Context, id string, request compute.ActionRequest) (compute.Result, error) {
	var result compute.Result
	err := c.DoJSON(ctx, "POST", "machines/"+url.PathEscape(id)+"/actions", request, &result)
	return result, err
}
func (c *Client) Operation(ctx context.Context, id string, requestID bool) (compute.Operation, error) {
	path := "operations/"
	if requestID {
		path += "by-request/"
	}
	var result compute.Operation
	err := c.DoJSON(ctx, "GET", path+url.PathEscape(id), nil, &result)
	return result, err
}
