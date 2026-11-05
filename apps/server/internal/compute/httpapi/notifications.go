package httpapi

import (
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
	"github.com/cofy-x/axv/apps/server/internal/notificationevent"
)

func registerNotifications(mux *http.ServeMux, service *control.Service) {
	bound := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-AXV-Host-ID") != service.Runtime().HostID {
				failure(w, compute.Problem("binding_changed", "The original AXV host identity is required for task notifications."))
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			handler(w, r)
		}
	}
	mux.HandleFunc("GET /v1/task-events", bound(func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) > 1 || (len(query) == 1 && len(query["after"]) != 1) {
			failure(w, compute.Problem("invalid_request", "Task events accept only one after cursor."))
			return
		}
		var after uint64
		if len(query) != 0 {
			after, err = strconv.ParseUint(query.Get("after"), 10, 63)
		}
		if err != nil || after > math.MaxInt64 {
			failure(w, compute.Problem("invalid_request", "Task events require a nonnegative integer after cursor."))
			return
		}
		result, err := service.NotificationEvents(r.Context(), after, r.Header.Get("X-AXV-Host-ID"))
		reply(w, result, err)
	}))
	mux.HandleFunc("GET /v1/projects/{id}/tasks/{task}/attempts/{attempt}/status", bound(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			failure(w, compute.Problem("invalid_request", "Task notification status does not accept query parameters."))
			return
		}
		hostID := r.Header.Get("X-AXV-Host-ID")
		target := notificationevent.Target{Kind: "task", StoreID: hostID, HostID: hostID, ProjectID: r.PathValue("id"), TaskID: r.PathValue("task"), AttemptID: r.PathValue("attempt")}
		result, err := service.ResolveNotification(r.Context(), target)
		reply(w, result, err)
	}))
}
