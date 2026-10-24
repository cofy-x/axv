package cli

import (
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
