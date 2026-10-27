package policy

import "strings"

// ValidPublicationName is the canonical, bounded label hint accepted by HTTP and CLI.
func ValidPublicationName(name string) bool {
	if len(name) < 1 || len(name) > 24 || name[0] == '-' || name[len(name)-1] == '-' || strings.HasPrefix(name, "xn--") {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
