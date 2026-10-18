// Package artifacts owns reviewed public release pins. Empty AXV entries mean
// publication has not happened; callers must never invent a download URL.
package artifacts

import (
	"fmt"

	"github.com/cofy-x/axv/apps/server/internal/artifactcache"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
)

const LimaVersion = compute.SupportedLimaVersion

type Release struct {
	Version  string
	Bundle   guestimage.Bundle
	Artifact artifactcache.Artifact
}
type Catalog struct {
	Lima   map[string]artifactcache.Artifact
	Ubuntu map[string]artifactcache.Artifact
	AXV    map[string]Release
}

func Official() Catalog {
	const lima = "https://github.com/lima-vm/lima/releases/download/v" + LimaVersion + "/"
	const ubuntu = "https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-"
	return Catalog{
		Lima: map[string]artifactcache.Artifact{
			"arm64": {ID: "lima", Name: "Lima " + LimaVersion + " for Apple silicon", URL: lima + "lima-" + LimaVersion + "-Darwin-arm64.tar.gz", SHA256: "9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e", MaxBytes: 128 << 20, Bytes: 38328082},
			"amd64": {ID: "lima", Name: "Lima " + LimaVersion + " for Intel Mac", URL: lima + "lima-" + LimaVersion + "-Darwin-x86_64.tar.gz", SHA256: "6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4", MaxBytes: 128 << 20, Bytes: 24958659},
		},
		Ubuntu: map[string]artifactcache.Artifact{
			"arm64": {ID: "ubuntu", Name: "Ubuntu 24.04 base arm64 (20260926)", URL: ubuntu + "arm64.img", SHA256: "1d6bffe64b848468ac97f821d369a4846d983de1800ccf6b5ec8853e85cefc55", MaxBytes: 4 << 30, Bytes: 620224512},
			"amd64": {ID: "ubuntu", Name: "Ubuntu 24.04 base amd64 (20260926)", URL: ubuntu + "amd64.img", SHA256: "6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2", MaxBytes: 4 << 30, Bytes: 625612288},
		},
		AXV: map[string]Release{},
	}
}
func Lookup(id string) (artifactcache.Artifact, bool) {
	c := Official()
	for arch, a := range c.Lima {
		if id == "lima-"+arch {
			return a, true
		}
	}
	for arch, a := range c.Ubuntu {
		if id == "ubuntu-"+arch {
			return a, true
		}
	}
	return artifactcache.Artifact{}, false
}

func (r Release) Validate(provider, arch string) error {
	if r.Version == "" || r.Bundle.Version != guestimage.Version || r.Bundle.ID == "" || r.Bundle.Name == "" || r.Bundle.Provider != provider || r.Bundle.Arch != arch || r.Bundle.Guest == nil {
		return fmt.Errorf("official AXV release metadata is incomplete or mismatched")
	}
	if err := r.Bundle.Guest.Validate(); err != nil {
		return err
	}
	if err := r.Bundle.Guest.Compatible(); err != nil {
		return err
	}
	if r.Bundle.Rootfs.SHA256 != r.Artifact.SHA256 {
		return fmt.Errorf("official AXV release artifact does not match its bundle")
	}
	return r.Artifact.Validate()
}
