//go:build !darwin && !linux

package runner

func appendActivityEvent(string, Activity) error { return ErrActivityUnavailable }
func markActivityEventsUnavailable(string)       {}

func ReadActivityEvents(string, string, string, uint64) (ActivityEvents, error) {
	return ActivityEvents{}, ErrActivityUnavailable
}
