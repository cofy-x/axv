package notifications

import (
	"mime"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
)

// Push subscriptions belong to the authenticated browser origin. Pocket Alert
// account configuration retains its separate machine-local authorization.
func (m *Manager) servePush(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method == http.MethodGet && path == "" {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			failure(w, ErrInput)
			return
		}
		writeJSON(w, http.StatusOK, m.PushSnapshot())
		return
	}
	if r.Method != http.MethodPost {
		failure(w, ErrMissing)
		return
	}
	if !httpaccess.AllowRequest(r, true) || path == "/rotate" && !httpaccess.LocalRequest(r, true) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden", "error": "Owner access and the exact AXV origin are required. Key rotation is machine-local."})
		return
	}
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || r.URL.ForceQuery || mediaErr != nil || media != "application/json" {
		failure(w, ErrInput)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	// AllowRequest has checked the sole Origin against local authority or the
	// trusted remote transport grant. Forwarded headers never select this value.
	origin := r.Header.Get("Origin")
	var snapshot PushSnapshot
	var err error
	switch path {
	case "/prepare", "/rotate":
		var input PushPrepare
		if err = decodeStrict(r.Body, &input); err == nil {
			if path == "/prepare" {
				snapshot, err = m.PreparePush(input)
			} else {
				snapshot, err = m.RotatePush(input)
			}
		}
	case "/subscribe":
		var input PushSubscribe
		if err = decodeStrict(r.Body, &input); err == nil {
			snapshot, err = m.SubscribePush(input, origin)
		}
	case "/test", "/disable", "/unsubscribe":
		var input PushSubscriptionIdentity
		if err = decodeStrict(r.Body, &input); err == nil {
			switch path {
			case "/test":
				snapshot, err = m.TestPush(input, origin)
			case "/disable":
				snapshot, err = m.DisablePush(input, origin)
			case "/unsubscribe":
				snapshot, err = m.UnsubscribePush(input, origin)
			}
		}
	default:
		err = ErrMissing
	}
	if err != nil {
		failure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
