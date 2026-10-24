package cloudflareapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type providerFixture struct {
	records    []DNSRecord
	config     json.RawMessage
	remote     bool
	writes     int
	configGets int
	beforeGet  func(int)
	loseCreate bool
	losePatch  bool
	loseConfig bool
}

func (fixture *providerFixture) client(t *testing.T) *Client {
	t.Helper()
	return testClient(t, func(request *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(request.URL.Path, "/client/v4")
		switch {
		case path == "/zones/zone":
			return resultResponse(Zone{ID: "zone", Name: "example.com"}), nil
		case path == "/accounts/account/cfd_tunnel/tunnel":
			return resultResponse(Tunnel{ID: "tunnel", RemoteConfig: fixture.remote}), nil
		case path == "/zones/zone/dns_records" && request.Method == http.MethodGet:
			if request.URL.Query().Get("name.exact") != "axv.example.com" {
				t.Fatal("DNS lookup broadened outside the owned name")
			}
			return resultResponse(append([]DNSRecord{}, fixture.records...)), nil
		case path == "/zones/zone/dns_records" && request.Method == http.MethodPost:
			var created DNSRecord
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			created.ID = "record"
			fixture.records = append(fixture.records, created)
			fixture.writes++
			if fixture.loseCreate {
				return nil, errors.New("response lost test-secret-token")
			}
			return resultResponse(created), nil
		case path == "/zones/zone/dns_records/record":
			index := -1
			for i, record := range fixture.records {
				if record.ID == "record" {
					index = i
				}
			}
			if index < 0 {
				return rawResponse(404, `{"success":false}`), nil
			}
			switch request.Method {
			case http.MethodGet:
				return resultResponse(fixture.records[index]), nil
			case http.MethodPatch:
				var patch map[string]string
				if err := json.NewDecoder(request.Body).Decode(&patch); err != nil || len(patch) != 1 || patch["content"] == "" {
					t.Fatal("DNS patch changed unowned fields")
				}
				fixture.records[index].Content = patch["content"]
				fixture.writes++
				if fixture.losePatch {
					return nil, errors.New("patch response lost")
				}
				return resultResponse(fixture.records[index]), nil
			case http.MethodDelete:
				fixture.records = append(fixture.records[:index], fixture.records[index+1:]...)
				fixture.writes++
				// Cloudflare's current documented DELETE envelope has no success.
				return rawResponse(200, `{"result":{"id":"record"}}`), nil
			}
		case path == "/accounts/account/cfd_tunnel/tunnel/configurations":
			if request.Method == http.MethodGet {
				fixture.configGets++
				if fixture.beforeGet != nil {
					fixture.beforeGet(fixture.configGets)
				}
			} else if request.Method == http.MethodPut {
				var body struct {
					Config json.RawMessage `json:"config"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				fixture.config = body.Config
				fixture.writes++
				if fixture.loseConfig {
					return nil, errors.New("config write response lost")
				}
			} else {
				t.Fatalf("unexpected config method %s", request.Method)
			}
			return resultResponse(map[string]any{"config": fixture.config, "version": 4}), nil
		}
		t.Fatalf("unexpected provider operation %s %s", request.Method, path)
		return nil, nil
	})
}

func cnameRequest() CNAMERequest {
	return CNAMERequest{ZoneID: "zone", TunnelID: "tunnel", Hostname: "axv.example.com", OwnerID: "random-publication-owner"}
}

func TestCNAMECreateRecoverAndDelete(t *testing.T) {
	fixture := &providerFixture{loseCreate: true}
	client := fixture.client(t)
	request := cnameRequest()
	_, err := client.EnsureCNAME(context.Background(), request)
	failureKind(t, err, "unavailable", true)
	fixture.loseCreate = false
	ownership, err := client.EnsureCNAME(context.Background(), request)
	if err != nil || ownership.RecordID != "record" || fixture.writes != 1 {
		t.Fatalf("lost response recovery duplicated the record: ownership=%v writes=%d err=%v", ownership, fixture.writes, err)
	}
	ownership.RecordID = "" // A durable prepared intent can revoke a lost create.
	if err := client.DeleteCNAME(context.Background(), ownership); err != nil || len(fixture.records) != 0 || fixture.writes != 2 {
		t.Fatalf("prepared ownership did not revoke exact resource: %v", err)
	}
	if err := client.DeleteCNAME(context.Background(), ownership); err != nil || fixture.writes != 2 {
		t.Fatalf("missing owned record was not idempotent: %v", err)
	}
}

func TestCNAMERefusesForeignAndModifiedProofs(t *testing.T) {
	for _, change := range []func(*DNSRecord){
		func(record *DNSRecord) { record.Comment = "another-owner" },
		func(record *DNSRecord) { record.Content = "foreign.cfargotunnel.com" },
		func(record *DNSRecord) { record.Proxied = false },
		func(record *DNSRecord) { record.Type = "A" },
		func(record *DNSRecord) { record.Name = "other.example.com" },
	} {
		record := DNSRecord{ID: "record", Type: "CNAME", Name: "axv.example.com", Content: "tunnel.cfargotunnel.com", Proxied: true, Comment: "AXV random-publication-owner"}
		change(&record)
		fixture := &providerFixture{records: []DNSRecord{record}}
		client := fixture.client(t)
		if _, err := client.EnsureCNAME(context.Background(), cnameRequest()); !errors.Is(err, ErrConflict) {
			t.Fatalf("foreign record adopted: %v", err)
		}
		proof := CNAMEOwnership{ZoneID: "zone", RecordID: "record", Hostname: "axv.example.com", Target: "tunnel.cfargotunnel.com", OwnerID: "random-publication-owner"}
		if err := client.DeleteCNAME(context.Background(), proof); !errors.Is(err, ErrConflict) || fixture.writes != 0 {
			t.Fatalf("foreign record deleted: %v", err)
		}
	}
}

func TestCNAMEUpdatesOnlyWithOldOwnership(t *testing.T) {
	old := DNSRecord{ID: "record", Type: "CNAME", Name: "axv.example.com", Content: "old.cfargotunnel.com", Proxied: true, Comment: "AXV random-publication-owner"}
	fixture := &providerFixture{records: []DNSRecord{old}}
	client := fixture.client(t)
	request := cnameRequest()
	request.Existing = &CNAMEOwnership{ZoneID: "zone", RecordID: "record", Hostname: old.Name, Target: old.Content, OwnerID: request.OwnerID}
	ownership, err := client.EnsureCNAME(context.Background(), request)
	if err != nil || ownership.Target != "tunnel.cfargotunnel.com" || fixture.writes != 1 || fixture.records[0].Comment != old.Comment {
		t.Fatalf("owned target update failed: %v", err)
	}
	request.Hostname = "axv.foreign.com"
	request.Existing = nil
	if _, err := client.EnsureCNAME(context.Background(), request); err == nil || fixture.writes != 1 {
		t.Fatal("zone boundary ignored")
	}
}

func TestCNAMERecoversLostOwnedUpdateResponse(t *testing.T) {
	fixture := &providerFixture{losePatch: true, records: []DNSRecord{{ID: "record", Type: "CNAME", Name: "axv.example.com", Content: "old.cfargotunnel.com", Proxied: true, Comment: "AXV random-publication-owner"}}}
	client := fixture.client(t)
	request := cnameRequest()
	request.Existing = &CNAMEOwnership{ZoneID: "zone", RecordID: "record", Hostname: request.Hostname, Target: "old.cfargotunnel.com", OwnerID: request.OwnerID}
	_, err := client.EnsureCNAME(context.Background(), request)
	failureKind(t, err, "unavailable", true)
	fixture.losePatch = false
	ownership, err := client.EnsureCNAME(context.Background(), request)
	if err != nil || fixture.writes != 1 || ownership.Target != "tunnel.cfargotunnel.com" {
		t.Fatalf("same owned record could not recover its lost update response: %v", err)
	}
}

const initialConfig = `{"originRequest":{"connectTimeout":21,"httpHostHeader":"foreign.example.com"},"future":{"preserve":9007199254740993},"ingress":[{"hostname":"other.example.com","path":"^/foo","service":"http://localhost:9000","originRequest":{"noTLSVerify":true},"opaque":{"keep":true}},{"service":"http_status:404"}]}`

func ingressRequest() IngressRequest {
	return IngressRequest{AccountID: "account", TunnelID: "tunnel", Hostname: "axv.example.com", Service: "http://127.0.0.1:4321"}
}

func decodeConfig(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func equalJSON(left, right json.RawMessage) bool {
	l, _ := fingerprint(left)
	r, _ := fingerprint(right)
	return l == r
}

func TestIngressPreservesForeignConfigurationAndRevokesOnlyOwnedRule(t *testing.T) {
	fixture := &providerFixture{remote: true, config: json.RawMessage(initialConfig)}
	client := fixture.client(t)
	snapshot, err := client.GetTunnelConfig(context.Background(), "account", "tunnel")
	if err != nil || snapshot.CheckHostnameAvailable("axv.example.com") != nil || !snapshot.HasHostname("other.example.com") {
		t.Fatalf("hostname availability invalid: %v", err)
	}
	request := ingressRequest()
	request.ExpectedFingerprint = snapshot.Fingerprint
	prepared, err := PrepareIngress(request)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := client.EnsureIngress(context.Background(), request)
	if err != nil || !reflect.DeepEqual(ownership, prepared) || fixture.writes != 1 {
		t.Fatalf("publication failed: ownership=%v err=%v", ownership, err)
	}
	before, after := decodeConfig(t, json.RawMessage(initialConfig)), decodeConfig(t, fixture.config)
	if !equalJSON(before["future"], after["future"]) || !equalJSON(before["originRequest"], after["originRequest"]) {
		t.Fatal("unowned tunnel configuration changed")
	}
	parsed, err := parseConfig(fixture.config)
	if err != nil || len(parsed.raw) != 3 || parsed.rules[1].Hostname != request.Hostname || !strings.Contains(string(parsed.raw[1]), `"httpHostHeader":"axv.example.com"`) {
		t.Fatalf("owned rule does not preserve public Host: %v", err)
	}
	initialParsed, _ := parseConfig(json.RawMessage(initialConfig))
	if !equalJSON(parsed.raw[0], initialParsed.raw[0]) || !equalJSON(parsed.raw[2], initialParsed.raw[1]) {
		t.Fatal("foreign ingress rules changed")
	}
	request.Existing = &prepared
	request.ExpectedFingerprint = "" // Lost write response recovery uses durable intent.
	if _, err := client.EnsureIngress(context.Background(), request); err != nil || fixture.writes != 1 {
		t.Fatalf("exact prepared rule could not be recovered without rewriting: %v", err)
	}
	if err := client.RemoveIngress(context.Background(), ownership); err != nil || fixture.writes != 2 || !equalJSON(fixture.config, json.RawMessage(initialConfig)) {
		t.Fatalf("unpublish changed foreign resources: %v", err)
	}
	if err := client.RemoveIngress(context.Background(), ownership); err != nil || fixture.writes != 2 {
		t.Fatalf("missing owned ingress is not idempotent: %v", err)
	}
}

func TestIngressRefusesSameHostnameWildcardAndModifiedOwnedRule(t *testing.T) {
	request := ingressRequest()
	rule, _ := desiredRule(request)
	prepared, _ := PrepareIngress(request)
	for _, config := range []string{
		`{"ingress":[` + string(rule) + `,{"service":"http_status:404"}]}`,
		`{"ingress":[{"hostname":"*.example.com","service":"http://localhost:99"},{"service":"http_status:404"}]}`,
		`{"ingress":[{"hostname":"axv.example.com","path":"/different","service":"http://127.0.0.1:4321"},{"service":"http_status:404"}]}`,
	} {
		fixture := &providerFixture{remote: true, config: json.RawMessage(config)}
		client := fixture.client(t)
		request.Existing = nil
		if _, err := client.EnsureIngress(context.Background(), request); !errors.Is(err, ErrConflict) || fixture.writes != 0 {
			t.Fatalf("foreign rule was claimed: %v", err)
		}
	}
	fixture := &providerFixture{remote: true, config: json.RawMessage(`{"ingress":[{"hostname":"axv.example.com","service":"http://127.0.0.1:4321","originRequest":{"httpHostHeader":"foreign.example.com"}},{"service":"http_status:404"}]}`)}
	if err := fixture.client(t).RemoveIngress(context.Background(), prepared); !errors.Is(err, ErrConflict) || fixture.writes != 0 {
		t.Fatalf("modified owned rule was deleted: %v", err)
	}
}

func TestIngressDetectsConcurrentChangeAndLocallyManagedTunnel(t *testing.T) {
	fixture := &providerFixture{remote: true, config: json.RawMessage(initialConfig)}
	fixture.beforeGet = func(read int) {
		if read == 2 {
			fixture.config = json.RawMessage(`{"ingress":[{"hostname":"new.example.com","service":"http://localhost:45"},{"service":"http_status:404"}]}`)
		}
	}
	if _, err := fixture.client(t).EnsureIngress(context.Background(), ingressRequest()); !errors.Is(err, ErrConflict) || fixture.writes != 0 {
		t.Fatalf("concurrent foreign config overwritten: %v", err)
	}
	fixture = &providerFixture{config: json.RawMessage(initialConfig)}
	if _, err := fixture.client(t).EnsureIngress(context.Background(), ingressRequest()); !errors.Is(err, ErrConflict) || fixture.writes != 0 || fixture.configGets != 0 {
		t.Fatalf("locally managed tunnel overwritten: %v", err)
	}
}

func TestIngressValidatesServicesAndFailsClosedCatchall(t *testing.T) {
	for _, service := range []string{"http://user:secret@localhost:80", "file:///etc/passwd", "http://localhost:80/?secret=one", "http://localhost:80/path", "javascript:alert(1)"} {
		request := ingressRequest()
		request.Service = service
		if _, err := PrepareIngress(request); err == nil {
			t.Fatal("accepted unsafe origin service")
		}
	}
	fixture := &providerFixture{remote: true, config: json.RawMessage(`{}`)}
	if _, err := fixture.client(t).EnsureIngress(context.Background(), ingressRequest()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := parseConfig(fixture.config)
	if len(parsed.rules) != 2 || parsed.rules[1].Service != "http_status:404" {
		t.Fatal("empty config did not gain a fail-closed final catchall")
	}
}

func TestIngressRecoversLostOwnedServiceUpdate(t *testing.T) {
	fixture := &providerFixture{remote: true, config: json.RawMessage(initialConfig)}
	client := fixture.client(t)
	request := ingressRequest()
	ownership, err := client.EnsureIngress(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Service, request.Existing = "http://127.0.0.1:5432", &ownership
	fixture.loseConfig = true
	_, err = client.EnsureIngress(context.Background(), request)
	if !IsUncertain(err) || fixture.writes != 2 {
		t.Fatalf("lost configuration response was treated as definite: %v", err)
	}
	fixture.loseConfig = false
	updated, err := client.EnsureIngress(context.Background(), request)
	if err != nil || updated.Service != request.Service || fixture.writes != 2 {
		t.Fatalf("same owned service update was not recovered: %v", err)
	}
}

func TestIngressConflictReportsWhetherWriteMayHaveHappened(t *testing.T) {
	for _, read := range []int{2, 3} {
		fixture := &providerFixture{remote: true, config: json.RawMessage(initialConfig)}
		fixture.beforeGet = func(index int) {
			if index == read {
				fixture.config = json.RawMessage(`{"ingress":[{"hostname":"external.example.com","service":"http://localhost:40"},{"service":"http_status:404"}]}`)
			}
		}
		_, err := fixture.client(t).EnsureIngress(context.Background(), ingressRequest())
		if !errors.Is(err, ErrConflict) || IsUncertain(err) != (read == 3) || fixture.writes != read-2 {
			t.Fatalf("conflict before/after write lost certainty: read=%d writes=%d err=%v", read, fixture.writes, err)
		}
	}
}
