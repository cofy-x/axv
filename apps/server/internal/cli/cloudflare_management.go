package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/cloudflarepublish"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/profile"
	"github.com/spf13/cobra"
)

// desktopCloudflareCall never starts a desktop or follows redirects. Every
// command verifies the selected profile's live desktop before accessing it.
func desktopCloudflareCall(ctx context.Context, method, path string, payload any, hostID string) (json.RawMessage, error) {
	home, err := profile.Home()
	if err != nil {
		return nil, err
	}
	state, err := launch.ReadState(home)
	if err != nil {
		return nil, fmt.Errorf("open the local AXV desktop before using Cloudflare: %w", err)
	}
	if err := launch.Verify(ctx, state); err != nil {
		return nil, fmt.Errorf("desktop is unavailable: %w", err)
	}
	base, err := launch.LocalDesktopURL(state.URL)
	if err != nil {
		return nil, err
	}
	var data []byte
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Origin", base)
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if hostID != "" {
		r.Header.Set("X-AXV-Host-ID", hostID)
	}
	client := &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("Cloudflare request did not complete; inspect publications or configuration before retrying: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if err != nil || len(body) > 16<<20 {
		return nil, fmt.Errorf("Cloudflare response could not be read; inspect the original operation before retrying")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Error != "" {
			return nil, fmt.Errorf("%s (%s)", failure.Error, failure.Code)
		}
		return nil, fmt.Errorf("Cloudflare returned HTTP %d", response.StatusCode)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("Cloudflare returned invalid JSON")
	}
	return body, nil
}

func printCloudflareJSON(cmd *cobra.Command, body json.RawMessage) error {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	return jsonResult(cmd.OutOrStdout(), value)
}

func addCloudflareManagementCommands(parent *cobra.Command) {
	for _, entry := range []struct{ name, description, path string }{
		{"config", "Show Cloudflare configuration with credentials omitted", "/api/cloudflare/config"},
		{"publications", "List original published service records and pending cleanup", "/api/cloudflare-publications"},
	} {
		command := &cobra.Command{Use: entry.name, Short: entry.description, Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, entry.path, nil, "")
			if err != nil {
				return err
			}
			return printCloudflareJSON(cmd, body)
		}}
		command.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
		parent.AddCommand(command)
	}
	var force bool
	check := &cobra.Command{Use: "check", Short: "Check the configured token, DNS read access and existing tunnel", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		path := "/api/cloudflare/health"
		if force {
			path += "?force=true"
		}
		body, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, path, nil, "")
		if err != nil {
			return err
		}
		return printCloudflareJSON(cmd, body)
	}}
	check.Flags().BoolVar(&force, "force", false, "bypass the one-minute health cache")
	check.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
	var hours int
	traffic := &cobra.Command{Use: "traffic", Short: "Read Cloudflare zone traffic without changing services", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if hours != 1 && hours != 6 && hours != 12 && hours != 24 && hours != 168 && hours != 720 {
			return usageError(fmt.Errorf("--hours must be 1, 6, 12, 24, 168 or 720"))
		}
		body, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, "/api/cloudflare/traffic?hours="+strconv.Itoa(hours), nil, "")
		if err != nil {
			return err
		}
		return printCloudflareJSON(cmd, body)
	}}
	traffic.Flags().IntVar(&hours, "hours", 24, "traffic window: 1, 6, 12, 24, 168 or 720 hours; availability depends on your Cloudflare plan")
	traffic.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
	parent.AddCommand(check, traffic, newCloudflareConfigure(), newCloudflarePublish())
	for _, action := range []string{"unpublish", "retry"} {
		command := &cobra.Command{Use: action + " <publication-id>", Short: action + " the exact recorded service publication", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			list, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, "/api/cloudflare-publications", nil, "")
			if err != nil {
				return err
			}
			var snapshot struct {
				Publications []struct {
					ID      string                 `json:"id"`
					Binding compute.ProjectBinding `json:"binding"`
				} `json:"publications"`
			}
			if json.Unmarshal(list, &snapshot) != nil {
				return fmt.Errorf("publication records are unavailable")
			}
			hostID := ""
			for _, item := range snapshot.Publications {
				if item.ID == args[0] {
					hostID = item.Binding.HostID
					break
				}
			}
			if hostID == "" {
				return fmt.Errorf("the original publication was not found")
			}
			body, err := desktopCloudflareCall(cmd.Context(), http.MethodPost, "/api/cloudflare-publications/"+url.PathEscape(args[0])+"/"+action, map[string]any{}, hostID)
			if err != nil {
				return err
			}
			return printCloudflareJSON(cmd, body)
		}}
		command.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
		parent.AddCommand(command)
	}
}

func newCloudflareConfigure() *cobra.Command {
	var filename string
	cmd := &cobra.Command{Use: "configure", Short: "Save a reviewed Cloudflare configuration snapshot from a JSON file", Args: arguments(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if filename == "" {
			return usageError(fmt.Errorf("--file is required; use '-' to read JSON from standard input"))
		}
		var input io.Reader = cmd.InOrStdin()
		if filename != "-" {
			file, err := os.Open(filename)
			if err != nil {
				return err
			}
			defer file.Close()
			input = file
		}
		data, err := io.ReadAll(io.LimitReader(input, 16<<10+1))
		if err != nil || len(data) > 16<<10 {
			return usageError(fmt.Errorf("configuration must be at most 16 KiB"))
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(data, &payload); err != nil || payload == nil || !jsonunicode.Valid(data) {
			return usageError(fmt.Errorf("provide one valid Unicode Cloudflare configuration JSON object"))
		}
		current, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, "/api/cloudflare/config", nil, "")
		if err != nil {
			return err
		}
		var snapshot struct {
			InstanceID string `json:"instanceId"`
			Revision   string `json:"revision"`
		}
		if err := json.Unmarshal(current, &snapshot); err != nil {
			return err
		}
		if _, ok := payload["instanceId"]; !ok {
			payload["instanceId"], _ = json.Marshal(snapshot.InstanceID)
		}
		if _, ok := payload["expectedRevision"]; !ok {
			payload["expectedRevision"], _ = json.Marshal(snapshot.Revision)
		}
		body, err := desktopCloudflareCall(cmd.Context(), http.MethodPut, "/api/cloudflare/config", payload, "")
		if err != nil {
			return err
		}
		return printCloudflareJSON(cmd, body)
	}}
	cmd.Flags().StringVar(&filename, "file", "", "reviewed settings JSON file, or '-' for standard input; never put API tokens in command arguments")
	return cmd
}

func newCloudflarePublish() *cobra.Command {
	var name, domain, visibility, bindingFile, requestID, instanceID string
	var port int
	cmd := &cobra.Command{Use: "publish <project-id>", Short: "Publish one HTTP service from an explicitly selected VM project", Args: arguments(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if port < 1024 || port > 65535 || !cloudflarepublish.ValidName(name) || profile.ValidateAllocationDomain(domain) != nil {
			return usageError(fmt.Errorf("provide --port from 1024 to 65535, an isolated --domain and --name with 1–24 lowercase letters, digits or interior hyphens"))
		}
		if visibility != "owner" && visibility != "public" {
			return usageError(fmt.Errorf("--visibility must be owner or public"))
		}
		if requestID != "" && (bindingFile == "" || instanceID == "") {
			return usageError(fmt.Errorf("recovery with --request-id requires the original --binding-file, --domain and journal --instance-id; list publications before retrying"))
		}
		var binding compute.ProjectBinding
		var err error
		if bindingFile != "" {
			binding, err = readProjectBinding(bindingFile, args[0])
		} else {
			client, connectErr := commandClient(cmd)
			if connectErr != nil {
				return connectErr
			}
			result, bindingErr := client.ProjectBinding(cmd.Context(), args[0])
			client.Close()
			binding, err = result.Binding, bindingErr
		}
		if err != nil {
			return err
		}
		list, err := desktopCloudflareCall(cmd.Context(), http.MethodGet, "/api/cloudflare-publications", nil, "")
		if err != nil {
			return err
		}
		var snapshot struct {
			InstanceID string `json:"instanceId"`
			Domain     string `json:"domain"`
			Supported  bool   `json:"supported"`
			Reason     string `json:"reason"`
		}
		if json.Unmarshal(list, &snapshot) != nil || snapshot.InstanceID == "" {
			return fmt.Errorf("publication instance is unavailable")
		}
		if instanceID != "" && instanceID != snapshot.InstanceID {
			return fmt.Errorf("the original publication journal is unavailable; the request was not redirected to a new journal")
		}
		if requestID == "" {
			if !snapshot.Supported {
				return fmt.Errorf("new publishing is unavailable: %s", snapshot.Reason)
			}
			if profile.ValidateAllocationDomain(snapshot.Domain) != nil || snapshot.Domain != domain {
				return fmt.Errorf("the configured allocation domain changed; review Cloudflare configuration before publishing")
			}
		}
		if requestID == "" {
			requestID = compute.NewID("publish")
		}
		payload := map[string]any{"instanceId": snapshot.InstanceID, "requestId": requestID, "binding": binding, "port": port, "name": name, "domain": domain, "visibility": visibility}
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Cloudflare request %s; journal %s; allocation domain %s; name %s\n", requestID, snapshot.InstanceID, domain, name); err != nil {
			return err
		}
		body, err := desktopCloudflareCall(cmd.Context(), http.MethodPost, "/api/cloudflare-publications", payload, binding.HostID)
		if err != nil {
			return fmt.Errorf("publication request %s: %w", requestID, err)
		}
		return printCloudflareJSON(cmd, body)
	}}
	cmd.Flags().StringVar(&name, "name", "", "1–24 lowercase letters, digits or interior hyphens; AXV assigns a fresh isolated HTTPS hostname")
	cmd.Flags().StringVar(&domain, "domain", "", "reviewed allocation suffix from Cloudflare configuration")
	cmd.Flags().IntVar(&port, "port", 0, "guest HTTP port from 1024 to 65535")
	cmd.Flags().StringVar(&visibility, "visibility", "owner", "owner requires Access authentication; public explicitly allows public Internet visitors")
	cmd.Flags().StringVar(&bindingFile, "binding-file", "", "original output of axv vm project binding, required for recovery")
	cmd.Flags().StringVar(&requestID, "request-id", "", "original receipt ID for recovery; never generate another ID to retry an uncertain request")
	cmd.Flags().StringVar(&instanceID, "instance-id", "", "original publication journal identity, required with --request-id")
	cmd.Flags().Bool("json", false, "write machine-readable JSON (also the default)")
	return cmd
}
