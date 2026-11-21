package hostinstall

import "time"

// ImageReload records the exact configuration change approved by the default
// image installer. Desktop clients can follow it without adopting other edits.
type ImageReload struct {
	HostID      string    `json:"hostId"`
	Previous    string    `json:"previousFingerprint"`
	Fingerprint string    `json:"fingerprint"`
	ImageID     string    `json:"imageId"`
	StartedAt   time.Time `json:"startedAt"`
}

func (r ImageReload) Pending(now time.Time) bool {
	elapsed := now.Sub(r.StartedAt)
	return elapsed >= 0 && elapsed < time.Minute
}
