//go:build !linux && !darwin

package guest

import "os"

func projectGitHubProfileOwner(os.FileInfo) bool { return false }
