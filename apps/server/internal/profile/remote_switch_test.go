package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestRemoteSwitchPreservesAllOtherProfileAndEnrollmentFields(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	original := `{"runtime_version":1,"desktop":{"addr":"127.0.0.1:4001"},"compute":{"mode":"external","socket":"/private/host.sock","future":true},"remote":{"enabled":false,"hostname":"axv-office","owner_user_id":"123","future":{"keep":true}},"future":{"nested":[1,2,3]}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal([]byte(original), &expected); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false, false} {
		lock, err := Lock(home)
		if err != nil {
			t.Fatal(err)
		}
		remote, err := SaveRemoteEnabled(home, enabled)
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
		if err != nil || remote != (Remote{Enabled: enabled, Hostname: "axv-office", OwnerUserID: "123"}) {
			t.Fatalf("saved switch: %+v %v", remote, err)
		}
		expected["remote"].(map[string]any)["enabled"] = enabled
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("switch overwrote unrelated fields: %s", data)
		}
		info, err := os.Stat(path)
		if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("configuration lost private permissions")
		}
	}
}

func TestRemoteReadIsIndependentAndInvalidSettingsAreNeverReplaced(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	valid := `{"desktop":{"data_dir":42},"compute":42,"proxy_url":false,"remote":{"enabled":true,"hostname":"axv-office","owner_user_id":"123"}}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRemote(home); err != nil || got != (Remote{Enabled: true, Hostname: "axv-office", OwnerUserID: "123"}) {
		t.Fatalf("unrelated configuration blocked remote settings: %+v %v", got, err)
	}
	for _, value := range []string{`{"remote":null}`, `{"remote":42}`, `{"remote":{"enabled":"true"}}`, `{"remote":{"hostname":"bad.name"}}`, `{"remote":{"owner_user_id":"01"}}`, `{"runtime_version":99,"remote":{"enabled":true}}`, `{"remote":{"enabled":false}} {}`} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := SaveRemoteEnabled(home, false); err == nil {
			t.Fatalf("invalid profile was treated as a default: %s", value)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != value {
			t.Fatalf("invalid profile was reset: %s, %v", after, err)
		}
	}
}

func TestMissingRemoteProfileDefaultsOffWithoutCreatingFiles(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	got, err := ReadRemote(home)
	if err != nil || got != (Remote{Hostname: "axv"}) {
		t.Fatalf("missing remote defaults: %+v %v", got, err)
	}
	if _, err := SaveRemoteEnabled(home, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("reading or retaining the default switch initialized profile state")
	}
}
