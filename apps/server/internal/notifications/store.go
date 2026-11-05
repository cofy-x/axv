package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

type Manager struct {
	mu          sync.Mutex
	write       func(string, any, os.FileMode) error
	completion  *diskState
	fingerprint [32]byte
	established bool
	state       diskState
	path        string
	lock        *os.File
	options     Options
	failure     string
	cancel      context.CancelFunc
	sendCancel  context.CancelFunc
	done        chan struct{}
	now         func() time.Time
}

func nonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func Open(ctx context.Context, options Options) (*Manager, error) {
	dir := filepath.Join(options.Home, "notifications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrStore
	}
	if !privateDirectory(dir) {
		return nil, ErrStore
	}
	path := filepath.Join(dir, "state.json")
	lockPath := filepath.Join(dir, "owner.lock")
	_, lockErr := os.Lstat(lockPath)
	for _, p := range []string{path, lockPath} {
		if i, e := os.Lstat(p); e == nil && (!i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0) {
			return nil, ErrStore
		} else if e != nil && !os.IsNotExist(e) {
			return nil, ErrStore
		}
	}
	lock, err := filelock.TryLock(lockPath)
	if err != nil {
		return nil, ErrStore
	}
	if !validatePrivateFile(lockPath, lock) {
		lock.Close()
		return nil, ErrStore
	}
	m := &Manager{path: path, lock: lock, options: options, now: time.Now, write: jsonfile.Write, done: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			lock.Close()
		}
	}()
	file, err := openPrivate(path)
	if os.IsNotExist(err) {
		if !os.IsNotExist(lockErr) {
			return nil, ErrStore
		}
		m.state = diskState{Version: 1, StoreID: nonce(), Revision: 1, Checkpoints: map[string]checkpoint{}, Events: []Notification{}, Config: Config{EventTypes: []string{"agent_waiting_approval", "agent_waiting_input", "agent_failed", "agent_interrupted", "task_failed", "task_interrupted", "task_unknown"}}}
		if m.state.StoreID == "" || m.save(m.state) != nil {
			return nil, ErrStore
		}
	} else if err != nil {
		return nil, ErrStore
	} else {
		defer file.Close()
		data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		if readErr != nil || len(data) > 4<<20 || !jsonunicode.Valid(data) || !uniqueJSON(data) {
			return nil, ErrStore
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&m.state) != nil || decoder.Decode(new(any)) != io.EOF || !validState(m.state) {
			return nil, ErrStore
		}
		m.fingerprint = sha256.Sum256(data)
		m.established = true
		for i := range m.state.Events {
			e := &m.state.Events[i]
			if e.Status == "sending" {
				e.Status = "uncertain"
				e.Error = "response_uncertain"
				t := m.now().Add(time.Minute)
				if e.Attempts < maxAttempts {
					e.NextAttemptAt = &t
				}
			}
		}
		if m.save(m.state) != nil {
			return nil, ErrStore
		}
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 10 * time.Second}
	}
	client := *options.Client
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m.options.Client = &client
	run, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	if options.DisableWorker {
		close(m.done)
	} else {
		go m.run(run)
	}
	ok = true
	return m, nil
}

func validText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

func validConfig(c Config) bool {
	if !validText(c.ApplicationID, 128) || !validText(c.DeviceID, 128) || len(c.EventTypes) > 10 {
		return false
	}
	seen := map[string]bool{}
	for _, kind := range c.EventTypes {
		if _, ok := eventTitles[kind]; !ok || kind == "test" || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	if c.ReturnOrigin == "" {
		return !c.Enabled
	}
	u, err := url.Parse(c.ReturnOrigin)
	if err != nil || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())) || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != c.ReturnOrigin {
		return false
	}
	return validText(c.ReturnOrigin, 2048)
}

func validState(s diskState) bool {
	if s.Version != 1 || !opaqueID(s.StoreID) || s.Revision == 0 || s.Checkpoints == nil || len(s.Checkpoints) > maxSources || len(s.Events) > maxEvents || !validConfig(s.Config) || !validText(s.Token, 4096) || s.Config.Enabled && s.Token == "" {
		return false
	}
	for key, c := range s.Checkpoints {
		if !identifier(key, true, 256) || !identifier(c.StoreID, true, 128) || c.Sequence > 1<<53-1 {
			return false
		}
	}
	if !validText(s.Intake.Error, 512) {
		return false
	}
	ids := map[string]bool{}
	for _, e := range s.Events {
		if !opaqueID(e.ID) || ids[e.ID] || e.CreatedAt.IsZero() || e.Attempts < 0 || e.Attempts > maxAttempts {
			return false
		}
		if _, ok := eventTitles[e.Kind]; !ok {
			return false
		}
		if !slices.Contains([]string{"pending", "sending", "accepted", "uncertain", "failed", "disabled", "superseded"}, e.Status) {
			return false
		}
		if !slices.Contains([]string{"", "provider_rejected", "response_uncertain", "quota_exhausted"}, e.Error) {
			return false
		}
		if e.Kind == "test" {
			if e.State != "test" || e.Target.Kind != "" {
				return false
			}
		} else if !validTarget(e.Target) || eventKind(notificationevent.Event{State: e.State, Target: e.Target}) != e.Kind {
			return false
		}
		ids[e.ID] = true
	}
	return true
}

func clone(s diskState) diskState {
	b, _ := json.Marshal(s)
	var result diskState
	_ = json.Unmarshal(b, &result)
	return result
}

func (m *Manager) currentStore() error {
	file, err := openPrivate(m.path)
	if !m.established && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ErrStore
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(data) > 4<<20 || !m.established || sha256.Sum256(data) != m.fingerprint {
		return ErrStore
	}
	return nil
}

func (m *Manager) save(next diskState) error {
	if m.currentStore() != nil {
		m.failure = "The notification store changed or is unavailable. Delivery is paused."
		return ErrStore
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil || len(data) > 4<<20 {
		m.failure = "Notification storage is full. Delivery is paused."
		return ErrStore
	}
	data = append(data, '\n')
	err = m.write(m.path, next, 0600)
	if err != nil {
		// Atomic publication may have succeeded before directory sync failed. Keep
		// that exact observed snapshot rather than rolling it back on later intake.
		if observed, readErr := os.ReadFile(m.path); readErr == nil && bytes.Equal(observed, data) {
			m.state = next
			m.fingerprint = sha256.Sum256(data)
			m.established = true
		}
		m.failure = "Notification history could not be saved. Delivery is paused."
		return ErrStore
	}
	m.state = next
	m.fingerprint = sha256.Sum256(data)
	m.established = true
	m.failure = ""
	return nil
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := clone(m.state)
	s.Config.TokenConfigured = s.Token != ""
	if s.NextSendAt != nil && !m.now().Before(*s.NextSendAt) {
		s.NextSendAt = nil
	}
	slices.Reverse(s.Events)
	return Snapshot{StoreID: s.StoreID, Revision: s.Revision, Config: s.Config, Events: s.Events, Intake: s.Intake, Error: m.failure, NextSendAt: s.NextSendAt}
}

func (m *Manager) check(id string, revision uint64) error {
	if id != m.state.StoreID || revision != m.state.Revision {
		return ErrConflict
	}
	return nil
}

func (m *Manager) Configure(u Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.completion != nil {
		return ErrStore
	}
	if err := m.check(u.StoreID, u.Revision); err != nil {
		return err
	}
	next := clone(m.state)
	switch u.TokenAction {
	case "keep":
		if u.Token != "" {
			return ErrInput
		}
	case "replace":
		if u.Token == "" || !validText(u.Token, 4096) {
			return ErrInput
		}
		next.Token = u.Token
	case "clear":
		if u.Token != "" {
			return ErrInput
		}
		next.Token = ""
	default:
		return ErrInput
	}
	next.Config = Config{Enabled: u.Enabled, ApplicationID: u.ApplicationID, DeviceID: u.DeviceID, ReturnOrigin: u.ReturnOrigin, EventTypes: append([]string{}, u.EventTypes...)}
	if !validConfig(next.Config) || next.Config.Enabled && next.Token == "" {
		return ErrInput
	}
	scopeChanged := m.state.Token != next.Token || m.state.Config.ApplicationID != next.Config.ApplicationID || m.state.Config.DeviceID != next.Config.DeviceID || m.state.Config.ReturnOrigin != next.Config.ReturnOrigin || m.state.Config.Enabled != next.Config.Enabled
	if m.sendCancel != nil {
		for _, e := range next.Events {
			if e.Status == "sending" && (scopeChanged || e.Kind != "test" && !selected(next.Config.EventTypes, e.Kind)) {
				m.sendCancel()
				break
			}
		}
	}
	// Configuration changes never retarget accepted outbox records to another
	// account, device or return origin.
	for i := range next.Events {
		e := &next.Events[i]
		if (scopeChanged || e.Kind != "test" && !selected(next.Config.EventTypes, e.Kind)) && (e.Status == "pending" || e.Status == "sending" || e.Status == "uncertain" && e.NextAttemptAt != nil) {
			if e.Status == "sending" {
				e.Status = "uncertain"
				e.Error = "response_uncertain"
			} else {
				e.Status = "disabled"
			}
			e.NextAttemptAt = nil
		}
	}
	if next.Config.Enabled && scopeChanged {
		next.EnabledAt = m.now().UTC()
	}
	next.Revision++
	return m.save(next)
}

func (m *Manager) Disable(id string, revision uint64) error {
	m.mu.Lock()
	if err := m.check(id, revision); err != nil {
		m.mu.Unlock()
		return err
	}
	c := m.state.Config
	m.mu.Unlock()
	return m.Configure(Update{StoreID: id, Revision: revision, Enabled: false, TokenAction: "keep", ApplicationID: c.ApplicationID, DeviceID: c.DeviceID, ReturnOrigin: c.ReturnOrigin, EventTypes: c.EventTypes})
}

func (m *Manager) room(s *diskState) bool {
	if len(s.Events) < maxEvents {
		return true
	}
	for i, e := range s.Events {
		if e.Status != "pending" && e.Status != "sending" && !(e.Status == "uncertain" && e.NextAttemptAt != nil) {
			s.Events = append(s.Events[:i], s.Events[i+1:]...)
			return true
		}
	}
	s.Intake.Gap = true
	s.Intake.Error = "The notification outbox reached its retention limit; some observations were not queued."
	return false
}

func (m *Manager) Test(id string, revision uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.completion != nil {
		return ErrStore
	}
	if err := m.check(id, revision); err != nil {
		return err
	}
	if !m.state.Config.Enabled || m.state.Token == "" {
		return ErrInput
	}
	next := clone(m.state)
	if !m.room(&next) {
		return ErrStore
	}
	eventID := nonce()
	if eventID == "" {
		return ErrStore
	}
	next.Events = append(next.Events, Notification{ID: eventID, CreatedAt: m.now().UTC(), Kind: "test", State: "test", Status: "pending"})
	return m.save(next)
}

func (m *Manager) Close() error {
	m.cancel()
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lock.Close()
}

func decodeStrict(r io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil || len(data) > 64<<10 || !jsonunicode.Valid(data) || !uniqueJSON(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInput
	}
	return nil
}

// Equal is used only for content-free state, never to compare credentials.
func equal(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
