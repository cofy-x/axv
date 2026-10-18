package profile

import (
	"os"
	"path/filepath"

	"github.com/cofy-x/axv/apps/server/internal/filelock"
)

func Lock(home string) (*os.File, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	return filelock.TryLock(filepath.Join(home, "command.lock"))
}
