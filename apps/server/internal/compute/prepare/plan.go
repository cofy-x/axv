package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/artifacts"
	"github.com/cofy-x/axv/apps/server/internal/compute/internal/providerutil"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

type artifact = artifactcache.Artifact
type artifactCatalog = artifacts.Catalog

func productionCatalog() artifactCatalog { return artifacts.Official() }

func (e *Engine) Plan(ctx context.Context, request Request) (Plan, error) {
	check, err := e.Check(ctx)
	if err != nil {
		return Plan{}, err
	}
	r := mergeRequest(check.Defaults, request)
	p := Plan{CacheHome: e.cache.Home, ID: compute.NewID("plan"), CreatedAt: now(), ExpiresAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano), Platform: e.platform, Arch: e.arch, Request: r, Actions: []string{}, Downloads: []Download{}, Checks: append([]Requirement{}, check.Checks...), CanStart: true, ConfigPath: filepath.Join(e.options.Home, "config.json"), HostConfigPath: filepath.Join(e.options.Home, "host.json"), Activation: "configured"}
	// Plans perform fresh dependency checks; cached status must not preserve a
	// blocker that the user has already resolved since the previous poll.
	filteredChecks := p.Checks[:0]
	for _, condition := range p.Checks {
		if condition.ID != "platform_dependencies" && condition.ID != "lima_dependency" && condition.ID != "dependency_check" {
			filteredChecks = append(filteredChecks, condition)
		}
	}
	p.Checks = filteredChecks
	if e.options.Activator != nil {
		p.Activation = "ready"
	}
	spec := specification{Plan: p, LimaVersion: compute.SupportedLimaVersion, LimaArtifact: e.catalog.Lima[e.arch]}
	spec.OriginalCompute, spec.ComputeFingerprint, err = readCompute(e.options.Home)
	if err != nil {
		p.Checks = append(p.Checks, blocked("configuration", "Profile configuration", err.Error()))
	}
	current, profileErr := profile.Read(e.options.Home)
	if profileErr == nil {
		spec.ComputeMode = current.Compute.Mode
	}
	if check.LockedReason != "" {
		p.Checks = append(p.Checks, blocked("configuration_locked", "Configuration source", check.LockedReason))
	}
	if check.Configured && profileErr == nil {
		spec.Existing = true
		if !sameExistingRequest(check.Defaults, request) {
			p.Checks = append(p.Checks, blocked("existing_configuration", "Existing configuration", "VM hosting is already configured. Preparation preserves its storage and capacity; changing them requires an explicit maintenance operation."))
		}
		spec.ComputeJSON, _ = json.Marshal(current.Compute)
		p.Provider = "firecracker"
		if current.Compute.Mode == "managed" {
			h, loadErr := compute.LoadConfig(current.Compute.HostConfig)
			if loadErr != nil {
				p.Checks = append(p.Checks, blocked("host_configuration", "VM service configuration", loadErr.Error()))
			} else {
				spec.HostJSON, _ = json.Marshal(h)
				if h.Lima != nil {
					p.Provider = "lima"
				}
			}
			p.HostConfigPath = current.Compute.HostConfig
		}
		p.Actions = append(p.Actions, "Preserve the existing VM configuration and storage.", "Verify and connect the local VM service.")
	} else if e.platform == "darwin" && (r.HostConfig != "" || r.Socket != "") {
		spec.Imported = true
		p.Provider = "lima"
		filtered := p.Checks[:0]
		for _, condition := range p.Checks {
			if condition.ID != "guest" {
				filtered = append(filtered, condition)
			}
		}
		p.Checks = filtered
		if r.HostConfig != "" && r.Socket != "" {
			p.Checks = append(p.Checks, blocked("local_service", "Local VM service", "Choose --socket or --host-config."))
		} else if r.HostConfig != "" {
			if !filepath.IsAbs(r.HostConfig) {
				p.Checks = append(p.Checks, blocked("local_service", "Local VM configuration", "The host configuration path must be absolute."))
			} else if host, loadErr := compute.LoadConfig(r.HostConfig); loadErr != nil {
				p.Checks = append(p.Checks, blocked("local_service", "Local VM configuration", loadErr.Error()))
			} else if host.Lima == nil || host.Firecracker != nil {
				p.Checks = append(p.Checks, blocked("local_service", "Local VM configuration", "Managed macOS hosting requires a Lima configuration."))
			} else {
				if probeErr := validateLima(ctx, host.Lima.Binary, e.arch); probeErr != nil {
					p.Checks = append(p.Checks, blocked("lima", "Lima installation", probeErr.Error()))
				}
				spec.HostJSON, _ = json.Marshal(host)
				spec.ComputeJSON, _ = json.Marshal(profile.Compute{Mode: "managed", HostConfig: r.HostConfig})
				p.HostConfigPath = r.HostConfig
			}
		} else if !filepath.IsAbs(r.Socket) || len(r.Socket) > 100 {
			p.Checks = append(p.Checks, blocked("local_service", "Local VM service", "The Unix socket must be absolute and at most 100 bytes."))
		} else {
			binding, probeErr := probeLocalService(ctx, r.Socket, false)
			if probeErr != nil {
				p.Checks = append(p.Checks, blocked("local_service", "Local VM service", probeErr.Error()))
			} else {
				spec.ExpectedHostID = binding.HostID
			}
			spec.ComputeJSON, _ = json.Marshal(profile.Compute{Mode: "external", Socket: r.Socket})
		}
		p.Actions = append(p.Actions, "Preserve the selected local VM service configuration and all existing VM storage.", "Save this profile's reference to that configuration and verify the local service.")
	} else if e.platform == "linux" {
		p.Provider = "firecracker"
		p.Request.Socket, err = e.localServiceSocket(r)
		if err != nil {
			p.Checks = append(p.Checks, blocked("local_service", "Local Firecracker service", err.Error()))
		} else {
			binding, probeErr := probeLocalService(ctx, p.Request.Socket, true)
			if probeErr != nil {
				p.Checks = append(p.Checks, blocked("local_service", "Local Firecracker service", probeErr.Error()))
			} else {
				spec.ExpectedHostID = binding.HostID
				p.Checks = append(p.Checks, ready("local_service", "Local Firecracker service", "The service on this computer has a compatible identity, protocol and available Firecracker provider."))
			}
		}
		p.Actions = append(p.Actions, "Verify the existing Firecracker system service on this computer.", "Save its local Unix socket in this AXV profile.", "Connect the desktop without starting or stopping the system service.")
	} else if e.platform == "darwin" {
		p.Provider = "lima"
		p.Request, err = e.validateStorage(r)
		if err != nil {
			p.Checks = append(p.Checks, blocked("storage", "VM storage", err.Error()))
		}
		if err = validateCapacity(r); err != nil {
			p.Checks = append(p.Checks, blocked("capacity", "VM capacity", err.Error()))
		}
		if err = macPrerequisites(ctx, e.arch); err != nil {
			p.Checks = append(p.Checks, blocked("platform", "macOS virtualization", err.Error()))
		} else {
			p.Checks = append(p.Checks, ready("platform", "macOS virtualization", "Native VZ and system SSH tools are available."))
		}
		lima, ok := e.catalog.Lima[e.arch]
		if !ok {
			p.Checks = append(p.Checks, blocked("architecture", "VM architecture", "This architecture has no validated VM artifacts."))
		} else {
			spec.LimaBinary = e.matchingLima(ctx)
			if _, statErr := os.Lstat(e.privateLima()); statErr == nil {
				if verifyErr := verifyInstallation(ctx, e.privateLima(), lima.SHA256); verifyErr != nil {
					p.Checks = append(p.Checks, blocked("private_lima", "Private Lima installation", verifyErr.Error()))
				}
			}
			if spec.LimaBinary == "" {
				spec.LimaBinary = filepath.Join(e.privateLima(), "bin", "limactl")
				p.Downloads = append(p.Downloads, Download{ID: lima.ID, Name: lima.Name, URL: lima.URL, SHA256: lima.SHA256, Bytes: lima.Bytes})
				p.Actions = append(p.Actions, "Install verified Lima 2.2.1 privately in "+e.privateLima()+".")
			} else {
				p.Actions = append(p.Actions, "Reuse the verified Lima 2.2.1 installation at "+spec.LimaBinary+".")
			}
			switch p.Request.ImageSource {
			case "", "official":
				release, published := e.catalog.AXV["lima/"+e.arch]
				if !published {
					p.Checks = append(p.Checks, blocked("release_pending", "Official AXV image", "Official AXV image publication is incomplete. No download URL is available. Advanced custom preparation is available explicitly."))
				} else if releaseErr := release.Validate("lima", e.arch); releaseErr != nil {
					p.Checks = append(p.Checks, blocked("release_invalid", "Official AXV image", releaseErr.Error()))
				} else {
					spec.ImageBundle, spec.ImageArtifact, spec.ImageShared = release.Bundle, release.Artifact, true
					p.Checks = append(p.Checks, ready("official_image", "Official AXV image", "Release "+release.Version+" for "+e.arch+"; artifact digest and capability contract are bound to this plan."))
					p.Downloads = append(p.Downloads, Download{ID: release.Artifact.ID, Name: release.Artifact.Name, URL: release.Artifact.URL, SHA256: release.Artifact.SHA256, Bytes: release.Artifact.Bytes})
				}
			case "ubuntu-base":
				base, exists := e.catalog.Ubuntu[e.arch]
				if !exists {
					p.Checks = append(p.Checks, blocked("architecture", "Ubuntu base", "No reviewed Ubuntu artifact exists for this architecture."))
				} else {
					spec.ImageArtifact, spec.ImageShared = base, true
					spec.ImageBundle = guestimage.Bundle{Version: guestimage.Version, ID: "ubuntu-base", Name: base.Name, Provider: "lima", Arch: e.arch, Rootfs: guestimage.Artifact{SHA256: base.SHA256}}
					p.Downloads = append(p.Downloads, Download{ID: base.ID, Name: base.Name, URL: base.URL, SHA256: base.SHA256, Bytes: base.Bytes})
					p.Checks = append(p.Checks, ready("base_image", "Ubuntu base (default transition)", "Pinned official Ubuntu base: create, start, stop, restart and delete are available. AXV guest enhancements are not installed; official AXV image publication is pending."))
				}
			case "bundle":
				bundle, bundleErr := guestimage.LoadBundle(ctx, p.Request.ImageBundle, "lima", e.arch)
				if bundleErr != nil {
					p.Checks = append(p.Checks, blocked("image_bundle", "Custom image bundle", bundleErr.Error()))
				} else {
					spec.ImageBundle, spec.ImageSource = bundle, bundle.RootfsPath
					spec.ImageArtifact = artifact{ID: "image", Name: bundle.Name, SHA256: bundle.Rootfs.SHA256, MaxBytes: compute.MaxImageBytes}
					p.Checks = append(p.Checks, ready("image_bundle", "Custom image bundle", "Local rootfs checksum verified. Enhanced guest capabilities require a compatible runtime handshake."))
				}
			default:
				p.Checks = append(p.Checks, blocked("image_source", "Image source", "Select official, bundle or ubuntu-base."))
			}
		}
		p.Actions = append(p.Actions, "Verify and copy the selected immutable VM image into "+filepath.Join(p.Request.StorageRoot, "images")+".", "Create VM storage in "+filepath.Join(p.Request.StorageRoot, "compute")+" and Lima state in "+p.Request.LimaHome+".", "Save the reviewed VM capacity and local service configuration.")
		if e.options.Activator != nil {
			p.Actions = append(p.Actions, "Start and verify the local VM service, then connect this desktop.")
		}
	}
	for i := range p.Downloads {
		download := &p.Downloads[i]
		a := spec.LimaArtifact
		if download.SHA256 == spec.ImageArtifact.SHA256 {
			a = spec.ImageArtifact
		}
		download.URL, download.OriginURL = a.URL, a.OriginURL
		if a.OriginURL != "" {
			p.Checks = append(p.Checks, ready("artifact_source_"+a.ID, "Artifact download source", "Explicit mirror: "+a.URL+"; original: "+a.OriginURL+". No automatic fallback; the pinned SHA-256 is unchanged."))
		}
		status, cacheErr := e.cache.Status(ctx, a)
		if cacheErr != nil {
			p.Checks = append(p.Checks, blocked("cache", "Download cache", cacheErr.Error()))
			continue
		}
		download.CacheState, download.CachePath, download.Cached = status.State, status.Path, status.State == "ready"
	}
	if check.ActiveJob != nil {
		p.Checks = append(p.Checks, blocked("active_task", "Preparation task", "Another preparation task is already running. Follow it before starting a new task."))
	}
	for _, condition := range p.Checks {
		if condition.Status == "blocked" {
			p.CanStart = false
		}
	}
	spec.Plan = p
	if err = e.store.savePlan(ctx, spec); err != nil {
		return Plan{}, err
	}
	e.options.Logger.Info("preparation.plan_created", "planId", p.ID, "provider", p.Provider, "canStart", p.CanStart)
	return p, nil
}

func mergeRequest(defaults, request Request) Request {
	r := defaults
	if request.StorageRoot != "" {
		r.StorageRoot = request.StorageRoot
		if request.LimaHome == "" {
			r.LimaHome = filepath.Join(r.StorageRoot, "lima")
		}
	}
	if request.LimaHome != "" {
		r.LimaHome = request.LimaHome
	}
	if request.CPUs != 0 {
		r.CPUs = request.CPUs
	}
	if request.MemoryMiB != 0 {
		r.MemoryMiB = request.MemoryMiB
	}
	if request.DiskGiB != 0 {
		r.DiskGiB = request.DiskGiB
	}
	if request.Machines != 0 {
		r.Machines = request.Machines
	}
	if request.Socket != "" {
		r.Socket = request.Socket
	}
	r.HostConfig = request.HostConfig
	if request.ImageSource != "" {
		r.ImageSource = request.ImageSource
	}
	if request.ImageBundle != "" {
		if request.ImageSource == "" {
			r.ImageSource = "bundle"
		}
		r.ImageBundle = request.ImageBundle
	}
	return r
}

func sameExistingRequest(defaults, request Request) bool {
	r := mergeRequest(defaults, request)
	return r.StorageRoot == defaults.StorageRoot && r.LimaHome == defaults.LimaHome && r.CPUs == defaults.CPUs && r.MemoryMiB == defaults.MemoryMiB && r.DiskGiB == defaults.DiskGiB && r.Machines == defaults.Machines && r.Socket == defaults.Socket && r.HostConfig == "" && r.ImageSource == defaults.ImageSource && r.ImageBundle == defaults.ImageBundle
}

func validateCapacity(r Request) error {
	if r.CPUs < 1 || r.CPUs > 4096 || r.MemoryMiB < 256 || r.MemoryMiB > 1<<30 || r.DiskGiB < 1 || r.DiskGiB > 1<<30 || r.Machines < 1 || r.Machines > 100 {
		return fmt.Errorf("use CPUs 1–4096, memory 256 MiB or more, disk 1 GiB or more, and 1–100 machines")
	}
	return nil
}

func (e *Engine) validateStorage(r Request) (Request, error) {
	var err error
	if r.StorageRoot, err = absoluteDirectory(r.StorageRoot); err != nil {
		return r, err
	}
	if r.LimaHome, err = absoluteDirectory(r.LimaHome); err != nil {
		return r, err
	}
	if !shortLimaHome(r.LimaHome) {
		return r, fmt.Errorf("Lima's SSH socket path would exceed macOS's limit. Choose a shorter AXV_HOME (AXV_DEV_HOME for development), or explicitly select a shorter dedicated Lima directory in advanced settings")
	}
	if len(filepath.Join(e.options.Home, "host", "control.sock")) > 100 {
		return r, fmt.Errorf("AXV_HOME is too long for the VM service socket; use a shorter profile path")
	}
	resolvedCache, cacheErr := absoluteDirectory(e.cache.Home)
	if cacheErr != nil {
		return r, cacheErr
	}
	for _, managed := range []string{filepath.Join(r.StorageRoot, "compute"), filepath.Join(r.StorageRoot, "images"), r.LimaHome} {
		if within(resolvedCache, managed) || within(managed, resolvedCache) {
			return r, fmt.Errorf("VM runtime directories must be separate from AXV_CACHE_HOME")
		}
	}
	for _, reserved := range []string{filepath.Join(r.StorageRoot, "compute"), filepath.Join(r.StorageRoot, "images"), e.dir} {
		if within(reserved, r.LimaHome) || within(r.LimaHome, reserved) {
			return r, fmt.Errorf("Lima state must be separate from VM storage, image cache and preparation state")
		}
	}
	if within(e.dir, r.StorageRoot) {
		return r, fmt.Errorf("VM storage cannot be inside preparation state")
	}
	if _, err = os.Lstat(filepath.Join(e.options.Home, "host.json")); err == nil {
		return r, fmt.Errorf("host.json already exists; preserve and explicitly configure its original VM service instead of replacing it")
	} else if !os.IsNotExist(err) {
		return r, err
	}
	for _, path := range []string{filepath.Join(r.StorageRoot, "compute"), r.LimaHome, filepath.Join(e.options.Home, "host")} {
		if err = requireEmptyDirectory(path); err != nil {
			return r, err
		}
	}
	if err = checkOwnedOrEmpty(filepath.Join(r.StorageRoot, "images"), e.owner+":images"); err != nil {
		return r, err
	}
	if err = checkOwnedOrEmpty(filepath.Join(e.options.Home, "tools"), e.owner+":tools"); err != nil {
		return r, err
	}
	return r, nil
}

func absoluteDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return path, fmt.Errorf("storage paths must be absolute")
	}
	path = filepath.Clean(path)
	parent, err := existingParent(path)
	if err != nil {
		return path, err
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return path, err
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return path, err
	}
	return filepath.Join(real, rel), nil
}

func existingParent(path string) (string, error) {
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("storage path is not a directory: %s", path)
			}
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(path)
		if next == path {
			return "", err
		}
		path = next
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func requireEmptyDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime path must be a real directory: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("existing non-empty runtime directory requires its original configuration: %s", path)
	}
	return nil
}

func checkOwnedOrEmpty(path, owner string) error {
	if err := requireEmptyDirectory(path); err == nil {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact directory must be a real directory: %s", path)
	}
	return owneddir.Check(path, owner)
}

func (e *Engine) localServiceSocket(r Request) (string, error) {
	if r.Socket != "" && r.HostConfig != "" {
		return "", fmt.Errorf("choose a local socket or a host configuration file")
	}
	socket := r.Socket
	if r.HostConfig != "" {
		if !filepath.IsAbs(r.HostConfig) {
			return "", fmt.Errorf("host configuration path must be absolute")
		}
		c, err := compute.LoadConfig(r.HostConfig)
		if err != nil {
			return "", err
		}
		if c.Firecracker == nil {
			return "", fmt.Errorf("Linux preparation requires a Firecracker system service")
		}
		socket = c.Socket
	}
	if !filepath.IsAbs(socket) || len(socket) > 100 || strings.ContainsRune(socket, 0) {
		return "", fmt.Errorf("provide an absolute local Unix socket path of at most 100 bytes")
	}
	return filepath.Clean(socket), nil
}

func macPrerequisites(ctx context.Context, arch string) error {
	if arch != "arm64" && arch != "amd64" {
		return fmt.Errorf("native arm64 or amd64 architecture is required")
	}
	for _, name := range []string{"ssh", "scp", "ssh-keygen"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("system tool %s is required", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b, err := providerutil.Command(ctx, "/usr/bin/sw_vers", "-productVersion")
	if err != nil {
		return fmt.Errorf("check macOS version: %w", err)
	}
	major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(string(b)), ".")[0])
	if major < 13 {
		return fmt.Errorf("Lima VZ requires macOS 13 or later")
	}
	b, err = providerutil.Command(ctx, "/usr/sbin/sysctl", "-n", "kern.hv_support")
	if err != nil || strings.TrimSpace(string(b)) != "1" {
		return fmt.Errorf("native hardware virtualization is unavailable")
	}
	return nil
}

func (e *Engine) privateLima() string {
	a := e.catalog.Lima[e.arch]
	pin := a.SHA256
	if len(pin) > 12 {
		pin = pin[:12]
	}
	if pin == "" {
		pin = "unsupported"
	}
	return filepath.Join(e.options.Home, "tools", "lima", compute.SupportedLimaVersion, e.arch+"-"+pin)
}
func imageCache(root string, a artifact) string {
	return filepath.Join(root, "images", "axv-image-"+a.SHA256+".img")
}

func (e *Engine) matchingLima(ctx context.Context) string {
	root := e.privateLima()
	if err := verifyInstallation(ctx, root, e.catalog.Lima[e.arch].SHA256); err != nil {
		return ""
	}
	binary := filepath.Join(root, "bin", "limactl")
	if validateLima(ctx, binary, e.arch) != nil {
		return ""
	}
	return binary
}

func (e *Engine) lightweightLima() string {
	private := filepath.Join(e.privateLima(), "bin", "limactl")
	if info, err := os.Lstat(private); err == nil && info.Mode().IsRegular() {
		return private
	}
	return ""
}

func validateLima(ctx context.Context, binary, arch string) error {
	info, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return fmt.Errorf("Lima executable is not an executable regular file")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b, err := providerutil.Command(ctx, binary, "--version")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || strings.TrimPrefix(fields[len(fields)-1], "v") != compute.SupportedLimaVersion {
		return fmt.Errorf("Lima must report validated version %s", compute.SupportedLimaVersion)
	}
	name := "x86_64"
	if arch == "arm64" {
		name = "aarch64"
	}
	root := filepath.Dir(filepath.Dir(binary))
	guest := filepath.Join(root, "share", "lima", "lima-guestagent.Linux-"+name)
	for _, path := range []string{guest, guest + ".gz"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf("Lima installation is missing its native Linux guest agent")
}

func probeLocalService(ctx context.Context, socket string, requireFirecracker bool) (Binding, error) {
	var response struct {
		Binding
		Providers []compute.Capability `json:"providers"`
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host/v1/info", nil)
	if err != nil {
		return Binding{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return Binding{}, fmt.Errorf("the local VM service could not be reached: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Binding{}, fmt.Errorf("local VM service returned HTTP %d", res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&response); err != nil {
		return Binding{}, err
	}
	if response.HostID == "" || response.ProtocolVersion != compute.ProtocolVersion || response.ConfigFingerprint == "" {
		return Binding{}, fmt.Errorf("local VM service has an incompatible identity or protocol")
	}
	if requireFirecracker {
		available := false
		for _, p := range response.Providers {
			if p.Provider == "firecracker" && p.Available {
				available = true
			}
		}
		if !available {
			return Binding{}, fmt.Errorf("the local system service has no available Firecracker provider")
		}
	}
	return response.Binding, nil
}

func stepsFor(spec specification) []Step {
	steps := []Step{}
	add := func(id, label string) { steps = append(steps, Step{ID: id, Label: label, Status: "pending"}) }
	if !spec.Existing {
		add("check", "Check the reviewed configuration")
		if spec.Plan.Provider == "lima" && !spec.Imported {
			add("lima", "Prepare Lima")
			add("image", "Prepare the selected VM image")
		} else {
			add("connect", "Verify the local system service")
		}
		add("commit", "Save configuration")
	}
	add("activate", "Connect the local VM service")
	return steps
}
