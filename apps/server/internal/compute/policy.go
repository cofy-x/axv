package compute

import (
	"regexp"
	"strings"
)

var machineID = regexp.MustCompile(`^vm-[0-9a-f]{32}$`)

// IsMachineID distinguishes immutable machine references from human names.
func IsMachineID(value string) bool { return machineID.MatchString(value) }

// NormalizeMachineName preserves case and Unicode while removing surrounding space.
func NormalizeMachineName(value string) string { return strings.TrimSpace(value) }

// ValidateMachineName validates a normalized human label, never an immutable ID.
func ValidateMachineName(name string) error {
	if err := ValidateResourceName(name); err != nil {
		return err
	}
	if IsMachineID(name) {
		return Problem("invalid_request", "VM names cannot use the reserved machine ID format vm- followed by 32 lowercase hexadecimal characters.")
	}
	return nil
}

// ValidateSpec checks allocation and workload policy without assuming the
// caller's architecture is the deployment host's architecture.
func ValidateSpec(s Spec, capacity Capacity, imageMinimum int64) error {
	if err := ValidateMachineName(s.Name); err != nil {
		return err
	}
	if s.Kind != "development" && s.Kind != "task" {
		return Problem("invalid_request", "Machine kind must be development or task.")
	}
	if s.Resources.CPUs < 1 || s.Resources.CPUs > capacity.CPUs || s.Resources.MemoryMiB < 128 || s.Resources.MemoryMiB > capacity.MemoryMiB || s.Resources.DiskGiB < 2 || s.Resources.DiskGiB > capacity.DiskGiB || s.ProjectGiB < 1 || s.ProjectGiB > capacity.DiskGiB {
		return Problem("invalid_request", "Machine resources must fit the configured host capacity; root disk >=2 GiB and project disk >=1 GiB.")
	}
	if s.Kind == "task" && (s.TTLSeconds < 60 || s.TTLSeconds > 30*24*3600) {
		return Problem("invalid_request", "Task machines require an explicit TTL between one minute and 30 days.")
	}
	if s.Kind == "development" && s.TTLSeconds != 0 {
		return Problem("invalid_request", "Development machines do not expire automatically.")
	}
	if s.Kind == "development" && !s.RetainVolume {
		return Problem("invalid_request", "Development project volumes must be retained.")
	}
	if s.Resources.DiskGiB < imageMinimum {
		return Problem("invalid_request", "This image requires a root disk of at least %d GiB.", imageMinimum)
	}
	return nil
}

// ValidateResourceName checks a normalized machine, image, or volume label.
func ValidateResourceName(name string) error {
	if strings.TrimSpace(name) != name || len(name) < 1 || len(name) > 80 || strings.ContainsAny(name, "\r\n\x00") {
		return Problem("invalid_request", "Use a name of 1–80 bytes without control characters.")
	}
	return nil
}

// ValidateRequestID checks a durable command replay key.
func ValidateRequestID(id string) error {
	if len(id) < 8 || len(id) > 128 || strings.ContainsAny(id, "\x00\r\n") {
		return Problem("invalid_request", "requestId must contain 8–128 bytes.")
	}
	return nil
}
