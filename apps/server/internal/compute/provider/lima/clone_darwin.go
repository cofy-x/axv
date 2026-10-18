package lima

import (
	"os"

	"golang.org/x/sys/unix"
)

func cloneOpenFile(source *os.File, destination string) error {
	return unix.Fclonefileat(int(source.Fd()), unix.AT_FDCWD, destination, 0)
}
