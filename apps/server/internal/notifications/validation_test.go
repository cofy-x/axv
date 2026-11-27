package notifications

import (
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func TestValidTargetAcceptsHostAndVMCodexTargets(t *testing.T) {
	host := notificationevent.Target{
		Kind:      "codex",
		StoreID:   "db-host-1",
		SessionID: "sess-1",
		RunID:     "run-1",
		ThreadID:  "thread-1",
		EventID:   "evt-1",
	}
	if !validTarget(host) {
		t.Fatal("expected valid host codex target")
	}
	vm := notificationevent.Target{
		Kind:        "codex",
		StoreID:     "db-vm-1",
		SessionID:   "sess-vm-1",
		RunID:       "run-vm-1",
		ThreadID:    "thread-vm-1",
		EventID:     "evt-vm-1",
		HostID:      "host-1",
		MachineID:   "vm-dev",
		MachineName: "Dev VM",
	}
	if !validTarget(vm) {
		t.Fatal("expected valid VM codex target")
	}
	missingHost := vm
	missingHost.HostID = ""
	if validTarget(missingHost) {
		t.Fatal("expected VM codex target without HostID to be rejected")
	}
}

func TestSchemaKeysCannotAliasThroughUnicodeCaseFolding(t *testing.T) {
	for _, data := range []string{
		"{\"storeId\":\"original\",\"ſtoreId\":\"replacement\"}",
		"{\"storeId\":\"original\",\"\\u017ftoreId\":\"replacement\"}",
		"{\"ſtoreId\":\"replacement\"}",
	} {
		var input Update
		if uniqueJSON([]byte(data)) || decodeStrict(strings.NewReader(data), &input) == nil {
			t.Fatal("ambiguous non-ASCII schema key was accepted")
		}
	}
	var value struct {
		Token string `json:"token"`
	}
	if decodeStrict(strings.NewReader("{\"token\":\"α\"}"), &value) != nil || value.Token != "α" {
		t.Fatal("valid Unicode field content was changed")
	}
}
