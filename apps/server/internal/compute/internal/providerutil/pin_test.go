package providerutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinJSONPreservesTheOriginalResourceConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	original := map[string]string{"binary": "/owned/provider"}
	if err := PinJSON(path, original); err != nil {
		t.Fatal(err)
	}
	if err := PinJSON(path, original); err != nil {
		t.Fatalf("unchanged configuration was rejected: %v", err)
	}
	if err := PinJSON(path, map[string]string{"binary": "/replacement/provider"}); err == nil {
		t.Fatal("accepted replacement provider resources")
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != `{"binary":"/owned/provider"}` {
		t.Fatalf("saved configuration changed: %q, %v", saved, err)
	}
}
