package runner

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ActivityUnknown         = "unknown"
	ActivityIdle            = "idle"
	ActivityWorking         = "working"
	ActivityWaitingApproval = "waiting_approval"
	ActivityWaitingInput    = "waiting_input"
	ActivityCompleted       = "completed"
	ActivityFailed          = "failed"
	ActivityInterrupted     = "interrupted"

	maxActivityBytes    = 4096
	maxActivityRequests = 256
	maxActivitySequence = 1<<53 - 1 // Integers remain exact in browser JSON readers.
)

var (
	ErrActivityUnavailable = errors.New("Codex activity is unavailable")
	activityIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	activityEventPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Activity is an optional observation of one exact run's provider thread. It
// is independent of readiness and process exit, and never contains user or
// provider content. A completed turn does not mean the runner has exited.
type Activity struct {
	RunID     string    `json:"runId"`
	ThreadID  string    `json:"threadId"`
	State     string    `json:"state"`
	EventID   string    `json:"eventId"`
	Sequence  uint64    `json:"sequence"`
	TurnID    string    `json:"turnId,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func validActivityState(state string) bool {
	switch state {
	case ActivityUnknown, ActivityIdle, ActivityWorking, ActivityWaitingApproval,
		ActivityWaitingInput, ActivityCompleted, ActivityFailed, ActivityInterrupted:
		return true
	}
	return false
}

func validActivity(activity Activity, runID, threadID string) bool {
	return activityIDPattern.MatchString(runID) && threadIDPattern.MatchString(threadID) &&
		activity.RunID == runID && activity.ThreadID == threadID && validActivityState(activity.State) &&
		activityEventPattern.MatchString(activity.EventID) && activity.Sequence > 0 && activity.Sequence <= maxActivitySequence &&
		(activity.TurnID == "" || activityIDPattern.MatchString(activity.TurnID)) &&
		((activity.State != ActivityCompleted && activity.State != ActivityFailed && activity.State != ActivityInterrupted) || activity.TurnID != "") &&
		!activity.UpdatedAt.IsZero() && activity.UpdatedAt.UnixMilli() > 0
}

type activityRequest struct {
	kind   string
	turnID string
}

type activityThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

type activityTurnIdentity struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// activityObservation is bounded protocol metadata. The bridge can retain it
// before readiness without retaining provider content or choosing a thread.
type activityObservation struct {
	method    string
	threadID  string
	turnID    string
	turnState string
	requestID string
	status    activityThreadStatus
}

// The bridge is the single owner of this observer. Its input is the official
// app-server stream, even when no browser or desktop service is attached.
type activityObserver struct {
	current                 Activity
	path                    string
	publish                 func(string, Activity) error
	unavailable             func(string)
	now                     func() time.Time
	dirty                   bool
	active                  bool
	threadState             string
	approvalFlag, inputFlag bool
	requests                map[string]activityRequest
	retired                 map[string]bool
	retiredOrder            []string
	overflow                bool
	threadOverflow          bool
	uncertain               bool
	turnOutcome             string
}

func newActivityObserver(statePath, runID, threadID string) *activityObserver {
	observer := &activityObserver{
		current: Activity{RunID: runID, ThreadID: threadID},
		path:    filepath.Join(filepath.Dir(statePath), "activity.json"),
		publish: writeActivity, unavailable: markActivityUnavailable, now: time.Now,
		requests: make(map[string]activityRequest), retired: make(map[string]bool),
	}
	observer.record(ActivityUnknown, "")
	return observer
}

func (observer *activityObserver) record(state, turnID string) {
	observer.advance(state, turnID, false)
}

func (observer *activityObserver) advance(state, turnID string, newEvent bool) {
	if newEvent || observer.current.State != state || observer.current.TurnID != turnID {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil || observer.current.Sequence >= maxActivitySequence {
			observer.unavailable(observer.path)
			return
		}
		observer.current.State, observer.current.TurnID = state, turnID
		observer.current.EventID = hex.EncodeToString(nonce[:])
		observer.current.Sequence++
		observer.current.UpdatedAt = observer.now().UTC()
		observer.dirty = true
	}
	if !observer.dirty {
		return
	}
	if err := observer.publish(observer.path, observer.current); err != nil {
		observer.unavailable(observer.path)
		// Optional observation must never change the provider's outcome. The
		// fixed message deliberately excludes protocol data and native paths.
		log.Print("Codex activity publication is unavailable")
		return
	}
	observer.dirty = false
}

func (observer *activityObserver) waitingState() string {
	if observer.overflow || observer.threadOverflow || observer.uncertain {
		return ActivityUnknown
	}
	approval, input := observer.approvalFlag, observer.inputFlag
	for _, request := range observer.requests {
		approval = approval || request.kind == ActivityWaitingApproval
		input = input || request.kind == ActivityWaitingInput
	}
	if input {
		return ActivityWaitingInput
	}
	if approval {
		return ActivityWaitingApproval
	}
	if observer.active {
		return ActivityWorking
	}
	if observer.turnOutcome != "" {
		return observer.turnOutcome
	}
	if observer.threadState == ActivityIdle {
		return ActivityIdle
	}
	return ActivityUnknown
}

func (observer *activityObserver) clearTurnRequests() {
	for id, request := range observer.requests {
		// An MCP elicitation can belong to the thread rather than a turn.
		// Only an exact response/resolved notification ends that request.
		if request.turnID != "" {
			delete(observer.requests, id)
		}
	}
}

func (observer *activityObserver) retire(turnID string) {
	if turnID == "" || observer.retired[turnID] {
		return
	}
	observer.retired[turnID] = true
	observer.retiredOrder = append(observer.retiredOrder, turnID)
	if len(observer.retiredOrder) > maxActivityRequests {
		delete(observer.retired, observer.retiredOrder[0])
		observer.retiredOrder = observer.retiredOrder[1:]
	}
}

func activityRequestID(raw json.RawMessage) string {
	var text string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &text) == nil {
		// Provider request IDs are arbitrary strings, not AXV identifiers.
		// Keep their exact identity with bounded memory and no retained text.
		digest := sha256.Sum256([]byte(text))
		return "s:" + hex.EncodeToString(digest[:])
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		if integer, err := number.Int64(); err == nil {
			return "n:" + strconv.FormatInt(integer, 10)
		}
	}
	return ""
}

func (observer *activityObserver) resolve(id string) {
	_, ok := observer.requests[id]
	if !ok {
		return
	}
	delete(observer.requests, id)
	// Status flags describe the entire thread, so one exact response cannot
	// clear a flag that may also cover another outstanding provider request.
	// A following status notification or exact turn outcome clears the flags.
	observer.record(observer.waitingState(), observer.current.TurnID)
}

func clientActivityResponse(message wireMessage) string {
	if message.Method == "" && (len(message.Result) > 0 || len(message.Error) > 0) {
		return activityRequestID(message.ID)
	}
	return ""
}

func (observer *activityObserver) fromClient(message wireMessage) {
	observer.resolve(clientActivityResponse(message))
}

// A resume response reports current thread activity, but its turns include
// historical outcomes from other runs. Seed only its current status.
func threadResponseActivity(result json.RawMessage) (activityObservation, bool) {
	var response struct {
		Thread struct {
			ID     string                 `json:"id"`
			Status activityThreadStatus   `json:"status"`
			Turns  []activityTurnIdentity `json:"turns"`
		} `json:"thread"`
		InitialTurnsPage struct {
			Data []activityTurnIdentity `json:"data"`
		} `json:"initialTurnsPage"`
	}
	if json.Unmarshal(result, &response) != nil || !threadIDPattern.MatchString(response.Thread.ID) {
		return activityObservation{}, false
	}
	status, valid := boundedActivityStatus(response.Thread.Status)
	event := activityObservation{method: "thread/status/changed", threadID: response.Thread.ID, status: status}
	if !valid || status.Type != "active" {
		return event, valid
	}
	// A resume can rejoin an already active thread, with its current turn
	// included in full history or in the requested recent-turn page. Terminal
	// history never becomes a result for this run. Conflicting active IDs
	// leave the current turn unknown until a live event establishes it.
	for _, turns := range [][]activityTurnIdentity{response.Thread.Turns, response.InitialTurnsPage.Data} {
		for _, turn := range turns {
			if turn.Status != "inProgress" {
				continue
			}
			if !activityIDPattern.MatchString(turn.ID) || (event.turnID != "" && event.turnID != turn.ID) {
				event.turnID = ""
				return event, true
			}
			event.turnID = turn.ID
		}
	}
	return event, true
}

func boundedActivityStatus(status activityThreadStatus) (activityThreadStatus, bool) {
	switch status.Type {
	case "idle", "notLoaded", "systemError":
		return activityThreadStatus{Type: status.Type}, true
	case "active":
		if len(status.ActiveFlags) > maxActivityRequests {
			return activityThreadStatus{}, false
		}
		approval, input := false, false
		for _, flag := range status.ActiveFlags {
			switch flag {
			case "waitingOnApproval":
				approval = true
			case "waitingOnUserInput":
				input = true
			default:
				return activityThreadStatus{}, false
			}
		}
		status.ActiveFlags = nil
		if approval {
			status.ActiveFlags = append(status.ActiveFlags, "waitingOnApproval")
		}
		if input {
			status.ActiveFlags = append(status.ActiveFlags, "waitingOnUserInput")
		}
		return status, true
	}
	return activityThreadStatus{}, false
}

func (observer *activityObserver) fromThreadStatus(status activityThreadStatus) {
	// Keep the exact turn result visible through the provider's following
	// idle notification. Only a new turn can supersede that result.
	if !observer.active && observer.current.TurnID != "" {
		return
	}
	switch status.Type {
	case "active":
		approval, input := false, false
		for _, flag := range status.ActiveFlags {
			switch flag {
			case "waitingOnApproval":
				approval = true
			case "waitingOnUserInput":
				input = true
			}
		}
		observer.approvalFlag, observer.inputFlag = approval, input
		observer.threadState = ActivityWorking
		observer.uncertain = false
		observer.record(observer.waitingState(), observer.current.TurnID)
	case "idle":
		observer.threadState = ActivityIdle
		observer.approvalFlag, observer.inputFlag = false, false
		observer.uncertain = false
		observer.record(observer.waitingState(), observer.current.TurnID)
	case "notLoaded", "systemError":
		// A runtime status is not a terminal turn outcome. Preserve the
		// exact turn and requests for a later recovery or completion while
		// exposing that its current activity can no longer be established.
		observer.uncertain = true
		observer.record(ActivityUnknown, observer.current.TurnID)
	}
}

func serverActivity(message wireMessage) (activityObservation, bool) {
	switch message.Method {
	case "turn/started", "turn/completed", "thread/status/changed", "serverRequest/resolved",
		"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval",
		"item/tool/requestUserInput", "mcpServer/elicitation/request":
	default:
		return activityObservation{}, false
	}
	// Parse only identity and lifecycle fields. Content, commands, errors and
	// questions remain exclusively on their original provider/TUI path.
	var params struct {
		ThreadID  string               `json:"threadId"`
		TurnID    string               `json:"turnId"`
		RequestID json.RawMessage      `json:"requestId"`
		Turn      activityTurnIdentity `json:"turn"`
		Status    activityThreadStatus `json:"status"`
	}
	if json.Unmarshal(message.Params, &params) != nil || !threadIDPattern.MatchString(params.ThreadID) {
		return activityObservation{}, false
	}
	event := activityObservation{method: message.Method, threadID: params.ThreadID}
	switch message.Method {
	case "turn/started", "turn/completed":
		if !activityIDPattern.MatchString(params.Turn.ID) {
			return activityObservation{}, false
		}
		event.turnID, event.turnState = params.Turn.ID, params.Turn.Status
		if message.Method == "turn/started" {
			return event, params.Turn.Status == "inProgress"
		}
		return event, params.Turn.Status == ActivityCompleted || params.Turn.Status == ActivityFailed || params.Turn.Status == ActivityInterrupted
	case "thread/status/changed":
		var valid bool
		event.status, valid = boundedActivityStatus(params.Status)
		return event, valid
	case "serverRequest/resolved":
		event.requestID = activityRequestID(params.RequestID)
	default:
		event.requestID = activityRequestID(message.ID)
	}
	if params.TurnID != "" && !activityIDPattern.MatchString(params.TurnID) {
		return activityObservation{}, false
	}
	event.turnID = params.TurnID
	return event, event.requestID != ""
}

func (observer *activityObserver) fromServer(message wireMessage) {
	if event, valid := serverActivity(message); valid {
		observer.observe(event)
	}
}

func (observer *activityObserver) observe(event activityObservation) {
	if event.method == "clientResponse" {
		observer.resolve(event.requestID)
		return
	}
	if event.threadID != observer.current.ThreadID {
		return
	}
	switch event.method {
	case "turn/started":
		if observer.retired[event.turnID] {
			return
		}
		if observer.current.TurnID == event.turnID {
			observer.record(observer.current.State, observer.current.TurnID)
			return
		}
		observer.retire(observer.current.TurnID)
		observer.active = true
		observer.threadState = ActivityWorking
		observer.turnOutcome = ""
		observer.approvalFlag, observer.inputFlag, observer.overflow, observer.uncertain = false, false, false, false
		observer.clearTurnRequests()
		observer.record(observer.waitingState(), event.turnID)
	case "turn/completed":
		if observer.current.TurnID == "" && observer.threadState == ActivityWorking {
			// This live completion is exact evidence for a turn omitted from
			// the initial paginated or excluded history.
			observer.active = true
			observer.current.TurnID = event.turnID
		}
		if event.turnID != observer.current.TurnID {
			return
		}
		state := event.turnState
		if !observer.active {
			if observer.turnOutcome == state {
				observer.record(observer.waitingState(), observer.current.TurnID)
			}
			return
		}
		observer.active = false
		observer.threadState = ActivityIdle
		observer.turnOutcome = state
		observer.approvalFlag, observer.inputFlag, observer.overflow, observer.uncertain = false, false, false, false
		observer.clearTurnRequests()
		observer.advance(observer.waitingState(), event.turnID, true)
	case "thread/status/changed":
		if event.turnID != "" {
			observer.observe(activityObservation{method: "turn/started", threadID: event.threadID, turnID: event.turnID, turnState: "inProgress"})
		}
		observer.fromThreadStatus(event.status)
	case "serverRequest/resolved":
		if event.turnID != "" && event.turnID != observer.current.TurnID {
			return
		}
		observer.resolve(event.requestID)
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval",
		"item/tool/requestUserInput", "mcpServer/elicitation/request":
		id, turnID := event.requestID, event.turnID
		if event.method == "mcpServer/elicitation/request" {
			// MCP turnId is optional correlation, not request identity or
			// lifetime. Even a correlated elicitation can outlive that turn.
			turnID = ""
		} else {
			if observer.current.TurnID == "" && observer.threadState == ActivityWorking && turnID != "" {
				// A live turn-scoped request can identify the active turn when
				// the resume response deliberately excluded turn history.
				observer.active = true
				observer.current.TurnID = turnID
			}
			if !observer.active || turnID == "" || turnID != observer.current.TurnID {
				return
			}
		}
		kind := ActivityWaitingInput
		if strings.HasSuffix(event.method, "/requestApproval") {
			kind = ActivityWaitingApproval
		}
		_, existed := observer.requests[id]
		if previous, ok := observer.requests[id]; ok {
			if previous.kind != kind || previous.turnID != turnID {
				return
			}
		} else if len(observer.requests) >= maxActivityRequests {
			if turnID == "" {
				// Untracked MCP requests can survive every turn boundary. Keep
				// this run unknown instead of pretending a later turn resolves
				// requests whose identities could not be retained.
				observer.threadOverflow = true
			} else {
				observer.overflow = true
			}
			observer.record(ActivityUnknown, observer.current.TurnID)
			return
		}
		observer.requests[id] = activityRequest{kind: kind, turnID: turnID}
		observer.advance(observer.waitingState(), observer.current.TurnID, !existed)
	}
}
