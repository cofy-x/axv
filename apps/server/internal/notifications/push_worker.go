package notifications

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/notifications/webpush"
)

func pushReceiptIndex(events []Notification, eventID, subscriptionID string) (int, int) {
	for i, e := range events {
		if e.ID == eventID {
			for j, d := range e.PushDeliveries {
				if d.SubscriptionID == subscriptionID {
					return i, j
				}
			}
		}
	}
	return -1, -1
}
func pushSubscriber(p *pushDiskState, id string) *pushSubscriptionDisk {
	if p != nil {
		for i := range p.Subscriptions {
			if p.Subscriptions[i].ID == id {
				return &p.Subscriptions[i]
			}
		}
	}
	return nil
}

func (m *Manager) finishPushWithoutSend(storeID, eventID, subscriptionID, status, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.StoreID != storeID || m.failure != "" || m.completion != nil {
		return
	}
	next := clone(m.state)
	i, j := pushReceiptIndex(next.Events, eventID, subscriptionID)
	if i < 0 || !pushPending(next.Events[i].PushDeliveries[j]) {
		return
	}
	d := &next.Events[i].PushDeliveries[j]
	d.Status = status
	d.Error = code
	d.NextAttemptAt = nil
	_ = m.save(next)
}

// dispatchPush shares the existing sequential dispatcher and durable outbox.
// Each receipt belongs to one immutable subscription and VAPID generation.
func (m *Manager) dispatchPush(ctx context.Context) {
	m.mu.Lock()
	if m.state.WebPush == nil || m.failure != "" || m.completion != nil {
		m.mu.Unlock()
		return
	}
	next := clone(m.state)
	now := m.now()
	changed := expirePushReceipts(&next, now)
	if changed && m.save(next) != nil {
		m.mu.Unlock()
		return
	}
	var event Notification
	var delivery PushDelivery
	var subscriber pushSubscriptionDisk
	for _, e := range m.state.Events {
		for _, d := range e.PushDeliveries {
			if d.Status != "pending" && !(d.Status == "uncertain" && d.NextAttemptAt != nil) || d.Attempts >= maxAttempts || d.NextAttemptAt != nil && now.Before(*d.NextAttemptAt) {
				continue
			}
			sub := pushSubscriber(m.state.WebPush, d.SubscriptionID)
			if sub == nil || !sub.Enabled || d.KeyID != m.state.WebPush.KeyID {
				continue
			}
			if sub.NextSendAt != nil && now.Before(*sub.NextSendAt) {
				continue
			}
			event = e
			delivery = d
			subscriber = *sub
			break
		}
		if event.ID != "" {
			break
		}
	}
	if event.ID == "" {
		m.mu.Unlock()
		return
	}
	storeID, revision, keyID := m.state.StoreID, m.state.WebPush.Revision, m.state.WebPush.KeyID
	m.mu.Unlock()
	if event.Kind != "test" {
		request, cancel := context.WithTimeout(ctx, 10*time.Second)
		resolution, err := m.resolve(request, event.Target)
		cancel()
		if err != nil {
			return
		}
		if !resolution.Available || waiting(event.Kind) && (!resolution.Current || resolution.State != event.State) || event.Kind == "task_unknown" && resolution.State != "unknown" {
			m.finishPushWithoutSend(storeID, event.ID, subscriber.ID, "superseded", "")
			return
		}
	}
	m.mu.Lock()
	p := m.state.WebPush
	if m.state.StoreID != storeID || p == nil || p.Revision != revision || p.KeyID != keyID || m.failure != "" || m.completion != nil {
		m.mu.Unlock()
		return
	}
	sub := pushSubscriber(p, subscriber.ID)
	if sub == nil || !sub.Enabled {
		m.mu.Unlock()
		return
	}
	next = clone(m.state)
	i, j := pushReceiptIndex(next.Events, event.ID, subscriber.ID)
	if i < 0 || !pushPending(next.Events[i].PushDeliveries[j]) || next.Events[i].PushDeliveries[j].Attempts != delivery.Attempts {
		m.mu.Unlock()
		return
	}
	if delivery.ExpiresAt.Sub(m.now()) < time.Second {
		m.mu.Unlock()
		m.finishPushWithoutSend(storeID, event.ID, subscriber.ID, "expired", "ttl_expired")
		return
	}
	next.Events[i].PushDeliveries[j].Status = "sending"
	next.Events[i].PushDeliveries[j].Attempts++
	next.Events[i].PushDeliveries[j].NextAttemptAt = nil
	if m.save(next) != nil {
		// A failed directory sync can follow publication. Restore the exact unsent
		// receipt before any later intake retries storage or another send.
		a, b := pushReceiptIndex(m.state.Events, event.ID, subscriber.ID)
		if a >= 0 && m.state.Events[a].PushDeliveries[b].Status == "sending" && m.state.Events[a].PushDeliveries[b].Attempts == delivery.Attempts+1 {
			recovery := clone(m.state)
			recovery.Events[a].PushDeliveries[b] = delivery
			m.completion = &recovery
		}
		m.mu.Unlock()
		return
	}
	ttl := delivery.ExpiresAt.Sub(m.now())
	if ttl < time.Second {
		m.mu.Unlock()
		m.finishPushWithoutSend(storeID, event.ID, subscriber.ID, "expired", "ttl_expired")
		return
	}
	if ttl > pushTTL {
		ttl = pushTTL
	}
	private, public, contact := p.PrivateKey, p.PublicKey, p.Contact
	send, cancel := context.WithTimeout(ctx, 10*time.Second)
	m.pushSendCancel = cancel
	m.pushSendingID = subscriber.ID
	payload, _ := json.Marshal(struct {
		Version        int       `json:"version"`
		StoreID        string    `json:"storeId"`
		KeyID          string    `json:"keyId"`
		InstallationID string    `json:"installationId"`
		SubscriptionID string    `json:"subscriptionId"`
		NotificationID string    `json:"notificationId"`
		ExpiresAt      time.Time `json:"expiresAt"`
	}{1, storeID, keyID, subscriber.InstallationID, subscriber.ID, event.ID, delivery.ExpiresAt})
	m.mu.Unlock()
	outcome := m.options.PushSender.Send(send, subscriber.Subscription, private, public, payload, ttl, contact)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushSendCancel = nil
	m.pushSendingID = ""
	next = clone(m.state)
	i, j = pushReceiptIndex(next.Events, event.ID, subscriber.ID)
	if i < 0 || next.Events[i].PushDeliveries[j].Status != "sending" {
		return
	}
	d := &next.Events[i].PushDeliveries[j]
	d.Error = ""
	d.NextAttemptAt = nil
	sub = pushSubscriber(next.WebPush, subscriber.ID)
	switch outcome.Status {
	case webpush.Accepted:
		d.Status = "accepted"
	case webpush.Expired:
		d.Status = "expired"
		d.Error = "subscription_expired"
		if sub != nil {
			sub.Enabled = false
			sub.Status = "expired"
			sub.Error = "subscription_expired"
			sub.NextSendAt = nil
			sub.Subscription = webpush.Subscription{}
		}
		for a := range next.Events {
			for b := range next.Events[a].PushDeliveries {
				other := &next.Events[a].PushDeliveries[b]
				if other.SubscriptionID == subscriber.ID && pushPending(*other) {
					other.Status = "expired"
					other.Error = "subscription_expired"
					other.NextAttemptAt = nil
				}
			}
		}
	case webpush.RateLimited:
		d.Status = "pending"
		d.Error = "rate_limited"
		retry := outcome.RetryAfter
		if retry < time.Minute {
			retry = time.Minute
		}
		if retry > 24*time.Hour {
			retry = 24 * time.Hour
		}
		at := m.now().Add(retry)
		if sub != nil {
			sub.NextSendAt = &at
			sub.Error = "rate_limited"
		}
		if d.Attempts < maxAttempts {
			d.NextAttemptAt = &at
		} else {
			d.Status = "failed"
		}
	case webpush.Uncertain:
		d.Status = "uncertain"
		d.Error = "response_uncertain"
		if d.Attempts < maxAttempts {
			at := m.now().Add(time.Duration(d.Attempts) * time.Minute)
			d.NextAttemptAt = &at
		}
	default:
		d.Status = "failed"
		d.Error = "provider_rejected"
	}
	if sub != nil && outcome.Status == webpush.Accepted {
		sub.Error = ""
		sub.NextSendAt = nil
	}
	if m.save(next) != nil {
		m.completion = &next
	}
}
