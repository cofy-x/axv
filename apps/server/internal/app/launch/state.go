package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

type State struct {
	LaunchID    string `json:"launchId,omitempty"`
	Executable  string `json:"executable"`
	InstanceID  string `json:"instanceId"`
	URL         string `json:"url"`
	PID         int    `json:"pid"`
	Fingerprint string `json:"fingerprint"`
}

func ReadState(home string) (State, error) {
	var state State
	err := jsonfile.Read(filepath.Join(home, "run", "desktop.json"), &state)
	return state, err
}

func Verify(ctx context.Context, state State) error {
	if state.InstanceID == "" || state.URL == "" {
		return fmt.Errorf("missing desktop instance identity")
	}
	base, err := LocalDesktopURL(state.URL)
	if err != nil {
		return err
	}
	var actual struct {
		InstanceID string `json:"instanceId"`
		LaunchID   string `json:"launchId"`
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	if err := requestJSON(ctx, client, base+"/api/runtime", &actual); err != nil {
		return err
	}
	if actual.InstanceID != state.InstanceID || actual.LaunchID != state.LaunchID {
		return fmt.Errorf("the HTTP listener belongs to another instance")
	}
	return nil
}

// LocalDesktopURL accepts only an unambiguous local runtime origin.
func LocalDesktopURL(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return "", fmt.Errorf("invalid local desktop URL")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("desktop URL must use a loopback IP address")
	}
	u.Path = ""
	return u.String(), nil
}

func requestJSON(ctx context.Context, client *http.Client, url string, value any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("service returned HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(value)
}
