//go:build darwin || linux

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func activityTestObserver(t *testing.T) (*activityObserver, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "manifest.json")
	observer := newActivityObserver(filepath.Join(directory, "state.json"), "run", testThreadID)
	observer.fromThreadStatus(activityThreadStatus{Type: "idle"})
	return observer, manifest
}

func activityEvent(t *testing.T, method string, params any, id ...any) wireMessage {
	t.Helper()
	message := wireMessage{Method: method}
	var err error
	message.Params, err = json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) > 0 {
		message.ID, err = json.Marshal(id[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	return message
}

func activityTurn(t *testing.T, method, thread, turn, status string) wireMessage {
	t.Helper()
	return activityEvent(t, method, map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": status}})
}

func activityAt(t *testing.T, manifest, state string) Activity {
	t.Helper()
	activity, err := ReadActivity(manifest, "run", testThreadID)
	if err != nil || activity.State != state {
		t.Fatalf("activity = %+v, %v; want %s", activity, err, state)
	}
	return activity
}

func TestActivityPersistsExactTurnLifecycleWithoutDesktopAttachment(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	initial := activityAt(t, manifest, ActivityIdle)
	if initial.Sequence != 2 || initial.TurnID != "" {
		t.Fatalf("initial observation = %+v", initial)
	}
	for _, event := range []wireMessage{
		activityTurn(t, "turn/started", otherThreadID, "other", "inProgress"),
		activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": "malformed"}),
		activityTurn(t, "turn/completed", testThreadID, "unseen", "completed"),
	} {
		observer.fromServer(event)
	}
	if current := activityAt(t, manifest, ActivityIdle); current != initial {
		t.Fatalf("unrelated event changed observation: %+v", current)
	}
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	working := activityAt(t, manifest, ActivityWorking)
	if working.TurnID != "turn-a" || working.Sequence != initial.Sequence+1 || working.EventID == initial.EventID {
		t.Fatalf("working observation = %+v", working)
	}
	for _, event := range []wireMessage{
		activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"),
		activityTurn(t, "turn/completed", testThreadID, "unseen", "completed"),
		activityEvent(t, "item/agentMessage/delta", map[string]any{"threadId": testThreadID, "turnId": "turn-a", "delta": "private transcript"}),
		activityEvent(t, "item/commandExecution/requestApproval", map[string]any{"threadId": testThreadID, "turnId": "old-turn", "command": "private command"}, 99),
	} {
		observer.fromServer(event)
	}
	if current := activityAt(t, manifest, ActivityWorking); current != working {
		t.Fatalf("irrelevant event changed observation: %+v", current)
	}
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	completed := activityAt(t, manifest, ActivityCompleted)
	observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "idle"}}))
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	if current := activityAt(t, manifest, ActivityCompleted); current != completed {
		t.Fatalf("completion was replaced or duplicated: %+v", current)
	}
	for index, status := range []string{"failed", "interrupted", "completed"} {
		turn := []string{"turn-b", "turn-c", "turn-d"}[index]
		observer.fromServer(activityTurn(t, "turn/started", testThreadID, turn, "inProgress"))
		before := activityAt(t, manifest, ActivityWorking)
		observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "failed"))
		observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
		if current := activityAt(t, manifest, ActivityWorking); current != before {
			t.Fatalf("retired turn changed observation: %+v", current)
		}
		observer.fromServer(activityTurn(t, "turn/completed", testThreadID, turn, status))
		result := activityAt(t, manifest, status)
		if result.Sequence != before.Sequence+1 || result.EventID == completed.EventID {
			t.Fatalf("new completion has no event identity: %+v", result)
		}
	}
	// This file belongs to the runner, not a browser connection or service
	// cache. Reading after the observer is gone retains the exact result.
	observer = nil
	final := activityAt(t, manifest, ActivityCompleted)
	if final.TurnID != "turn-d" {
		t.Fatalf("detached result = %+v", final)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), "activity.json"))
	if err != nil || len(data) > maxActivityBytes || strings.Contains(string(data), "private") || strings.Contains(string(data), "command") {
		t.Fatalf("content entered observation: %q, %v", data, err)
	}
}

func TestActivityTracksOverlappingRequestsAndExactResponses(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	params := map[string]any{"threadId": testThreadID, "turnId": "turn-a", "command": "do not persist", "questions": "do not persist"}
	observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", params, 1))
	first := activityAt(t, manifest, ActivityWaitingApproval)
	observer.fromServer(activityEvent(t, "item/fileChange/requestApproval", params, "1"))
	second := activityAt(t, manifest, ActivityWaitingApproval)
	if second.EventID == first.EventID || second.Sequence != first.Sequence+1 {
		t.Fatalf("new request reused attention event: %+v", second)
	}
	observer.fromServer(activityEvent(t, "item/fileChange/requestApproval", params, "1"))
	if duplicate := activityAt(t, manifest, ActivityWaitingApproval); duplicate != second {
		t.Fatalf("duplicate request generated event: %+v", duplicate)
	}
	observer.fromServer(activityEvent(t, "item/tool/requestUserInput", params, 2))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromClient(wireMessage{ID: json.RawMessage(`1`), Result: json.RawMessage(`{"decision":"accept"}`)})
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": otherThreadID, "requestId": 2}))
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "turnId": "old", "requestId": 2}))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": 2}))
	activityAt(t, manifest, ActivityWaitingApproval)
	// A method-bearing frame reusing a response ID is not a response.
	observer.fromClient(wireMessage{ID: json.RawMessage(`"1"`), Method: "anything", Result: json.RawMessage(`{}`)})
	activityAt(t, manifest, ActivityWaitingApproval)
	observer.fromClient(wireMessage{ID: json.RawMessage(`"1"`), Error: json.RawMessage(`{"code":-1}`)})
	activityAt(t, manifest, ActivityWorking)
	for _, method := range []string{"item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request"} {
		observer.fromServer(activityEvent(t, method, params, 8))
		want := ActivityWaitingInput
		if strings.HasSuffix(method, "/requestApproval") {
			want = ActivityWaitingApproval
		}
		activityAt(t, manifest, want)
		observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": 8}))
		activityAt(t, manifest, ActivityWorking)
	}
	// MCP elicitation is the documented request whose turn ID is optional.
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", map[string]any{"threadId": testThreadID}, 9))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": 9}))
	activityAt(t, manifest, ActivityWorking)
	observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval", "waitingOnUserInput"}}}))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{}}}))
	activityAt(t, manifest, ActivityWorking)
	observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", params, 10))
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "interrupted"))
	result := activityAt(t, manifest, ActivityInterrupted)
	observer.fromClient(wireMessage{ID: json.RawMessage(`10`), Result: json.RawMessage(`{}`)})
	if after := activityAt(t, manifest, ActivityInterrupted); after != result {
		t.Fatalf("late response replaced result: %+v", after)
	}
}

func TestActivityAcceptsOpaqueProviderRequestIDsWithoutRetainingTheirText(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	params := map[string]any{"threadId": testThreadID, "turnId": "turn-a"}
	for _, id := range []any{"", "approval / private request ☃", strings.Repeat("private", 1000), int64(-9223372036854775808)} {
		request := activityEvent(t, "item/commandExecution/requestApproval", params, id)
		observer.fromServer(request)
		activityAt(t, manifest, ActivityWaitingApproval)
		for key := range observer.requests {
			if len(key) > 66 || strings.Contains(key, "private") {
				t.Fatalf("provider request text retained: %q", key)
			}
		}
		observer.fromClient(wireMessage{ID: request.ID, Result: json.RawMessage(`{}`)})
		activityAt(t, manifest, ActivityWorking)
	}
	for _, id := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`1.5`), json.RawMessage(`9223372036854775808`)} {
		request := activityEvent(t, "item/commandExecution/requestApproval", params)
		request.ID = id
		observer.fromServer(request)
		activityAt(t, manifest, ActivityWorking)
	}
	observer.fromServer(activityEvent(t, "tool/requestUserInput", params, "unsupported-method"))
	activityAt(t, manifest, ActivityWorking)
}

func TestActivityThreadStatusAndRequestsKeepIndependentEvidence(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	status := func(flags ...string) wireMessage {
		return activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "active", "activeFlags": flags}})
	}
	observer.fromServer(status())
	activityAt(t, manifest, ActivityUnknown)
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", map[string]any{"threadId": testThreadID}, 1))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromClient(wireMessage{ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)})
	// Thread activity alone cannot identify an exact turn. Resolving the
	// request returns to unknown until a live turn event establishes it.
	activityAt(t, manifest, ActivityUnknown)
	observer.fromServer(status("waitingOnApproval"))
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", map[string]any{"threadId": testThreadID}, 2))
	observer.fromClient(wireMessage{ID: json.RawMessage(`2`), Result: json.RawMessage(`{}`)})
	activityAt(t, manifest, ActivityWaitingApproval)
	observer.fromServer(status())
	activityAt(t, manifest, ActivityUnknown)
	observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "idle"}}))
	activityAt(t, manifest, ActivityIdle)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	observer.fromServer(status("waitingOnApproval"))
	observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", map[string]any{"threadId": testThreadID, "turnId": "turn-a"}, 3))
	observer.fromClient(wireMessage{ID: json.RawMessage(`3`), Result: json.RawMessage(`{}`)})
	activityAt(t, manifest, ActivityWaitingApproval)
	observer.fromServer(status())
	activityAt(t, manifest, ActivityWorking)
}

func TestActivityCorrelatedMCPRequestsOutliveTheirTurn(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	params := map[string]any{"threadId": testThreadID, "turnId": "turn-a"}
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", params, 1))
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-b", "inProgress"))
	activityAt(t, manifest, ActivityWaitingInput)
	// Correlation with a completed turn does not invalidate a standalone MCP
	// request for this same bound thread.
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", params, 2))
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": 1}))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": 2}))
	activityAt(t, manifest, ActivityWorking)
}

func TestActivityRequestOverflowRespectsRequestLifetime(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "mcpServer/elicitation/request"} {
		t.Run(method, func(t *testing.T) {
			observer, manifest := activityTestObserver(t)
			observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
			params := map[string]any{"threadId": testThreadID, "turnId": "turn-a"}
			for index := 0; index <= maxActivityRequests; index++ {
				observer.fromServer(activityEvent(t, method, params, index))
			}
			activityAt(t, manifest, ActivityUnknown)
			if len(observer.requests) != maxActivityRequests {
				t.Fatalf("request tracking exceeded its bound: %d", len(observer.requests))
			}
			observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
			if method != "mcpServer/elicitation/request" {
				activityAt(t, manifest, ActivityCompleted)
				return
			}
			activityAt(t, manifest, ActivityUnknown)
			for index := 0; index < maxActivityRequests; index++ {
				observer.fromClient(wireMessage{ID: json.RawMessage(strconv.Itoa(index)), Result: json.RawMessage(`{}`)})
			}
			observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-b", "inProgress"))
			observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-b", "completed"))
			activityAt(t, manifest, ActivityUnknown)
		})
	}
}

func TestActivityInitialThreadStatusDoesNotImportHistoricalTurnOutcome(t *testing.T) {
	for _, mode := range []string{ModeNew, ModeResume} {
		t.Run(mode, func(t *testing.T) {
			harness := newBridgeHarness(t, mode)
			manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
			method := "thread/resume"
			if mode == ModeNew {
				method = "thread/start"
			}
			harness.send(t, map[string]any{"id": 1, "method": method, "params": map[string]any{"threadId": testThreadID}})
			harness.upstreamMessage(t)
			harness.reply(t, map[string]any{"id": 1, "result": map[string]any{"thread": map[string]any{
				"id": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnUserInput"}},
				"turns": []any{map[string]any{"id": "historic", "status": "completed"}},
			}}})
			if mode == ModeNew {
				persist := harness.upstreamMessage(t)
				harness.reply(t, map[string]any{"id": persist.ID, "result": map[string]any{}})
			}
			harness.receive(t)
			if activity := activityAt(t, manifest, ActivityWaitingInput); activity.TurnID != "" {
				t.Fatalf("historical turn was imported into this run: %+v", activity)
			}
			harness.reply(t, map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{}}}})
			harness.receive(t)
			activityAt(t, manifest, ActivityUnknown)
		})
	}
}

func TestActivityActiveResumeSeedsOnlyCurrentInProgressTurn(t *testing.T) {
	for _, source := range []string{"turns", "initialTurnsPage"} {
		t.Run(source, func(t *testing.T) {
			harness := newBridgeHarness(t, ModeResume)
			manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
			harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]any{"threadId": testThreadID}})
			harness.upstreamMessage(t)
			turns := []any{map[string]any{"id": "historic", "status": "failed"}, map[string]any{"id": "active-turn", "status": "inProgress", "items": []any{map[string]any{"text": "private history"}}}}
			thread := map[string]any{"id": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{}}}
			result := map[string]any{"thread": thread}
			if source == "turns" {
				thread["turns"] = turns
			} else {
				result["initialTurnsPage"] = map[string]any{"data": turns}
			}
			harness.reply(t, map[string]any{"id": 1, "result": result})
			harness.receive(t)
			if activity := activityAt(t, manifest, ActivityWorking); activity.TurnID != "active-turn" {
				t.Fatalf("active resume lost current turn: %+v", activity)
			}
			harness.reply(t, map[string]any{"id": 2, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": testThreadID, "turnId": "active-turn"}})
			harness.receive(t)
			activityAt(t, manifest, ActivityWaitingApproval)
			harness.reply(t, map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": "active-turn", "status": "completed"}}})
			harness.receive(t)
			activityAt(t, manifest, ActivityCompleted)
		})
	}
}

func TestActivityActiveResumeWithoutCurrentTurnUsesLiveIdentity(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "turn/completed"} {
		t.Run(method, func(t *testing.T) {
			harness := newBridgeHarness(t, ModeResume)
			manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
			harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]any{"threadId": testThreadID, "excludeTurns": true}})
			harness.upstreamMessage(t)
			harness.reply(t, map[string]any{"id": 1, "result": map[string]any{"thread": map[string]any{"id": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{}}}}})
			harness.receive(t)
			activityAt(t, manifest, ActivityUnknown)
			params := map[string]any{"threadId": testThreadID, "turnId": "active-turn"}
			want := ActivityWaitingApproval
			if method == "turn/completed" {
				params["turn"] = map[string]any{"id": "active-turn", "status": "completed"}
				want = ActivityCompleted
			}
			harness.reply(t, map[string]any{"id": 2, "method": method, "params": params})
			harness.receive(t)
			if activity := activityAt(t, manifest, want); activity.TurnID != "active-turn" {
				t.Fatalf("live exact turn identity lost: %+v", activity)
			}
		})
	}
}

func TestActivityRetainsExactRequestsAndResponsesBeforeReadiness(t *testing.T) {
	for _, mode := range []string{ModeNew, ModeResume} {
		for _, resolved := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/resolved=%t", mode, resolved), func(t *testing.T) {
				harness := newBridgeHarness(t, mode)
				manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
				method := "thread/resume"
				if mode == ModeNew {
					method = "thread/start"
				}
				harness.send(t, map[string]any{"id": 1, "method": method, "params": map[string]any{"threadId": testThreadID}})
				harness.upstreamMessage(t)
				for _, threadID := range []string{otherThreadID, testThreadID} {
					harness.reply(t, map[string]any{"id": "private request/" + threadID, "method": "mcpServer/elicitation/request", "params": map[string]any{
						"threadId": threadID, "turnId": "prior-turn", "message": "private request content",
					}})
					harness.receive(t)
				}
				if _, err := ReadActivity(manifest, "run", testThreadID); !os.IsNotExist(err) {
					t.Fatalf("activity published before verified readiness: %v", err)
				}
				if resolved {
					harness.send(t, map[string]any{"id": "private request/" + testThreadID, "result": map[string]any{"action": "accept", "content": "private response"}})
					if reply := harness.upstreamMessage(t); string(reply.Result) == "" {
						t.Fatal("early response was not forwarded")
					}
				}
				harness.reply(t, map[string]any{"id": 1, "result": map[string]any{"thread": map[string]any{"id": testThreadID, "status": map[string]any{"type": "idle"}}}})
				if mode == ModeNew {
					persist := harness.upstreamMessage(t)
					harness.reply(t, map[string]any{"id": persist.ID, "result": map[string]any{}})
				}
				harness.receive(t)
				if resolved {
					activityAt(t, manifest, ActivityIdle)
					return
				}
				activityAt(t, manifest, ActivityWaitingInput)
				harness.reply(t, map[string]any{"method": "turn/started", "params": map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": "turn-a", "status": "inProgress"}}})
				harness.receive(t)
				harness.reply(t, map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": "turn-a", "status": "completed"}}})
				harness.receive(t)
				activityAt(t, manifest, ActivityWaitingInput)
				harness.reply(t, map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": testThreadID, "requestId": "private request/" + testThreadID}})
				harness.receive(t)
				activityAt(t, manifest, ActivityCompleted)
				data, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), "activity.json"))
				if err != nil || strings.Contains(string(data), "private") {
					t.Fatalf("pre-ready activity retained content: %s, %v", data, err)
				}
			})
		}
	}
}

func TestActivityPreReadyMetadataIsBoundedAndOverflowRemainsUnknown(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	bridge := newBridge(Manifest{SessionID: "session", RunID: "run", ThreadID: testThreadID, Mode: ModeResume}, filepath.Join(directory, "state.json"), io.Discard, nil)
	for index := 0; index <= maxActivityRequests; index++ {
		message := activityEvent(t, "mcpServer/elicitation/request", map[string]any{"threadId": testThreadID, "message": strings.Repeat("private body", 1000)}, fmt.Sprintf("private request/%d", index))
		event, valid := serverActivity(message)
		if !valid {
			t.Fatal("valid request was not recognized")
		}
		bridge.observeActivity(event)
	}
	if len(bridge.activityPending) != maxActivityRequests || !bridge.activityLost {
		t.Fatalf("unbounded startup metadata: count=%d, lost=%t", len(bridge.activityPending), bridge.activityLost)
	}
	if strings.Contains(fmt.Sprintf("%+v", bridge.activityPending), "private") {
		t.Fatal("startup metadata retained request text")
	}
	if err := bridge.publishReady(context.Background(), testThreadID); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "manifest.json")
	activityAt(t, manifest, ActivityUnknown)
	if bridge.activityPending != nil {
		t.Fatal("startup metadata survived readiness")
	}
	bridge.activity.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	bridge.activity.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	activityAt(t, manifest, ActivityUnknown)
}

func TestActivityReadinessWithoutProviderStatusRemainsUnknown(t *testing.T) {
	harness := newBridgeHarness(t, ModeResume)
	manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
	harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]any{"threadId": testThreadID}})
	harness.upstreamMessage(t)
	harness.reply(t, threadResponse(1, testThreadID))
	harness.receive(t)
	activityAt(t, manifest, ActivityUnknown)
	if state := stateAt(t, harness.path); state.Status != StatusReady {
		t.Fatalf("missing optional status changed readiness: %+v", state)
	}
}

func TestActivityPublicationFailureDoesNotChangeReadyStateOrForwarding(t *testing.T) {
	harness := newBridgeHarness(t, ModeResume)
	manifest := filepath.Join(filepath.Dir(harness.path), "manifest.json")
	if _, err := ReadActivity(manifest, "run", testThreadID); !os.IsNotExist(err) {
		t.Fatalf("activity published before ready: %v", err)
	}
	harness.send(t, map[string]any{"id": 1, "method": "thread/resume", "params": map[string]any{"threadId": testThreadID}})
	harness.upstreamMessage(t)
	harness.reply(t, threadResponse(1, testThreadID))
	harness.receive(t)
	activityAt(t, manifest, ActivityUnknown)
	path := filepath.Join(filepath.Dir(harness.path), "activity.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	// A destination directory makes the optional atomic publication fail.
	harness.reply(t, map[string]any{"method": "turn/started", "params": map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": "turn-a", "status": "inProgress"}}})
	if got := harness.receive(t); got.Method != "turn/started" {
		t.Fatalf("observer changed forwarding: %+v", got)
	}
	if _, err := ReadActivity(manifest, "run", testThreadID); !errors.Is(err, ErrActivityUnavailable) {
		t.Fatalf("publication error hidden: %v", err)
	}
	if state := stateAt(t, harness.path); state.Status != StatusReady || state.ErrorCode != "" {
		t.Fatalf("optional observation changed readiness: %+v", state)
	}
	harness.reply(t, map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": testThreadID, "status": "malformed"}})
	if got := harness.receive(t); got.Method != "thread/status/changed" {
		t.Fatalf("malformed observation was not forwarded: %+v", got)
	}
	harness.reply(t, map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": testThreadID, "turn": map[string]any{"id": "turn-a", "status": "failed", "error": map[string]any{"message": "private failure"}}}})
	if got := harness.receive(t); got.Method != "turn/completed" {
		t.Fatalf("completion changed: %+v", got)
	}
	activityAt(t, manifest, ActivityFailed)
	if state := stateAt(t, harness.path); state.Status != StatusReady {
		t.Fatalf("provider failure replaced process readiness: %+v", state)
	}
}

func TestActivityRuntimeUncertaintyRetainsTurnForRecoveryAndCompletion(t *testing.T) {
	for _, status := range []string{"systemError", "notLoaded"} {
		t.Run(status, func(t *testing.T) {
			observer, manifest := activityTestObserver(t)
			observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
			observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", map[string]any{"threadId": testThreadID, "turnId": "turn-a"}, 1))
			before := activityAt(t, manifest, ActivityWaitingApproval)
			observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": status}}))
			unknown := activityAt(t, manifest, ActivityUnknown)
			if unknown.TurnID != "turn-a" || unknown.Sequence != before.Sequence+1 {
				t.Fatalf("uncertainty lost exact turn: %+v", unknown)
			}
			observer.fromClient(wireMessage{ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)})
			if after := activityAt(t, manifest, ActivityUnknown); after != unknown {
				t.Fatalf("response guessed runtime recovery: %+v", after)
			}
			observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": "active", "activeFlags": []string{}}}))
			activityAt(t, manifest, ActivityWorking)
			observer.fromServer(activityEvent(t, "thread/status/changed", map[string]any{"threadId": testThreadID, "status": map[string]any{"type": status}}))
			activityAt(t, manifest, ActivityUnknown)
			observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "failed"))
			activityAt(t, manifest, ActivityFailed)
		})
	}
}

func TestActivitySameEventRetriesFailedPublicationAndClearsMarker(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	observer.publish = func(string, Activity) error { return errors.New("fixture storage failure") }
	event := activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress")
	observer.fromServer(event)
	if _, err := ReadActivity(manifest, "run", testThreadID); !errors.Is(err, ErrActivityUnavailable) {
		t.Fatalf("publication failure hidden: %v", err)
	}
	failed := observer.current
	if !observer.dirty {
		t.Fatal("failed observation was marked published")
	}
	observer.publish = writeActivity
	observer.fromServer(event)
	recovered := activityAt(t, manifest, ActivityWorking)
	if recovered != failed || observer.dirty {
		t.Fatalf("retry generated another event or remained dirty: %+v", recovered)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(manifest), activityUnavailableFile)); !os.IsNotExist(err) {
		t.Fatalf("unavailability marker survived recovery: %v", err)
	}
}

func TestActivityThreadRequestsSurviveTurnBoundariesUntilExactResolution(t *testing.T) {
	observer, manifest := activityTestObserver(t)
	threadParams := map[string]any{"threadId": testThreadID}
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", threadParams, "thread-request"))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-a", "inProgress"))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "item/commandExecution/requestApproval", map[string]any{"threadId": testThreadID, "turnId": "turn-a"}, "turn-request"))
	before := activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-a", "completed"))
	completedWhileWaiting := activityAt(t, manifest, ActivityWaitingInput)
	if len(observer.requests) != 1 || completedWhileWaiting.EventID == before.EventID {
		t.Fatalf("completion lost thread request or event: %+v, %+v", observer.requests, completedWhileWaiting)
	}
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": "turn-request"}))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromClient(wireMessage{ID: json.RawMessage(`"thread-request"`), Result: json.RawMessage(`{}`)})
	activityAt(t, manifest, ActivityCompleted)
	observer.fromServer(activityEvent(t, "mcpServer/elicitation/request", threadParams, "second-request"))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityTurn(t, "turn/started", testThreadID, "turn-b", "inProgress"))
	activityAt(t, manifest, ActivityWaitingInput)
	observer.fromServer(activityEvent(t, "serverRequest/resolved", map[string]any{"threadId": testThreadID, "requestId": "second-request"}))
	activityAt(t, manifest, ActivityWorking)
	observer.fromServer(activityTurn(t, "turn/completed", testThreadID, "turn-b", "interrupted"))
	activityAt(t, manifest, ActivityInterrupted)
}

func TestReadActivityRejectsDamagedUnsafeOrMismatchedObservations(t *testing.T) {
	for _, kind := range []string{"wrong-run", "wrong-thread", "unknown-field", "duplicate-field", "trailing", "state", "event", "sequence", "large-sequence", "time", "terminal-no-turn", "oversized", "public", "symlink", "hardlink", "directory", "marker"} {
		t.Run(kind, func(t *testing.T) {
			observer, manifest := activityTestObserver(t)
			path := observer.path
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			runID, threadID := "run", testThreadID
			switch kind {
			case "wrong-run":
				runID = "different"
			case "wrong-thread":
				threadID = otherThreadID
			case "unknown-field":
				data = append([]byte(`{"transcript":"private",`), data[1:]...)
			case "duplicate-field":
				data = append([]byte(`{"runId":"run",`), data[1:]...)
			case "trailing":
				data = append(data, []byte(`{}`)...)
			case "state":
				observer.current.State = "invented"
				data, _ = json.Marshal(observer.current)
			case "event":
				observer.current.EventID = "unsafe event"
				data, _ = json.Marshal(observer.current)
			case "sequence":
				observer.current.Sequence = 0
				data, _ = json.Marshal(observer.current)
			case "large-sequence":
				observer.current.Sequence = maxActivitySequence + 1
				data, _ = json.Marshal(observer.current)
			case "time":
				observer.current.UpdatedAt = time.Time{}
				data, _ = json.Marshal(observer.current)
			case "terminal-no-turn":
				observer.current.State = ActivityCompleted
				data, _ = json.Marshal(observer.current)
			case "oversized":
				data = []byte(strings.Repeat("x", maxActivityBytes+1))
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				other := filepath.Join(filepath.Dir(path), "other.json")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					err = os.Symlink(other, path)
				} else {
					err = os.Link(other, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "marker":
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), activityUnavailableFile), []byte("activity unavailable\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "public" && kind != "symlink" && kind != "hardlink" && kind != "directory" {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if activity, err := ReadActivity(manifest, runID, threadID); !errors.Is(err, ErrActivityUnavailable) || activity != (Activity{}) {
				t.Fatalf("unsafe activity accepted: %+v, %v", activity, err)
			}
		})
	}
}
