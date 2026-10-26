package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

type CNAMEOwnership struct {
	ZoneID   string `json:"zoneId"`
	RecordID string `json:"recordId"`
	Hostname string `json:"hostname"`
	Target   string `json:"target"`
	OwnerID  string `json:"ownerId"`
}

type CNAMERequest struct {
	ZoneID, Hostname, TunnelID, OwnerID string
	Existing                            *CNAMEOwnership
}

func canonicalDNS(value string) string { return strings.TrimSuffix(strings.ToLower(value), ".") }

func ownedDNS(record DNSRecord, ownership CNAMEOwnership) bool {
	return record.ID == ownership.RecordID && record.Type == "CNAME" && canonicalDNS(record.Name) == ownership.Hostname &&
		canonicalDNS(record.Content) == ownership.Target && record.Proxied && record.Comment == "AXV "+ownership.OwnerID
}

func (c *Client) ListDNSRecords(ctx context.Context, zoneID, hostname string) ([]DNSRecord, error) {
	if !validID(zoneID) || !validHostname(hostname) {
		return nil, invalidInput()
	}
	return listPages[DNSRecord](ctx, c, "/zones/"+zoneID+"/dns_records", url.Values{"name.exact": {hostname}})
}

func (c *Client) getDNSRecord(ctx context.Context, zoneID, recordID string) (DNSRecord, error) {
	var result DNSRecord
	_, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records/"+recordID, nil, &result)
	return result, err
}

// EnsureCNAME only creates a missing name or updates a proven owned record.
// A persisted random OwnerID comment also recovers a lost create response. The
// caller must save OwnerID before its first request, never derive it from a host.
func (c *Client) EnsureCNAME(ctx context.Context, request CNAMERequest) (CNAMEOwnership, error) {
	if !validID(request.ZoneID) || !validID(request.TunnelID) || !validID(request.OwnerID) || !validHostname(request.Hostname) {
		return CNAMEOwnership{}, invalidInput()
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	want := CNAMEOwnership{ZoneID: request.ZoneID, Hostname: request.Hostname,
		Target: strings.ToLower(request.TunnelID) + ".cfargotunnel.com", OwnerID: request.OwnerID}
	if old := request.Existing; old != nil && (old.ZoneID != want.ZoneID || old.Hostname != want.Hostname || old.OwnerID != want.OwnerID || !validID(old.RecordID) || !validHostname(old.Target)) {
		return CNAMEOwnership{}, invalidInput()
	}
	zone, err := c.GetZone(ctx, request.ZoneID)
	if err != nil {
		return CNAMEOwnership{}, err
	}
	if want.Hostname != zone.Name && !strings.HasSuffix(want.Hostname, "."+zone.Name) {
		return CNAMEOwnership{}, invalidInput()
	}
	records, err := c.ListDNSRecords(ctx, request.ZoneID, request.Hostname)
	if err != nil {
		return CNAMEOwnership{}, err
	}
	if len(records) > 1 {
		return CNAMEOwnership{}, conflict("DNS", "multiple records already use this hostname")
	}
	if len(records) == 1 {
		record := records[0]
		proof := want
		proof.RecordID = record.ID
		if request.Existing != nil {
			proof = *request.Existing
		}
		desiredProof := want
		desiredProof.RecordID = record.ID
		// A PATCH response can be lost after replacing the old owned target.
		// Recover only the same recorded ID with the exact new owned content.
		recoveredUpdate := request.Existing != nil && record.ID == request.Existing.RecordID && ownedDNS(record, desiredProof)
		if !validID(record.ID) || !ownedDNS(record, proof) && !recoveredUpdate {
			return CNAMEOwnership{}, conflict("DNS", "the hostname belongs to another record or its ownership changed")
		}
		want.RecordID = record.ID
		if canonicalDNS(record.Content) != want.Target {
			// PATCH leaves provider fields and custom metadata outside our owned
			// content untouched. Cloudflare has no conditional DNS write here.
			latest, readErr := c.getDNSRecord(ctx, proof.ZoneID, proof.RecordID)
			if readErr != nil {
				return CNAMEOwnership{}, readErr
			}
			if !ownedDNS(latest, proof) {
				return CNAMEOwnership{}, conflict("DNS", "the record changed while the update was being prepared")
			}
			_, err = c.do(ctx, http.MethodPatch, "/zones/"+want.ZoneID+"/dns_records/"+want.RecordID,
				map[string]any{"content": want.Target}, nil)
			if err != nil {
				return CNAMEOwnership{}, err
			}
		}
	} else {
		var created DNSRecord
		_, err = c.do(ctx, http.MethodPost, "/zones/"+want.ZoneID+"/dns_records", map[string]any{
			"type": "CNAME", "name": want.Hostname, "content": want.Target, "proxied": true, "ttl": 1, "comment": "AXV " + want.OwnerID,
		}, &created)
		if err != nil {
			return CNAMEOwnership{}, err
		}
		want.RecordID = created.ID
		if !validID(created.ID) || !ownedDNS(created, want) {
			return CNAMEOwnership{}, &Error{Kind: "invalid_response", Uncertain: true}
		}
	}
	actual, err := c.getDNSRecord(ctx, want.ZoneID, want.RecordID)
	if err != nil {
		return CNAMEOwnership{}, uncertain(err)
	}
	if !ownedDNS(actual, want) {
		return CNAMEOwnership{}, uncertain(conflict("DNS", "the record changed while publication was being verified"))
	}
	return want, nil
}

// DeleteCNAME verifies the complete proof before deleting. A missing RecordID
// is a prepared create whose response may have been lost; only an exact random
// owner comment, target, type, name and proxy match may recover that ID.
func (c *Client) DeleteCNAME(ctx context.Context, ownership CNAMEOwnership) error {
	if !validID(ownership.ZoneID) || ownership.RecordID != "" && !validID(ownership.RecordID) || !validID(ownership.OwnerID) || !validHostname(ownership.Hostname) || !validHostname(ownership.Target) {
		return invalidInput()
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if ownership.RecordID == "" {
		records, err := c.ListDNSRecords(ctx, ownership.ZoneID, ownership.Hostname)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		if len(records) != 1 || !validID(records[0].ID) {
			return conflict("DNS", "the prepared hostname has ambiguous ownership")
		}
		ownership.RecordID = records[0].ID
		if !ownedDNS(records[0], ownership) {
			return conflict("DNS", "the prepared hostname belongs to another record")
		}
	}
	actual, err := c.getDNSRecord(ctx, ownership.ZoneID, ownership.RecordID)
	var failure *Error
	if errors.As(err, &failure) && failure.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownedDNS(actual, ownership) {
		return conflict("DNS", "the recorded resource no longer matches its ownership proof")
	}
	_, err = c.do(ctx, http.MethodDelete, "/zones/"+ownership.ZoneID+"/dns_records/"+ownership.RecordID, nil, nil)
	if errors.As(err, &failure) && failure.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = c.getDNSRecord(ctx, ownership.ZoneID, ownership.RecordID)
	if errors.As(err, &failure) && failure.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return uncertain(err)
	}
	return uncertain(conflict("DNS", "the record still exists after removal"))
}
