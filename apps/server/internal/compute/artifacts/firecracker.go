package artifacts

import (
	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

// FirecrackerGuest pins the published amd64 Guest and its matching, separately
// distributed kernel. The archive includes the original distribution materials.
type FirecrackerGuest struct {
	Bundle      guestimage.Bundle
	RootfsBytes int64
	Archive     artifactcache.Artifact
	Kernel      artifactcache.Artifact
}

func DefaultFirecrackerGuest() FirecrackerGuest {
	const version = "0.1.0-guest.2"
	return FirecrackerGuest{
		RootfsBytes: 2146435072,
		Bundle: guestimage.Bundle{
			Version: 1, ID: "axv-firecracker-amd64-00e852d0209ef1708b9323dcfcd79a67",
			Name: "AXV Firecracker amd64", Provider: "firecracker", Arch: "amd64",
			Rootfs: guestimage.Artifact{File: "root.ext4", SHA256: "00e852d0209ef1708b9323dcfcd79a67f175b8e86aaf7c0d93887f193937c2c7"},
			Guest: &guestimage.Contract{
				ImageVersion: 1, ProtocolVersion: 3,
				Capabilities: []string{"exec", "terminals", "ssh", "network", "project-files", "project-tasks", "project-previews", "project-restore", "project-initialization"},
				BinarySHA256: "dc185d6be492b94b9b88bd11f30c97a64d398413eaeaa2039722d0208f3f7338",
			},
		},
		Archive: artifactcache.Artifact{
			ID: "axv-firecracker-amd64", Name: "AXV Guest " + version,
			URL:    "https://github.com/cofy-x/axv/releases/download/v" + version + "/axv_guest_" + version + "_firecracker_amd64.tar.gz",
			SHA256: "e16b710aa4dae51928ba5b81207762fbd8f6332a7cc565c90f179e0b3fdae532",
			Bytes:  95199062, MaxBytes: 95199062,
		},
		Kernel: artifactcache.Artifact{
			ID: "firecracker-kernel-amd64", Name: "Firecracker Linux 6.1.155 kernel",
			URL:    "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155",
			SHA256: "e41c7048bd2475e7e788153823fcb9166a7e0b78c4c443bd6446d015fa735f53",
			Bytes:  44278288, MaxBytes: 44278288,
		},
	}
}
