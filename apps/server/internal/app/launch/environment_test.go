package launch

import (
	"os"
	"testing"
)

func TestSnapshotEnvironmentIsScopedAndClearsSupervisorDefaults(t *testing.T) {
	values := CaptureEnvironment(func(key string) string {
		switch key {
		case "CODEX_HOME":
			return "/private/test/codex"
		case "HTTPS_PROXY":
			return "http://snapshot-user:snapshot-password@127.0.0.1:7890"
		default:
			return ""
		}
	})
	for _, key := range environmentKeys {
		t.Setenv(key, "inherited-supervisor-value")
	}
	t.Setenv("AXV_TEST_UNRELATED", "preserved")
	values["AXV_TEST_UNRELATED"] = "must-not-apply"
	if err := RestoreEnvironment(values); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CODEX_HOME") != "/private/test/codex" || os.Getenv("HTTPS_PROXY") != values["HTTPS_PROXY"] || os.Getenv("HTTP_PROXY") != "" || os.Getenv("AXV_TEST_UNRELATED") != "preserved" {
		t.Fatal("snapshot environment was not restored within its declared scope")
	}
	if _, ok := CaptureEnvironment(os.Getenv)["AXV_TEST_UNRELATED"]; ok {
		t.Fatal("captured unrelated shell environment")
	}
}

func TestIncompleteSnapshotCannotInheritOrPartiallyApplySupervisorEnvironment(t *testing.T) {
	t.Setenv("PATH", "existing-path")
	t.Setenv("HTTPS_PROXY", "existing-proxy")
	values := CaptureEnvironment(func(string) string { return "replacement" })
	delete(values, "HTTPS_PROXY")
	if err := RestoreEnvironment(values); err == nil {
		t.Fatal("accepted an incomplete runtime snapshot")
	}
	if os.Getenv("PATH") != "existing-path" || os.Getenv("HTTPS_PROXY") != "existing-proxy" {
		t.Fatal("partially applied an invalid runtime snapshot")
	}
}
