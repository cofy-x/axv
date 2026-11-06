package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notifications/webpush"
)

const maxPushSubscriptions = 16
const pushTTL = 15 * time.Minute

// PushIdentity addresses the persistent notification store and its independent
// Web Push configuration revision. Delivery progress never changes this revision.
type PushIdentity struct {
	StoreID  string `json:"storeId"`
	Revision uint64 `json:"revision"`
}

type PushPrepare struct {
	PushIdentity
	Contact string `json:"contact"`
}

type PushSubscriptionIdentity struct {
	PushIdentity
	KeyID          string `json:"keyId"`
	InstallationID string `json:"installationId"`
	SubscriptionID string `json:"subscriptionId"`
}

type PushSubscribe struct {
	PushIdentity
	KeyID          string               `json:"keyId"`
	InstallationID string               `json:"installationId"`
	Label          string               `json:"label"`
	EventTypes     []string             `json:"eventTypes"`
	Subscription   webpush.Subscription `json:"subscription"`
}

// PushSubscription contains safe configuration only. A browser-provided label
// and installation ID do not grant authority; the HTTP owner guard does.
type PushSubscription struct {
	ID             string     `json:"id"`
	InstallationID string     `json:"installationId"`
	Origin         string     `json:"origin"`
	Label          string     `json:"label"`
	Enabled        bool       `json:"enabled"`
	Status         string     `json:"status"`
	EventTypes     []string   `json:"eventTypes"`
	CreatedAt      time.Time  `json:"createdAt"`
	NextSendAt     *time.Time `json:"nextSendAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	Fingerprint    string     `json:"fingerprint,omitempty"`
}

type PushSnapshot struct {
	StoreID       string             `json:"storeId"`
	Revision      uint64             `json:"revision"`
	KeyID         string             `json:"keyId,omitempty"`
	PublicKey     string             `json:"publicKey,omitempty"`
	Contact       string             `json:"contact,omitempty"`
	Subscriptions []PushSubscription `json:"subscriptions"`
	Error         string             `json:"error,omitempty"`
}

type PushDelivery struct {
	SubscriptionID string     `json:"subscriptionId"`
	KeyID          string     `json:"keyId"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  *time.Time `json:"nextAttemptAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	Error          string     `json:"error,omitempty"`
}

type pushSubscriptionDisk struct {
	PushSubscription
	Subscription webpush.Subscription `json:"subscription"`
	EnabledAt    time.Time            `json:"enabledAt"`
}

type pushDiskState struct {
	Revision      uint64                 `json:"revision"`
	KeyID         string                 `json:"keyId"`
	PrivateKey    string                 `json:"privateKey"`
	PublicKey     string                 `json:"publicKey"`
	Contact       string                 `json:"contact"`
	Subscriptions []pushSubscriptionDisk `json:"subscriptions"`
}

func pushID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

func validPushState(p *pushDiskState) bool {
	if p == nil {
		return true
	}
	if p.Revision == 0 || p.Revision > 1<<53-1 || !pushID(p.KeyID) || webpush.ValidateVAPIDKeys(p.PrivateKey, p.PublicKey) != nil || webpush.ValidateSubject(p.Contact) != nil || len(p.Subscriptions) > maxPushSubscriptions {
		return false
	}
	ids, installations, endpoints := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range p.Subscriptions {
		// The browser comparison digest is derived for responses, never adopted
		// as stored identity or accepted instead of private subscription data.
		if s.Fingerprint != "" {
			return false
		}
		if !pushID(s.ID) || !pushID(s.InstallationID) || !validPushOrigin(s.Origin) || !validText(s.Label, 80) || len(s.EventTypes) > 10 || s.CreatedAt.IsZero() || s.EnabledAt.IsZero() || !validPushTypes(s.EventTypes) || ids[s.ID] || installations[s.Origin+":"+s.InstallationID] {
			return false
		}
		if s.Status != "active" && s.Status != "disabled" && s.Status != "expired" {
			return false
		}
		if s.Enabled != (s.Status == "active") || !pushError(s.Error) {
			return false
		}
		if s.Status == "expired" {
			if s.Subscription.Endpoint != "" || s.Subscription.Keys.P256DH != "" || s.Subscription.Keys.Auth != "" {
				return false
			}
		} else {
			if webpush.Validate(s.Subscription) != nil || endpoints[s.Subscription.Endpoint] {
				return false
			}
			endpoints[s.Subscription.Endpoint] = true
		}
		ids[s.ID] = true
		installations[s.Origin+":"+s.InstallationID] = true
	}
	return true
}

func pushError(value string) bool {
	return slices.Contains([]string{"", "provider_rejected", "response_uncertain", "rate_limited", "subscription_expired", "ttl_expired"}, value)
}
func validPushDeliveries(ds []PushDelivery) bool {
	if len(ds) > maxPushSubscriptions {
		return false
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if !pushID(d.SubscriptionID) || !pushID(d.KeyID) || seen[d.SubscriptionID] || d.ExpiresAt.IsZero() || d.Attempts < 0 || d.Attempts > maxAttempts || !pushError(d.Error) || !slices.Contains([]string{"pending", "sending", "accepted", "uncertain", "failed", "disabled", "superseded", "expired"}, d.Status) {
			return false
		}
		seen[d.SubscriptionID] = true
	}
	return true
}
func validPushTypes(types []string) bool {
	seen := map[string]bool{}
	for _, kind := range types {
		if _, ok := eventTitles[kind]; !ok || kind == "test" || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	return len(types) > 0 && len(types) <= 10
}
func validPushOrigin(origin string) bool {
	u, e := url.Parse(origin)
	if e != nil || !validText(origin, 2048) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || u.String() != origin {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

func (m *Manager) pushSnapshotLocked() PushSnapshot {
	s := PushSnapshot{StoreID: m.state.StoreID, Subscriptions: []PushSubscription{}, Error: m.failure}
	if p := m.state.WebPush; p != nil {
		s.Revision = p.Revision
		s.KeyID = p.KeyID
		s.PublicKey = p.PublicKey
		s.Contact = p.Contact
		for _, sub := range p.Subscriptions {
			v := sub.PushSubscription
			v.EventTypes = append([]string{}, v.EventTypes...)
			if sub.Subscription.Endpoint != "" {
				digest := sha256.Sum256([]byte(sub.Subscription.Endpoint + "\x00" + sub.Subscription.Keys.P256DH + "\x00" + sub.Subscription.Keys.Auth))
				v.Fingerprint = hex.EncodeToString(digest[:])
			}
			s.Subscriptions = append(s.Subscriptions, v)
		}
	}
	return s
}
func (m *Manager) PushSnapshot() PushSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pushSnapshotLocked()
}
func (m *Manager) pushCheck(i PushIdentity) error {
	if m.completion != nil || m.currentStore() != nil {
		return ErrStore
	}
	revision := uint64(0)
	if m.state.WebPush != nil {
		revision = m.state.WebPush.Revision
	}
	if i.StoreID != m.state.StoreID || i.Revision != revision {
		return ErrConflict
	}
	if revision >= 1<<53-1 {
		return ErrStore
	}
	return nil
}
func (m *Manager) PreparePush(i PushPrepare) (PushSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.pushCheck(i.PushIdentity); e != nil {
		return PushSnapshot{}, e
	}
	if webpush.ValidateSubject(i.Contact) != nil {
		return PushSnapshot{}, ErrInput
	}
	if m.state.WebPush != nil {
		if m.state.WebPush.Contact != i.Contact {
			return PushSnapshot{}, ErrConflict
		}
		return m.pushSnapshotLocked(), nil
	}
	return m.preparePushLocked(i.Contact)
}
func (m *Manager) preparePushLocked(contact string) (PushSnapshot, error) {
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		return PushSnapshot{}, ErrStore
	}
	keyID := nonce()
	if keyID == "" {
		return PushSnapshot{}, ErrStore
	}
	next := clone(m.state)
	revision := uint64(1)
	if next.WebPush != nil {
		revision = next.WebPush.Revision + 1
	}
	next.WebPush = &pushDiskState{Revision: revision, KeyID: keyID, PrivateKey: private, PublicKey: public, Contact: contact, Subscriptions: []pushSubscriptionDisk{}}
	disablePushDeliveries(&next, "", true)
	if m.pushSendCancel != nil {
		m.pushSendCancel()
	}
	if e = m.save(next); e != nil {
		return PushSnapshot{}, e
	}
	return m.pushSnapshotLocked(), nil
}
func (m *Manager) RotatePush(i PushPrepare) (PushSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.pushCheck(i.PushIdentity); e != nil {
		return PushSnapshot{}, e
	}
	if m.state.WebPush == nil || webpush.ValidateSubject(i.Contact) != nil {
		return PushSnapshot{}, ErrInput
	}
	return m.preparePushLocked(i.Contact)
}
func (m *Manager) SubscribePush(i PushSubscribe, origin string) (PushSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.pushCheck(i.PushIdentity); e != nil {
		return PushSnapshot{}, e
	}
	p := m.state.WebPush
	if p == nil || p.KeyID != i.KeyID {
		return PushSnapshot{}, ErrConflict
	}
	if !pushID(i.InstallationID) || !validPushOrigin(origin) || !validText(i.Label, 80) || !validPushTypes(i.EventTypes) {
		return PushSnapshot{}, ErrInput
	}
	if err := webpush.Validate(i.Subscription); err != nil {
		if errors.Is(err, webpush.ErrUnsupportedService) {
			return PushSnapshot{}, ErrPushService
		}
		return PushSnapshot{}, ErrInput
	}
	if len(p.Subscriptions) >= maxPushSubscriptions {
		return PushSnapshot{}, ErrInput
	}
	for _, sub := range p.Subscriptions {
		if sub.InstallationID == i.InstallationID && sub.Origin == origin || sub.Subscription.Endpoint == i.Subscription.Endpoint {
			return PushSnapshot{}, ErrConflict
		}
	}
	id := nonce()
	if id == "" {
		return PushSnapshot{}, ErrStore
	}
	next := clone(m.state)
	now := m.now().UTC()
	next.WebPush.Subscriptions = append(next.WebPush.Subscriptions, pushSubscriptionDisk{PushSubscription: PushSubscription{ID: id, InstallationID: i.InstallationID, Origin: origin, Label: i.Label, Enabled: true, Status: "active", EventTypes: append([]string{}, i.EventTypes...), CreatedAt: now}, Subscription: i.Subscription, EnabledAt: now})
	next.WebPush.Revision++
	if !fitsNotificationBudget(next) {
		return PushSnapshot{}, ErrInput
	}
	if e := m.save(next); e != nil {
		return PushSnapshot{}, e
	}
	return m.pushSnapshotLocked(), nil
}
func pushSubscriptionIndex(p *pushDiskState, i PushSubscriptionIdentity, origin string) (int, error) {
	if p == nil || p.KeyID != i.KeyID {
		return -1, ErrConflict
	}
	if !pushID(i.InstallationID) || !pushID(i.SubscriptionID) || !validPushOrigin(origin) {
		return -1, ErrInput
	}
	for n, s := range p.Subscriptions {
		if s.ID == i.SubscriptionID {
			if s.InstallationID != i.InstallationID || s.Origin != origin {
				return -1, ErrConflict
			}
			return n, nil
		}
	}
	return -1, ErrMissing
}
func disablePushDeliveries(s *diskState, id string, all bool) {
	for i := range s.Events {
		for j := range s.Events[i].PushDeliveries {
			d := &s.Events[i].PushDeliveries[j]
			if (all || d.SubscriptionID == id) && pushPending(*d) {
				if d.Status == "sending" {
					d.Status = "uncertain"
					d.Error = "response_uncertain"
				} else {
					d.Status = "disabled"
				}
				d.NextAttemptAt = nil
			}
		}
	}
}
func pushPending(d PushDelivery) bool {
	return d.Status == "pending" || d.Status == "sending" || d.Status == "uncertain" && d.NextAttemptAt != nil
}
func (m *Manager) changePushSubscription(i PushSubscriptionIdentity, origin string, remove bool) (PushSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.pushCheck(i.PushIdentity); e != nil {
		return PushSnapshot{}, e
	}
	n, e := pushSubscriptionIndex(m.state.WebPush, i, origin)
	if e != nil {
		return PushSnapshot{}, e
	}
	next := clone(m.state)
	if remove {
		next.WebPush.Subscriptions = append(next.WebPush.Subscriptions[:n], next.WebPush.Subscriptions[n+1:]...)
	} else if next.WebPush.Subscriptions[n].Status != "expired" {
		next.WebPush.Subscriptions[n].Enabled = false
		next.WebPush.Subscriptions[n].Status = "disabled"
	}
	disablePushDeliveries(&next, i.SubscriptionID, false)
	next.WebPush.Revision++
	if m.pushSendingID == i.SubscriptionID && m.pushSendCancel != nil {
		m.pushSendCancel()
	}
	if e = m.save(next); e != nil {
		return PushSnapshot{}, e
	}
	return m.pushSnapshotLocked(), nil
}
func (m *Manager) DisablePush(i PushSubscriptionIdentity, origin string) (PushSnapshot, error) {
	return m.changePushSubscription(i, origin, false)
}
func (m *Manager) UnsubscribePush(i PushSubscriptionIdentity, origin string) (PushSnapshot, error) {
	return m.changePushSubscription(i, origin, true)
}
func (m *Manager) TestPush(i PushSubscriptionIdentity, origin string) (PushSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.pushCheck(i.PushIdentity); e != nil {
		return PushSnapshot{}, e
	}
	n, e := pushSubscriptionIndex(m.state.WebPush, i, origin)
	if e != nil {
		return PushSnapshot{}, e
	}
	sub := m.state.WebPush.Subscriptions[n]
	if !sub.Enabled {
		return PushSnapshot{}, ErrInput
	}
	next := clone(m.state)
	expirePushReceipts(&next, m.now())
	id := nonce()
	if id == "" {
		return PushSnapshot{}, ErrStore
	}
	now := m.now().UTC()
	if !m.admit(&next, Notification{ID: id, CreatedAt: now, Kind: "test", State: "test", Status: "disabled", PushDeliveries: []PushDelivery{{SubscriptionID: sub.ID, KeyID: next.WebPush.KeyID, Status: "pending", ExpiresAt: now.Add(pushTTL)}}}) {
		return PushSnapshot{}, ErrStore
	}
	if e = m.save(next); e != nil {
		return PushSnapshot{}, e
	}
	return m.pushSnapshotLocked(), nil
}

func pushForEvent(p *pushDiskState, kind string, observed time.Time) []PushDelivery {
	if p == nil {
		return nil
	}
	var result []PushDelivery
	for _, sub := range p.Subscriptions {
		if sub.Enabled && !observed.Before(sub.EnabledAt) && selected(sub.EventTypes, kind) {
			result = append(result, PushDelivery{SubscriptionID: sub.ID, KeyID: p.KeyID, Status: "pending", ExpiresAt: observed.UTC().Add(pushTTL)})
		}
	}
	return result
}

func expirePushReceipts(s *diskState, now time.Time) bool {
	changed := false
	for i := range s.Events {
		for j := range s.Events[i].PushDeliveries {
			d := &s.Events[i].PushDeliveries[j]
			if (d.Status == "pending" || d.Status == "uncertain" && d.NextAttemptAt != nil) && d.ExpiresAt.Sub(now) < time.Second {
				d.Status, d.Error, d.NextAttemptAt = "expired", "ttl_expired", nil
				changed = true
			}
		}
	}
	return changed
}
