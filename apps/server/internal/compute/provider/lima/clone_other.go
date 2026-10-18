//go:build !darwin

package lima

import (
	"fmt"
	"os"
)

func cloneOpenFile(*os.File, string) error { return fmt.Errorf("native file cloning is unavailable") }
