package providerutil

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cofy-x/axv/apps/server/internal/atomicfile"
)

// PinJSON prevents a changed configuration from pointing at replacement resources.
func PinJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	saved, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return atomicfile.Write(path, b, 0600)
	}
	if err != nil {
		return err
	}
	if string(saved) != string(b) {
		return fmt.Errorf("runtime configuration differs from %s; use the original provider settings", path)
	}
	return nil
}
