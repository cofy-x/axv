package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/jsonfile"
	"github.com/cofy-x/axv/apps/server/internal/profile"
)

func TestDoctorIsReadOnlyAndFormatsSummary(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent-profile")
	report := Run(context.Background(), home, "0.1.0-test")
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("doctor created profile directory: %v", err)
	}
	if report.Version != "0.1.0-test" || len(report.Checks) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	var out bytes.Buffer
	if err := WriteText(&out, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "AXV doctor") || !strings.Contains(out.String(), "Summary:") {
		t.Fatalf("unexpected formatted output:\n%s", out.String())
	}
}

func TestDarwinSocketPathPrefersConfiguredLimaHome(t *testing.T) {
	// Construct a profile path whose default <home>/lima exceeds macOS's 104-byte limit.
	longHome := filepath.Join(t.TempDir(), strings.Repeat("long-profile-dir-", 4))
	if err := os.MkdirAll(longHome, 0700); err != nil {
		t.Fatal(err)
	}

	// 1. Unconfigured long profile warns about default AXV_HOME/lima.
	var unconfigured Report
	checkDarwinLimaPath(&unconfigured, longHome)
	if len(unconfigured.Checks) != 1 || unconfigured.Checks[0].Status != StatusWarn || !strings.Contains(unconfigured.Checks[0].Detail, filepath.Join(longHome, "lima")) {
		t.Fatalf("expected warn for long default lima path, got %+v", unconfigured.Checks)
	}

	// 2. Managed profile with short --lima-home reports OK using the configured path.
	hostCfgPath := filepath.Join(longHome, "host.json")
	shortLima := "/tmp/axv-l"
	cfg := compute.Config{
		DataDir: filepath.Join(longHome, "compute"),
		Socket:  "/tmp/axv-h.sock",
		Capacity: compute.Capacity{
			Resources: compute.Resources{CPUs: 4, MemoryMiB: 4096, DiskGiB: 40},
			Machines:  2,
			Workers:   1,
		},
		Lima: &compute.LimaConfig{
			Binary:  "/usr/local/bin/limactl",
			Version: compute.SupportedLimaVersion,
			Home:    shortLima,
		},
	}
	if err := jsonfile.Write(hostCfgPath, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveCompute(longHome, profile.Compute{Mode: "managed", HostConfig: hostCfgPath}); err != nil {
		t.Fatal(err)
	}

	var configured Report
	checkDarwinLimaPath(&configured, longHome)
	if len(configured.Checks) != 1 || configured.Checks[0].Status != StatusOK || !strings.Contains(configured.Checks[0].Detail, shortLima) {
		t.Fatalf("expected OK for configured short lima.home, got %+v", configured.Checks)
	}

	// 3. Managed profile with an overlong configured lima.home still warns on that configured path.
	cfg.Lima.Home = filepath.Join(longHome, "custom-lima-dir")
	if err := jsonfile.Write(hostCfgPath, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	var tooLongConfigured Report
	checkDarwinLimaPath(&tooLongConfigured, longHome)
	if len(tooLongConfigured.Checks) != 1 || tooLongConfigured.Checks[0].Status != StatusWarn || !strings.Contains(tooLongConfigured.Checks[0].Detail, cfg.Lima.Home) {
		t.Fatalf("expected warn for overlong configured lima.home, got %+v", tooLongConfigured.Checks)
	}
}
