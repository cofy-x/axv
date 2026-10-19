package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/codex"
	codexhttp "github.com/cofy-x/axv/apps/server/internal/codex/httpapi"
	computegateway "github.com/cofy-x/axv/apps/server/internal/compute/gateway"
	"github.com/cofy-x/axv/apps/server/internal/config"
	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	hostdirhttp "github.com/cofy-x/axv/apps/server/internal/hostdir/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/hosttailscale"
	"github.com/cofy-x/axv/apps/server/internal/httpapi"
	"github.com/cofy-x/axv/apps/server/internal/projectfiles"
	"github.com/cofy-x/axv/apps/server/internal/storage"
	"github.com/cofy-x/axv/apps/server/internal/terminal"
	"github.com/cofy-x/axv/apps/server/internal/webui"
)

type Options struct {
	LaunchID           string
	RunnerExecutable   string
	ComputeGateway     *computegateway.Gateway
	PreparationHandler http.Handler
	ComputeSocket      string
	InstanceID         string
	Ready              func(string) error
}

// Run serves the desktop until a shutdown signal or listener error occurs.
func Run(settings config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunContext(ctx, settings, Options{ComputeSocket: os.Getenv("AXV_COMPUTE_SOCKET")})
}

func RunContext(ctx context.Context, settings config.Config, options Options) (result error) {
	listener, err := net.Listen("tcp", settings.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.MkdirAll(settings.DataDir, 0700); err != nil {
		return err
	}
	store, err := storage.Open(filepath.Join(settings.DataDir, "desktop.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	terminals := terminal.New()
	defer terminals.Close()
	codexSessions, err := codex.New(store, codex.Options{
		DataDir:          settings.DataDir,
		ProfileHome:      settings.Home,
		ConfigPath:       settings.ConfigPath,
		WorkspaceDir:     settings.WorkspaceDir,
		ProxyURL:         settings.ProxyURL,
		TmuxSocket:       settings.TmuxSocket,
		RunnerExecutable: options.RunnerExecutable,
		Logger:           diagnostics.FromContext(ctx).With("component", "codex"),
	})
	if err != nil {
		return err
	}
	codexAPI := codexhttp.New(codexSessions)
	defer func() {
		codexSessions.Close()
		codexAPI.Close()
	}()
	computeGateway := options.ComputeGateway
	if computeGateway == nil {
		computeGateway = computegateway.NewGateway(options.ComputeSocket, computegateway.GatewayOptions{
			ConfigPath:  settings.ConfigPath,
			ProfileHome: settings.Home,
		})
	}
	defer computeGateway.Close()
	application := http.NewServeMux()
	projectfiles.Register(application, store)
	if options.InstanceID != "" {
		application.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": options.InstanceID, "launchId": options.LaunchID})
		})
	}
	if options.PreparationHandler != nil {
		application.Handle("/api/compute/preparation/", options.PreparationHandler)
	}
	application.Handle("/api/compute/", computeGateway)
	application.Handle("/", httpapi.New(store, webui.Handler()))
	handler := newHandler(application, terminals, codexAPI)
	remoteStatus, stopRemote := startRemote(ctx, settings, handler)
	defer func() {
		if err := stopRemote(); err != nil {
			diagnostics.FromContext(ctx).Error("remote.close_failed", err)
			result = errors.Join(result, fmt.Errorf("close remote access: %w", err))
		}
	}()
	application.Handle("GET /api/remote/status", remoteStatusHandler(options.InstanceID, remoteStatus))
	hostTailscale := hosttailscale.New(ctx)
	defer hostTailscale.Close()
	application.Handle("GET /api/connection", connectionHandler(remoteStatus, hostTailscale))
	server := &http.Server{
		Addr:              settings.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	// Launchers use the address of this bound listener, never another process's
	// health endpoint. Port zero lets development and tests allocate safely.
	address := listener.Addr().(*net.TCPAddr)
	advertised := address.IP
	if advertised.IsUnspecified() {
		if advertised.To4() != nil {
			advertised = net.ParseIP("127.0.0.1")
		} else {
			advertised = net.IPv6loopback
		}
	}
	actualURL := "http://" + net.JoinHostPort(advertised.String(), fmt.Sprint(address.Port))
	if options.Ready != nil {
		if err := options.Ready(actualURL); err != nil {
			_ = server.Close()
			return err
		}
	}
	fmt.Printf("DESKTOP_READY %s\n", actualURL)
	log.Printf("axv desktop: %s (data: %s)", actualURL, settings.DataDir)
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// HTTP Shutdown excludes hijacked connections; close shells explicitly first.
	terminals.Close()
	codexSessions.Close()
	codexAPI.Close()
	return server.Shutdown(shutdown)
}

func newHandler(application http.Handler, terminals *terminal.Manager, codexAPI *codexhttp.Handler) http.Handler {
	mux := http.NewServeMux()
	codexAPI.Register(mux)
	hostdirhttp.Register(mux)
	mux.HandleFunc("GET /api/terminal/info", terminals.Info)
	mux.HandleFunc("GET /api/terminal", terminals.Connect)
	mux.Handle("/", application)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}
