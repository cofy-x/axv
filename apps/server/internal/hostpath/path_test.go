package hostpath

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveUsesHomeOnlyForLeadingMarker(t *testing.T) {
	home := t.TempDir()
	for _, value := range []string{"~", "~/projects", `~\projects`} {
		t.Run(value, func(t *testing.T) {
			expected := home
			if value != "~" {
				expected = filepath.Join(home, "projects")
			}
			actual, err := Resolve(value, func() (string, error) { return home, nil })
			if err != nil || actual != expected {
				t.Fatalf("resolved home path = %q, expected %q: %v", actual, expected, err)
			}
		})
	}
	failure := errors.New("home unavailable")
	if _, err := Resolve("~/projects", func() (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatalf("home lookup failure was lost: %v", err)
	}
	expected, err := filepath.Abs("projects")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := Resolve("projects", func() (string, error) { return "", failure })
	if err != nil || actual != expected {
		t.Fatalf("relative path unnecessarily depended on home lookup: %q %v", actual, err)
	}
	if _, err := Resolve("project\x00name", func() (string, error) { return home, nil }); err == nil {
		t.Fatal("accepted a NUL in a host path")
	}
}
