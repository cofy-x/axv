package publish

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/access"
	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
)

func (m *Manager) stillWanted(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.state.Records[id]
	return ok && !item.Remove && m.workAllowedLocked(id)
}

func (m *Manager) fail(id, state, phase, message string) {
	m.failRuntime(nil, id, state, phase, message)
}

// expected confines an asynchronous health failure to the runtime it checked.
// Detachment and failure publication share the journal lock, so Retry can only
// observe the failure after the old route has been fenced off.
func (m *Manager) failRuntime(expected *runtimeEntry, id, state, phase, message string) {
	m.mu.Lock()
	if m.ctx.Err() != nil || expected != nil && m.live[expected.hostname] != expected {
		m.mu.Unlock()
		return
	}
	entry := m.detachLocked(id)
	next := m.copyLocked()
	item, exists := next.Records[id]
	if exists {
		if item.Remove {
			item.Publication.State = "error"
			item.Publication.CleanupPending = true
		} else {
			item.Publication.State = state
		}
		item.Publication.Phase = phase
		item.Publication.Error = message
		item.Publication.UpdatedAt = m.now()
		next.Records[id] = item
		if err := m.saveLocked(next); err != nil {
			m.runtimeErrors[id] = "Could not persist publication state. Original receipts and cloud ownership were retained; repair storage and restart AXV before retrying."
		} else {
			delete(m.runtimeErrors, id)
		}
	}
	m.mu.Unlock()
	m.finishRetirement(entry)
}

func sameResources(s Settings, r resources) bool {
	return s.AccountID == r.AccountID && s.ZoneID == r.ZoneID && s.TunnelID == r.TunnelID && s.Domain == r.Domain && s.Listen == r.Listen && s.Issuer == r.Issuer && s.Audience == r.Audience && s.OwnerSubject == r.OwnerSubject
}

func (m *Manager) reconcile(id string) {
	item, ok := m.record(id)
	if !ok || !m.workAllowed(id) {
		return
	}
	if item.Remove {
		m.cleanup(id, item)
		return
	}
	if !m.stillWanted(id) {
		return
	}
	settings, err := m.options.Settings()
	if err != nil || !sameResources(settings, item.Resources) || validateSettings(settings, item.Input) != nil || m.ValidateDesktopOrigin(settings.DesktopOrigin) != nil {
		m.fail(id, "error", "configuration", "The original publishing configuration is unavailable or changed. Restore it before retrying, or unpublish this original record.")
		return
	}
	if err = m.ensureSurface(settings.Listen); err != nil {
		m.fail(id, "error", "origin", "The isolated publishing origin could not listen. Check its local address and retry.")
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 25*time.Second)
	target, err := m.options.Resolve(ctx, item.Route, item.Input.Binding, item.Input.Port)
	if !m.stillWanted(id) {
		cancel()
		if target.Close != nil {
			target.Close()
		}
		return
	}
	if err == nil && (target.Handler == nil || target.Validate == nil || target.Close == nil) {
		err = problem(500, "publication_target_invalid", "The captured forwarding target is incomplete.")
	}
	if err == nil {
		err = target.Validate(ctx)
	}
	if !m.stillWanted(id) {
		cancel()
		if target.Close != nil {
			target.Close()
		}
		return
	}
	if err == nil {
		probeContext, stop := context.WithTimeout(ctx, 5*time.Second)
		request, _ := http.NewRequestWithContext(probeContext, http.MethodHead, "http://published-service/", nil)
		probe := &probeResponse{header: http.Header{}, status: 200}
		target.Handler.ServeHTTP(probe, request)
		if probe.status >= 500 || probeContext.Err() != nil {
			err = problem(503, "publication_service_unavailable", "The original guest service is not responding.")
		}
		stop()
	}
	cancel()
	if err != nil {
		if target.Close != nil {
			target.Close()
		}
		m.fail(id, "unavailable", "verify-target", "The service in its original project, VM, volume, boot or guest environment is unavailable. Retry checks that original binding; it never switches targets.")
		return
	}
	entryContext, entryCancel := context.WithCancel(m.ctx)
	entry := &runtimeEntry{manager: m, id: id, hostname: item.Publication.Hostname, generation: item.Publication.Generation, ctx: entryContext, cancel: entryCancel, target: target, settings: settings, visibility: item.Input.Visibility, connections: map[net.Conn]int{}}
	activated := false
	defer func() {
		if !activated {
			entry.close()
		}
	}()
	if item.Input.Visibility == "owner" && m.options.Authorize == nil {
		entry.verifier, err = access.New(entryContext, access.Options{Issuer: settings.Issuer, Audience: settings.Audience, OwnerSubject: settings.OwnerSubject})
		if err != nil {
			m.fail(id, "error", "access", "The publishing Access identity is invalid. Check its explicit issuer, audience and owner locally.")
			return
		}
	}
	client, err := m.options.Client(settings)
	if err != nil {
		m.fail(id, "error", "configuration", "The Cloudflare API client could not be configured.")
		return
	}
	defer client.CloseIdleConnections()
	if !m.stillWanted(id) {
		return
	}
	m.cloudOp.Lock()
	defer m.cloudOp.Unlock()
	if !m.stillWanted(id) {
		return
	}
	freshDNS := item.Resources.DNS == nil
	if err = m.update(id, func(r *record) {
		r.Publication.Phase = "dns"
		if r.Resources.DNS == nil {
			r.Resources.DNS = &cfclient.CNAMEOwnership{ZoneID: r.Resources.ZoneID, Hostname: r.Publication.Hostname, Target: strings.ToLower(r.Resources.TunnelID) + ".cfargotunnel.com", OwnerID: id}
		}
	}); err != nil {
		return
	}
	item, _ = m.record(id)
	previousDNS := item.Resources.DNS
	if previousDNS.RecordID == "" {
		previousDNS = nil
	}
	cloudContext, stop := context.WithTimeout(m.ctx, 30*time.Second)
	dns, err := client.EnsureCNAME(cloudContext, cfclient.CNAMERequest{ZoneID: item.Resources.ZoneID, Hostname: item.Publication.Hostname, TunnelID: item.Resources.TunnelID, OwnerID: id, Existing: previousDNS})
	stop()
	if err != nil {
		if freshDNS && !cfclient.IsUncertain(err) {
			_ = m.update(id, func(r *record) { r.Resources.DNS = nil })
		}
		m.fail(id, "error", "dns", "Cloudflare DNS could not be reconciled. Existing foreign records were not adopted; inspect local setup and retry this publication.")
		return
	}
	if err = m.update(id, func(r *record) { r.Resources.DNS = &dns; r.Publication.Phase = "ingress" }); err != nil {
		return
	}
	if !m.stillWanted(id) {
		return
	}
	item, _ = m.record(id)
	request := cfclient.IngressRequest{AccountID: item.Resources.AccountID, TunnelID: item.Resources.TunnelID, Hostname: item.Publication.Hostname, Service: "http://" + item.Resources.Listen, Existing: item.Resources.Ingress}
	freshIngress := item.Resources.Ingress == nil
	if freshIngress {
		cloudContext, stop = context.WithTimeout(m.ctx, 30*time.Second)
		configuration, readErr := client.GetTunnelConfig(cloudContext, item.Resources.AccountID, item.Resources.TunnelID)
		stop()
		if readErr != nil || configuration.CheckHostnameAvailable(item.Publication.Hostname) != nil {
			m.fail(id, "error", "ingress", "The tunnel configuration is unavailable or the hostname belongs to another route. No foreign ingress was adopted.")
			return
		}
		request.ExpectedFingerprint = configuration.Fingerprint
		owned, prepareErr := cfclient.PrepareIngress(request)
		if prepareErr != nil {
			m.fail(id, "error", "ingress", "The isolated tunnel origin could not be prepared.")
			return
		}
		// Commit intent before the provider mutation. If its response is lost,
		// only this exact prepared rule can be recovered or removed later.
		if err = m.update(id, func(r *record) {
			r.Resources.Ingress = &owned
			r.Resources.IngressExpected = request.ExpectedFingerprint
		}); err != nil {
			return
		}
		request.Existing = &owned
	}
	if !m.stillWanted(id) {
		return
	}
	cloudContext, stop = context.WithTimeout(m.ctx, 30*time.Second)
	ingress, err := client.EnsureIngress(cloudContext, request)
	stop()
	if err != nil {
		if freshIngress && !cfclient.IsUncertain(err) {
			_ = m.update(id, func(r *record) { r.Resources.Ingress = nil; r.Resources.IngressExpected = "" })
		}
		m.fail(id, "error", "ingress", "Cloudflare ingress could not be reconciled. Existing foreign routes were not adopted; inspect local setup and retry this publication.")
		return
	}
	if err = m.update(id, func(r *record) { r.Resources.Ingress = &ingress; r.Resources.IngressExpected = "" }); err != nil {
		return
	}
	m.mu.Lock()
	current := m.state.Records[id]
	if !m.workAllowedLocked(id) || current.Remove || m.retiring[id] != nil || m.live[current.Publication.Hostname] != nil {
		m.mu.Unlock()
		return
	}
	next := m.copyLocked()
	current.Publication.State = "published"
	current.Publication.Phase = "routed"
	current.Publication.Error = ""
	current.Publication.CleanupPending = false
	current.Publication.UpdatedAt = m.now()
	next.Records[id] = current
	if err = m.saveLocked(next); err == nil {
		m.live[current.Publication.Hostname] = entry
		activated = true
	} else {
		m.runtimeErrors[id] = "Cloud resources were retained, but final publication state could not be saved. Retry or unpublish the original record."
	}
	m.mu.Unlock()
}

func (m *Manager) cleanup(id string, item record) {
	m.retire(id)
	if item.Resources.Ingress != nil || item.Resources.DNS != nil {
		settings, err := m.options.Settings()
		if err != nil || settings.APIToken == "" {
			m.fail(id, "error", "cleanup", "An API credential is required to remove this publication's original cloud resources.")
			return
		}
		client, err := m.options.Client(settings)
		if err != nil {
			m.fail(id, "error", "cleanup", "The Cloudflare client is unavailable; cleanup intent was retained.")
			return
		}
		defer client.CloseIdleConnections()
		if item.Resources.Ingress != nil {
			m.cloudOp.Lock()
			defer m.cloudOp.Unlock()
			if err = m.update(id, func(r *record) { r.Publication.Phase = "remove-ingress" }); err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			if !m.workAllowed(id) {
				cancel()
				return
			}
			err = client.RemoveIngress(ctx, *item.Resources.Ingress)
			cancel()
			if err != nil {
				m.fail(id, "error", "remove-ingress", "The original ingress could not be removed safely. Its ownership and cleanup intent were retained for retry.")
				return
			}
			if err = m.update(id, func(r *record) { r.Resources.Ingress = nil; r.Resources.IngressExpected = "" }); err != nil {
				return
			}
		}
		if item.Resources.DNS != nil {
			if err = m.update(id, func(r *record) { r.Publication.Phase = "remove-dns" }); err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			if !m.workAllowed(id) {
				cancel()
				return
			}
			err = client.DeleteCNAME(ctx, *item.Resources.DNS)
			cancel()
			if err != nil {
				m.fail(id, "error", "remove-dns", "The original DNS record could not be removed safely. Its ownership and cleanup intent were retained for retry.")
				return
			}
			if err = m.update(id, func(r *record) { r.Resources.DNS = nil }); err != nil {
				return
			}
		}
	}
	_ = m.update(id, func(r *record) {
		r.Publication.State = "removed"
		r.Publication.Phase = "removed"
		r.Publication.Error = ""
		r.Publication.CleanupPending = false
	})
}

type probeResponse struct {
	header http.Header
	status int
}

func (p *probeResponse) Header() http.Header { return p.header }
func (p *probeResponse) WriteHeader(status int) {
	if status >= 200 {
		p.status = status
	}
}

func (p *probeResponse) Write(data []byte) (int, error) { return len(data), nil }

func (m *Manager) monitor() {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
		m.checkBindings()
	}
}

func (m *Manager) checkBindings() {
	m.mu.Lock()
	entries := make([]*runtimeEntry, 0, len(m.live))
	for _, entry := range m.live {
		if !entry.closed && entry.ctx.Err() == nil {
			entry.wg.Add(1)
			entries = append(entries, entry)
		}
	}
	m.mu.Unlock()
	var checks sync.WaitGroup
	for _, entry := range entries {
		checks.Add(1)
		go func(e *runtimeEntry) {
			defer checks.Done()
			ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
			err := e.target.Validate(ctx)
			cancel()
			// Retirement waits for all checked requests. Release this check's
			// lease before a failed check joins that same retirement.
			e.wg.Done()
			if err != nil {
				m.failRuntime(e, e.id, "unavailable", "binding", "The original published project environment is no longer available. Open connections were revoked; explicitly retry the original record or unpublish it.")
			}
		}(entry)
	}
	checks.Wait()
}
