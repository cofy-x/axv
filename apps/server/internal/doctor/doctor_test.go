package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
