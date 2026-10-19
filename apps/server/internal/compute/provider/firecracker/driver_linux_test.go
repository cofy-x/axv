//go:build linux

package firecracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func jailFixture(t *testing.T) (*Driver, compute.Machine) {
	t.Helper()
	d := &Driver{root: t.TempDir(), owner: "host-test", prefix: "axvtest", config: compute.FirecrackerConfig{Binary: "/bin/firecracker", CgroupParent: "system.slice/axv-host.service"}}
	m := compute.Machine{ID: "vm-test", Slot: 1}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.ext4", "project.ext4", "seed.ext4", "kernel"} {
		if err := os.WriteFile(filepath.Join(d.directory(m), name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return d, m
}

func TestFreshJailPreservesDisksAcrossLaunches(t *testing.T) {
	d, m := jailFixture(t)
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.ext4", "project.ext4"} {
		if err := os.WriteFile(filepath.Join(d.jail(m), name), []byte("guest data in "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"dev/net/tun", "dev/kvm", "sys/cache", "firecracker.pid", "api.sock", "vsock.sock"} {
		path := filepath.Join(d.jail(m), name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("previous jailer launch"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dev", "sys", "firecracker.pid", "api.sock", "vsock.sock"} {
		if _, err := os.Lstat(filepath.Join(d.jail(m), name)); !os.IsNotExist(err) {
			t.Fatalf("old jailer artifact %s survived: %v", name, err)
		}
	}
	for _, name := range []string{"root.ext4", "project.ext4", "seed.ext4", "kernel"} {
		durable, err := os.Stat(filepath.Join(d.directory(m), name))
		if err != nil {
			t.Fatal(err)
		}
		jailed, err := os.Stat(filepath.Join(d.jail(m), name))
		if err != nil || !os.SameFile(durable, jailed) {
			t.Fatalf("launch replaced durable %s: %v", name, err)
		}
		want := name
		if name == "root.ext4" || name == "project.ext4" {
			want = "guest data in " + name
		}
		if data, err := os.ReadFile(filepath.Join(d.jail(m), name)); err != nil || string(data) != want {
			t.Fatalf("launch lost %s contents: %q %v", name, data, err)
		}
	}
}

func TestFreshJailRefusesUnownedDirectory(t *testing.T) {
	d, m := jailFixture(t)
	if err := os.MkdirAll(d.jail(m), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(d.jail(m), "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.prepareJail(m); err == nil {
		t.Fatal("adopted an unowned jail")
	}
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "keep" {
		t.Fatalf("unowned resource changed: %q %v", data, err)
	}
}

func TestFreshJailRetainsRuntimeWhenDurableDiskMissing(t *testing.T) {
	d, m := jailFixture(t)
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d.directory(m), "root.ext4")); err != nil {
		t.Fatal(err)
	}
	if err := d.prepareJail(m); err == nil {
		t.Fatal("missing durable disk was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(d.jail(m), "root.ext4")); err != nil || string(data) != "root.ext4" {
		t.Fatalf("remaining runtime disk was discarded: %q %v", data, err)
	}
}

func TestConfirmedStopClearsLaunchGrace(t *testing.T) {
	d, m := jailFixture(t)
	if err := jsonfile.Write(filepath.Join(d.directory(m), "launch.json"), map[string]time.Time{"at": time.Now().UTC()}, 0600); err != nil {
		t.Fatal(err)
	}
	if observation, err := d.Inspect(context.Background(), m); err != nil || observation.Status != "unknown" {
		t.Fatalf("unsettled launch was not protected: %+v %v", observation, err)
	}
	if err := d.Stop(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if observation, err := d.Inspect(context.Background(), m); err != nil || observation.Status != "stopped" {
		t.Fatalf("confirmed stop retained launch grace: %+v %v", observation, err)
	}
}

func TestNewPinsResolvedExecutables(t *testing.T) {
	root := t.TempDir()
	config := compute.FirecrackerConfig{UID: 60000, GID: 60000, Version: "1.17.0", Subnet: "172.30.0.0/22", DNS: "1.1.1.1", CgroupParent: "system.slice/axv-host.service"}
	for name, destination := range map[string]*string{"firecracker": &config.Binary, "jailer": &config.Jailer} {
		actual := filepath.Join(root, name+"-v1.17.0")
		if err := os.WriteFile(actual, nil, 0700); err != nil {
			t.Fatal(err)
		}
		*destination = filepath.Join(root, name)
		if err := os.Symlink(actual, *destination); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := New(filepath.Join(root, "data"), "host-test", config)
	if err != nil {
		t.Fatal(err)
	}
	d := provider.(*Driver)
	if filepath.Base(d.config.Binary) != "firecracker-v1.17.0" || filepath.Base(d.config.Jailer) != "jailer-v1.17.0" || !strings.Contains(d.jail(compute.Machine{ID: "vm-test"}), "/firecracker-v1.17.0/") {
		t.Fatalf("provider and jailer resolve different paths: %+v", d.config)
	}
}

func TestJailerOnlyJoinsPreparedDelegatedCgroup(t *testing.T) {
	d, m := jailFixture(t)
	args := d.jailerArgs(m)
	for index, arg := range args {
		if arg == "--cgroup" {
			t.Fatal("jailer can recursively change ancestor controllers")
		}
		if arg == "--parent-cgroup" && args[index+1] != "system.slice/axv-host.service/axv-host-test/vm-test" {
			t.Fatalf("jailer escaped the prepared delegated leaf: %q", args[index+1])
		}
	}
	if got := d.cgroup(m); got != "/sys/fs/cgroup/system.slice/axv-host.service/axv-host-test/vm-test" {
		t.Fatalf("inspection and jailer cgroups differ: %s", got)
	}
}

func TestMissingOwnershipMarkerDoesNotProveMachineAbsence(t *testing.T) {
	d, m := jailFixture(t)
	if err := os.Remove(filepath.Join(d.directory(m), ".axv-owner")); err != nil {
		t.Fatal(err)
	}
	if observation, err := d.Inspect(context.Background(), m); err == nil || observation.Status == "absent" {
		t.Fatalf("damaged machine was reported absent: %+v %v", observation, err)
	}
	if err := d.Stop(context.Background(), m); err == nil {
		t.Fatal("damaged ownership was accepted as a completed stop")
	}
	if err := d.Delete(context.Background(), m); err == nil {
		t.Fatal("damaged ownership was accepted as a completed deletion")
	}
	if data, err := os.ReadFile(filepath.Join(d.directory(m), "project.ext4")); err != nil || string(data) != "project.ext4" {
		t.Fatalf("unverified machine data changed: %q %v", data, err)
	}
}

func TestMissingMachineMetadataRetainsRemainingJail(t *testing.T) {
	d, m := jailFixture(t)
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(d.directory(m)); err != nil {
		t.Fatal(err)
	}
	if observation, err := d.Inspect(context.Background(), m); err == nil || observation.Status == "absent" {
		t.Fatalf("orphaned resources were reported absent: %+v %v", observation, err)
	}
	if err := d.Stop(context.Background(), m); err == nil {
		t.Fatal("missing metadata was accepted as a completed stop")
	}
	if data, err := os.ReadFile(filepath.Join(d.jail(m), "root.ext4")); err != nil || string(data) != "root.ext4" {
		t.Fatalf("orphaned jail data changed: %q %v", data, err)
	}
}

func TestEnsureDoesNotRecreateMissingMetadataForRemainingJail(t *testing.T) {
	d, m := jailFixture(t)
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(d.jail(m), "root.ext4")
	before, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(d.directory(m)); err != nil {
		t.Fatal(err)
	}
	// Ownership inspection must precede provider probing, configuration pins,
	// token generation and disk preparation, even on an unavailable host.
	err = d.Ensure(context.Background(), m, compute.Image{}, compute.Volume{})
	if err == nil || !strings.Contains(err.Error(), "ownership metadata is missing while provider resources remain") {
		t.Fatalf("orphaned runtime did not block preparation first: %v", err)
	}
	if _, err = os.Lstat(d.directory(m)); !os.IsNotExist(err) {
		t.Fatalf("preparation recreated missing machine metadata: %v", err)
	}
	after, err := os.Stat(root)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("preparation replaced an orphaned disk: %v", err)
	}
	if data, err := os.ReadFile(root); err != nil || string(data) != "root.ext4" {
		t.Fatalf("preparation changed orphaned disk contents: %q %v", data, err)
	}
}

func TestEnsureDoesNotReplaceMissingStateAfterDiskPreparation(t *testing.T) {
	d, m := jailFixture(t)
	imagePin := filepath.Join(d.directory(m), "image.json")
	if err := os.WriteFile(imagePin, []byte("keep original image identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(context.Background(), m, compute.Image{}, compute.Volume{}); err == nil || !strings.Contains(err.Error(), "refusing to replace its identity") {
		t.Fatalf("prepared resources did not block state replacement first: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(d.directory(m), "state.json")); !os.IsNotExist(err) {
		t.Fatalf("preparation replaced missing state: %v", err)
	}
	if data, err := os.ReadFile(imagePin); err != nil || string(data) != "keep original image identity" {
		t.Fatalf("preparation changed image identity before checking state: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(d.directory(m), "root.ext4")); err != nil || string(data) != "root.ext4" {
		t.Fatalf("preparation changed a prepared disk: %q %v", data, err)
	}
}

func TestMissingStateCanResumeBeforeDiskPreparation(t *testing.T) {
	d, m := jailFixture(t)
	if err := os.RemoveAll(d.directory(m)); err != nil {
		t.Fatal(err)
	}
	if err := owneddir.Ensure(d.directory(m), d.owner+":"+m.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.directory(m), "image.json"), []byte("original image pin"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.directory(m), ".axv-write-interrupted"), []byte("unpublished metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.checkStateCreation(m); err != nil {
		t.Fatalf("an interrupted first state publication cannot resume: %v", err)
	}
}

func TestDeleteChecksJailOwnershipBeforeProviderMutation(t *testing.T) {
	d, m := jailFixture(t)
	if err := os.MkdirAll(d.jail(m), 0700); err != nil {
		t.Fatal(err)
	}
	// Any attempted network command must fail for a different reason, revealing
	// whether deletion crossed its ownership boundary before checking the jail.
	t.Setenv("PATH", t.TempDir())
	if err := d.Delete(context.Background(), m); err == nil || !strings.Contains(err.Error(), "verify runtime ownership") {
		t.Fatalf("deletion mutated provider state before checking the jail: %v", err)
	}
}

func TestManagedDirectoryRejectsSymlinkAndMissingVolumeMarker(t *testing.T) {
	d, _ := jailFixture(t)
	target := filepath.Join(d.root, "target")
	if err := owneddir.Ensure(target, "owner"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d.root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := checkManagedDirectory(link, "owner"); err == nil {
		t.Fatal("a symlink was accepted as an owned directory")
	}
	volume := compute.Volume{ID: "vol-test"}
	volumeDirectory := filepath.Join(d.root, "volumes", volume.ID)
	if err := os.MkdirAll(volumeDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteVolume(context.Background(), volume); err == nil {
		t.Fatal("missing ownership marker was accepted as completed volume deletion")
	}
}

func TestNewRejectsInvalidJailerIdentity(t *testing.T) {
	for _, tc := range []struct {
		owner  string
		gid    int
		reason string
	}{
		{owner: "host-test", gid: 2147483648, reason: "jailer UID/GID"},
		{owner: "short", gid: 60000, reason: "host ownership identity"},
		{owner: "host/other", gid: 60000, reason: "host ownership identity"},
		{owner: "host\"test", gid: 60000, reason: "host ownership identity"},
		{owner: "host test", gid: 60000, reason: "host ownership identity"},
		{owner: "host\ttest", gid: 60000, reason: "host ownership identity"},
		{owner: "host界test", gid: 60000, reason: "host ownership identity"},
	} {
		config := compute.FirecrackerConfig{Binary: "/missing/firecracker", Jailer: "/missing/jailer", UID: 60000, GID: tc.gid, Version: "1.17.0", Subnet: "172.30.0.0/22", DNS: "1.1.1.1", CgroupParent: "system.slice/axv-host.service"}
		root := t.TempDir()
		if _, err := New(root, tc.owner, config); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("invalid identity was not rejected before resource creation: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "firecracker")); !os.IsNotExist(err) {
			t.Fatalf("invalid identity created provider resources: %v", err)
		}
	}
}

func TestNewRejectsResolversUnavailableToIsolatedGuests(t *testing.T) {
	for _, dns := range []string{"0.1.2.3", "100.64.0.53", "169.254.1.53", "192.0.0.53", "192.0.2.53", "198.18.0.53", "203.0.113.53", "224.0.0.53", "240.0.0.53"} {
		config := compute.FirecrackerConfig{DNS: dns, Subnet: "172.30.0.0/22"}
		if _, err := New(t.TempDir(), "host-test", config); err == nil || !strings.Contains(err.Error(), "public IPv4 DNS resolver") {
			t.Errorf("resolver %s was accepted before provisioning: %v", dns, err)
		}
	}
}

func TestReusedVolumeNeverRecreatesMissingStorage(t *testing.T) {
	d, _ := jailFixture(t)
	v := compute.Volume{ID: "vol-reused", Revision: 3, SizeGiB: 1, Retain: true}
	t.Setenv("PATH", t.TempDir())
	if err := d.ensureVolume(context.Background(), v); err == nil || !strings.Contains(err.Error(), "storage is missing; refusing to recreate") {
		t.Fatalf("a reused volume did not fail before preparation: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(d.root, "volumes", v.ID)); !os.IsNotExist(err) {
		t.Fatalf("preparation created replacement volume storage: %v", err)
	}
}

func TestInitialVolumePublicationCanResumeWithoutReformatting(t *testing.T) {
	for _, revision := range []int64{1, 2} {
		d, _ := jailFixture(t)
		v := compute.Volume{ID: "vol-initial", Revision: revision, SizeGiB: 1, Retain: true}
		dir := filepath.Join(d.root, "volumes", v.ID)
		if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
			t.Fatal(err)
		}
		disk := filepath.Join(dir, "disk.ext4")
		if err := os.WriteFile(disk, []byte("published disk before creation marker"), 0600); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(disk)
		if err != nil {
			t.Fatal(err)
		}
		// Initial retry can publish its missing marker without mkfs. A newer
		// revision cannot prove initialization, so it must retain uncertainty.
		t.Setenv("PATH", t.TempDir())
		err = d.ensureVolume(context.Background(), v)
		if revision == 1 && err != nil || revision > 1 && (err == nil || !strings.Contains(err.Error(), "creation metadata is missing")) {
			t.Fatalf("revision %d recovery: %v", revision, err)
		}
		after, err := os.Stat(disk)
		if err != nil || !os.SameFile(before, after) {
			t.Fatalf("revision %d replaced the existing disk: %v", revision, err)
		}
		if data, err := os.ReadFile(disk); err != nil || string(data) != "published disk before creation marker" {
			t.Fatalf("revision %d reformatted existing data: %q %v", revision, data, err)
		}
		data, markerErr := os.ReadFile(filepath.Join(dir, "created"))
		if revision == 1 && (markerErr != nil || string(data) != v.ID) || revision > 1 && !os.IsNotExist(markerErr) {
			t.Fatalf("revision %d published an invalid creation marker: %q %v", revision, data, markerErr)
		}
	}
}

func linkedProjectFixture(t *testing.T) (*Driver, compute.Machine, compute.Volume) {
	t.Helper()
	d, m := jailFixture(t)
	v := compute.Volume{ID: "vol-test", Revision: 3, SizeGiB: 1, Retain: true}
	m.Spec.VolumeID, m.Spec.RetainVolume = v.ID, true
	if err := jsonfile.Write(filepath.Join(d.directory(m), "state.json"), state{MachineID: m.ID, ImageID: "image-test"}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.directory(m), "prepared"), []byte(m.ID), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(d.root, "volumes", v.ID)
	if err := owneddir.Ensure(dir, d.owner+":"+v.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "created"), []byte(v.ID), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(d.directory(m), "project.ext4"), filepath.Join(dir, "disk.ext4")); err != nil {
		t.Fatal(err)
	}
	return d, m, v
}

func TestPreparedAndRetainedMachinesPreserveUnverifiedProjectDisks(t *testing.T) {
	for _, damaged := range []string{"directory", "marker", "disk", "replacement"} {
		t.Run(damaged, func(t *testing.T) {
			d, m, v := linkedProjectFixture(t)
			dir := filepath.Join(d.root, "volumes", v.ID)
			disk := filepath.Join(dir, "disk.ext4")
			var err error
			switch damaged {
			case "directory":
				err = os.RemoveAll(dir)
			case "marker":
				err = os.WriteFile(filepath.Join(dir, "created"), []byte("another-volume"), 0600)
			case "disk", "replacement":
				err = os.Remove(disk)
				if err == nil && damaged == "replacement" {
					err = os.WriteFile(disk, []byte("foreign disk"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(d.directory(m), "project.ext4")
			before, err := os.Stat(project)
			if err != nil {
				t.Fatal(err)
			}
			// Neither operation may reach provider probes or network mutation.
			t.Setenv("PATH", t.TempDir())
			for _, operation := range []func() error{
				func() error { return d.Ensure(context.Background(), m, compute.Image{}, v) },
				func() error { return d.Delete(context.Background(), m) },
			} {
				if err := operation(); err == nil || !strings.Contains(err.Error(), "volume") {
					t.Fatalf("unverified retained volume reached provider mutation: %v", err)
				}
			}
			after, err := os.Stat(project)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("last project link was discarded: %v", err)
			}
			if data, err := os.ReadFile(project); err != nil || string(data) != "project.ext4" {
				t.Fatalf("last project contents were changed: %q %v", data, err)
			}
			if damaged == "directory" {
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("missing volume storage was replaced: %v", err)
				}
			}
		})
	}
}

func TestRetainedVolumeDoesNotRequireMachineLinkForSafeDeletion(t *testing.T) {
	d, m, v := linkedProjectFixture(t)
	if err := os.Remove(filepath.Join(d.directory(m), "project.ext4")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.projectVolumeDisk(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.checkProjectVolume(m, v.ID); err != nil {
		t.Fatalf("missing machine link made intact retained data uncertain: %v", err)
	}
	if err := d.checkRetainedProjectDeletion(m); err != nil {
		t.Fatalf("intact retained volume prevented safe runtime deletion: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(d.root, "volumes", v.ID, "disk.ext4")); err != nil || string(data) != "project.ext4" {
		t.Fatalf("intact retained volume changed: %q %v", data, err)
	}
}

func TestDeleteRetainedMachineBeforeInitialProjectCreation(t *testing.T) {
	d, m := jailFixture(t)
	m.Spec.VolumeID, m.Spec.RetainVolume = "vol-not-created", true
	if err := os.Remove(filepath.Join(d.directory(m), "project.ext4")); err != nil {
		t.Fatal(err)
	}
	// Absent owned networking needs only read-only discovery. Unexpected
	// mutations fail in the fixtures and cannot touch the deployment host.
	bin := t.TempDir()
	for program, body := range map[string]string{
		"ip":  "case \"$*\" in '-j link show') printf '[]';; *) exit 9;; esac\n",
		"nft": "case \"$*\" in '-j list tables') printf '{\"nftables\":[]}';; *) exit 9;; esac\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, program), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	if err := d.Delete(context.Background(), m); err != nil {
		t.Fatalf("a failed first preparation with no project data could not be deleted: %v", err)
	}
	if _, err := os.Lstat(d.directory(m)); !os.IsNotExist(err) {
		t.Fatalf("failed initial runtime was not removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(d.root, "volumes", m.Spec.VolumeID)); !os.IsNotExist(err) {
		t.Fatalf("cleanup created project storage: %v", err)
	}
}

func TestDeleteRetainedMachinePreservesLastProjectLinkInsideJail(t *testing.T) {
	d, m, v := linkedProjectFixture(t)
	if err := d.prepareJail(m); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(d.jail(m), "project.ext4")
	before, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(d.directory(m), "project.ext4")); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(d.root, "volumes", v.ID)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	for _, operation := range []func() error{
		func() error { return d.Ensure(context.Background(), m, compute.Image{}, v) },
		func() error { return d.Delete(context.Background(), m) },
	} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "project volume") {
			t.Fatalf("last jail link was not protected before provider mutation: %v", err)
		}
	}
	after, err := os.Stat(project)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("last jail project link was removed: %v", err)
	}
	if data, err := os.ReadFile(project); err != nil || string(data) != "project.ext4" {
		t.Fatalf("last jail project data changed: %q %v", data, err)
	}
}
