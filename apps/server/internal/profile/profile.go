// Package profile owns the shared AXV profile, validation and non-destructive updates.
package profile

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type Desktop struct {
	Addr    string `json:"addr,omitempty"`
	DataDir string `json:"data_dir,omitempty"`
}

type Compute struct {
	Mode       string `json:"mode"`
	HostConfig string `json:"host_config,omitempty"`
	Socket     string `json:"socket,omitempty"`
}

type Remote struct {
	Enabled     bool   `json:"enabled"`
	Hostname    string `json:"hostname,omitempty"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
}

type Profile struct {
	Home            string     `json:"-"`
	WorkspaceDir    string     `json:"workspace_dir,omitempty"`
	ProxyURL        string     `json:"proxy_url,omitempty"`
	RuntimeVersion  int        `json:"runtime_version,omitempty"`
	Desktop         Desktop    `json:"desktop,omitempty"`
	Remote          Remote     `json:"remote,omitempty"`
	Cloudflare      Cloudflare `json:"cloudflare,omitempty"`
	CloudflareError string     `json:"-"`
	Compute         Compute    `json:"compute,omitempty"`
	ComputeError    string     `json:"-"`
}

func ValidateRemote(r Remote) error {
	if r.Hostname != "" {
		if len(r.Hostname) > 63 || r.Hostname[0] == '-' || r.Hostname[len(r.Hostname)-1] == '-' {
			return fmt.Errorf("remote.hostname must be a lowercase DNS label of 1 to 63 characters")
		}
		for _, c := range r.Hostname {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return fmt.Errorf("remote.hostname must be a lowercase DNS label of 1 to 63 characters")
			}
		}
	}
	if r.OwnerUserID != "" {
		id, err := strconv.ParseInt(r.OwnerUserID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != r.OwnerUserID {
			return fmt.Errorf("remote.owner_user_id must be a canonical positive numeric Tailscale user ID")
		}
	}
	return nil
}

func ValidateCompute(c Compute) error {
	switch c.Mode {
	case "disabled", "":
	case "managed":
		if !filepath.IsAbs(c.HostConfig) {
			return fmt.Errorf("compute.host_config must be absolute")
		}
	case "external":
		if !filepath.IsAbs(c.Socket) {
			return fmt.Errorf("compute.socket must be absolute")
		}
	default:
		return fmt.Errorf("unknown compute mode %q", c.Mode)
	}
	return nil
}

func validateDesktop(p Profile) error {
	if p.Desktop.DataDir != "" && !filepath.IsAbs(p.Desktop.DataDir) {
		return fmt.Errorf("desktop.data_dir must be absolute")
	}
	if p.ProxyURL != "" {
		proxy, err := url.Parse(p.ProxyURL)
		if err != nil || proxy == nil || proxy.Hostname() == "" ||
			(proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") ||
			proxy.Fragment != "" || strings.ContainsAny(p.ProxyURL, "\r\n\x00") {
			return errors.New("AXV configuration proxy_url must be an http, https, socks5, or socks5h URL with a host")
		}
	}
	return ValidateRemote(p.Remote)
}
