package hosttailscale

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

const runningStatus = `{"BackendState":"Running","Self":{"HostName":"host-machine","Online":true,"TailscaleIPs":["100.64.1.2","fd7a:115c:a1e0::2"]}}`

func TestStatusProjectionPreservesConnectionStatesWithoutPrivateData(t *testing.T) {
	for _, tc := range []struct {
		backend string
		online  bool
		want    State
	}{
		{"Running", true, StateConnected}, {"Running", false, StateDisconnected},
		{"Stopped", false, StateDisconnected}, {"NeedsLogin", false, StateNeedsLogin},
		{"NeedsMachineAuth", false, StateNeedsLogin}, {"Starting", false, StateDisconnected}, {"NoState", false, StateDisconnected},
	} {
		t.Run(tc.backend+"-"+string(tc.want), func(t *testing.T) {
			data, err := json.Marshal(map[string]any{
				"BackendState": tc.backend,
				"AuthURL":      "https://login.tailscale.com/a/private-enrollment",
				"User":         map[string]string{"123": "private@example.com"},
				"Peer":         map[string]string{"peer": "private-peer"},
				"Health":       []string{"/private/state"},
				"Self":         map[string]any{"HostName": "host-machine", "Online": tc.online, "UserID": "123", "TailscaleIPs": []string{"100.64.1.2", "invalid-address", "100.64.1.2", "fd7a:115c:a1e0::2", "fe80::1%private-interface"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			status := decodeStatus(data)
			if status.State != tc.want || !status.Installed || status.DaemonAvailable == nil || !*status.DaemonAvailable || status.BackendState != tc.backend || status.Hostname != "host-machine" || !slices.Equal(status.IPs, []string{"100.64.1.2", "fd7a:115c:a1e0::2"}) {
				t.Fatalf("connection status lost: %+v", status)
			}
			encoded, err := json.Marshal(status)
			if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "123") || strings.Contains(string(encoded), "AuthURL") {
				t.Fatalf("private status data escaped: %s, %v", encoded, err)
			}
		})
	}
	for _, data := range []string{`null`, `{}`, `[]`, `{"BackendState":"Running"} {}`, `{"BackendState":"https://login.tailscale.com/a/private"}`, `{"BackendState":"Running","Self":{"TailscaleIPs":[123]}}`} {
		status := decodeStatus([]byte(data))
		if status.State != StateUnavailable || status.DaemonAvailable != nil || status.BackendState != "" {
			t.Fatalf("invalid response was accepted: %s: %+v", data, status)
		}
	}
	status := decodeStatus([]byte(`{"BackendState":"Stopped","TailscaleIPs":["100.64.1.3"],"Self":{"HostName":"bad\nname"}}`))
	if status.Hostname != "" || !slices.Equal(status.IPs, []string{"100.64.1.3"}) {
		t.Fatalf("unsafe hostname or missing fallback addresses: %+v", status)
	}
}

func TestDetectorCoalescesCachesAndPreservesOtherRequesters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := New(ctx)
	t.Cleanup(d.Close)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	d.now = func() time.Time { return time.Unix(0, clock.Load()) }
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	d.find = func() (string, error) { return "/fixture/tailscale", nil }
	d.run = func(ctx context.Context, command string) ([]byte, error) {
		if command != "/fixture/tailscale" {
			return nil, errors.New("wrong executable")
		}
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return []byte(runningStatus), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first := make(chan Status, 1)
	go func() { first <- d.Status(firstCtx) }()
	<-entered
	var readers sync.WaitGroup
	results := make(chan Status, 32)
	for range 32 {
		readers.Go(func() { results <- d.Status(context.Background()) })
	}
	cancelFirst()
	if status := <-first; status.State != StateUnavailable {
		t.Fatalf("cancelled reader did not detach: %+v", status)
	}
	close(release)
	readers.Wait()
	close(results)
	for status := range results {
		if status.State != StateConnected {
			t.Fatalf("one cancelled reader affected others: %+v", status)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests spawned %d queries", calls.Load())
	}
	status := d.Status(context.Background())
	status.IPs[0], *status.DaemonAvailable = "changed", false
	if next := d.Status(context.Background()); next.IPs[0] != "100.64.1.2" || !*next.DaemonAvailable || calls.Load() != 1 {
		t.Fatalf("cached status was mutated or queried again: %+v", next)
	}
	clock.Add(int64(15 * time.Second))
	if next := d.Status(context.Background()); next.State != StateConnected || calls.Load() != 2 {
		t.Fatalf("expired status did not refresh: %+v, queries %d", next, calls.Load())
	}
}

func TestDetectorKeepsMissingCommandAndUnavailableDaemonDistinct(t *testing.T) {
	for _, tc := range []struct {
		name      string
		findError error
		runError  error
		state     State
		installed bool
	}{
		{"missing", exec.ErrNotFound, nil, StateNotInstalled, false},
		{"cannot-inspect", fs.ErrPermission, nil, StateUnavailable, false},
		{"daemon-off-or-inaccessible", nil, errors.New("/private/state https://login.tailscale.com/a/private"), StateUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New(context.Background())
			t.Cleanup(d.Close)
			calls := 0
			d.find = func() (string, error) { return "/fixture/tailscale", tc.findError }
			d.run = func(context.Context, string) ([]byte, error) { calls++; return nil, tc.runError }
			status := d.Status(context.Background())
			if status.State != tc.state || status.Installed != tc.installed || status.DaemonAvailable != nil || strings.Contains(status.Message, "private") {
				t.Fatalf("misleading or unsafe status: %+v", status)
			}
			if tc.findError != nil && calls != 0 {
				t.Fatal("missing executable was run")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := New(ctx)
	t.Cleanup(d.Close)
	d.timeout = 10 * time.Millisecond
	d.find = func() (string, error) { return "/fixture/tailscale", nil }
	d.run = func(ctx context.Context, _ string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	if status := d.Status(context.Background()); status.State != StateUnavailable || !status.Installed {
		t.Fatalf("bounded query failure hidden: %+v", status)
	}
	cancel()
	d.find = func() (string, error) { t.Fatal("query ran after shutdown"); return "", nil }
	if status := d.Status(context.Background()); status.State != StateUnavailable {
		t.Fatalf("shutdown query did not stop: %+v", status)
	}
}

func TestDetectorCloseCancelsAndJoinsActiveQuery(t *testing.T) {
	d := New(context.Background())
	t.Cleanup(d.Close)
	entered, cancelled, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	d.find = func() (string, error) { return "/fixture/tailscale", nil }
	d.run = func(ctx context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		close(exited)
		return nil, ctx.Err()
	}
	queryDone := make(chan struct{})
	go func() { d.Status(context.Background()); close(queryDone) }()
	<-entered
	closed := make(chan struct{})
	go func() { d.Close(); close(closed) }()
	<-cancelled
	select {
	case <-closed:
		t.Fatal("detector close returned before its command exited")
	default:
	}
	close(release)
	<-closed
	select {
	case <-exited:
	default:
		t.Fatal("detector did not join the command")
	}
	<-queryDone
	if status := d.Status(context.Background()); status.State != StateUnavailable || calls.Load() != 1 {
		t.Fatalf("closed detector started another query: %+v, %d", status, calls.Load())
	}
}

func TestCommandDiscoverySupportsBundlesAndRejectsUnsafeLookup(t *testing.T) {
	info, err := fs.Stat(fstest.MapFS{"command": &fstest.MapFile{Mode: 0755}}, "command")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, tc := range []struct{ platform, candidate string }{
		{"darwin", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"},
		{"darwin", filepath.Join(home, "Applications/Tailscale.app/Contents/MacOS/Tailscale")},
		{"linux", "/usr/bin/tailscale"},
		{"windows", filepath.Join(home, "Tailscale/tailscale.exe")},
	} {
		path, err := findExecutable(tc.platform, func(string) (string, error) { return "", exec.ErrNotFound }, func(path string) (os.FileInfo, error) {
			if path == tc.candidate {
				return info, nil
			}
			return nil, fs.ErrNotExist
		}, func() (string, error) { return home, nil }, func(string) string { return home })
		if err != nil || path != tc.candidate {
			t.Fatalf("installed command not discovered for %s: %s, %v", tc.platform, path, err)
		}
	}
	_, err = findExecutable("linux", func(string) (string, error) { return "tailscale", exec.ErrDot }, func(string) (os.FileInfo, error) {
		t.Fatal("unsafe PATH lookup fell through to another executable")
		return nil, fs.ErrNotExist
	}, os.UserHomeDir, os.Getenv)
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("unsafe executable was accepted: %v", err)
	}
}
