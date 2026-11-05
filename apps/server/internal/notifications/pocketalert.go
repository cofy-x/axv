package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

const pocketAlertEndpoint = "https://api.pocketalert.app/v1/messages"

// Only fixed status copy and the opaque return link leave AXV. No provider
// response content or credential is retained as diagnostic/history text.
func (m *Manager) send(ctx context.Context, storeID string, config Config, token string, event Notification) (string, time.Duration) {
	title := eventTitles[event.Kind]
	payload := map[string]string{
		"title":   title,
		"message": title + ". Observed " + event.CreatedAt.UTC().Format(time.RFC3339) + ". Open AXV to review the original work: " + m.Link(storeID, event.ID, config.ReturnOrigin),
		"level":   "default",
	}
	if config.ApplicationID != "" {
		payload["application_id"] = config.ApplicationID
	}
	if config.DeviceID != "" {
		payload["device_id"] = config.DeviceID
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, pocketAlertEndpoint, bytes.NewReader(body))
	if err != nil {
		return "failed", 0
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Token", token)
	response, err := m.options.Client.Do(request)
	if err != nil {
		return "uncertain", 0
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
	switch {
	case response.StatusCode == http.StatusCreated:
		return "accepted", 0
	case response.StatusCode == http.StatusTooManyRequests:
		delay := 24 * time.Hour
		if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= 86400 {
			delay = time.Duration(seconds) * time.Second
		}
		if delay < time.Minute {
			delay = time.Minute
		}
		return "pending", delay
	case response.StatusCode >= 500 || response.StatusCode >= 200 && response.StatusCode < 300:
		return "uncertain", 0
	default:
		return "failed", 0
	}
}
