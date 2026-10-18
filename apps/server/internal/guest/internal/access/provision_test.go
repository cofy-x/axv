package access

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/guest/sshkey"
)

//go:embed provision.sh
var provisionScript string

func TestAccountRepairOnlyRestoresOwnedLockedPassword(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("a POSIX shell is not installed")
	}
	// Run the actual repair entry point with an isolated directory and fake
	// account utilities. No host credentials or services are accessed.
	prefix, _, ok := strings.Cut(provisionScript, "\ncommand -v ssh-keygen")
	if !ok {
		t.Fatal("account repair boundary is missing")
	}
	prefix = strings.Replace(prefix, "ssh_dir=/etc/ssh/axv", "ssh_dir=$AXV_TEST_SSH_DIR", 1)
	script := `
id() { case "$*" in '-u') echo 0;; '-u axv') echo 1000;; *) return 1;; esac; }
stat() { case "$2" in '%u') echo 0;; '%a') echo 755;; *) return 1;; esac; }
getent() { [ "$*" = 'shadow axv' ] || return 1; printf 'axv:%s:1:0:99999:7:::\n' "$AXV_TEST_PASSWORD"; }
usermod() { [ "$#" -eq 3 ] && [ "$1" = --password ] && [ "$2" = '*NP*' ] && [ "$3" = axv ] || return 1; printf repaired > "$AXV_TEST_REPAIRED"; }
` + prefix + "\nexit 99\n"
	for _, tc := range []struct {
		name, password string
		repair         bool
		invalid        string
	}{
		{name: "cloud-init relocked AXV account", password: "!*NP*", repair: true},
		{name: "AXV account already usable", password: "*NP*"},
		{name: "another locked account", password: "!"},
		{name: "another disabled password", password: "*"},
		{name: "user password", password: "$6$user$hash"},
		{name: "locked user password", password: "!$6$user$hash"},
		{name: "uninitialized service", password: "!*NP*", invalid: "initialized"},
		{name: "missing ownership", password: "!*NP*", invalid: "owner"},
		{name: "different machine", password: "!*NP*", invalid: "machine"},
		{name: "linked initialization marker", password: "!*NP*", invalid: "symlink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, value := range map[string]string{"initialized": "1\n", "owner": "vm-test\n"} {
				if tc.invalid == name {
					continue
				}
				if name == "owner" && tc.invalid == "machine" {
					value = "vm-other\n"
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.invalid == "symlink" {
				if err := os.Remove(filepath.Join(dir, "initialized")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "owner"), filepath.Join(dir, "initialized")); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(dir, "repaired")
			cmd := exec.Command("sh", "-s", "--", "vm-test", "--repair-account")
			cmd.Stdin = strings.NewReader(script)
			cmd.Env = append(os.Environ(), "AXV_TEST_SSH_DIR="+dir, "AXV_TEST_PASSWORD="+tc.password, "AXV_TEST_REPAIRED="+marker)
			output, err := cmd.CombinedOutput()
			if (err != nil) != (tc.invalid != "") {
				t.Fatalf("repair error = %v, invalid = %q, output = %s", err, tc.invalid, output)
			}
			_, err = os.Stat(marker)
			if (err == nil) != tc.repair {
				t.Fatalf("account mutation = %v, want %v", err == nil, tc.repair)
			}
		})
	}
}

func TestProvisionedHostKeyCommentsDoNotChangeItsPersistentIdentity(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("OpenSSH ssh-keygen is not installed")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("a POSIX shell is not installed")
	}
	// Exercise the actual provisioning block with temporary files. Only Linux
	// root ownership checks are substituted; key generation, parsing, matching
	// and preservation all run through the production shell and ssh-keygen.
	_, block, ok := strings.Cut(provisionScript, "\nkey=$ssh_dir/ssh_host_ed25519_key\n")
	if !ok {
		t.Fatal("host-key provisioning block is missing")
	}
	block, _, ok = strings.Cut(block, "\nclients=")
	if !ok {
		t.Fatal("host-key provisioning boundary is missing")
	}
	script := "set -eu\nssh_dir=$1\nkey=$ssh_dir/ssh_host_ed25519_key\n" +
		"stat() { [ \"$1\" = -c ] && [ \"$2\" = %u ]; printf '0\\n'; }\n" +
		"chown() { :; }\n" + block
	dir := t.TempDir()
	run := func() ([]byte, error) {
		cmd := exec.Command("sh", "-s", "--", dir)
		cmd.Stdin = strings.NewReader(script)
		return cmd.CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("first provision: %v: %s", err, output)
	}
	key := filepath.Join(dir, "ssh_host_ed25519_key")
	private, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if normalized, err := sshkey.Normalize(string(public)); err != nil || string(public) != normalized+"\n" {
		t.Fatalf("public key was not canonical: %q, %v", public, err)
	}
	if err = os.WriteFile(key+".pub", []byte(strings.TrimSpace(string(public))+" independently changed public comment\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err != nil {
		t.Fatalf("repeated provision with a different comment: %v: %s", err, output)
	}
	after, err := os.ReadFile(key)
	if err != nil || string(after) != string(private) {
		t.Fatal("repeated provisioning replaced the host private key")
	}
	other := testPublicKey(t) + " another identity\n"
	if err = os.WriteFile(key+".pub", []byte(other), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "does not match") {
		t.Fatalf("mismatched public identity accepted: %v: %s", err, output)
	}
	after, err = os.ReadFile(key + ".pub")
	if err != nil || string(after) != other {
		t.Fatal("failed provisioning overwrote the mismatched public key")
	}
	if err = os.WriteFile(filepath.Join(dir, "initialized"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "refusing to replace its identity") {
		t.Fatalf("missing initialized host key was replaced: %v: %s", err, output)
	}
}
