//go:build linux

package guest

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func projectFileRootIdentity(root *os.Root, original fs.FileInfo) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(original, opened) {
		return "", projectProblem(409, "project_changed", "The project root changed while its identity was inspected.")
	}
	handle, _, err := unix.NameToHandleAt(int(file.Fd()), ".", 0)
	if err != nil {
		return "", projectProblem(501, "unsupported", fmt.Sprintf("Stable project root identity requires filesystem file handles: %v", err))
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &filesystem); err != nil {
		return "", err
	}
	return projectFileIdentityHandle(handle, filesystem)
}

func projectFileIdentityHandle(handle unix.FileHandle, filesystem unix.Statfs_t) (string, error) {
	if handle.Size() == 0 {
		return "", projectProblem(501, "unsupported", "Stable project root identity requires filesystem file handles.")
	}
	// Mount device numbers can change when a persistent volume moves to another
	// VM. The filesystem's opaque handle identifies the directory incarnation,
	// unlike inode numbers and coarse birth timestamps that can both be reused.
	// A handle is scoped to its filesystem, so include the filesystem ID too:
	// unrelated ext4 roots can both have inode 2 and generation 0. ext4 derives
	// this ID from its persistent UUID, independently of the mount's device ID.
	identity := make([]byte, 20+handle.Size())
	binary.LittleEndian.PutUint64(identity, uint64(filesystem.Type))
	binary.LittleEndian.PutUint32(identity[8:], uint32(filesystem.Fsid.Val[0]))
	binary.LittleEndian.PutUint32(identity[12:], uint32(filesystem.Fsid.Val[1]))
	binary.LittleEndian.PutUint32(identity[16:], uint32(handle.Type()))
	copy(identity[20:], handle.Bytes())
	// Hash the complete identity to keep even a maximum-size handle within the
	// wire limit and keep its opaque filesystem format out of the public API.
	return "file-handle:" + projectDigest(identity), nil
}
