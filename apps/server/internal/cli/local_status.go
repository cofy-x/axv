package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
)

// readDesktopStatus inspects only the verified local desktop instance. Status
// commands never initialize configuration, follow a proxy, or start a service.
func readDesktopStatus(ctx context.Context, home, path string) (json.RawMessage, error) {
	state, err := launch.ReadState(home)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := launch.Verify(ctx, state); err != nil {
		return nil, fmt.Errorf("desktop is unavailable: %w", err)
	}
	base, err := launch.LocalDesktopURL(state.URL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local status returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("local status exceeds 1 MiB")
	}
	var identity struct {
		InstanceID string `json:"instanceId"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(body, &identity); err != nil {
		return nil, fmt.Errorf("invalid local status: %w", err)
	}
	if identity.InstanceID != state.InstanceID {
		return nil, fmt.Errorf("local status belongs to another desktop instance")
	}
	if identity.State == "" {
		return nil, fmt.Errorf("local status is missing its state")
	}
	return body, nil
}

// putLocalDesktop sends a verified local-origin JSON mutation to the running
// desktop. If no desktop state exists, it returns running=false with nil error.
func putLocalDesktop(ctx context.Context, home, path string, payload any) (json.RawMessage, bool, error) {
	state, err := launch.ReadState(home)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := launch.Verify(ctx, state); err != nil {
		return nil, false, nil
	}
	base, err := launch.LocalDesktopURL(state.URL)
	if err != nil {
		return nil, true, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, true, err
	}
	client := &http.Client{
		Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, base+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, true, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	response, err := client.Do(request)
	if err != nil {
		return nil, true, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return nil, true, err
	}
	if len(body) > 1<<20 {
		return nil, true, fmt.Errorf("local desktop response exceeds 1 MiB")
	}
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &problem) == nil && problem.Error != "" {
			return nil, true, fmt.Errorf("%s", problem.Error)
		}
		return nil, true, fmt.Errorf("local desktop returned HTTP %d", response.StatusCode)
	}
	return body, true, nil
}
