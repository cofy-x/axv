package launch

import (
	"fmt"
	"os"
)

// Capture only service inputs, not the caller's entire shell environment. These
// values live in the private runtime snapshot, never supervisor command arguments.
// Empty values deliberately clear an inherited supervisor setting.
var environmentKeys = []string{
	"AXV_CACHE_HOME", "AXV_IMAGE_BUNDLE", "AXV_ARTIFACT_MIRRORS",
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TMPDIR",
	"CODEX_HOME", "OPENAI_API_KEY", "OPENAI_BASE_URL",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

func CaptureEnvironment(getenv func(string) string) map[string]string {
	values := make(map[string]string, len(environmentKeys))
	for _, key := range environmentKeys {
		values[key] = getenv(key)
	}
	return values
}

// RestoreEnvironment runs only in the dedicated _desktop child before startup.
// The service uses its captured settings even if the profile or shell changes.
func RestoreEnvironment(values map[string]string) error {
	for _, key := range environmentKeys {
		if _, ok := values[key]; !ok {
			return fmt.Errorf("desktop environment snapshot is incomplete: missing %s", key)
		}
	}
	for _, key := range environmentKeys {
		value := values[key]
		var err error
		if value == "" {
			err = os.Unsetenv(key)
		} else {
			err = os.Setenv(key, value)
		}
		if err != nil {
			return fmt.Errorf("restore desktop environment %s: %w", key, err)
		}
	}
	return nil
}
