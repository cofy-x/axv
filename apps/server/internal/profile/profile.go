// Package profile owns the shared AXV profile, validation and non-destructive updates.
package profile

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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

type Profile struct {
	Home           string  `json:"-"`
	WorkspaceDir   string  `json:"workspace_dir,omitempty"`
	ProxyURL       string  `json:"proxy_url,omitempty"`
	RuntimeVersion int     `json:"runtime_version,omitempty"`
	Desktop        Desktop `json:"desktop,omitempty"`
	Compute        Compute `json:"compute,omitempty"`
	ComputeError   string  `json:"-"`
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
	return nil
}
