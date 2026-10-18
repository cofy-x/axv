package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	computeclient "github.com/cofy-x/axv/apps/server/internal/compute/client"
	"golang.org/x/crypto/ssh"
)

func publicKey(t *testing.T) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func TestSSHResultPreservesRemoteStatusAndReportsLocalTermination(t *testing.T) {
	remote := exec.Command("sh", "-c", "exit 42").Run()
	if result := sshResult(context.Background(), remote); ExitCode(result) != 42 || result.Error() != "" {
		t.Fatalf("remote status was not preserved: %v", result)
	}
	terminated := exec.Command("sh", "-c", "kill -KILL $$").Run()
	if result := sshResult(context.Background(), terminated); ExitCode(result) != 1 || !strings.Contains(result.Error(), "system SSH process terminated: signal: killed") {
		t.Fatalf("local termination was hidden: %v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := sshResult(ctx, terminated); ExitCode(result) != 130 {
		t.Fatalf("interruption was not preserved: %v", result)
	}
}

func TestSSHKeysAreAtomicConcurrentAndHostPinsNeverSilentlyChange(t *testing.T) {
	home := t.TempDir()
	key := publicKey(t)
	alias := sshAlias("host", testMachineID)
	results := make(chan sshFiles, 8)
	failures := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			files, err := profileSSH(context.Background(), home, alias, key)
			results <- files
			failures <- err
		})
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected sshFiles
	for files := range results {
		if expected.Identity == "" {
			expected = files
		}
		if files != expected {
			t.Fatal("concurrent initialization replaced the private key")
		}
	}
	for path, mode := range map[string]os.FileMode{filepath.Join(home, "ssh"): 0700, expected.Identity: 0600, expected.KnownHosts: 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("incorrect mode: %s %v", path, err)
		}
	}
	before, _ := os.ReadFile(expected.KnownHosts)
	if strings.Count(string(before), alias) != 1 {
		t.Fatalf("duplicate host pin: %s", before)
	}
	if _, err := profileSSH(context.Background(), home, alias, publicKey(t)); err == nil || !strings.Contains(err.Error(), "host key changed") {
		t.Fatalf("changed key accepted: %v", err)
	}
	after, _ := os.ReadFile(expected.KnownHosts)
	if string(before) != string(after) {
		t.Fatal("mismatch replaced the trusted key")
	}
	other, err := profileSSH(context.Background(), home, sshAlias("host", strings.Replace(testMachineID, "a", "b", 1)), publicKey(t))
	if err != nil || other.PublicKey != expected.PublicKey {
		t.Fatalf("a different VM replaced profile identity: %v", err)
	}
}

func TestSSHRefusesSymlinkAndOverlyPermissiveCredentialPaths(t *testing.T) {
	for _, kind := range []string{"directory symlink", "private key symlink", "private key public"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "ssh")
			target := filepath.Join(t.TempDir(), "untouched")
			if kind == "directory symlink" {
				os.Mkdir(target, 0700)
				os.Symlink(target, dir)
			} else {
				os.Mkdir(dir, 0700)
				if kind == "private key symlink" {
					os.WriteFile(target, []byte("keep"), 0600)
					os.Symlink(target, filepath.Join(dir, "client_ed25519"))
				} else {
					os.WriteFile(filepath.Join(dir, "client_ed25519"), []byte("keep"), 0644)
				}
			}
			if _, err := profileSSH(context.Background(), home, "alias", publicKey(t)); err == nil {
				t.Fatal("accepted unsafe SSH storage")
			}
			if kind == "private key symlink" {
				data, _ := os.ReadFile(target)
				if string(data) != "keep" {
					t.Fatal("changed symlink target")
				}
			}
		})
	}
}

func TestSSHConfigurationShellQuotesPathsAndKeepsFixedIDs(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client is unavailable")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments")
	// The real OpenSSH parser and shell must pass these characters literally.
	helper := filepath.Join(dir, "helper ' quoted $dollar % percent")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + strings.ReplaceAll(record, "'", "'\"'\"'") + "'\nexit 0\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "profile ' $value %")
	binding := computeclient.Binding{HostID: "host-original", ConfigFingerprint: "original-config"}
	files := sshFiles{Identity: filepath.Join(dir, "identity ' $value %"), KnownHosts: filepath.Join(dir, "known ' $value %")}
	config, alias, err := sshConfiguration(home, helper, binding, testMachineID, "boot-original", files)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ssh.conf")
	if err = os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("ssh", "-F", path, "-T", alias, "true")
	if err = command.Run(); err == nil {
		t.Fatal("fixture is deliberately not an SSH server")
	}
	arguments, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("OpenSSH did not execute the quoted proxy: %v", err)
	}
	want := strings.Join([]string{"_ssh-proxy", "--profile", home, "--host-id", binding.HostID, "--fingerprint", binding.ConfigFingerprint, "--machine-id", testMachineID, "--boot-id", "boot-original", ""}, "\n")
	if string(arguments) != want {
		t.Fatalf("proxy arguments were shell-interpreted:\ngot %q\nwant %q", arguments, want)
	}
	if !strings.Contains(config, "StrictHostKeyChecking yes") || !strings.Contains(config, "IdentityAgent none") || !strings.Contains(config, "ControlPath none") {
		t.Fatal("SSH configuration lost identity isolation")
	}
	if _, _, err = sshConfiguration(home+"\nHost *", helper, binding, testMachineID, "", files); err == nil {
		t.Fatal("configuration injection accepted")
	}
	for _, target := range []string{"identity", "known-hosts"} {
		unsafe := files
		if target == "identity" {
			unsafe.Identity = filepath.Join(dir, "${HOME}", "identity")
		} else {
			unsafe.KnownHosts = filepath.Join(dir, "${HOME}", "known_hosts")
		}
		if _, _, err = sshConfiguration(home, helper, binding, testMachineID, "", unsafe); err == nil {
			t.Fatalf("accepted OpenSSH expansion in %s path", target)
		}
	}
}
