package guest

import (
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/guest/protocol"
)

func (s *service) projectTaskRequest(w http.ResponseWriter, r *http.Request, action string) {
	if s.tasks == nil {
		reason := "Project tasks require a compatible image, durable guest state and delegated cgroup v2 controllers."
		if s.taskError != nil {
			reason = s.taskError.Error()
		}
		projectFailure(w, projectProblem(501, "unsupported", reason))
		return
	}
	var result any
	var err error
	switch action {
	case "submit":
		var in protocol.TaskSubmit
		if err = decodeProjectJSON(w, r, &in, 256<<10, false); err == nil {
			result, err = s.tasks.submit(in)
		}
	case "inspect", "cancel":
		var in protocol.TaskIdentity
		if err = decodeProjectFile(w, r, &in, false); err == nil {
			if action == "cancel" {
				result, err = s.tasks.cancel(in)
			} else {
				s.tasks.mu.Lock()
				var record *guestTaskRecord
				if record, err = s.tasks.inspectLocked(in); err == nil {
					result = copyTaskExecution(record)
				}
				s.tasks.mu.Unlock()
			}
		}
	case "logs":
		var in protocol.TaskLogRequest
		if err = decodeProjectFile(w, r, &in, false); err == nil {
			result, err = s.tasks.logs(in)
		}
	}
	if err != nil {
		projectFailure(w, err)
		return
	}
	respond(w, 200, result)
}
