// image-manifest is an offline image-builder tool. It never runs during VM start.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	guestimage "github.com/cofy-x/axv/apps/server/internal/guest/image"
	guestproto "github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func digest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > guestimage.MaxRootfsBytes {
		return "", fmt.Errorf("image input must be a nonempty regular file of at most %d bytes", guestimage.MaxRootfsBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func run(args []string) error {
	flags := flag.NewFlagSet("image-manifest", flag.ContinueOnError)
	checkBundle := flags.String("check-bundle", "", "validate a complete current-release bundle manifest without executing its guest")
	provider := flags.String("provider", "", "lima or firecracker")
	arch := flags.String("arch", "", "amd64 or arm64")
	guest := flags.String("guest", "", "preinstalled guest executable")
	rootfs := flags.String("rootfs", "", "built rootfs; omit for the in-image manifest")
	output := flags.String("output", "", "new JSON file")
	id := flags.String("id", "", "immutable bundle identifier; defaults to platform and rootfs digest")
	name := flags.String("name", "", "bundle display name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *checkBundle != "" {
		if flags.NArg() != 0 || flags.NFlag() != 1 {
			return fmt.Errorf("--check-bundle cannot be combined with image build arguments")
		}
		return checkReleaseBundle(*checkBundle)
	}
	if flags.NArg() != 0 || *output == "" {
		return fmt.Errorf("supply --output and image build arguments")
	}
	if err := guestimage.ValidatePlatform(*provider, *arch); err != nil {
		return err
	}
	sum, err := digest(*guest)
	if err != nil {
		return err
	}
	contract := guestimage.Contract{ImageVersion: guestimage.Version, ProtocolVersion: guestproto.Version, Capabilities: guestimage.Capabilities(), BinarySHA256: sum}
	if err = contract.Validate(); err != nil {
		return err
	}
	var value any = guestimage.Installed{Version: guestimage.Version, Provider: *provider, Arch: *arch, Guest: contract}
	if *rootfs != "" {
		if *name == "" {
			return fmt.Errorf("bundle requires --name")
		}
		sum, err = digest(*rootfs)
		if err != nil {
			return err
		}
		if *id == "" {
			*id = fmt.Sprintf("axv-%s-%s-%s", *provider, *arch, sum[:32])
		}
		bundle := guestimage.Bundle{Version: guestimage.Version, ID: *id, Name: *name, Provider: *provider, Arch: *arch, Guest: &contract, Rootfs: guestimage.Artifact{File: filepath.Base(*rootfs), SHA256: sum}}
		if err = bundle.Validate(); err != nil {
			return err
		}
		value = bundle
	}
	f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(value); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Keep release checks on the same contract as imports and image construction.
// The complete release must advertise every feature this guest implements.
func checkReleaseBundle(path string) error {
	var metadata guestimage.Bundle
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return fmt.Errorf("image manifest must be a regular file of at most 65536 bytes")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return fmt.Errorf("image manifest is too large")
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	bundle, err := guestimage.LoadBundle(context.Background(), path, metadata.Provider, metadata.Arch)
	if err != nil {
		return err
	}
	if bundle.Guest == nil || bundle.Guest.BinarySHA256 == "" {
		return fmt.Errorf("release bundle must declare an enhanced AXV guest and its digest")
	}
	if err := bundle.Guest.Compatible(); err != nil {
		return err
	}
	for _, capability := range guestimage.Capabilities() {
		if !guestimage.Has(bundle.Guest.Capabilities, capability) {
			return fmt.Errorf("release bundle is missing guest capability %s", capability)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(bundle)
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
