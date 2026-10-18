package image

import (
	"fmt"
	"os"
	"runtime"

	"github.com/cofy-x/axv/apps/server/internal/filedigest"
)

const InstalledManifest = "/usr/share/axv/image.json"

type Installed struct {
	Version  int      `json:"version"`
	Provider string   `json:"provider"`
	Arch     string   `json:"arch"`
	Guest    Contract `json:"guest"`
}

// ReadInstalled is intentionally read-only. Package installation and binary
// replacement belong to the image builder, never the running guest service.
func ReadInstalled() (Installed, error) {
	return readInstalled(InstalledManifest, os.Executable)
}

func readInstalled(path string, executable func() (string, error)) (Installed, error) {
	var image Installed
	if err := decode(path, &image); err != nil {
		return image, fmt.Errorf("AXV image manifest: %w", err)
	}
	if image.Version != Version || image.Arch != runtime.GOARCH || (image.Provider != "lima" && image.Provider != "firecracker") {
		return image, fmt.Errorf("incompatible installed image format or architecture")
	}
	if err := image.Guest.Validate(); err != nil {
		return image, err
	}
	if err := image.Guest.Compatible(); err != nil {
		return image, err
	}
	if image.Guest.BinarySHA256 != "" {
		path, err := executable()
		if err != nil {
			return image, err
		}
		var verifier filedigest.Verifier
		if err = verifier.Verify(path, image.Guest.BinarySHA256); err != nil {
			return image, err
		}
	}
	return image, nil
}
