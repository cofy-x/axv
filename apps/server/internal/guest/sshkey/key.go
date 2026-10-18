// Package sshkey validates the public-key policy shared by local clients, the
// host API and the guest SSH registry.
package sshkey

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Normalize accepts exactly one plain Ed25519 public key and strips its comment.
// Authorized-key options and certificates are not accepted.
func Normalize(value string) (string, error) {
	if len(value) > 2048 || strings.ContainsAny(strings.TrimSuffix(value, "\n"), "\r\n\x00") {
		return "", fmt.Errorf("supply one Ed25519 public key")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return "", fmt.Errorf("supply one plain Ed25519 public key without key options")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}
