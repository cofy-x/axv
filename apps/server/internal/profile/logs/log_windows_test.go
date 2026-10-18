package logs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsUsesStructuredStderrWithoutClaimingPrivateFiles(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	logger, err := Open(home, "desktop", &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("service.start")
	if logger.Path() != "" || !bytes.Contains(output.Bytes(), []byte(`"event":"service.start"`)) {
		t.Fatalf("Windows fallback unavailable: %s", output.Bytes())
	}
	if _, err := os.Stat(filepath.Join(home, "logs")); !os.IsNotExist(err) {
		t.Fatalf("Windows fallback created files: %v", err)
	}
	if err := PrepareLegacy(home, "desktop"); err == nil {
		t.Fatal("Windows accepted private supervisor-file logging")
	}
}
