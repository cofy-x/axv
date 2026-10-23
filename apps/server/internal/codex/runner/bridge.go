package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"github.com/cofy-x/axv/apps/server/internal/vmwork"
)

const managedConversationMessage = "This AXV session keeps one Codex conversation. Use New Session in AXV to start another conversation."

type contextRequest struct {
	batchIDs       []string
	method         string
	expectedTurnID string
}

type protocolBridge struct {
	manifest Manifest
	state    State
	path     string
	stdin    io.Writer
	client   *websocket.Conn

	startIssued     bool
	pending         map[string]string
	persistID       string
	persisting      bool
	candidateID     string
	buffer          [][]byte
	bufferBytes     int
	deadline        <-chan time.Time
	ready           bool
	contextRequests map[string]contextRequest
	vmWork          *vmwork.Context
	vmWorkLoaded    bool
	activity        *activityObserver
	activityPending []activityObservation
	activityLost    bool
}

func newBridge(manifest Manifest, statePath string, stdin io.Writer, client *websocket.Conn) *protocolBridge {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	return &protocolBridge{
		manifest: manifest,
		state: State{
			RunID: manifest.RunID, ThreadID: manifest.ThreadID,
			HistoryHome: manifest.HistoryHome, Status: StatusStarting,
		},
		path: statePath, stdin: stdin, client: client,
		pending: make(map[string]string), persistID: "axv-runner-persist-" + hex.EncodeToString(nonce),
		contextRequests: make(map[string]contextRequest),
		vmWork:          manifest.vmWork, vmWorkLoaded: manifest.VMContextPath == "" || manifest.vmWork != nil,
	}
}

func (bridge *protocolBridge) fail(code string) error {
	bridge.state.Status = StatusFailed
	bridge.state.ErrorCode = code
	_ = writeState(bridge.path, bridge.state)
	return ErrProtocol
}

func (bridge *protocolBridge) publishReady(ctx context.Context, threadID string) error {
	state := bridge.state
	state.ThreadID = threadID
	// Retain the verified provider identity even if queue publication fails.
	bridge.state.ThreadID = threadID
	queue := ContextQueue{Directory: filepath.Dir(bridge.path), SessionID: bridge.manifest.SessionID, RunID: bridge.manifest.RunID, ThreadID: threadID}
	if queue.Initialize(ctx) != nil {
		return bridge.fail("codex_context_state_failed")
	}
	state.ContextVersion = 1
	bridge.state.ContextVersion = 1
	state.Status = StatusReady
	state.ErrorCode = ""
	if writeState(bridge.path, state) != nil {
		return bridge.fail("codex_persist_failed")
	}
	bridge.state = state
	bridge.ready = true
	bridge.activity = newActivityObserver(bridge.path, state.RunID, threadID)
	// Lost metadata may include standalone requests that outlive turns.
	bridge.activity.threadOverflow = bridge.activityLost
	for _, event := range bridge.activityPending {
		bridge.activity.observe(event)
	}
	bridge.activityPending = nil
	return nil
}

func (bridge *protocolBridge) observeActivity(event activityObservation) {
	if bridge.activity != nil {
		bridge.activity.observe(event)
		return
	}
	threadID := bridge.manifest.ThreadID
	if bridge.candidateID != "" {
		threadID = bridge.candidateID
	}
	if threadID != "" && event.threadID != "" && event.threadID != threadID {
		return
	}
	if event.method == "clientResponse" {
		// Before readiness, retain only responses to observed activity
		// requests. Other provider requests are outside this observer.
		matched := false
		for _, pending := range bridge.activityPending {
			if pending.requestID == event.requestID {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
	}
	if len(bridge.activityPending) >= maxActivityRequests {
		bridge.activityLost = true
		return
	}
	bridge.activityPending = append(bridge.activityPending, event)
}

func (bridge *protocolBridge) send(ctx context.Context, raw []byte) error {
	if bridge.client.Write(ctx, websocket.MessageText, raw) != nil {
		return ErrUnavailable
	}
	return nil
}

func (bridge *protocolBridge) reject(ctx context.Context, id json.RawMessage) error {
	if len(id) == 0 {
		return nil
	}
	data, _ := json.Marshal(map[string]any{
		"id":    id,
		"error": map[string]any{"code": -32600, "message": managedConversationMessage},
	})
	return bridge.send(ctx, data)
}

func (bridge *protocolBridge) fromClient(ctx context.Context, raw []byte) error {
	var message wireMessage
	if json.Unmarshal(raw, &message) != nil {
		return bridge.fail("codex_protocol_error")
	}
	var id string
	if json.Unmarshal(message.ID, &id) == nil && id == bridge.persistID {
		return bridge.reject(ctx, message.ID)
	}
	method := message.Method
	if method == "thread/fork" {
		return bridge.reject(ctx, message.ID)
	}
	if method == "thread/start" {
		if bridge.manifest.Mode != ModeNew || bridge.startIssued || len(message.ID) == 0 {
			return bridge.reject(ctx, message.ID)
		}
		var params struct {
			Ephemeral bool `json:"ephemeral"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.Ephemeral {
			return bridge.reject(ctx, message.ID)
		}
		bridge.startIssued = true
		bridge.pending[string(message.ID)] = method
	}
	if method == "thread/resume" {
		var params struct {
			ThreadID string          `json:"threadId"`
			Path     json.RawMessage `json:"path"`
			History  json.RawMessage `json:"history"`
		}
		if json.Unmarshal(message.Params, &params) != nil || len(message.ID) == 0 ||
			bridge.state.ThreadID == "" || params.ThreadID != bridge.state.ThreadID ||
			(len(params.Path) > 0 && string(params.Path) != "null") ||
			(len(params.History) > 0 && string(params.History) != "null") {
			return bridge.reject(ctx, message.ID)
		}
		bridge.pending[string(message.ID)] = method
	}
	if method == "turn/start" || method == "turn/steer" {
		return bridge.forwardTurn(ctx, raw, message)
	}
	if err := writeRawMessage(bridge.stdin, raw); err != nil {
		return err
	}
	if id := clientActivityResponse(message); id != "" {
		bridge.observeActivity(activityObservation{method: "clientResponse", requestID: id})
	}
	return nil
}

func (bridge *protocolBridge) fromServer(ctx context.Context, raw []byte) error {
	var message wireMessage
	if json.Unmarshal(raw, &message) != nil {
		return bridge.fail("codex_protocol_error")
	}
	if event, valid := serverActivity(message); valid {
		bridge.observeActivity(event)
	}
	var id string
	if json.Unmarshal(message.ID, &id) == nil && id == bridge.persistID {
		if !bridge.persisting || len(message.Error) != 0 || len(message.Result) == 0 {
			return bridge.fail("codex_persist_failed")
		}
		if bridge.publishReady(ctx, bridge.candidateID) != nil {
			return ErrUnavailable
		}
		bridge.persisting = false
		bridge.deadline = nil
		for _, buffered := range bridge.buffer {
			if err := bridge.send(ctx, buffered); err != nil {
				return err
			}
		}
		bridge.buffer = nil
		bridge.bufferBytes = 0
		return nil
	}
	if bridge.persisting {
		bridge.bufferBytes += len(raw)
		if bridge.bufferBytes > maxMessageBytes {
			return bridge.fail("codex_protocol_error")
		}
		bridge.buffer = append(bridge.buffer, raw)
		return nil
	}
	// A server approval request may reuse a client request's numeric ID. Only
	// responses, never server requests or notifications, bind conversations.
	if message.Method == "" {
		if request := bridge.contextRequests[string(message.ID)]; len(request.batchIDs) > 0 {
			delete(bridge.contextRequests, string(message.ID))
			var response struct {
				TurnID string `json:"turnId"`
				Turn   struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			status, turnID := "unknown", ""
			if len(message.Error) != 0 && string(message.Error) != "null" {
				status = "failed"
			} else if json.Unmarshal(message.Result, &response) == nil {
				if request.method == "turn/steer" {
					if contextIdentifier(response.TurnID) && response.TurnID == request.expectedTurnID {
						status, turnID = "accepted", response.TurnID
					}
				} else if contextIdentifier(response.Turn.ID) {
					status, turnID = "accepted", response.Turn.ID
				}
			}
			if err := bridge.contextQueue().acknowledge(ctx, request.batchIDs, turnID, status); err != nil {
				return bridge.fail("codex_context_state_failed")
			}
		}
		method := bridge.pending[string(message.ID)]
		delete(bridge.pending, string(message.ID))
		if method == "thread/start" || method == "thread/resume" {
			if len(message.Error) != 0 {
				_ = bridge.send(ctx, raw)
				if bridge.ready {
					return nil
				}
				if method == "thread/resume" {
					return bridge.fail("codex_history_unavailable")
				}
				return bridge.fail("codex_start_failed")
			}
			threadID := responseThreadID(message.Result)
			if !threadIDPattern.MatchString(threadID) ||
				(method == "thread/resume" && threadID != bridge.state.ThreadID) {
				return bridge.fail("codex_protocol_error")
			}
			if !bridge.ready {
				if event, valid := threadResponseActivity(message.Result); valid {
					bridge.observeActivity(event)
				}
			}
			if method == "thread/start" {
				// A new empty thread is not durable yet. Naming it through the
				// official API persists it without inventing a user/model turn.
				bridge.candidateID = threadID
				bridge.persisting = true
				bridge.deadline = time.After(15 * time.Second)
				bridge.buffer = append(bridge.buffer, raw)
				bridge.bufferBytes = len(raw)
				return writeMessage(bridge.stdin, map[string]any{
					"id": bridge.persistID, "method": "thread/name/set",
					"params": map[string]string{"threadId": threadID, "name": "AXV " + bridge.manifest.SessionID},
				})
			}
			if !bridge.ready {
				if bridge.publishReady(ctx, threadID) != nil {
					return ErrUnavailable
				}
			}
		}
	}
	return bridge.send(ctx, raw)
}

func (bridge *protocolBridge) run(ctx context.Context, stdout io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer bridge.client.CloseNow()
	defer func() {
		if bridge.ready {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = bridge.contextQueue().Close(cleanup)
		}
	}()
	serverReads := readMessages(ctx, stdout)
	clientReads := make(chan wireRead, 1)
	go func() {
		defer close(clientReads)
		for {
			kind, data, err := bridge.client.Read(ctx)
			if err == nil && kind != websocket.MessageText {
				err = ErrProtocol
			}
			select {
			case clientReads <- wireRead{raw: data, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-bridge.deadline:
			return bridge.fail("codex_persist_failed")
		case read, ok := <-clientReads:
			if !ok || read.err != nil {
				return nil
			}
			if err := bridge.fromClient(ctx, read.raw); err != nil {
				return err
			}
		case read, ok := <-serverReads:
			if !ok || read.err != nil {
				return bridge.fail("codex_runtime_failed")
			}
			if err := bridge.fromServer(ctx, read.raw); err != nil {
				return err
			}
		}
	}
}

func (bridge *protocolBridge) contextQueue() ContextQueue {
	return ContextQueue{Directory: filepath.Dir(bridge.path), SessionID: bridge.manifest.SessionID, RunID: bridge.manifest.RunID, ThreadID: bridge.state.ThreadID}
}

// The official TUI remains the only producer of turns. Preserve every original
// turn override and input item, appending references only to its next actual
// user message for this exact provider conversation.
func (bridge *protocolBridge) forwardTurn(ctx context.Context, raw []byte, message wireMessage) error {
	var params map[string]json.RawMessage
	var threadID string
	var input []json.RawMessage
	if json.Unmarshal(message.Params, &params) != nil || json.Unmarshal(params["threadId"], &threadID) != nil ||
		!bridge.ready || threadID != bridge.state.ThreadID || len(message.ID) == 0 {
		return bridge.reject(ctx, message.ID)
	}
	if json.Unmarshal(params["input"], &input) != nil || len(input) == 0 ||
		(len(params["toolOutput"]) != 0 && string(params["toolOutput"]) != "null") {
		return writeRawMessage(bridge.stdin, raw)
	}
	expectedTurnID := ""
	if message.Method == "turn/steer" &&
		(json.Unmarshal(params["expectedTurnId"], &expectedTurnID) != nil || !contextIdentifier(expectedTurnID)) {
		// An invalid steer is not a valid user submission. Let the provider
		// reject it without consuming an attachment intended for real input.
		return writeRawMessage(bridge.stdin, raw)
	}
	if _, exists := bridge.contextRequests[string(message.ID)]; exists {
		return bridge.reject(ctx, message.ID)
	}
	workInput, err := bridge.vmWorkInput()
	if err != nil {
		return bridge.fail("codex_vm_context_failed")
	}
	input = append(input, workInput...)
	workOnly := append([]json.RawMessage(nil), input...)
	documents, ids, err := bridge.contextQueue().claim(ctx)
	if err != nil {
		return bridge.fail("codex_context_state_failed")
	}
	if len(ids) == 0 && len(workInput) == 0 {
		return writeRawMessage(bridge.stdin, raw)
	}
	for _, document := range documents {
		item, _ := json.Marshal(map[string]any{"type": "text", "text": contextText(document), "text_elements": []any{}})
		input = append(input, item)
	}
	params["input"], _ = json.Marshal(input)
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil {
		return bridge.fail("codex_protocol_error")
	}
	outer["params"], _ = json.Marshal(params)
	augmented, err := json.Marshal(outer)
	if err != nil || len(augmented) >= maxMessageBytes {
		if len(ids) > 0 {
			if err := bridge.contextQueue().acknowledge(ctx, ids, "", "failed", "The combined message exceeded the Codex protocol limit. These snapshots were not submitted."); err != nil {
				return bridge.fail("codex_context_state_failed")
			}
		}
		if len(workInput) == 0 {
			return writeRawMessage(bridge.stdin, raw)
		}
		params["input"], _ = json.Marshal(workOnly)
		outer["params"], _ = json.Marshal(params)
		augmented, err = json.Marshal(outer)
		if err != nil || len(augmented) >= maxMessageBytes {
			rejection, _ := json.Marshal(map[string]any{"id": message.ID, "error": map[string]any{"code": -32602, "message": "This message leaves no room for its VM project context. Reduce the input and submit it again."}})
			return bridge.send(ctx, rejection)
		}
		return writeRawMessage(bridge.stdin, augmented)
	}
	if len(ids) > 0 {
		bridge.contextRequests[string(message.ID)] = contextRequest{batchIDs: ids, method: message.Method, expectedTurnID: expectedTurnID}
	}
	return writeRawMessage(bridge.stdin, augmented)
}
