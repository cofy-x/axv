// Package notificationevent defines content-free work observations shared by
// independent execution owners and notification consumers.
package notificationevent

import "time"

type Target struct {
	Kind      string `json:"kind"`
	StoreID   string `json:"storeId"`
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	EventID   string `json:"eventId,omitempty"`
	HostID    string `json:"hostId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	AttemptID string `json:"attemptId,omitempty"`
}

type Event struct {
	ID         string    `json:"id"`
	Sequence   uint64    `json:"sequence"`
	ObservedAt time.Time `json:"observedAt"`
	State      string    `json:"state"`
	Target     Target    `json:"target"`
}

// Next is the inclusive last consumed sequence. Gaps never imply an outcome.
type Batch struct {
	SourceID    string  `json:"sourceId"`
	StoreID     string  `json:"storeId"`
	First       uint64  `json:"first"`
	Next        uint64  `json:"next"`
	Events      []Event `json:"events"`
	Gap         bool    `json:"gap"`
	Unavailable bool    `json:"unavailable"`
}

type Resolution struct {
	Available bool   `json:"available"`
	Current   bool   `json:"current"`
	State     string `json:"state"`
	Target    Target `json:"target"`
}
