package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedExecutableSurvivesInstalledCommandReplacement(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "axv")
	if err := os.WriteFile(source, []byte("original-runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	first, err := PinExecutable(home, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement-runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	next, err := PinExecutable(home, source)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(first)
	if err != nil || string(contents) != "original-runtime" || first == next {
		t.Fatalf("installed command replacement changed pinned runtime: %s %q %v", first, contents, err)
	}
	again, err := PinExecutable(home, source)
	if err != nil || again != next {
		t.Fatalf("identical runtime was not reused: %q %v", again, err)
	}
}

func TestPinnedExecutableRejectsChangedAndSymlinkedPublishedCopies(t *testing.T) {
	for _, kind := range []string{"changed", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(t.TempDir(), "axv")
			if err := os.WriteFile(source, []byte("runtime"), 0700); err != nil {
				t.Fatal(err)
			}
			pinned, err := PinExecutable(home, source)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "changed" {
				err = os.WriteFile(pinned, []byte("changed"), 0700)
			} else {
				if err := os.Remove(pinned); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(source, pinned)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := PinExecutable(home, source); err == nil {
				t.Fatalf("accepted %s executable", kind)
			}
		})
	}
}
