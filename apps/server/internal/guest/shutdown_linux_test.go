//go:build linux

package guest

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestShutdownUsesTheInstalledProviderAndPreservesFailures(t *testing.T) {
	denied := errors.New("sudo denied the request")
	for _, provider := range []string{"firecracker", "lima", "unknown"} {
		t.Run(provider, func(t *testing.T) {
			var command []string
			err := shutdownGuest(context.Background(), provider, func(_ context.Context, program string, args ...string) error {
				command = append([]string{program}, args...)
				return denied
			})
			if provider == "unknown" {
				if err == nil || command != nil {
					t.Fatalf("unknown provider invoked a shutdown command: %v, %v", command, err)
				}
				return
			}
			program := "/sbin/poweroff"
			if provider == "firecracker" {
				program = "/sbin/reboot"
			}
			if !reflect.DeepEqual(command, []string{"sudo", "-n", program}) || !errors.Is(err, denied) {
				t.Fatalf("shutdown crossed the provider/privilege boundary or hid failure: %v, %v", command, err)
			}
		})
	}
}
