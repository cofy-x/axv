// Package cloudflareapi implements bounded Cloudflare management API calls.
// It does not store credentials, run cloudflared, or establish desktop authority.
package cloudflareapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	apiOrigin        = "https://api.cloudflare.com/client/v4"
	requestTimeout   = 10 * time.Second
	operationTimeout = 30 * time.Second
	maxResponseBytes = 2 << 20
	maxPages         = 20
	pageSize         = 50
)

type Options struct {
	Token string
	// HTTPClient is a trusted composition/testing dependency. Its transport is
	// retained; cookies, redirects and unbounded timeouts are never inherited.
	// A supplied *http.Transport is cloned with proxy lookup disabled.
	HTTPClient *http.Client
}

type Client struct {
	token string
	http  *http.Client
}

// String and GoString keep routine formatting from exposing the bearer token.
func (*Client) String() string     { return "Cloudflare API client (credentials redacted)" }
func (c *Client) GoString() string { return c.String() }

// Error excludes provider messages and underlying transport error strings,
// either of which can contain credentials. Uncertain marks a write whose
// outcome must be reconciled instead of blindly replayed.
type Error struct {
	Kind       string
	StatusCode int
	Code       int
	Uncertain  bool
	cause      error
}

func (e *Error) Error() string {
	message := "Cloudflare API request failed"
	switch e.Kind {
	case "invalid_input":
		message = "Cloudflare API settings or resource identifiers are invalid"
	case "unauthorized":
		message = "Cloudflare rejected the API token"
	case "forbidden":
		message = "Cloudflare denied access; check the token permissions and resource scope"
	case "not_found":
		message = "Cloudflare resource was not found"
	case "rate_limited":
		message = "Cloudflare rate limited this request; retry later"
	case "invalid_response":
		message = "Cloudflare returned an invalid or oversized response"
	case "unavailable":
		message = "Cloudflare could not be reached within the request limit"
	case "inactive_token":
		message = "The Cloudflare API token is not active"
	case "unsupported":
		message = "Cloudflare analytics is unavailable for this token, plan, dataset or time range"
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	if e.Uncertain {
		message += "; inspect the resource before retrying this change"
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

var ErrConflict = errors.New("Cloudflare resource conflicts with existing configuration")

// ConflictError identifies a reviewable conflict without returning another
// application's configuration or any provider response body.
type ConflictError struct {
	Resource  string
	Reason    string
	Uncertain bool
}

func (e *ConflictError) Error() string { return "Cloudflare " + e.Resource + " conflict: " + e.Reason }
func (*ConflictError) Unwrap() error   { return ErrConflict }

func conflict(resource, reason string) error {
	return &ConflictError{Resource: resource, Reason: reason}
}

// IsUncertain distinguishes a definitely rejected preflight from a write that
// may have changed provider state. Durable ownership must survive the latter.
func IsUncertain(err error) bool {
	var failure *Error
	if errors.As(err, &failure) && failure.Uncertain {
		return true
	}
	var collision *ConflictError
	return errors.As(err, &collision) && collision.Uncertain
}

func New(options Options) (*Client, error) {
	if options.Token == "" || len(options.Token) > 4096 || strings.IndexFunc(options.Token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r > unicode.MaxASCII
	}) >= 0 {
		return nil, &Error{Kind: "invalid_input"}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if options.HTTPClient != nil {
		if injected, ok := options.HTTPClient.Transport.(*http.Transport); ok {
			transport = injected.Clone()
		} else if options.HTTPClient.Transport != nil {
			return &Client{token: options.Token, http: restrictedClient(options.HTTPClient.Transport, options.HTTPClient.Timeout)}, nil
		}
	}
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}).DialContext
	if options.HTTPClient != nil {
		if injected, ok := options.HTTPClient.Transport.(*http.Transport); ok && injected.DialContext != nil {
			transport.DialContext = injected.DialContext
		}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = max(transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
	transport.TLSHandshakeTimeout = requestTimeout
	transport.ResponseHeaderTimeout = requestTimeout
	transport.MaxResponseHeaderBytes = 32 << 10
	timeout := time.Duration(0)
	if options.HTTPClient != nil {
		timeout = options.HTTPClient.Timeout
	}
	return &Client{token: options.Token, http: restrictedClient(transport, timeout)}, nil
}

func restrictedClient(transport http.RoundTripper, timeout time.Duration) *http.Client {
	if timeout <= 0 || timeout > requestTimeout {
		timeout = requestTimeout
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Cloudflare API redirects are not allowed")
	}}
}

func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

type envelope struct {
	Success *bool           `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code int `json:"code"`
	} `json:"errors"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		Count      int `json:"count"`
		TotalPages int `json:"total_pages"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

func invalidInput() error { return &Error{Kind: "invalid_input"} }

func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validHostname(value string) bool {
	if len(value) > 253 || !strings.Contains(value, ".") || value != strings.ToLower(value) || net.ParseIP(value) != nil {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) (envelope, error) {
	var result envelope
	data, status, err := c.exchange(ctx, method, path, body, method != http.MethodGet)
	if err != nil {
		return result, err
	}
	decodeErr := json.Unmarshal(data, &result)
	if status < 200 || status > 299 || decodeErr == nil && result.Success != nil && !*result.Success {
		code := 0
		if len(result.Errors) > 0 {
			code = result.Errors[0].Code
		}
		failure := statusError(status, method != http.MethodGet)
		failure.Code = code
		return result, failure
	}
	// The documented DNS DELETE response contains only result.id; other REST
	// resources require the normal explicit success envelope.
	if decodeErr != nil || result.Success == nil && method != http.MethodDelete || len(result.Result) == 0 || string(result.Result) == "null" {
		return result, &Error{Kind: "invalid_response", StatusCode: status, Uncertain: method != http.MethodGet}
	}
	if out != nil && json.Unmarshal(result.Result, out) != nil {
		return result, &Error{Kind: "invalid_response", StatusCode: status, Uncertain: method != http.MethodGet}
	}
	return result, nil
}

func statusError(status int, write bool) *Error {
	kind := "provider_error"
	switch status {
	case http.StatusUnauthorized:
		kind = "unauthorized"
	case http.StatusForbidden:
		kind = "forbidden"
	case http.StatusNotFound:
		kind = "not_found"
	case http.StatusTooManyRequests:
		kind = "rate_limited"
	}
	return &Error{Kind: kind, StatusCode: status, Uncertain: write && status >= 500}
}

func (c *Client) exchange(ctx context.Context, method, path string, body any, write bool) ([]byte, int, error) {
	if ctx == nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, 0, invalidInput()
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil || len(encoded) > maxResponseBytes {
			return nil, 0, invalidInput()
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, apiOrigin+path, payload)
	if err != nil || request.URL.Scheme != "https" || request.URL.Host != "api.cloudflare.com" || request.URL.User != nil {
		return nil, 0, invalidInput()
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, 0, &Error{Kind: "unavailable", Uncertain: write, cause: ctx.Err()}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, response.StatusCode, &Error{Kind: "invalid_response", StatusCode: response.StatusCode, Uncertain: write}
	}
	return data, response.StatusCode, nil
}

func listPages[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	values := make(url.Values, len(query)+2)
	for key, value := range query {
		values[key] = append([]string(nil), value...)
	}
	values.Set("per_page", strconv.Itoa(pageSize))
	all := []T{}
	for page := 1; page <= maxPages; page++ {
		values.Set("page", strconv.Itoa(page))
		var items []T
		response, err := c.do(ctx, http.MethodGet, path+"?"+values.Encode(), nil, &items)
		if err != nil {
			return nil, err
		}
		if len(items) > pageSize || response.ResultInfo.Page != 0 && response.ResultInfo.Page != page {
			return nil, &Error{Kind: "invalid_response"}
		}
		all = append(all, items...)
		pages := response.ResultInfo.TotalPages
		if pages > maxPages || response.ResultInfo.TotalCount > maxPages*pageSize ||
			pages > page && len(items) == 0 || response.ResultInfo.Count != 0 && response.ResultInfo.Count != len(items) {
			return nil, &Error{Kind: "invalid_response"}
		}
		if pages > 0 && page >= pages || pages == 0 && len(items) < pageSize {
			if response.ResultInfo.TotalCount > len(all) {
				return nil, &Error{Kind: "invalid_response"}
			}
			return all, nil
		}
		if page == maxPages {
			return nil, &Error{Kind: "invalid_response"}
		}
	}
	return nil, &Error{Kind: "invalid_response"}
}

func uncertain(err error) error {
	var failure *Error
	if errors.As(err, &failure) {
		copy := *failure
		copy.Uncertain = true
		return &copy
	}
	var collision *ConflictError
	if errors.As(err, &collision) {
		copy := *collision
		copy.Uncertain = true
		return &copy
	}
	return err
}
