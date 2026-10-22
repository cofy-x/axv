// Package image defines the immutable guest image contract shared by image
// builders, environment preparation and provider startup.
package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

const Version = 1

var capabilityName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Contract declares enhanced guest features. A nil contract is an ordinary Linux
// image with provider lifecycle management only. BinarySHA256 checks the image's
// own bytes; it is never compared to the AXV application's build identity.
type Contract struct {
	ImageVersion    int      `json:"imageVersion"`
	ProtocolVersion int      `json:"protocolVersion"`
	Capabilities    []string `json:"capabilities"`
	BinarySHA256    string   `json:"binarySHA256,omitempty"`
}

// Capabilities returns the features implemented by this guest release. Each
// caller receives its own slice so image construction cannot mutate runtime
// capability negotiation.
func Capabilities() []string {
	return []string{"exec", "terminals", "ssh", "network", "project-files", "project-tasks", "project-previews", "project-restore", "project-initialization"}
}

func Has(capabilities []string, name string) bool {
	for _, value := range capabilities {
		if value == name {
			return true
		}
	}
	return false
}
func (c Contract) Compatible() error {
	if c.ImageVersion != Version || c.ProtocolVersion != protocol.Version {
		return fmt.Errorf("AXV guest requires image contract %d and protocol %d; basic VM management remains available", Version, protocol.Version)
	}
	return nil
}
func (c Contract) Validate() error {
	if c.ImageVersion < 1 || c.ProtocolVersion < 1 {
		return fmt.Errorf("guest contract needs positive image and protocol versions")
	}
	seen := map[string]bool{}
	for _, capability := range c.Capabilities {
		if !capabilityName.MatchString(capability) || seen[capability] || len(c.Capabilities) > 64 {
			return fmt.Errorf("guest capabilities must be at most 64 unique lowercase names")
		}
		seen[capability] = true
	}
	if c.BinarySHA256 != "" {
		b, err := hex.DecodeString(c.BinarySHA256)
		if err != nil || len(b) != sha256.Size {
			return fmt.Errorf("invalid guest integrity digest")
		}
	}
	return nil
}
