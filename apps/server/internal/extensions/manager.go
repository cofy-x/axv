package extensions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode"
)

const sessionTTL = 30 * time.Minute
const maxSessions = 256

type session struct {
	storeID, app, digest, client, window string
	generation                           int64
	expires                              time.Time
}

// Manager's sessions are host-held capabilities. They are never passed to an
// extension document, persisted, or reusable after an installation changes.
type Manager struct {
	store    *Store
	mu       sync.Mutex
	sessions map[string]session
	now      func() time.Time
}

func New(store *Store) *Manager {
	return &Manager{store: store, sessions: map[string]session{}, now: time.Now}
}
func expired() error {
	return failure("extension_session_expired", "The extension session expired. Reopen the application while preserving any unsaved draft.")
}
func storeChanged() error {
	return failure("extension_store_changed", "The application store changed. Preserve any draft, then review the new application store before continuing.")
}
func validClient(value string) bool {
	return len(value) > 0 && len(value) <= 128 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

type SessionInput struct {
	Digest     string `json:"digest"`
	Generation int64  `json:"generation"`
	ClientID   string `json:"clientId"`
	WindowID   string `json:"windowId"`
}

func (m *Manager) createSession(ctx context.Context, id string, input SessionInput) (string, error) {
	if !validClient(input.ClientID) || !validClient(input.WindowID) {
		return "", failure("invalid_extension_session", "Provide a browser client ID and a window ID of at most 128 characters.")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.store.scope(ctx, id, input.Digest, input.Generation); err != nil {
		return "", err
	}
	now := m.now()
	for key, s := range m.sessions {
		if !s.expires.After(now) {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= maxSessions {
		return "", failure("extension_session_limit", "Too many extension sessions are open. Close an application window and try again.")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	key := hex.EncodeToString(random[:])
	m.sessions[key] = session{storeID: m.store.ID(), app: id, digest: input.Digest, generation: input.Generation, client: input.ClientID, window: input.WindowID, expires: now.Add(sessionTTL)}
	return key, nil
}

func (m *Manager) revokeBefore(id string, generation int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.sessions {
		// A session may have opened after the management transaction committed.
		// Preserve that generation, including a still newer concurrent upgrade.
		if s.app == id && s.generation < generation {
			delete(m.sessions, key)
		}
	}
}

func (m *Manager) release(id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; !ok || s.app != id {
		return expired()
	}
	delete(m.sessions, key)
	return nil
}

func (m *Manager) rpc(ctx context.Context, id, key, method string, value json.RawMessage, expected *int64) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok || !s.expires.After(m.now()) {
		delete(m.sessions, key)
		return Document{}, expired()
	}
	if s.app != id {
		return Document{}, expired()
	}
	switch method {
	case "storage.get":
		if value != nil || expected != nil {
			return Document{}, failure("invalid_extension_rpc", "storage.get does not accept parameters.")
		}
	case "storage.set":
		if value == nil || expected == nil {
			return Document{}, failure("invalid_extension_rpc", "storage.set requires value and expectedRevision.")
		}
	default:
		return Document{}, failure("invalid_extension_rpc", "This extension RPC method is not supported.")
	}
	return m.store.sessionData(ctx, s, value, expected)
}
