package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

func vmWorkBridge(t *testing.T) (*protocolBridge, *bytes.Buffer, vmwork.Context) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	work := vmwork.Context{
		Version: vmwork.Version, SessionID: "session", RunID: "run",
		Socket: filepath.Join(directory, "host.sock"), ConfigFingerprint: "fingerprint",
		Executable: filepath.Join(directory, "axv"),
		Project: vmwork.Project{
			Project: compute.Project{ID: "project", HostID: "host", Kind: "vm", Name: "VM repository", VolumeID: "volume", Root: "repo", RootIdentity: "root-identity", Revision: 1},
			Binding: compute.ProjectBinding{HostID: "host", ProjectID: "project", ProjectRevision: 1, VolumeID: "volume", VolumeRevision: 2, MachineID: "vm-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BootID: "boot-original", InstanceID: "guest-original"},
		},
	}
	contextPath, skillPath, err := vmwork.Publish(directory, work)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SessionID: work.SessionID, RunID: work.RunID, VMContextPath: contextPath, VMSkillPath: skillPath}
	manifest.vmWork, err = loadVMWork(manifest, directory)
	if err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	bridge := newBridge(manifest, filepath.Join(directory, "state.json"), output, nil)
	bridge.ready, bridge.state.ThreadID = true, testThreadID
	if err := bridge.contextQueue().Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return bridge, output, work
}

func submitVMWorkTurn(t *testing.T, bridge *protocolBridge, value map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var message wireMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	return bridge.forwardTurn(context.Background(), raw, message)
}

func TestVMWorkAccompaniesEachUserTurnWithSkillAndPreservesOverrides(t *testing.T) {
	bridge, output, work := vmWorkBridge(t)
	document := testContextDocument()
	queue := bridge.contextQueue()
	if _, err := queue.Enqueue(context.Background(), "snapshot-request", []ContextDocument{document}, true); err != nil {
		t.Fatal(err)
	}
	for index, method := range []string{"turn/start", "turn/steer"} {
		output.Reset()
		params := map[string]any{
			"threadId": testThreadID, "expectedTurnId": "original-turn",
			"input": []any{map[string]string{"type": "text", "text": "Inspect the VM project"}, map[string]string{"type": "image", "url": "https://example.invalid/reference.png"}},
			"model": "user-model", "approvalPolicy": "on-request", "sandboxPolicy": map[string]any{"type": "workspaceWrite", "networkAccess": false},
		}
		if err := submitVMWorkTurn(t, bridge, map[string]any{"id": index + 1, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		var forwarded struct{ Params map[string]json.RawMessage }
		if err := json.Unmarshal(output.Bytes(), &forwarded); err != nil {
			t.Fatal(err)
		}
		for key, value := range params {
			if key == "input" {
				continue
			}
			original, _ := json.Marshal(value)
			var got, want any
			_ = json.Unmarshal(forwarded.Params[key], &got)
			_ = json.Unmarshal(original, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("provider override %s changed: %s", key, forwarded.Params[key])
			}
		}
		var input []map[string]any
		if err := json.Unmarshal(forwarded.Params["input"], &input); err != nil {
			t.Fatal(err)
		}
		want := 4
		if index == 0 {
			want++
		}
		if len(input) != want || input[0]["text"] != "Inspect the VM project" || input[1]["type"] != "image" ||
			input[3]["type"] != "skill" || input[3]["name"] != vmwork.SkillName || input[3]["path"] != bridge.manifest.VMSkillPath {
			t.Fatalf("original input or skill missing: %+v", input)
		}
		text, _ := input[2]["text"].(string)
		for _, expected := range []string{"$" + vmwork.SkillName, work.Project.Binding.BootID, work.Project.Binding.InstanceID, work.Executable, "internal-vm-project", bridge.manifest.VMContextPath} {
			if !strings.Contains(text, expected) {
				t.Fatalf("captured work context missing %q: %s", expected, text)
			}
		}
		if index == 0 && !strings.Contains(input[4]["text"].(string), document.Content) {
			t.Fatal("document snapshot was lost when adding VM work context")
		}
	}
}

func TestVMWorkDoesNotCreateUserInputForToolOnlyOrInvalidSteer(t *testing.T) {
	bridge, output, _ := vmWorkBridge(t)
	for _, message := range []map[string]any{
		{"id": 1, "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{}, "toolOutput": map[string]string{"name": "test", "output": "done"}}},
		{"id": 2, "method": "turn/steer", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "invalid steer"}}}},
	} {
		output.Reset()
		if err := submitVMWorkTurn(t, bridge, message); err != nil {
			t.Fatal(err)
		}
		original, _ := json.Marshal(message)
		if strings.TrimSpace(output.String()) != string(original) {
			t.Fatalf("non-user input was augmented: %s", output.String())
		}
	}
}

func TestVMWorkChangesFailBeforeForwardingOrConsumingSnapshots(t *testing.T) {
	for _, change := range []string{"binding", "run", "missing", "skill"} {
		t.Run(change, func(t *testing.T) {
			bridge, output, work := vmWorkBridge(t)
			if _, err := bridge.contextQueue().Enqueue(context.Background(), "snapshot-request", []ContextDocument{testContextDocument()}, true); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "binding", "run":
				if change == "binding" {
					work.Project.Binding.BootID = "replacement-boot"
				} else {
					work.RunID = "newer-run"
				}
				data, _ := json.Marshal(work)
				err = os.WriteFile(bridge.manifest.VMContextPath, data, 0600)
			case "missing":
				err = os.Remove(bridge.manifest.VMContextPath)
			case "skill":
				err = os.WriteFile(bridge.manifest.VMSkillPath, []byte("changed skill"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := submitVMWorkTurn(t, bridge, map[string]any{"id": 1, "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{map[string]string{"type": "text", "text": "Run tests"}}}}); err == nil || output.Len() != 0 {
				t.Fatalf("changed VM context was forwarded: %v, %s", err, output.String())
			}
			batches, err := bridge.contextQueue().List(context.Background())
			if err != nil || len(batches) != 1 || batches[0].Status != "pending" {
				t.Fatalf("VM context failure consumed document snapshots: %+v %v", batches, err)
			}
		})
	}
}

func TestVMWorkProtocolLimitNeverForwardsAnUnboundTurn(t *testing.T) {
	for _, scenario := range []string{"snapshots exceed limit", "VM context exceeds limit"} {
		t.Run(scenario, func(t *testing.T) {
			bridge, output, _ := vmWorkBridge(t)
			document := testContextDocument()
			if _, err := bridge.contextQueue().Enqueue(context.Background(), "snapshot-request", []ContextDocument{document}, true); err != nil {
				t.Fatal(err)
			}
			workInput, err := bridge.vmWorkInput()
			if err != nil {
				t.Fatal(err)
			}
			workBytes := 0
			for _, item := range workInput {
				workBytes += len(item) + 1 // one comma per appended input item
			}
			documentItem, _ := json.Marshal(map[string]any{"type": "text", "text": contextText(document), "text_elements": []any{}})
			textItem := map[string]string{"type": "text", "text": ""}
			turn := map[string]any{"id": "large-turn", "method": "turn/start", "params": map[string]any{"threadId": testThreadID, "input": []any{textItem}}}
			empty, _ := json.Marshal(turn)
			targetBytes := maxMessageBytes - workBytes - len(documentItem)/2
			if scenario == "VM context exceeds limit" {
				targetBytes = maxMessageBytes - workBytes/2
			}
			textItem["text"] = strings.Repeat("x", targetBytes-len(empty))
			raw, _ := json.Marshal(turn)
			if len(raw) >= maxMessageBytes {
				t.Fatal("fixture must fit the original TUI protocol limit")
			}

			// Only a small rejection response crosses this socket. Forwarding is
			// inspected directly so the test does not depend on TUI frame limits.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			connected, stop := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.CloseNow()
				bridge.client = connection
				close(connected)
				<-stop
			}))
			defer server.Close()
			defer close(stop)
			peer, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.CloseNow()
			select {
			case <-connected:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var message wireMessage
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Fatal(err)
			}
			if err := bridge.forwardTurn(ctx, raw, message); err != nil {
				t.Fatal(err)
			}
			if scenario == "VM context exceeds limit" {
				_, data, err := peer.Read(ctx)
				var rejection wireMessage
				if err != nil || json.Unmarshal(data, &rejection) != nil || len(rejection.Error) == 0 || string(rejection.ID) != `"large-turn"` || output.Len() != 0 {
					t.Fatalf("oversized VM turn was not rejected before forwarding: response=%s bytes=%d error=%v", data, output.Len(), err)
				}
			} else {
				var forwarded struct {
					Params struct{ Input []map[string]any }
				}
				if err := json.Unmarshal(output.Bytes(), &forwarded); err != nil || output.Len() >= maxMessageBytes || len(forwarded.Params.Input) != 3 || forwarded.Params.Input[2]["type"] != "skill" || forwarded.Params.Input[2]["path"] != bridge.manifest.VMSkillPath || !strings.Contains(forwarded.Params.Input[1]["text"].(string), bridge.manifest.VMContextPath) {
					t.Fatalf("snapshot fallback lost the required VM context or forwarded the oversized attachment: bytes=%d input=%d error=%v", output.Len(), len(forwarded.Params.Input), err)
				}
			}
			batches, err := bridge.contextQueue().List(ctx)
			if err != nil || len(batches) != 1 || batches[0].Status != "failed" || batches[0].ProviderTurnID != "" || !strings.Contains(batches[0].Error, "not submitted") {
				t.Fatalf("unsubmitted snapshots retained an uncertain or successful receipt: %+v %v", batches, err)
			}
		})
	}
}
