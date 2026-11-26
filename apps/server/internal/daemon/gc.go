package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	desktoplaunch "github.com/cofy-x/axv/apps/server/internal/app/launch"
)

// GCRemovedFile records a stale artifact removed (or planned for removal) by GC.
type GCRemovedFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Kind  string `json:"kind"`
}

// GCReport summarizes stale runtime artifacts under AXV_HOME.
type GCReport struct {
	Home           string          `json:"home"`
	DryRun         bool            `json:"dryRun"`
	Removed        []GCRemovedFile `json:"removed"`
	ReclaimedBytes int64           `json:"reclaimedBytes"`
	KeptCount      int             `json:"keptCount"`
}

const gcGraceWindow = 60 * time.Second

// GC safely prunes unreferenced pinned executables and stale launch records
// under home without touching any live service or recorded Codex run executable.
func GC(ctx context.Context, home string, dryRun bool) (GCReport, error) {
	return gc(ctx, home, dryRun, time.Now())
}

func gc(ctx context.Context, home string, dryRun bool, now time.Time) (GCReport, error) {
	report := GCReport{Home: home, DryRun: dryRun, Removed: []GCRemovedFile{}}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if _, err := os.Stat(home); os.IsNotExist(err) {
		return report, nil
	}
	if !dryRun {
		lock, err := commandLock(ctx, home)
		if err != nil {
			return report, err
		}
		defer lock.Close()
	}

	referenced := collectReferencedPaths(home)
	if exe, err := os.Executable(); err == nil {
		referenced[filepath.Clean(exe)] = true
	}

	// 1. Scan AXV_HOME/bin/axv-* pinned executables.
	binDir := filepath.Join(home, "bin")
	if entries, err := os.ReadDir(binDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "axv-") {
				continue
			}
			full := filepath.Join(binDir, entry.Name())
			info, statErr := entry.Info()
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			if referenced[full] || referenced[entry.Name()] || now.Sub(info.ModTime()) < gcGraceWindow {
				report.KeptCount++
				continue
			}
			if !dryRun {
				if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
					return report, err
				}
			}
			report.Removed = append(report.Removed, GCRemovedFile{Path: full, Bytes: info.Size(), Kind: "pinned-binary"})
			report.ReclaimedBytes += info.Size()
		}
	}

	// 2. Scan AXV_HOME/run stale job snapshots and plists.
	runDir := filepath.Join(home, "run")
	if entries, err := os.ReadDir(runDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			isStaleSnapshot := (strings.HasPrefix(name, "desktop-") || strings.HasPrefix(name, "host-")) && strings.HasSuffix(name, ".json")
			isStalePlist := strings.HasPrefix(name, "com.axv.") && strings.HasSuffix(name, ".plist")
			if !isStaleSnapshot && !isStalePlist {
				continue
			}
			full := filepath.Join(runDir, name)
			info, statErr := entry.Info()
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			if referenced[full] || now.Sub(info.ModTime()) < gcGraceWindow {
				report.KeptCount++
				continue
			}
			if !dryRun {
				if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
					return report, err
				}
			}
			kind := "run-snapshot"
			if isStalePlist {
				kind = "run-plist"
			}
			report.Removed = append(report.Removed, GCRemovedFile{Path: full, Bytes: info.Size(), Kind: kind})
			report.ReclaimedBytes += info.Size()
		}
	}
	return report, nil
}

// collectReferencedPaths finds every pinned executable, snapshot and definition
// referenced by live desktop state, daemon manifest, or persisted run manifests.
func collectReferencedPaths(home string) map[string]bool {
	refs := map[string]bool{}
	if state, err := desktoplaunch.ReadState(home); err == nil {
		if state.Executable != "" {
			refs[filepath.Clean(state.Executable)] = true
		}
	}
	if m, err := readManifest(home); err == nil {
		for _, job := range m.Jobs {
			if len(job.Args) > 0 && job.Args[0] != "" {
				refs[filepath.Clean(job.Args[0])] = true
			}
			if job.Marker != "" {
				refs[filepath.Clean(job.Marker)] = true
			}
			if job.Definition != "" {
				refs[filepath.Clean(job.Definition)] = true
			}
		}
	}
	// Preserve any pinned binary referenced by Codex / VM work run manifests under data/.
	dataDir := filepath.Join(home, "data")
	_ = filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > 512*1024 {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(raw)
		for idx := strings.Index(text, "axv-"); idx >= 0; {
			rest := text[idx:]
			end := 4
			for end < len(rest) && isHexByte(rest[end]) {
				end++
			}
			if end == 68 { // "axv-" + 64 hex chars
				refs[rest[:end]] = true
			}
			next := strings.Index(rest[4:], "axv-")
			if next < 0 {
				break
			}
			idx += 4 + next
		}
		return nil
	})
	return refs
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
