package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/limitedbuffer"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
	"github.com/cofy-x/axv/apps/server/internal/storage"
)

type codexInstallStatus struct {
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type guestCodexInfo struct {
	codex.Info
	AuthMode           string             `json:"authMode"`
	HostProxyAvailable bool               `json:"hostProxyAvailable"`
	FullAutoDefault    bool               `json:"fullAutoDefault"`
	Install            codexInstallStatus `json:"install"`
}

type guestCodex struct {
	service *service
	mu      sync.Mutex
	store   *storage.Store
	manager *codex.Manager
	handler *codexhttp.Handler
	mux     *http.ServeMux
	proxy   guestProxyBridge

	installMu sync.Mutex
	install   codexInstallStatus
}

func newGuestCodex(s *service) *guestCodex {
	return &guestCodex{
		service: s,
		install: codexInstallStatus{State: "idle"},
	}
}

func (g *guestCodex) close() {
	g.mu.Lock()
	handler := g.handler
	manager := g.manager
	store := g.store
	g.handler, g.manager, g.store, g.mux = nil, nil, nil, nil
	g.mu.Unlock()
	g.proxy.close()
	if handler != nil {
		handler.Close()
	}
	if manager != nil {
		manager.Close()
	}
	if store != nil {
		_ = store.Close()
	}
}

func (g *guestCodex) ensure() (*codex.Manager, *http.ServeMux, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.manager != nil && g.mux != nil {
		return g.manager, g.mux, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, nil, fmt.Errorf("guest home directory is unavailable")
	}
	profileHome := filepath.Join(home, ".local", "share", "axv", "codex")
	dataDir := filepath.Join(profileHome, "data")
	if err := os.MkdirAll(filepath.Join(profileHome, "run", "codex"), 0700); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, nil, err
	}
	store, err := storage.Open(filepath.Join(dataDir, "codex.sqlite"))
	if err != nil {
		return nil, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	manager, err := codex.New(store, codex.Options{
		DataDir:          dataDir,
		ProfileHome:      profileHome,
		WorkspaceDir:     g.service.config.ProjectRoot,
		RunnerExecutable: exe,
		RunnerSubcommand: "codex-runner",
		DefaultFullAuto:  true,
		ExtraEnv: func() []string {
			// Only inject zero-credential host proxy when no local OAuth/API key is active,
			// or when the host proxy is reachable and preferred.
			if detectLocalCodexAuth() != "none" {
				return nil
			}
			return g.proxy.env(g.service.ctx)
		},
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	handler := codexhttp.NewGuest(manager)
	mux := http.NewServeMux()
	handler.RegisterAt(mux, "/v1/codex")
	g.store, g.manager, g.handler, g.mux = store, manager, handler, mux
	return manager, mux, nil
}

func detectLocalCodexAuth() string {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		return "api_key"
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			codexHome = filepath.Join(home, ".codex")
		}
	}
	if codexHome != "" {
		if info, err := os.Stat(filepath.Join(codexHome, "auth.json")); err == nil && !info.IsDir() && info.Size() > 2 {
			return "oauth"
		}
	}
	return "none"
}

func (g *guestCodex) handleInfo(w http.ResponseWriter, r *http.Request) {
	manager, _, err := g.ensure()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	info, err := manager.Info(r.Context())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	hostProxy := g.proxy.probe(r.Context())
	authMode := detectLocalCodexAuth()
	if authMode == "none" && hostProxy {
		authMode = "proxy"
	}
	g.installMu.Lock()
	install := g.install
	g.installMu.Unlock()
	respond(w, http.StatusOK, guestCodexInfo{
		Info:               info,
		AuthMode:           authMode,
		HostProxyAvailable: hostProxy,
		FullAutoDefault:    true,
		Install:            install,
	})
}

func (g *guestCodex) handleInstallStatus(w http.ResponseWriter, _ *http.Request) {
	g.installMu.Lock()
	status := g.install
	g.installMu.Unlock()
	respond(w, http.StatusOK, status)
}

func (g *guestCodex) handleInstallStart(w http.ResponseWriter, _ *http.Request) {
	g.installMu.Lock()
	if g.install.State == "running" {
		status := g.install
		g.installMu.Unlock()
		respond(w, http.StatusAccepted, status)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	g.install = codexInstallStatus{
		State:     "running",
		Message:   "Installing official Codex CLI into VM root disk (~/.local/bin/codex)…",
		UpdatedAt: now,
	}
	status := g.install
	g.installMu.Unlock()

	go g.runInstall()
	respond(w, http.StatusAccepted, status)
}

func (g *guestCodex) runInstall() {
	finish := func(state, message string) {
		g.installMu.Lock()
		g.install = codexInstallStatus{
			State:     state,
			Message:   message,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		g.installMu.Unlock()
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		finish("failed", "Guest home directory is unavailable.")
		return
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		finish("failed", fmt.Sprintf("Create ~/.local/bin: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(g.service.ctx, 5*time.Minute)
	defer cancel()

	// Prefer npm if available inside the VM; otherwise install the official standalone GitHub release binary.
	var cmd *exec.Cmd
	if npmPath, err := exec.LookPath("npm"); err == nil {
		prefix := filepath.Join(home, ".local")
		cmd = exec.CommandContext(ctx, npmPath, "install", "--no-audit", "--no-fund", "--prefix", prefix, "@openai/codex@latest")
	} else {
		arch := "x86_64"
		if runtime.GOARCH == "arm64" {
			arch = "aarch64"
		}
		script := fmt.Sprintf(`set -euo pipefail
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ASSET="codex-%s-unknown-linux-musl.tar.gz"
URL="https://github.com/openai/codex/releases/latest/download/$ASSET"
curl -fsSL "$URL" -o "$TMP/$ASSET"
tar -xzf "$TMP/$ASSET" -C "$TMP"
BIN="$(find "$TMP" -maxdepth 2 -type f -name 'codex*' | head -n 1)"
test -n "$BIN"
install -m 0755 "$BIN" %q
`, arch, filepath.Join(binDir, "codex"))
		cmd = exec.CommandContext(ctx, "/bin/bash", "-lc", script)
	}
	var out limitedbuffer.Buffer
	out.Limit = 32 << 10
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(string(out.Data))
		if detail != "" {
			finish("failed", fmt.Sprintf("Codex install failed: %s", detail))
		} else {
			finish("failed", fmt.Sprintf("Codex install failed: %v", err))
		}
		return
	}
	finish("succeeded", "Official Codex CLI installed in ~/.local/bin/codex.")
}

func (g *guestCodex) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	_, mux, err := g.ensure()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_json", fmt.Errorf("could not read request body"))
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		fail(w, http.StatusBadRequest, "invalid_json", fmt.Errorf("expected a JSON object"))
		return
	}
	if projectRaw, ok := payload["projectPath"].(string); ok {
		rel := strings.TrimPrefix(projectRaw, g.service.config.ProjectRoot)
		rel = strings.TrimPrefix(rel, "/")
		resolved, err := g.service.directory(rel)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_directory", err)
			return
		}
		payload["projectPath"] = resolved
	} else {
		resolved, err := g.service.directory("")
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_directory", err)
			return
		}
		payload["projectPath"] = resolved
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal_error", err)
		return
	}
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(rewritten))
	clone.ContentLength = int64(len(rewritten))
	mux.ServeHTTP(w, clone)
}

func (g *guestCodex) handleNotifications(w http.ResponseWriter, r *http.Request) {
	manager, _, err := g.ensure()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	batches, err := manager.NotificationSources(r.Context(), map[string]uint64{})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "notification_activity_unavailable", err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"batches": batches})
}

func (g *guestCodex) handleResolveNotification(w http.ResponseWriter, r *http.Request) {
	manager, _, err := g.ensure()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	var target notificationevent.Target
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&target); err != nil {
		fail(w, http.StatusBadRequest, "invalid_json", err)
		return
	}
	local := target
	local.HostID, local.MachineID, local.MachineName = "", "", ""
	resolution, err := manager.ResolveNotification(r.Context(), local)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "notification_activity_unavailable", err)
		return
	}
	resolution.Target = target
	respond(w, http.StatusOK, resolution)
}

func (g *guestCodex) handleProxy(w http.ResponseWriter, r *http.Request) {
	_, mux, err := g.ensure()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "codex_unavailable", err)
		return
	}
	mux.ServeHTTP(w, r)
}
