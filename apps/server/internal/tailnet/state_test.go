package tailnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentBindingRejectsReplacementWithoutOverwritingIt(t *testing.T) {
	for _, change := range []string{"owner", "node", "configured owner"} {
		t.Run(change, func(t *testing.T) {
			options := testOptions(t)
			store, lock, err := prepareState(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.bind(validStatus(), ""); err != nil {
				t.Fatal(err)
			}
			lock.Close()
			path := filepath.Join(options.StateDir, bindingName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if change == "configured owner" {
				options.OwnerUserID = "456"
			}
			reopened, nextLock, err := prepareState(options)
			if change == "configured owner" {
				if err == nil {
					nextLock.Close()
					t.Fatal("configured owner replaced the recorded enrollment")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer nextLock.Close()
				status := validStatus()
				if change == "owner" {
					status.Self.UserID = 456
				} else {
					status.Self.ID = "replacement"
				}
				if reopened.bind(status, "") == nil {
					t.Fatal("a changed owner or node was silently rebound")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("rejected enrollment changed the recorded identity")
			}
		})
	}
}

func TestStateRejectsUnknownDataAndMissingOrCorruptBinding(t *testing.T) {
	for _, kind := range []string{"unknown directory", "missing binding", "corrupt binding"} {
		t.Run(kind, func(t *testing.T) {
			options := testOptions(t)
			if kind == "unknown directory" {
				if err := os.MkdirAll(options.StateDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(options.StateDir, "tailscaled.state"), []byte("unowned"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				_, lock, err := prepareState(options)
				if err != nil {
					t.Fatal(err)
				}
				lock.Close()
				path := filepath.Join(options.StateDir, bindingName)
				if kind == "missing binding" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, lock, err := prepareState(options); err == nil {
				lock.Close()
				t.Fatal("unrecognized state was adopted or reset")
			}
		})
	}
}

func TestStateRejectsLinkedOrNonprivateProviderFilesRecursively(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public file", "public directory"} {
		t.Run(kind, func(t *testing.T) {
			options := testOptions(t)
			_, lock, err := prepareState(options)
			if err != nil {
				t.Fatal(err)
			}
			lock.Close()
			directory := filepath.Join(options.StateDir, "certs")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, "private.key")
			if err := os.WriteFile(target, []byte("fixture-key"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(target, filepath.Join(options.StateDir, "tailscaled.state")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, filepath.Join(options.StateDir, "tailscaled.state")); err != nil {
					t.Fatal(err)
				}
			case "public file":
				if err := os.Chmod(target, 0644); err != nil {
					t.Fatal(err)
				}
			case "public directory":
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, lock, err := prepareState(options); err == nil {
				lock.Close()
				t.Fatal("unsafe provider state was adopted")
			}
		})
	}
}

func TestRestartAcceptsTailscalePublicCertificateLayoutAndRetainsBinding(t *testing.T) {
	options := testOptions(t)
	store, lock, err := prepareState(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.bind(validStatus(), ""); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	certificates := filepath.Join(options.StateDir, "certs")
	if err := os.Mkdir(certificates, 0700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{testDomain + ".crt": 0644, testDomain + ".key": 0600, "acme-account.key.pem": 0600} {
		if err := os.WriteFile(filepath.Join(certificates, name), []byte("owned fixture"), mode); err != nil {
			t.Fatal(err)
		}
	}
	reopened, nextLock, err := prepareState(options)
	if err != nil {
		t.Fatalf("valid Tailscale certificate cache blocked restart: %v", err)
	}
	defer nextLock.Close()
	if reopened.identity != store.identity {
		t.Fatal("restart changed the recorded identity")
	}
}

func TestStateRejectsConcurrentWriterAndAmbientEnrollmentCredentials(t *testing.T) {
	options := testOptions(t)
	_, lock, err := prepareState(options)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, otherLock, err := prepareState(options); err == nil {
		otherLock.Close()
		t.Fatal("two embedded nodes acquired the same state")
	}
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE", "TSNET_FORCE_LOGIN"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "must-not-be-used")
			fresh := options
			fresh.StateDir = filepath.Join(t.TempDir(), "not-created")
			if _, otherLock, err := prepareState(fresh); err == nil {
				otherLock.Close()
				t.Fatal("ambient enrollment credential was used")
			}
			if _, err := os.Lstat(fresh.StateDir); !os.IsNotExist(err) {
				t.Fatal("credential rejection initialized remote state")
			}
		})
	}
}

func TestHostnameAndOwnerValidation(t *testing.T) {
	for _, hostname := range []string{"a", "axv", strings.Repeat("a", 63)} {
		options := testOptions(t)
		options.Hostname = hostname
		_, lock, err := prepareState(options)
		if err != nil {
			t.Fatalf("valid hostname %q: %v", hostname, err)
		}
		lock.Close()
	}
	for _, owner := range []string{"0", "-1", "+123", "0123", "123\n"} {
		options := testOptions(t)
		options.OwnerUserID = owner
		if _, lock, err := prepareState(options); err == nil {
			lock.Close()
			t.Fatalf("non-canonical owner accepted: %q", owner)
		}
	}
}
