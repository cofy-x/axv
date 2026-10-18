package lima

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/owneddir"
)

func TestExistingVolumeMustMatchItsOwnedRawDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake limactl uses a POSIX shell")
	}
	for _, tc := range []struct {
		name, format string
		size         int64
		outside      bool
		valid        bool
		backing      bool
	}{
		{name: "matching raw disk", format: "raw", size: 1 << 30, valid: true},
		{name: "qcow2 before first VZ boot", format: "qcow2", size: 1024, valid: true},
		{name: "external qcow2 backing", format: "qcow2", size: 1024, backing: true},
		{name: "truncated disk", format: "raw", size: 1024},
		{name: "unexpected format", format: "vmdk", size: 1 << 30},
		{name: "outside provider home", format: "raw", size: 1 << 30, outside: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			v := compute.Volume{ID: "vol-test", SizeGiB: 1}
			dir := filepath.Join(home, "_disks", v.ID)
			if tc.outside {
				dir = filepath.Join(home, "unrelated")
			}
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(filepath.Join(dir, "datadisk"))
			if err != nil {
				t.Fatal(err)
			}
			if err = file.Truncate(tc.size); err != nil {
				t.Fatal(err)
			}
			if tc.format == "qcow2" {
				header := make([]byte, 1024)
				copy(header, "QFI\xfb")
				binary.BigEndian.PutUint32(header[4:8], 3)
				binary.BigEndian.PutUint64(header[24:32], 1<<30)
				if tc.backing {
					binary.BigEndian.PutUint64(header[8:16], 4096)
				}
				if _, err = file.WriteAt(header, 0); err != nil {
					t.Fatal(err)
				}
			}
			file.Close()
			data, _ := json.Marshal(diskInfo{Name: v.ID, Dir: dir, Format: tc.format, Size: 1 << 30})
			binary := filepath.Join(root, "limactl")
			script := "#!/bin/sh\n[ \"$*\" = '--tty=false disk list --json' ] || exit 2\nprintf '%s' '" + strings.ReplaceAll(string(data), "'", "'\\''") + "'\n"
			if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			d := &Driver{config: compute.LimaConfig{Binary: binary}, root: root, home: root, owner: "test-host"}
			if err = owneddir.Ensure(filepath.Join(root, "volumes", v.ID), d.owner+":"+v.ID); err != nil {
				t.Fatal(err)
			}
			err = d.EnsureVolume(context.Background(), v)
			if (err == nil) != tc.valid {
				t.Fatalf("existing disk acceptance = %v, valid=%t", err, tc.valid)
			}
			_, markerErr := os.Stat(filepath.Join(root, "volumes", v.ID, "created"))
			if !tc.valid && !os.IsNotExist(markerErr) {
				t.Fatal("an unverified disk was recorded as created")
			}
		})
	}
}
