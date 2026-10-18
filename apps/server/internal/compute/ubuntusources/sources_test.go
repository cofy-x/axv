package ubuntusources

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func invoke(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return invokeScript(t, Script, args...)
}
func invokeScript(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX image configuration")
	}
	cmd := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
	// A configuration-only test must never invoke the real package manager,
	// including when tests run as root on a Linux builder.
	bin := t.TempDir()
	for _, name := range []string{"apt", "apt-get"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho 'unexpected package manager invocation' >&2\nexit 91\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(script)
	b, err := cmd.CombinedOutput()
	return string(b), err
}
func TestUbuntuSourceArchitectureAndSignatureContract(t *testing.T) {
	for _, source := range []string{"official", "aliyun"} {
		for _, arch := range []string{"arm64", "amd64"} {
			output, err := invoke(t, "print", source, "ubuntu", "24.04", arch)
			if err != nil {
				t.Fatal(output, err)
			}
			for _, value := range []string{"Suites: noble noble-updates noble-backports", "Suites: noble-security", "Architectures: " + arch, "Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg"} {
				if !strings.Contains(output, value) {
					t.Fatalf("missing %s: %s", value, output)
				}
			}
			if (arch == "arm64") != strings.Contains(output, "ubuntu-ports") {
				t.Fatal(output)
			}
			if (source == "aliyun") != strings.Contains(output, "mirrors.aliyun.com") {
				t.Fatal(output)
			}
			if strings.Contains(strings.ToLower(output), "trusted") {
				t.Fatal(output)
			}
		}
	}
	for _, args := range [][]string{{"print", "aliyun", "debian", "24.04", "amd64"}, {"print", "official", "ubuntu", "22.04", "amd64"}, {"print", "official", "ubuntu", "24.04", "riscv64"}, {"print", "shell;bad", "ubuntu", "24.04", "arm64"}} {
		if _, err := invoke(t, args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, data := range map[string]string{"etc/os-release": "ID=ubuntu\nVERSION_ID=24.04\n", "etc/apt/sources.list": "deb original custom\n", "etc/apt/sources.list.d/ubuntu.sources": "original deb822\n", "usr/share/keyrings/ubuntu-archive-keyring.gpg": "fixture key", "etc/ssl/certs/ca-certificates.crt": "fixture trust roots"} {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestApplyIsExplicitTargetScopedAndPreservesBackups(t *testing.T) {
	a, b, cache := fixtureRoot(t), fixtureRoot(t), fixtureRoot(t)
	output, err := invoke(t, "apply", "aliyun", a, "arm64")
	if err != nil {
		t.Fatal(output, err)
	}
	got, err := os.ReadFile(filepath.Join(a, "etc/apt/sources.list.d/ubuntu.sources"))
	if err != nil || !strings.Contains(string(got), "https://mirrors.aliyun.com/ubuntu-ports") {
		t.Fatal(string(got), err)
	}
	for _, root := range []string{b, cache} {
		got, _ := os.ReadFile(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"))
		if string(got) != "original deb822\n" {
			t.Fatal("another VM/cache was modified")
		}
	}
	backups, err := filepath.Glob(filepath.Join(a, "var/lib/axv/package-sources-*/ubuntu.sources"))
	if err != nil || len(backups) != 1 {
		t.Fatal(backups, err)
	}
	original, _ := os.ReadFile(backups[0])
	if string(original) != "original deb822\n" {
		t.Fatal("lost original")
	}
	cloud, err := os.ReadFile(filepath.Join(a, "etc/cloud/cloud.cfg.d/99-axv-package-sources.cfg"))
	if err != nil || !strings.Contains(string(cloud), "preserve_sources_list: true") {
		t.Fatalf("cloud-init can undo the explicit source selection: %v", err)
	}
	if output, err = invoke(t, "apply", "aliyun", a, "arm64"); err != nil || !strings.Contains(output, "already configured") {
		t.Fatalf("replay was not idempotent: %v %s", err, output)
	}
	replayed, err := filepath.Glob(filepath.Join(a, "var/lib/axv/package-sources-*/ubuntu.sources"))
	if err != nil || len(replayed) != 1 {
		t.Fatalf("replay accumulated backups: %v %v", replayed, err)
	}
}
func TestApplyRejectsCustomLayoutsAndUnsupportedOSWithoutMutation(t *testing.T) {
	for _, kind := range []string{"custom", "old", "symlink", "missing-ca", "state-symlink", "cloud-policy"} {
		root := fixtureRoot(t)
		switch kind {
		case "state-symlink":
			os.MkdirAll(filepath.Join(root, "var/lib"), 0700)
			os.Symlink(t.TempDir(), filepath.Join(root, "var/lib/axv"))
		case "cloud-policy":
			os.MkdirAll(filepath.Join(root, "etc/cloud/cloud.cfg.d"), 0700)
			os.WriteFile(filepath.Join(root, "etc/cloud/cloud.cfg.d/99-axv-package-sources.cfg"), []byte("user policy\n"), 0600)
		case "missing-ca":
			os.Remove(filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"))
		case "custom":
			os.WriteFile(filepath.Join(root, "etc/apt/sources.list.d/custom.list"), []byte("custom"), 0600)
		case "old":
			os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=22.04\n"), 0600)
		case "symlink":
			os.Remove(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"))
			os.Symlink("../sources.list", filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"))
		}
		if output, err := invoke(t, "apply", "official", root, "amd64"); err == nil {
			t.Fatal(kind, output)
		}
		got, _ := os.ReadFile(filepath.Join(root, "etc/apt/sources.list"))
		if string(got) != "deb original custom\n" {
			t.Fatal("failure mutated sources")
		}
	}
}

func TestSourceWriteFailureRollsBackAndAbruptExitCanReplay(t *testing.T) {
	for _, crash := range []bool{false, true} {
		root := fixtureRoot(t)
		fault := "raise OSError('fixture write failure')"
		if crash {
			fault = "replace(sources, content)\n            os._exit(75)"
		}
		// Inject the failure after the cloud-init policy is published. All
		// backup, locking, replacement and recovery code is the production script.
		script := strings.Replace(Script, "replace(sources, content)", fault, 1)
		if output, err := invokeScript(t, script, "apply", "aliyun", root, "amd64"); err == nil {
			t.Fatal("injected failure succeeded", output)
		}
		if !crash {
			got, _ := os.ReadFile(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"))
			if string(got) != "original deb822\n" {
				t.Fatal("failed write did not restore original sources")
			}
			if _, err := os.Stat(filepath.Join(root, "etc/cloud/cloud.cfg.d/99-axv-package-sources.cfg")); !os.IsNotExist(err) {
				t.Fatalf("failed write retained a new cloud policy: %v", err)
			}
		}
		if output, err := invoke(t, "apply", "aliyun", root, "amd64"); err != nil {
			t.Fatal("failed operation left an unrecoverable lock", output, err)
		}
		if _, err := os.Stat(filepath.Join(root, "etc/apt/sources.list")); !os.IsNotExist(err) {
			t.Fatalf("replay retained duplicate active sources: %v", err)
		}
		backups, _ := filepath.Glob(filepath.Join(root, "var/lib/axv/package-sources-*/ubuntu.sources"))
		found := false
		for _, path := range backups {
			data, _ := os.ReadFile(path)
			found = found || string(data) == "original deb822\n"
		}
		if !found {
			t.Fatal("interruption or replay lost the original source backup")
		}
	}
}

func TestOSReleaseIsParsedWithoutExecutingShell(t *testing.T) {
	root := fixtureRoot(t)
	marker := filepath.Join(root, "must-not-exist")
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=24.04\nUNRELATED=$(touch '"+marker+"')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := invoke(t, "apply", "official", root, "amd64"); err != nil {
		t.Fatal(output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed os-release as shell code", err)
	}
}
