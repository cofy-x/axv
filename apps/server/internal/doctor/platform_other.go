//go:build !linux && !darwin

package doctor

import "context"

func checkPlatform(_ context.Context, r *Report, _, _ string) {
	r.add(Check{
		ID:       "platform_vm",
		Category: "vm",
		Label:    "Local VM hosting",
		Status:   StatusInfo,
		Detail:   "Native VM hosting requires macOS (Lima/VZ) or Linux (Firecracker/KVM). Foreground desktop (`axv serve`) remains available.",
	})
}
