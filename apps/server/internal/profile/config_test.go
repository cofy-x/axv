package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeCreatesOnlyThePortableStarterProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "profile")
	p, err := Initialize(home)
	if err != nil || p.RuntimeVersion != 1 || p.Desktop.Addr != "127.0.0.1:3001" || p.Compute.Mode != "disabled" || p.WorkspaceDir != "" {
		t.Fatalf("starter: %#v %v", p, err)
	}
	path := filepath.Join(home, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Initialize(home); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repeated initialization changed the profile: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("initialized runtime, data or workspace directories: %#v %v", entries, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("configuration is not private: %v", err)
	}
}

func TestComputeReadIgnoresUnrelatedConfigurationAndKeepsComputeErrors(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	data := `{"runtime_version":1,"desktop":42,"workspace_dir":true,"proxy_url":[],"compute":{"mode":"external","socket":"/run/axv.sock"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadCompute(home)
	if err != nil || p.ComputeError != "" || p.Compute.Socket != "/run/axv.sock" || p.Desktop != (Desktop{}) || p.WorkspaceDir != "" || p.ProxyURL != "" {
		t.Fatalf("compute read depended on desktop: %#v %v", p, err)
	}
	for _, data := range []string{`{"compute":42}`, `{"compute":{"mode":"external","socket":"relative"}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		p, err = ReadCompute(home)
		if err != nil || p.ComputeError == "" {
			t.Fatalf("compute error was not preserved for override: %#v %v", p, err)
		}
	}
	for _, data := range []string{`{`, `null`, `{} {}`, `{"runtime_version":2}`, `{"runtime_version":"1"}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCompute(home); err == nil {
			t.Fatalf("accepted invalid shared configuration: %s", data)
		}
	}
}

func TestSavePreservesAgentConfiguration(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := `{"workspace_dir":"/projects","proxy_url":"http://localhost:7890","future":{"nested":[1,true]},"desktop":{"addr":"127.0.0.1:4100"}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveCompute(home, Compute{Mode: "external", Socket: "/run/axv/control.sock"}); err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_dir", "proxy_url", "future", "desktop"} {
		var before map[string]any
		var after map[string]any
		_ = json.Unmarshal([]byte(original), &before)
		_ = json.Unmarshal(b, &after)
		a, _ := json.Marshal(before[key])
		z, _ := json.Marshal(after[key])
		if string(a) != string(z) {
			t.Fatalf("changed %s", key)
		}
	}
	p, err := Read(home)
	if err != nil || p.Compute.Mode != "external" || p.Desktop.Addr != "127.0.0.1:4100" {
		t.Fatalf("read: %+v %v", p, err)
	}
}

func TestInvalidProfilesAreNeverReplaced(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{`, `{} {}`, `{"runtime_version":42}`, `{"compute":{"mode":"mystery"}}`, `{"compute":{"mode":"managed","host_config":"relative"}}`} {
		t.Run(data, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.json")
			_ = os.WriteFile(path, []byte(data), 0600)
			if _, err := Read(home); err == nil {
				t.Fatal("accepted invalid profile")
			}
			if err := SaveCompute(home, Compute{Mode: "disabled"}); err == nil {
				t.Fatal("replaced invalid profile")
			}
			b, _ := os.ReadFile(path)
			if string(b) != data {
				t.Fatal("modified invalid profile")
			}
		})
	}
}

func TestReadDoesNotCreateProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	p, err := Read(home)
	if err != nil || p.Compute.Mode != "disabled" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("read created profile: %v", err)
	}
}

func TestDesktopRetainsComputeErrorsWithoutDiscardingTheProfile(t *testing.T) {
	for _, value := range []string{`{"mode":"managed","host_config":"relative"}`, `{"mode":"mystery"}`, `42`, `{"mode":42}`} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.json")
			data := `{"runtime_version":1,"workspace_dir":"/projects","compute":` + value + `}`
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			profile, err := ReadDesktop(home)
			if err != nil || profile.ComputeError == "" || profile.WorkspaceDir != "/projects" {
				t.Fatalf("desktop could not retain compute failure: %#v %v", profile, err)
			}
			if _, err := Read(home); err == nil {
				t.Fatal("strict reader accepted invalid compute settings")
			}
			if _, err := InitializeDesktop(home); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != data {
				t.Fatal("desktop initialization modified broken compute configuration")
			}
		})
	}
	for _, value := range []string{`{`, `null`, `{} {}`, `{"runtime_version":9}`, `{"desktop":{"data_dir":42}}`} {
		home := t.TempDir()
		os.WriteFile(filepath.Join(home, "config.json"), []byte(value), 0600)
		if _, err := ReadDesktop(home); err == nil {
			t.Fatalf("desktop accepted invalid whole profile: %s", value)
		}
	}
}
