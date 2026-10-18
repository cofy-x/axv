package compute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

const SupportedLimaVersion = "2.2.1"

type FirecrackerConfig struct {
	Binary  string `json:"binary"`
	Jailer  string `json:"jailer"`
	Version string `json:"version"`
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
	Subnet  string `json:"subnet"`
	DNS     string `json:"dns"`
}

type LimaConfig struct {
	Home    string `json:"home,omitempty"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
}

type Config struct {
	DataDir     string             `json:"dataDir"`
	Socket      string             `json:"socket"`
	SocketGroup *int               `json:"socketGroup,omitempty"`
	Capacity    Capacity           `json:"capacity"`
	Firecracker *FirecrackerConfig `json:"firecracker,omitempty"`
	Lima        *LimaConfig        `json:"lima,omitempty"`
	Images      []Image            `json:"images"`
}

var simpleID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func LoadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("host configuration: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return c, fmt.Errorf("host configuration must contain one JSON object")
	}
	if !filepath.IsAbs(c.DataDir) || !filepath.IsAbs(c.Socket) {
		return c, fmt.Errorf("dataDir and socket must be absolute paths")
	}
	if len(c.Socket) > 100 {
		return c, fmt.Errorf("socket path must be at most 100 bytes; use a short dedicated runtime directory")
	}
	if c.SocketGroup != nil && *c.SocketGroup < 0 {
		return c, fmt.Errorf("socketGroup must be nonnegative")
	}
	if c.Capacity.CPUs < 1 || c.Capacity.CPUs > 4096 || c.Capacity.MemoryMiB < 256 || c.Capacity.MemoryMiB > 1<<30 || c.Capacity.DiskGiB < 1 || c.Capacity.DiskGiB > 1<<30 || c.Capacity.Machines < 1 || c.Capacity.Machines > 100 || c.Capacity.Workers < 1 || c.Capacity.Workers > 16 {
		return c, fmt.Errorf("set explicit capacity: CPUs 1–4096, memory >=256 MiB, disk >=1 GiB, machines 1–100, workers 1–16")
	}
	if c.Firecracker == nil && c.Lima == nil {
		return c, fmt.Errorf("configure at least one VM provider")
	}
	seen := map[string]bool{}
	for _, image := range c.Images {
		if !simpleID.MatchString(image.ID) || seen[image.ID] {
			return c, fmt.Errorf("image IDs must be unique simple identifiers")
		}
		seen[image.ID] = true
		if image.Provider != "firecracker" && image.Provider != "lima" {
			return c, fmt.Errorf("unsupported image provider %q", image.Provider)
		}
		if image.Arch != "amd64" && image.Arch != "arm64" {
			return c, fmt.Errorf("image arch must be amd64 or arm64")
		}
		if !filepath.IsAbs(image.Rootfs) || !digest(image.RootfsSHA256) {
			return c, fmt.Errorf("image %s requires an absolute rootfs path and SHA-256", image.ID)
		}
		if image.Provider == "firecracker" && (!filepath.IsAbs(image.Kernel) || !digest(image.KernelSHA256)) {
			return c, fmt.Errorf("image %s requires an absolute kernel path and SHA-256", image.ID)
		}
		if image.Guest != nil {
			if err := image.Guest.Validate(); err != nil {
				return c, fmt.Errorf("image %s: %w", image.ID, err)
			}
		}

	}
	return c, nil
}

func digest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32
}

func (c Config) Image(id, provider string) (Image, error) {
	for _, image := range c.Images {
		if image.ID == id && image.Provider == provider {
			if image.Arch != runtime.GOARCH {
				return Image{}, Problem("unsupported", "Cross-architecture virtualization is not supported.")
			}
			return image, nil
		}
	}
	return Image{}, Problem("invalid_request", "Select a configured image for this provider.")
}

func (c Config) ValidateRequest(r CreateRequest) error {
	image, err := c.Image(r.Spec.ImageID, r.Spec.Provider)
	if err != nil {
		return err
	}
	return ValidateSpec(r.Spec, c.Capacity, image.MinRootGiB)
}

// Fingerprint identifies effective host settings when a launcher reuses a service.
func (c Config) Fingerprint() string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
