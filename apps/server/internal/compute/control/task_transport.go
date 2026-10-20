package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

func taskJSON(ctx context.Context, m compute.Machine, d compute.Driver, action string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	token, err := d.GuestToken(m)
	if err != nil {
		return compute.Problem("guest_unavailable", "The original guest task transport is unavailable.")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	transport := compute.GuestTransport(m, d)
	defer transport.CloseIdleConnections()
	r, err := http.NewRequestWithContext(ctx, "POST", "http://guest/v1/project-tasks/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	if err != nil {
		return compute.Problem("guest_unavailable", "The original task request could not be confirmed. Query its durable receipt; do not submit another command.")
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || !jsonunicode.Valid(data) {
		return compute.Problem("guest_unavailable", "The guest task response was incomplete or invalid.")
	}
	if response.StatusCode != http.StatusOK && !(action == "submit" && response.StatusCode == http.StatusAccepted) {
		var p compute.Error
		if json.Unmarshal(data, &p) != nil || p.Code == "" || p.Message == "" {
			return compute.Problem("guest_unavailable", "The guest returned an invalid task error (HTTP %d).", response.StatusCode)
		}
		return &p
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(output) != nil || decoder.Decode(new(any)) != io.EOF {
		return compute.Problem("guest_unavailable", "The guest returned invalid task data.")
	}
	return nil
}
