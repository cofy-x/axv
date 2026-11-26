package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
)

func TestGCPrunesOnlyUnreferencedStaleArtifacts(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"bin", "run", "data/codex/runs"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	activeBin := filepath.Join(home, "bin", "axv-"+strings.Repeat("a", 64))
	codexBin := filepath.Join(home, "bin", "axv-"+strings.Repeat("b", 64))
	staleBin := filepath.Join(home, "bin", "axv-"+strings.Repeat("c", 64))
	freshBin := filepath.Join(home, "bin", "axv-"+strings.Repeat("d", 64))
	staleSnap := filepath.Join(home, "run", "desktop-stale.json")
	stalePlist := filepath.Join(home, "run", "com.axv.old.plist")
	for _, f := range []string{activeBin, codexBin, staleBin, freshBin, staleSnap, stalePlist} {
		if err := os.WriteFile(f, []byte("fixture-payload"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-5 * time.Minute)
	for _, f := range []string{activeBin, codexBin, staleBin, staleSnap, stalePlist} {
		if err := os.Chtimes(f, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	// Reference activeBin in desktop.json and codexBin in a codex run manifest.
	if err := jsonfile.Write(filepath.Join(home, "run", "desktop.json"), desktoplaunch.State{Executable: activeBin}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "codex", "runs", "run1.json"), []byte(`{"executable":"`+codexBin+`"}`), 0600); err != nil {
		t.Fatal(err)
	}

	dry, err := gc(context.Background(), home, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Removed) != 3 {
		t.Fatalf("dry run expected 3 removals (staleBin, staleSnap, stalePlist), got %+v", dry.Removed)
	}
	if _, err := os.Stat(staleBin); err != nil {
		t.Fatalf("dry run deleted %s", staleBin)
	}

	applied, err := gc(context.Background(), home, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Removed) != 3 || applied.ReclaimedBytes == 0 {
		t.Fatalf("unexpected GC result: %+v", applied)
	}
	for _, kept := range []string{activeBin, codexBin, freshBin} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("GC removed referenced or fresh file %s: %v", kept, err)
		}
	}
	for _, gone := range []string{staleBin, staleSnap, stalePlist} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("GC left stale file %s behind: %v", gone, err)
		}
	}
}

func TestSystemdUnitEscapesEnvironmentAndArguments(t *testing.T) {
	j := Job{
		Role:  "desktop",
		Label: "com.axv.test.desktop",
		Args:  []string{"/tmp/path with spaces/axv", "_desktop", "--snapshot", "/tmp/$snap\"1.json"},
		Log:   "/tmp/desktop.log",
		Environment: map[string]string{
			"AXV_HOME": "/tmp/home",
			"SPECIAL":  `a$b"c\d%e`,
		},
	}
	unit := systemdUnit(j, true)
	for _, want := range []string{
		"KillMode=process",
		"Type=exec",
		`Environment="SPECIAL=a$$b\"c\\d%%e"`,
		`ExecStart="/tmp/path with spaces/axv" "_desktop" "--snapshot" "/tmp/$$snap\"1.json"`,
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
}
