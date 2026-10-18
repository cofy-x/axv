package artifactcache

import (
	"path/filepath"
	"testing"
)

func TestHomeIsIndependentAndExplicitOverrideDoesNotResolveDefault(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "os-user")
	env := map[string]string{"AXV_HOME": "/different/profile", "AXV_DEV_HOME": "/different/dev", "AXV_TEST_TMPDIR": "/different/test"}
	resolved, err := ResolveHome(func(key string) string { return env[key] }, func() (string, error) { return userHome, nil })
	if err != nil || resolved != filepath.Join(userHome, ".axv", "cache") {
		t.Fatalf("cache=%q err=%v", resolved, err)
	}
	env["AXV_CACHE_HOME"] = filepath.Join(t.TempDir(), "explicit")
	resolved, err = ResolveHome(func(key string) string { return env[key] }, func() (string, error) { t.Fatal("explicit cache consulted OS home"); return "", nil })
	if err != nil || resolved != env["AXV_CACHE_HOME"] {
		t.Fatalf("cache=%q err=%v", resolved, err)
	}
	env["AXV_CACHE_HOME"] = "relative-cache"
	if _, err = ResolveHome(func(key string) string { return env[key] }, func() (string, error) { t.Fatal("invalid explicit cache fell back to home"); return "", nil }); err == nil {
		t.Fatal("relative override accepted")
	}
}
