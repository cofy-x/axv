package runner

const (
	activityEventsFile            = "activity-events.json"
	activityEventsUnavailableFile = "activity-events-unavailable"
	maxActivityEvents             = 512
	maxActivityEventsBytes        = 512 << 10
)

// ActivityEvents retains sanitized observations of one exact run, independently
// of desktop lifetime. Next is the highest retained sequence, not the next ID.
type ActivityEvents struct {
	First       uint64
	Next        uint64
	Events      []Activity
	Latest      Activity
	Gap         bool
	Unavailable bool
}

type activityEventJournal struct {
	Version  int        `json:"version"`
	RunID    string     `json:"runId"`
	ThreadID string     `json:"threadId"`
	First    uint64     `json:"first"`
	Last     uint64     `json:"last"`
	Events   []Activity `json:"events"`
}
