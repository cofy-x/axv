//go:build !darwin && !linux

package runner

func writeActivity(string, Activity) error { return ErrActivityUnavailable }
func markActivityUnavailable(string)       {}
func ReadActivity(string, string, string) (Activity, error) {
	return Activity{}, ErrActivityUnavailable
}
