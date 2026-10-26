package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
)

// A fault is consumed once, so a later explicit retry uses a healthy provider.
// A trailing slash matches one resource beneath a collection.
type ownershipFault struct {
	method, path string
	nth, status  int
	seen         int
	afterWrite   bool
}

type ownershipFaultTransport struct {
	cloud *fakeCloud
	mu    sync.Mutex
	fault *ownershipFault
}

func (transport *ownershipFaultTransport) arm(fault ownershipFault) {
	transport.mu.Lock()
	transport.fault = &fault
	transport.mu.Unlock()
}

func (transport *ownershipFaultTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := strings.TrimPrefix(request.URL.Path, "/client/v4")
	transport.mu.Lock()
	fault := transport.fault
	matched := fault != nil && request.Method == fault.method && (path == fault.path || strings.HasSuffix(fault.path, "/") && strings.HasPrefix(path, fault.path))
	if matched {
		fault.seen++
		matched = fault.seen == fault.nth
	}
	if matched {
		transport.fault = nil
	}
	transport.mu.Unlock()
	if !matched {
		return transport.cloud.RoundTrip(request)
	}
	if fault.afterWrite {
		response, err := transport.cloud.RoundTrip(request)
		if err != nil {
			return response, err
		}
		_ = response.Body.Close()
	}
	if fault.status == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: fault.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":false,"result":{},"errors":[]}`)), Request: request}, nil
}

func fixtureWithOwnershipFault(t *testing.T, fault ownershipFault) (fixture, *ownershipFaultTransport) {
	t.Helper()
	f := publishingFixture(t)
	transport := &ownershipFaultTransport{cloud: f.cloud}
	transport.arm(fault)
	f.manager.options.Client = func(settings Settings) (*cfclient.Client, error) {
		return cfclient.New(cfclient.Options{Token: settings.APIToken, HTTPClient: &http.Client{Transport: transport}})
	}
	return f, transport
}

func insertForeignMatchingIngress(t *testing.T, f fixture, value Publication) []byte {
	t.Helper()
	f.cloud.mu.Lock()
	defer f.cloud.mu.Unlock()
	rules := f.cloud.configuration["ingress"].([]any)
	foreign := map[string]any{"hostname": value.Hostname, "service": "http://" + f.settings.Listen, "originRequest": map[string]any{"httpHostHeader": value.Hostname}}
	f.cloud.configuration["ingress"] = append(append(append([]any{}, rules[:len(rules)-1]...), foreign), rules[len(rules)-1])
	f.cloud.version++
	encoded, err := json.Marshal(f.cloud.configuration)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDefiniteIngressFailureCannotAdoptOrRemoveMatchingForeignRule(t *testing.T) {
	for _, stage := range []struct {
		name, method, path string
		nth                int
	}{
		{"write", http.MethodPut, "/accounts/account/cfd_tunnel/tunnel/configurations", 1},
		{"tunnel-preflight", http.MethodGet, "/accounts/account/cfd_tunnel/tunnel", 1},
		{"initial-config-read", http.MethodGet, "/accounts/account/cfd_tunnel/tunnel/configurations", 1},
		{"prepared-config-read", http.MethodGet, "/accounts/account/cfd_tunnel/tunnel/configurations", 2},
		{"final-config-read", http.MethodGet, "/accounts/account/cfd_tunnel/tunnel/configurations", 3},
	} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, 0} {
			// A lost write response is uncertain and is covered separately.
			if stage.method == http.MethodPut && status == 0 {
				continue
			}
			for _, action := range []string{"retry", "unpublish"} {
				t.Run(fmt.Sprintf("%s/%d/%s", stage.name, status, action), func(t *testing.T) {
					f, _ := fixtureWithOwnershipFault(t, ownershipFault{method: stage.method, path: stage.path, nth: stage.nth, status: status})
					value := publish(t, f, "public", "denied-ingress")
					failed := waitState(t, f.manager, value.ID, "error")
					item, _ := f.manager.record(value.ID)
					if failed.Phase != "ingress" || item.Resources.Ingress != nil || item.Resources.IngressExpected != "" {
						t.Fatalf("definitive denial retained ingress adoption authority: publication=%+v resources=%+v", failed, item.Resources)
					}
					before := insertForeignMatchingIngress(t, f, value)
					if action == "retry" {
						if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
							t.Fatal(err)
						}
						waitState(t, f.manager, value.ID, "error")
						item, _ = f.manager.record(value.ID)
						if item.Resources.Ingress != nil || item.Resources.IngressExpected != "" {
							t.Fatal("retry adopted a matching foreign rule after a definitive denial")
						}
					}
					if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
						t.Fatal(err)
					}
					waitState(t, f.manager, value.ID, "removed")
					f.cloud.mu.Lock()
					defer f.cloud.mu.Unlock()
					after, _ := json.Marshal(f.cloud.configuration)
					if string(after) != string(before) || f.cloud.ingressWrites != 0 || len(f.cloud.dns) != 0 {
						t.Fatalf("cleanup changed foreign configuration after a denied write: writes=%d before=%s after=%s", f.cloud.ingressWrites, before, after)
					}
				})
			}
		}
	}
}

func TestDefiniteDNSFailureCannotAdoptOrRemoveForeignRecord(t *testing.T) {
	for _, stage := range []struct {
		name, method, path string
	}{
		{"write", http.MethodPost, "/zones/zone/dns_records"},
		{"zone-preflight", http.MethodGet, "/zones/zone"},
		{"record-preflight", http.MethodGet, "/zones/zone/dns_records"},
	} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, 0} {
			if stage.method == http.MethodPost && status == 0 {
				continue
			}
			for _, action := range []string{"retry", "unpublish"} {
				t.Run(fmt.Sprintf("%s/%d/%s", stage.name, status, action), func(t *testing.T) {
					f, _ := fixtureWithOwnershipFault(t, ownershipFault{method: stage.method, path: stage.path, nth: 1, status: status})
					value := publish(t, f, "public", "denied-dns")
					failed := waitState(t, f.manager, value.ID, "error")
					item, _ := f.manager.record(value.ID)
					if failed.Phase != "dns" || item.Resources.DNS != nil || item.Resources.Ingress != nil {
						t.Fatalf("definitive failure retained DNS ownership: publication=%+v resources=%+v", failed, item.Resources)
					}
					foreign := cfclient.DNSRecord{ID: "foreign-record", Type: "CNAME", Name: value.Hostname, Content: "tunnel.cfargotunnel.com", Proxied: true, Comment: "belongs to another application"}
					f.cloud.mu.Lock()
					f.cloud.dns[foreign.ID] = foreign
					before, _ := json.Marshal(f.cloud.configuration)
					f.cloud.mu.Unlock()
					if action == "retry" {
						if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
							t.Fatal(err)
						}
						waitState(t, f.manager, value.ID, "error")
						item, _ = f.manager.record(value.ID)
						if item.Resources.DNS != nil {
							t.Fatal("retry retained ownership of a foreign DNS record")
						}
					}
					if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
						t.Fatal(err)
					}
					waitState(t, f.manager, value.ID, "removed")
					if f.manager.Busy() {
						t.Fatal("foreign DNS blocked removal of an unwritten publication")
					}
					f.cloud.mu.Lock()
					defer f.cloud.mu.Unlock()
					after, _ := json.Marshal(f.cloud.configuration)
					if len(f.cloud.dns) != 1 || f.cloud.dns[foreign.ID] != foreign || f.cloud.dnsCreates != 0 || f.cloud.ingressWrites != 0 || string(after) != string(before) {
						t.Fatalf("cleanup changed foreign resources after a definite DNS failure: dns=%+v configuration=%s", f.cloud.dns, after)
					}
				})
			}
		}
	}
}

func TestUncertainCloudOwnershipSurvivesLaterDefiniteFailure(t *testing.T) {
	for _, phase := range []string{"dns", "ingress"} {
		for _, failure := range []string{"lost-response", "provider-unavailable", "verification-401", "verification-403", "verification-transport"} {
			for _, action := range []string{"retry", "unpublish"} {
				t.Run(phase+"/"+failure+"/"+action, func(t *testing.T) {
					fault := ownershipFault{method: http.MethodPost, path: "/zones/zone/dns_records", nth: 1, afterWrite: true}
					if phase == "ingress" {
						fault.method = http.MethodPut
						fault.path = "/accounts/account/cfd_tunnel/tunnel/configurations"
					}
					switch failure {
					case "provider-unavailable":
						fault.status = http.StatusServiceUnavailable
					case "verification-401", "verification-403", "verification-transport":
						fault.method = http.MethodGet
						fault.afterWrite = false
						if phase == "dns" {
							fault.path += "/"
						} else {
							// Prepared snapshot, Ensure preflight, final recheck, then verification.
							fault.nth = 4
						}
						if failure == "verification-401" {
							fault.status = http.StatusUnauthorized
						} else if failure == "verification-403" {
							fault.status = http.StatusForbidden
						}
					}
					f, transport := fixtureWithOwnershipFault(t, fault)
					f.cloud.mu.Lock()
					originalConfig, _ := json.Marshal(f.cloud.configuration)
					f.cloud.mu.Unlock()
					value := publish(t, f, "public", "uncertain")
					failed := waitState(t, f.manager, value.ID, "error")
					prepared, _ := f.manager.record(value.ID)
					if failed.Phase != phase || prepared.Resources.DNS == nil || phase == "ingress" && prepared.Resources.Ingress == nil {
						t.Fatalf("uncertain write lost original ownership: publication=%+v resources=%+v", failed, prepared.Resources)
					}
					preflight := ownershipFault{method: http.MethodGet, path: "/zones/zone", nth: 1, status: http.StatusForbidden}
					if phase == "ingress" {
						preflight.path = "/accounts/account/cfd_tunnel/tunnel"
					}
					transport.arm(preflight)
					if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
						t.Fatal(err)
					}
					failed = waitState(t, f.manager, value.ID, "error")
					retained, _ := f.manager.record(value.ID)
					if failed.Phase != phase || retained.Resources.DNS == nil || *retained.Resources.DNS != *prepared.Resources.DNS || phase == "ingress" && (retained.Resources.Ingress == nil || *retained.Resources.Ingress != *prepared.Resources.Ingress || retained.Resources.IngressExpected != prepared.Resources.IngressExpected) {
						t.Fatalf("later definite failure erased an earlier uncertain ownership proof: publication=%+v resources=%+v", failed, retained.Resources)
					}
					if action == "retry" {
						if _, err := f.manager.Retry(context.Background(), value.ID); err != nil {
							t.Fatal(err)
						}
						waitState(t, f.manager, value.ID, "published")
						f.cloud.mu.Lock()
						if f.cloud.dnsCreates != 1 || f.cloud.ingressWrites != 1 {
							t.Errorf("recovery replayed an already applied write: DNS creates=%d ingress writes=%d", f.cloud.dnsCreates, f.cloud.ingressWrites)
						}
						f.cloud.mu.Unlock()
					}
					if _, err := f.manager.Unpublish(context.Background(), value.ID); err != nil {
						t.Fatal(err)
					}
					waitState(t, f.manager, value.ID, "removed")
					f.cloud.mu.Lock()
					defer f.cloud.mu.Unlock()
					actualConfig, _ := json.Marshal(f.cloud.configuration)
					wantIngressWrites := 2
					if phase == "dns" && action == "unpublish" {
						wantIngressWrites = 0
					}
					if len(f.cloud.dns) != 0 || f.cloud.dnsCreates != 1 || f.cloud.ingressWrites != wantIngressWrites || string(actualConfig) != string(originalConfig) {
						t.Fatalf("uncertain cleanup leaked owned resources or changed foreign configuration: DNS=%+v ingress writes=%d before=%s after=%s", f.cloud.dns, f.cloud.ingressWrites, originalConfig, actualConfig)
					}
				})
			}
		}
	}
}
