package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func publicKey(t *testing.T) (string, ssh.Signer) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), signer
}

func TestNormalizePreservesKeyIdentityAndStripsComments(t *testing.T) {
	key, _ := publicKey(t)
	for _, value := range []string{
		key,
		key + "\n",
		key + " workstation profile",
		key + " workstation profile\n",
		" \t" + key + "\tprofile ",
		key + " " + strings.Repeat("x", 2048-len(key)-1),
	} {
		got, err := Normalize(value)
		if err != nil || got != key {
			t.Errorf("Normalize(%q) = %q, %v; want %q", value, got, err, key)
		}
	}
}

func TestNormalizeRejectsOptionsCertificatesAndMalformedKeys(t *testing.T) {
	key, signer := publicKey(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := ssh.NewPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &ssh.Certificate{Key: signer.PublicKey(), CertType: ssh.UserCert, ValidBefore: ssh.CertTimeInfinity}
	if err := certificate.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, value string
	}{
		{"empty", ""},
		{"malformed", "ssh-ed25519 broken"},
		{"options", "command=\"sh\" " + key},
		{"flag option", "no-pty " + key},
		{"certificate", string(ssh.MarshalAuthorizedKey(certificate))},
		{"other algorithm", string(ssh.MarshalAuthorizedKey(otherPublic))},
		{"multiple keys", key + "\n" + key},
		{"leading newline", "\n" + key},
		{"extra newline", key + "\n\n"},
		{"carriage return", key + "\r\n"},
		{"newline in comment", key + " first\nsecond"},
		{"NUL in comment", key + " profile\x00"},
		{"oversized", key + " " + strings.Repeat("x", 2048-len(key))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Normalize(tc.value); err == nil || got != "" {
				t.Fatalf("Normalize(%q) = %q, %v; want an empty key and error", tc.value, got, err)
			}
		})
	}
}
