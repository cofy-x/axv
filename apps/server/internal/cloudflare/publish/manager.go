package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/cloudflare/access"
	cfclient "github.com/cofy-x/axv/apps/server/internal/cloudflare/client"
	"github.com/cofy-x/axv/apps/server/internal/cloudflare/policy"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type Manager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	options       Options
	mu            sync.Mutex
	op            sync.Mutex
	cloudOp       sync.Mutex
	state         journal
	path          string
	lock          *os.File
	closed        bool
	workers       map[string]bool
	withdrawn     map[string]bool
	runtimeErrors map[string]string
	storageErr    error
	closeErr      error
	live          map[string]*runtimeEntry
	retiring      map[string]*runtimeEntry
	surface       *surface
	wg            sync.WaitGroup
	now           func() time.Time
	isolationKey  [32]byte
	isolationUsed map[string]int64
}

type runtimeEntry struct {
	manager     *Manager
	id          string
	hostname    string
	generation  string
	ctx         context.Context
	cancel      context.CancelFunc
	target      Target
	verifier    *access.Verifier
	settings    Settings
	visibility  string
	closed      bool
	connections map[net.Conn]int
	wg          sync.WaitGroup
	once        sync.Once
	closeErr    error
}

func New(parent context.Context, options Options) (*Manager, error) {
	if parent == nil || options.Home == "" || options.Settings == nil {
		return nil, errors.New("Cloudflare publishing requires a lifetime, profile home and settings reader")
	}
	if options.Capture == nil {
		options.Capture = gatewayCapture(options.Gateway)
	}
	if options.Resolve == nil {
		options.Resolve = resolveCaptured
	}
	if options.Client == nil {
		options.Client = func(s Settings) (*cfclient.Client, error) {
			return cfclient.New(cfclient.Options{Token: s.APIToken})
		}
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{ctx: ctx, cancel: cancel, options: options, path: filepath.Join(options.Home, "cloudflare", "publications.json"), now: time.Now, workers: map[string]bool{}, withdrawn: map[string]bool{}, runtimeErrors: map[string]string{}, live: map[string]*runtimeEntry{}, retiring: map[string]*runtimeEntry{}, state: journal{Version: 2, InstanceID: "publications-" + rand.Text(), Records: map[string]record{}}}
	if _, err := rand.Read(m.isolationKey[:]); err != nil {
		cancel()
		return nil, errors.New("Cloudflare browser isolation could not initialize")
	}
	if err := m.load(); err != nil {
		cancel()
		if m.lock != nil {
			_ = m.lock.Close()
		}
		return nil, err
	}
	if len(m.state.Records) > 0 {
		settings, err := options.Settings()
		if err == nil {
			err = m.ValidateDesktopOrigin(settings.DesktopOrigin)
		}
		if err != nil {
			cancel()
			if m.lock != nil {
				_ = m.lock.Close()
			}
			return nil, err
		}
	}
	var cleanup []string
	var suspend []string
	for id, item := range m.state.Records {
		if item.Remove && item.Publication.State != "removed" {
			cleanup = append(cleanup, id)
		} else if item.Publication.State == "published" || item.Publication.State == "provisioning" {
			suspend = append(suspend, id)
		}
	}
	for _, id := range suspend {
		if err := m.update(id, func(r *record) {
			r.Publication.State = "unavailable"
			r.Publication.Phase = "restart-required"
			r.Publication.Error = "AXV restarted. Review and resume this original publication to reopen its exact target."
		}); err != nil {
			cancel()
			if m.lock != nil {
				_ = m.lock.Close()
			}
			return nil, err
		}
	}
	for _, id := range cleanup {
		m.schedule(id)
	}
	m.wg.Add(1)
	go m.monitor()
	return m, nil
}

func (m *Manager) InstanceID() string { m.mu.Lock(); defer m.mu.Unlock(); return m.state.InstanceID }
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storageErr != nil {
		return true
	}
	for _, item := range m.state.Records {
		if item.Publication.State != "removed" {
			return true
		}
	}
	return false
}

// Availability checks only local configuration and retained site ownership.
// Cleanup and exact receipt retries remain independent of accepting new sites.
func (m *Manager) Availability() (bool, string) {
	settings, err := m.options.Settings()
	if err != nil || validateManagement(settings) != nil {
		return false, "Complete Cloudflare account and cookie-isolation settings in Setup before publishing."
	}
	if m.ValidateDesktopOrigin(settings.DesktopOrigin) != nil {
		return false, "The desktop cookie site conflicts with a retained publication. Choose an independent desktop domain."
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.storageErr != nil {
		return false, "Publishing storage or shutdown needs attention before accepting another service."
	}
	mode, _ := policy.AllocationMode(settings.Domain)
	if mode == "dedicated-site" {
		for _, item := range m.state.Records {
			if item.CookieSite == settings.Domain {
				return false, "This dedicated domain is permanently assigned. Use a different domain for another publication."
			}
		}
	}
	return true, ""
}

// ValidateDesktopOrigin protects every retained publication site, including
// removed generations whose browsers may still hold cookies or service workers.
func (m *Manager) ValidateDesktopOrigin(origin string) error {
	if origin == "" {
		return nil
	}
	if !policy.ValidOrigin(origin) {
		return problem(400, "publication_desktop_origin", "Provide a canonical public HTTPS desktop origin.")
	}
	u, _ := url.Parse(origin)
	site, err := policy.CookieSite(u.Hostname())
	if err != nil {
		return problem(400, "publication_desktop_site", "The desktop origin needs a known independent cookie site.")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.state.Records {
		if item.CookieSite == site {
			return problem(409, "publication_desktop_site_conflict", "This cookie site belongs to a retained application publication. Choose a different desktop site.")
		}
	}
	return nil
}

func allocatedHostname(name, generation, domain string) (string, error) {
	if !policy.ValidPublicationName(name) || len(generation) != 32 || strings.ToLower(generation) != generation {
		return "", errors.New("invalid publication name or generation")
	}
	if _, err := hex.DecodeString(generation); err != nil {
		return "", errors.New("invalid publication generation")
	}
	if err := policy.ValidateAllocationDomain(domain); err != nil {
		return "", err
	}
	hostname := name + "-" + generation + "." + domain
	if !policy.ValidOrigin("https://" + hostname) {
		return "", errors.New("generated publication hostname exceeds DNS limits")
	}
	return hostname, nil
}

func projected(item record) Publication {
	value := item.Publication
	value.URL = ""
	if value.State == "published" {
		value.URL = "https://" + value.Hostname
	}
	return value
}

func (m *Manager) projectedLocked(item record) Publication {
	view := projected(item)
	if view.State == "published" {
		entry := m.live[view.Hostname]
		if m.closed || entry == nil || entry.closed || entry.ctx.Err() != nil {
			view.State = "unavailable"
			view.Phase = "origin-closed"
			view.Error = "The original publishing origin is closed. Explicitly retry the retained publication to check its original target."
			view.URL = ""
		}
	}
	if m.withdrawn[item.Publication.ID] && !item.Remove {
		view.State = "unavailable"
		view.Phase = "withdrawn"
		view.Error = "Access was withdrawn in this AXV process. Save the original removal before cloud cleanup."
		view.URL = ""
	}
	if message := m.runtimeErrors[item.Publication.ID]; message != "" {
		view.State = "error"
		view.Phase = "persistence"
		view.Error = message
		view.URL = ""
	}
	return view
}

func (m *Manager) List() []Publication {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Publication, 0, len(m.state.Records))
	for _, item := range m.state.Records {
		result = append(result, m.projectedLocked(item))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func validateRegistration(input Registration) error {
	if compute.ValidateRequestID(input.RequestID) != nil || input.InstanceID == "" || input.Port < 1024 || input.Port > 65535 || !policy.ValidPublicationName(input.Name) || policy.ValidateAllocationDomain(input.Domain) != nil || input.Visibility != "owner" && input.Visibility != "public" {
		return problem(400, "invalid_publication_request", "Provide an original request identity, a lowercase name of 1 to 24 letters, digits or internal hyphens without the reserved xn-- prefix, visibility and a guest TCP port from 1024 to 65535.")
	}
	b := input.Binding
	for _, value := range []string{b.HostID, b.ProjectID, b.VolumeID, b.MachineID, b.BootID, b.InstanceID} {
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
			return problem(400, "invalid_publication_request", "A complete bounded original project binding is required.")
		}
	}
	if b.ProjectRevision < 1 || b.VolumeRevision < 1 {
		return problem(400, "invalid_publication_request", "Positive original project and volume revisions are required.")
	}
	return nil
}

func validateManagement(s Settings) error {
	host, port, err := net.SplitHostPort(s.Listen)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if s.APIToken == "" || s.AccountID == "" || s.ZoneID == "" || s.TunnelID == "" || policy.ValidateAllocationDomain(s.Domain) != nil || policy.ValidateIsolation(s.DesktopOrigin, s.Domain) != nil || err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return problem(409, "publication_not_configured", "Complete Cloudflare management settings on the AXV computer before publishing.")
	}
	return nil
}

func validateSettings(s Settings, input Registration) error {
	if input.Domain != s.Domain {
		return problem(409, "publication_configuration_changed", "The reviewed publishing domain changed. Refresh Setup and review the original request before publishing.")
	}
	if err := validateManagement(s); err != nil {
		return err
	}
	if input.Visibility == "owner" {
		v, e := access.New(context.Background(), access.Options{Issuer: s.Issuer, Audience: s.Audience, OwnerSubject: s.OwnerSubject})
		if e != nil {
			return problem(409, "publication_access_not_configured", "Owner publishing requires the Access issuer, publishing application audience and explicit owner subject.")
		}
		v.Close()
	}
	return nil
}

func (m *Manager) Publish(ctx context.Context, input Registration) (Publication, error) {
	if input.Visibility == "" {
		input.Visibility = "owner"
	}
	if err := validateRegistration(input); err != nil {
		return Publication{}, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		return Publication{}, problem(503, "publication_stopping", "AXV publishing is stopping.")
	}
	if input.InstanceID != m.state.InstanceID {
		m.mu.Unlock()
		return Publication{}, problem(409, "publication_instance_changed", "The original publishing journal is unavailable. Inspect publications before releasing the original recovery request.")
	}
	for _, item := range m.state.Records {
		if item.Input.RequestID == input.RequestID {
			view := m.projectedLocked(item)
			m.mu.Unlock()
			if item.Input != input {
				return Publication{}, problem(409, "idempotency_conflict", "This request ID belongs to another publication payload.")
			}
			return view, nil
		}
	}
	m.mu.Unlock()
	captureContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	route, err := m.options.Capture(captureContext, input.Binding)
	cancel()
	if err != nil || route.Socket == "" || route.ConfigFingerprint == "" || !filepath.IsAbs(route.Socket) {
		return Publication{}, problem(409, "publication_binding_unavailable", "The reviewed project environment could not be captured. Inspect the original project before publishing.")
	}
	lock, err := profile.Lock(m.options.Home)
	if err != nil {
		return Publication{}, problem(409, "publication_config_busy", "The AXV profile is busy. Retry after configuration changes finish.")
	}
	defer lock.Close()
	settings, err := m.options.Settings()
	if err != nil {
		return Publication{}, problem(409, "publication_not_configured", "Cloudflare publishing settings are unavailable. Check them locally.")
	}
	if err = validateSettings(settings, input); err != nil {
		return Publication{}, err
	}
	if err = m.ValidateDesktopOrigin(settings.DesktopOrigin); err != nil {
		return Publication{}, err
	}
	var generationBytes [16]byte
	if _, err = rand.Read(generationBytes[:]); err != nil {
		return Publication{}, problem(500, "publication_generation_failed", "A unique publication hostname could not be allocated.")
	}
	generation := hex.EncodeToString(generationBytes[:])
	hostname, err := allocatedHostname(input.Name, generation, settings.Domain)
	if err != nil {
		return Publication{}, problem(400, "publication_hostname_invalid", "The configured domain cannot fit a unique publication hostname.")
	}
	site, err := policy.CookieSite(hostname)
	if err != nil {
		return Publication{}, problem(400, "publication_cookie_site", "The publication needs a known independent cookie site.")
	}
	if settings.DesktopOrigin != "" {
		desktopURL, _ := url.Parse(settings.DesktopOrigin)
		desktopSite, _ := policy.CookieSite(desktopURL.Hostname())
		if site == desktopSite {
			return Publication{}, problem(409, "publication_desktop_site_conflict", "Published applications and the desktop require different cookie sites.")
		}
	}
	m.mu.Lock()
	active := 0
	for _, item := range m.state.Records {
		if item.Publication.State != "removed" {
			active++
			if !sameResources(settings, item.Resources) {
				m.mu.Unlock()
				return Publication{}, problem(409, "publication_configuration_changed", "Existing publications still belong to another configuration. Restore it or unpublish them before accepting another target.")
			}
		}
		if item.Publication.Hostname == hostname || item.CookieSite == site {
			m.mu.Unlock()
			return Publication{}, problem(409, "publication_site_retired", "This cookie site is permanently assigned to an original publication and cannot be reused.")
		}
	}
	if active >= 64 || len(m.state.Records) >= 4096 {
		m.mu.Unlock()
		return Publication{}, problem(429, "publication_capacity", "Publication capacity is full. Existing records and recovery identities were preserved.")
	}
	now := m.now()
	id := rand.Text()
	item := record{Input: input, CookieSite: site, Route: route, Resources: resources{AccountID: settings.AccountID, ZoneID: settings.ZoneID, TunnelID: settings.TunnelID, Domain: settings.Domain, Listen: settings.Listen, Issuer: settings.Issuer, Audience: settings.Audience, OwnerSubject: settings.OwnerSubject}, Publication: Publication{ID: id, RequestID: input.RequestID, Name: input.Name, Generation: generation, Domain: settings.Domain, Hostname: hostname, Visibility: input.Visibility, Binding: input.Binding, Port: input.Port, State: "provisioning", Phase: "verify-target", CreatedAt: now, UpdatedAt: now}}
	next := m.copyLocked()
	next.Records[id] = item
	err = m.saveLocked(next)
	m.mu.Unlock()
	if err != nil {
		var failure *Problem
		if errors.As(err, &failure) {
			return Publication{}, failure
		}
		return Publication{}, problem(500, "publication_save_failed", "The publication could not be saved. No cloud resources were changed.")
	}
	m.schedule(id)
	return projected(item), nil
}

func (m *Manager) Unpublish(ctx context.Context, id string) (Publication, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return Publication{}, problem(503, "publication_stopping", "AXV publishing is stopping.")
	}
	item, ok := m.record(id)
	if !ok {
		return Publication{}, problem(404, "publication_not_found", "The original publication was not found.")
	}
	if item.Publication.State == "removed" {
		return projected(item), nil
	}
	// Withdrawal is immediate even if storage cannot acknowledge the intent.
	// The volatile fence also prevents an already queued worker from reopening it.
	m.mu.Lock()
	if m.withdrawn == nil {
		m.withdrawn = map[string]bool{}
	}
	m.withdrawn[id] = true
	m.mu.Unlock()
	m.retire(id)
	if err := m.update(id, func(r *record) {
		r.Remove = true
		r.Publication.State = "removing"
		r.Publication.Phase = "revoke-origin"
		r.Publication.Error = ""
		r.Publication.CleanupPending = true
	}); err != nil {
		m.mu.Lock()
		if m.storageErr == nil {
			m.storageErr = err
		}
		m.runtimeErrors[id] = "Access was withdrawn in this AXV process, but its removal could not be saved. Repair storage and restart AXV, then unpublish the retained record; it will not reopen automatically."
		m.mu.Unlock()
		return Publication{}, err
	}
	m.schedule(id)
	item, _ = m.record(id)
	return projected(item), nil
}

func (m *Manager) Retry(ctx context.Context, id string) (Publication, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	m.mu.Lock()
	closed := m.closed
	storageFailed := m.storageErr != nil
	m.mu.Unlock()
	if closed {
		return Publication{}, problem(503, "publication_stopping", "AXV publishing is stopping.")
	}
	if storageFailed {
		return Publication{}, problem(500, "publication_storage_failed", "Publication storage failed. Repair its filesystem and restart AXV before retrying; original receipts and cloud ownership were preserved.")
	}
	item, ok := m.record(id)
	if !ok {
		return Publication{}, problem(404, "publication_not_found", "The original publication was not found.")
	}
	if item.Publication.State == "removed" || item.Publication.State == "published" {
		m.mu.Lock()
		view := m.projectedLocked(m.state.Records[id])
		m.mu.Unlock()
		return view, nil
	}
	if err := m.update(id, func(r *record) {
		r.Publication.Error = ""
		if r.Remove {
			r.Publication.State = "removing"
		} else {
			r.Publication.State = "provisioning"
		}
		r.Publication.Phase = "retry-original"
	}); err != nil {
		return Publication{}, err
	}
	m.schedule(id)
	item, _ = m.record(id)
	return projected(item), nil
}

func (m *Manager) schedule(id string) {
	m.mu.Lock()
	if !m.workAllowedLocked(id) || m.workers[id] || m.retiring[id] != nil {
		m.mu.Unlock()
		return
	}
	m.workers[id] = true
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.workers, id)
			item := m.state.Records[id]
			again := m.retryPendingLocked(id, item)
			m.mu.Unlock()
			if again {
				m.schedule(id)
			}
		}()
		m.reconcile(id)
	}()
}

func (m *Manager) workAllowedLocked(id string) bool {
	item, exists := m.state.Records[id]
	return exists && !m.closed && m.ctx.Err() == nil && m.storageErr == nil && m.runtimeErrors[id] == "" && (!m.withdrawn[id] || item.Remove)
}

func (m *Manager) workAllowed(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workAllowedLocked(id)
}

func (m *Manager) retire(id string) {
	m.mu.Lock()
	entry := m.detachLocked(id)
	m.mu.Unlock()
	m.finishRetirement(entry)
}

// detachLocked fences the original runtime before any journal write or wait.
// A replacement cannot start while that runtime still owns forwarding resources.
func (m *Manager) detachLocked(id string) *runtimeEntry {
	for host, e := range m.live {
		if e.id == id {
			e.closed = true
			e.cancel()
			delete(m.live, host)
			if m.retiring == nil {
				m.retiring = map[string]*runtimeEntry{}
			}
			m.retiring[id] = e
			return e
		}
	}
	return m.retiring[id]
}

func (m *Manager) retryPendingLocked(id string, item record) bool {
	return m.workAllowedLocked(id) && (item.Remove && item.Publication.State == "removing" || item.Publication.State == "provisioning" && item.Publication.Phase == "retry-original")
}

func (m *Manager) finishRetirement(entry *runtimeEntry) {
	if entry == nil {
		return
	}
	err := entry.close()
	m.mu.Lock()
	if m.retiring[entry.id] != entry {
		m.mu.Unlock()
		return
	}
	delete(m.retiring, entry.id)
	if err != nil {
		m.closeErr = errors.Join(m.closeErr, err)
		m.runtimeErrors[entry.id] = "The original publishing connection could not close cleanly. Restart AXV before changing its target."
	}
	again := m.retryPendingLocked(entry.id, m.state.Records[entry.id])
	m.mu.Unlock()
	if again {
		m.schedule(entry.id)
	}
}

func (e *runtimeEntry) close() error {
	e.once.Do(func() {
		e.cancel()
		e.manager.mu.Lock()
		connections := make([]net.Conn, 0, len(e.connections))
		for c := range e.connections {
			connections = append(connections, c)
		}
		e.manager.mu.Unlock()
		for _, c := range connections {
			if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				e.closeErr = errors.Join(e.closeErr, err)
			}
		}
		e.wg.Wait()
		if e.target.Close != nil {
			e.target.Close()
		}
		if e.verifier != nil {
			e.verifier.Close()
		}
	})
	return e.closeErr
}

func (m *Manager) Close() error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if m.closed {
		err := m.closeErr
		m.mu.Unlock()
		return err
	}
	m.closed = true
	m.cancel()
	entries := make([]*runtimeEntry, 0, len(m.live))
	for _, e := range m.live {
		e.closed = true
		entries = append(entries, e)
	}
	for _, e := range m.retiring {
		entries = append(entries, e)
	}
	m.live = map[string]*runtimeEntry{}
	surface := m.surface
	var result error
	m.mu.Unlock()
	if surface != nil {
		result = errors.Join(result, surface.close())
	}
	for _, e := range entries {
		result = errors.Join(result, e.close())
	}
	m.wg.Wait()
	if m.lock != nil {
		result = errors.Join(result, m.lock.Close())
	}
	m.mu.Lock()
	result = errors.Join(result, m.closeErr)
	m.closeErr = result
	m.mu.Unlock()
	return result
}
