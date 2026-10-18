package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
	"github.com/cofy-x/axv/apps/server/internal/guest/sshkey"
	"github.com/cofy-x/axv/apps/server/internal/sshstream"
)

func registerGuestSSH(mux *http.ServeMux, service *control.Service) {
	mux.HandleFunc("GET /v1/machines/{id}/ssh", func(w http.ResponseWriter, r *http.Request) {
		access, ok := accessMachine(service, w, r, true)
		if !ok {
			return
		}
		m, driver := access.Machine, access.Driver
		var result guestproto.SSHInfo
		if !guestJSON(w, r, m, driver, "GET", "/v1/ssh", nil, &result) {
			return
		}
		if result.Version != guestproto.Version || result.MachineID != m.ID || result.BootID == "" || result.User != "axv" {
			accessProblem(w, 503, "guest_identity_mismatch", "The guest SSH protocol or machine identity does not match.")
			return
		}
		if result.HostKey != "" {
			key, err := sshkey.Normalize(result.HostKey)
			if err != nil {
				accessProblem(w, 503, "guest_identity_mismatch", "The guest returned an invalid SSH host key.")
				return
			}
			result.HostKey = key
		} else if result.Available {
			accessProblem(w, 503, "guest_identity_mismatch", "The guest omitted its SSH host key.")
			return
		}
		result.HostID = access.HostID
		write(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /v1/machines/{id}/ssh/clients", func(w http.ResponseWriter, r *http.Request) {
		access, ok := accessMachine(service, w, r, true)
		if !ok {
			return
		}
		m, driver := access.Machine, access.Driver
		var request guestproto.SSHClientRequest
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if !accessBoot(w, r, m, driver, request.BootID) {
			return
		}
		var result guestproto.SSHClient
		if !guestJSON(w, r, m, driver, "POST", "/v1/ssh/clients", request, &result) {
			return
		}
		key, err := sshkey.Normalize(request.PublicKey)
		if err != nil || result.BootID != request.BootID || result.ClientID != request.ClientID || result.PublicKey != key {
			accessProblem(w, 503, "guest_identity_mismatch", "The SSH registration response does not match this request.")
			return
		}
		write(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /v1/machines/{id}/ssh/attach", func(w http.ResponseWriter, r *http.Request) {
		access, ok := accessMachine(service, w, r, true)
		if !ok {
			return
		}
		m, driver := access.Machine, access.Driver
		if !strings.EqualFold(r.Header.Get("Upgrade"), sshstream.Protocol) {
			accessProblem(w, 400, "invalid_request", "An AXV SSH stream upgrade is required.")
			return
		}
		boot := r.URL.Query().Get("bootId")
		if !accessBoot(w, r, m, driver, boot) {
			return
		}
		token, err := driver.GuestToken(m)
		if err != nil {
			failure(w, err)
			return
		}
		transport := compute.GuestTransport(m, driver)
		defer transport.CloseIdleConnections()
		proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme, p.Out.URL.Host, p.Out.Host = "http", "guest", "guest"
			p.Out.URL.Path, p.Out.URL.RawPath = "/v1/ssh/attach", ""
			p.Out.URL.RawQuery = url.Values{"bootId": []string{boot}}.Encode()
			p.Out.Header.Set("Authorization", "Bearer "+token)
			p.Out.Header.Del("Cookie")
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			accessProblem(w, 503, "ssh_unavailable", "The guest SSH connection failed. Its target was not replaced.")
		}}
		proxy.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /v1/machines/{id}/network", func(w http.ResponseWriter, r *http.Request) {
		access, ok := accessMachine(service, w, r, false)
		if !ok {
			return
		}
		m, driver := access.Machine, access.Driver
		var result guestproto.NetworkInfo
		if !guestJSON(w, r, m, driver, "GET", "/v1/network", nil, &result) {
			return
		}
		if result.Version != guestproto.Version || result.MachineID != m.ID || result.BootID == "" || result.ObservedAt.IsZero() || len(result.Addresses) > 256 {
			accessProblem(w, 503, "guest_identity_mismatch", "The guest network protocol or machine identity does not match.")
			return
		}
		for i := range result.Addresses {
			address := &result.Addresses[i]
			ip, err := netip.ParseAddr(address.Address)
			if err != nil || address.PrefixLength < 0 || address.PrefixLength > ip.BitLen() || address.Interface == "" ||
				(address.Family != "ipv4" && address.Family != "ipv6") || (address.Family == "ipv4") != ip.Is4() ||
				!ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				accessProblem(w, 503, "guest_identity_mismatch", "The guest returned an invalid network address.")
				return
			}
			address.Reachability = guestproto.Reachability{From: "deployment-host", State: "unknown", Reason: "not_verified"}
			if observer, ok := driver.(interface {
				GuestReachability(compute.Machine, netip.Addr) guestproto.Reachability
			}); ok {
				address.Reachability = observer.GuestReachability(m, ip)
			}
		}
		result.HostID, result.Provider = access.HostID, m.Spec.Provider
		write(w, http.StatusOK, result)
	})
}

func accessProblem(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]string{"code": code, "error": message})
}

func accessMachine(service *control.Service, w http.ResponseWriter, r *http.Request, requireHost bool) (control.GuestAccess, bool) {
	capability := "ssh"
	if strings.Contains(r.URL.Path, "/network") {
		capability = "network"
	}
	access, err := service.BoundGuestAccess(r.Context(), r.PathValue("id"), capability, r.Header.Get("X-AXV-Host-ID"), requireHost)
	if err != nil {
		failure(w, err)
		return control.GuestAccess{}, false
	}
	return access, true
}

func accessBoot(w http.ResponseWriter, r *http.Request, m compute.Machine, driver compute.Driver, boot string) bool {
	if boot == "" {
		accessProblem(w, 400, "invalid_request", "The original VM bootId is required.")
		return false
	}
	health, err := compute.Health(r.Context(), m, driver)
	if err != nil {
		accessProblem(w, 503, "guest_unavailable", "The guest identity could not be verified: "+err.Error())
		return false
	}
	if health.BootID != boot {
		accessProblem(w, 409, "boot_changed", "The VM boot changed; this SSH request was not redirected.")
		return false
	}
	return true
}

func guestJSON(w http.ResponseWriter, r *http.Request, m compute.Machine, driver compute.Driver, method, path string, body, result any) bool {
	token, err := driver.GuestToken(m)
	if err != nil {
		failure(w, err)
		return false
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			failure(w, err)
			return false
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	transport := compute.GuestTransport(m, driver)
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, "http://guest"+path, bytes.NewReader(data))
	if err != nil {
		failure(w, err)
		return false
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		accessProblem(w, 503, "guest_unavailable", "The guest access service is unavailable.")
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem); err != nil || problem.Code == "" || problem.Error == "" {
			accessProblem(w, 503, "guest_unavailable", fmt.Sprintf("The guest does not provide this access capability (HTTP %d).", response.StatusCode))
		} else {
			accessProblem(w, response.StatusCode, problem.Code, problem.Error)
		}
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(result); err != nil || decoder.Decode(new(any)) != io.EOF {
		accessProblem(w, 503, "guest_unavailable", "The guest returned invalid access metadata.")
		return false
	}
	return true
}
