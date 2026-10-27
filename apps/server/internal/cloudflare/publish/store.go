package publish

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const maxJournalBytes = 16 << 20

func (m *Manager) load() error {
	info, err := os.Lstat(m.path)
	if os.IsNotExist(err) {
		if _, lockErr := os.Lstat(m.path + ".lock"); lockErr == nil {
			return errors.New("Cloudflare publication journal is missing but its ownership marker remains; preserve and repair it explicitly")
		} else if !os.IsNotExist(lockErr) {
			return lockErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Cloudflare publication journal must be a regular owned file")
	}
	// Acquire the lifetime lock before reading. A parsed snapshot must never be
	// allowed to replace a newer writer's committed resource ownership.
	if err := m.lockJournal(); err != nil {
		return err
	}
	file, err := os.Open(m.path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxJournalBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return errors.New("Cloudflare publication journal exceeds its bounded capacity")
	}
	if !jsonunicode.Valid(data) {
		return errors.New("Cloudflare publication journal contains invalid Unicode JSON; preserve and repair it explicitly")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state journal
	if err := decoder.Decode(&state); err != nil || decoder.Decode(new(any)) != io.EOF || state.Version != 2 || state.InstanceID == "" || state.Records == nil {
		return errors.New("invalid Cloudflare publication journal; preserve it and repair it explicitly")
	}
	if len(state.Records) > 4096 {
		return errors.New("Cloudflare publication receipt capacity exceeded")
	}
	requests, hosts, sites := map[string]bool{}, map[string]bool{}, map[string]bool{}
	active := 0
	for id, item := range state.Records {
		if id == "" || item.Publication.ID != id || item.Publication.RequestID != item.Input.RequestID || item.Input.InstanceID != state.InstanceID || validateRegistration(item.Input) != nil || item.Route.Socket == "" || item.Route.ConfigFingerprint == "" || !filepath.IsAbs(item.Route.Socket) || requests[item.Input.RequestID] {
			return errors.New("invalid original publication identity in journal")
		}
		hostname, err := allocatedHostname(item.Input.Name, item.Publication.Generation, item.Publication.Domain)
		site, siteErr := policy.CookieSite(hostname)
		if err != nil || siteErr != nil || hostname != item.Publication.Hostname || item.Publication.Name != item.Input.Name || item.Publication.Domain != item.Input.Domain || item.CookieSite != site || hosts[hostname] || sites[site] || item.Publication.Visibility != item.Input.Visibility || item.Publication.Port != item.Input.Port || item.Publication.Binding != item.Input.Binding {
			return errors.New("publication journal changed its original request binding")
		}
		r := item.Resources
		if r.AccountID == "" || r.ZoneID == "" || r.TunnelID == "" || r.Domain != item.Publication.Domain || r.Listen == "" {
			return errors.New("invalid publication resource scope in journal")
		}
		if dns := r.DNS; dns != nil && (dns.ZoneID != r.ZoneID || dns.OwnerID != id || dns.Hostname != hostname || dns.Target != strings.ToLower(r.TunnelID)+".cfargotunnel.com") {
			return errors.New("publication DNS ownership does not match its original identity")
		}
		if ingress := r.Ingress; ingress != nil {
			expected, err := cfclient.PrepareIngress(cfclient.IngressRequest{AccountID: r.AccountID, TunnelID: r.TunnelID, Hostname: hostname, Service: "http://" + r.Listen})
			if err != nil || *ingress != expected {
				return errors.New("publication ingress ownership does not match its original identity")
			}
		}
		if item.Publication.State == "removed" && (!item.Remove || r.DNS != nil || r.Ingress != nil) || item.Publication.State == "published" && (item.Remove || r.DNS == nil || r.DNS.RecordID == "" || r.Ingress == nil) {
			return errors.New("publication lifecycle and resource ownership disagree")
		}
		switch item.Publication.State {
		case "provisioning", "published", "unavailable", "removing", "removed", "error":
		default:
			return errors.New("unknown Cloudflare publication state")
		}
		if item.Publication.State != "removed" {
			active++
		}
		hosts[hostname], sites[site] = true, true
		requests[item.Input.RequestID] = true
	}
	if active > 64 {
		return errors.New("Cloudflare active publication capacity exceeded")
	}
	m.state = state
	return nil
}

func (m *Manager) lockJournal() error {
	if m.lock != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	lock, err := filelock.TryLock(m.path + ".lock")
	if err != nil {
		return err
	}
	m.lock = lock
	return nil
}

// saveLocked publishes one complete transaction. It never overwrites malformed
// state or evicts receipts to make room. Callers keep old memory on failure.
func (m *Manager) saveLocked(next journal) error {
	if m.storageErr != nil {
		return m.storageErr
	}
	alreadyLocked := m.lock != nil
	if err := m.lockJournal(); err != nil {
		return err
	}
	if !alreadyLocked {
		if _, err := os.Lstat(m.path); err == nil {
			m.storageErr = problem(409, "publication_instance_changed", "A publication journal was created by another manager. Restart AXV to inspect its original receipts before publishing.")
			return m.storageErr
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return errors.New("Cloudflare publication journal is full; existing receipts were preserved")
	}
	if err := atomicfile.Write(m.path, append(data, '\n'), 0600); err != nil {
		m.storageErr = err
		return fmt.Errorf("save publication journal: %w", err)
	}
	m.state = next
	return nil
}

func (m *Manager) update(id string, change func(*record)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.copyLocked()
	item, ok := next.Records[id]
	if !ok {
		return problem(404, "publication_not_found", "The original publication was not found.")
	}
	change(&item)
	item.Publication.UpdatedAt = m.now()
	next.Records[id] = item
	if err := m.saveLocked(next); err != nil {
		m.runtimeErrors[id] = "Could not persist publication state. Original receipts and cloud ownership were retained; repair storage and restart AXV before retrying."
		return problem(500, "publication_save_failed", "Could not save publication state. Existing identities were preserved; inspect its status before retrying.")
	}
	delete(m.runtimeErrors, id)
	return nil
}

func (m *Manager) copyLocked() journal {
	next := m.state
	next.Records = make(map[string]record, len(m.state.Records))
	for id, item := range m.state.Records {
		next.Records[id] = item
	}
	return next
}

func (m *Manager) record(id string) (record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.state.Records[id]
	return value, ok
}
