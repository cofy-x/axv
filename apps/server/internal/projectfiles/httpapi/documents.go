package httpapi

import (
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func (a *api) links(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.DocumentLinks(r.Context(), storage.DocumentLink{EntryID: r.PathValue("id")})
	reply(w, map[string]any{"links": items}, err)
}

func (a *api) origins(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.DocumentOrigins(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"origins": items}, err)
}
func (a *api) addLink(w http.ResponseWriter, r *http.Request) {
	var in storage.DocumentLink
	if !decode(w, r, &in) {
		return
	}
	if in.ID != "" || in.EntryID != "" || in.CreatedAt != "" {
		fail(w, problem("invalid_link", "Link identity is assigned by AXV."))
		return
	}
	l, err := a.store.AddDocumentLink(r.Context(), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"link": l})
}
func (a *api) removeLink(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]bool{"ok": true}, a.store.DeleteDocumentLink(r.Context(), r.PathValue("id")))
}
func (a *api) related(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "projectId", "hostId", "machineId", "conversationId"); err != nil {
		fail(w, err)
		return
	}
	q := r.URL.Query()
	f := storage.DocumentLink{ProjectID: q.Get("projectId"), HostID: q.Get("hostId"), MachineID: q.Get("machineId"), ConversationID: q.Get("conversationId")}
	count := 0
	if f.ProjectID != "" {
		count++
	}
	if f.ConversationID != "" {
		count++
	}
	if f.HostID != "" || f.MachineID != "" {
		if f.HostID == "" || f.MachineID == "" {
			fail(w, problem("invalid_link", "Machine lookup requires its original host identity."))
			return
		}
		count++
	}
	if count != 1 {
		fail(w, problem("invalid_link", "Choose one project, conversation or host and machine."))
		return
	}
	items, err := a.store.DocumentLinks(r.Context(), f)
	reply(w, map[string]any{"links": items}, err)
}
func (a *api) snapshots(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Snapshots(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"snapshots": items}, err)
}
func (a *api) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := a.store.CreateSnapshot(r.Context(), r.PathValue("id"), in.Revision)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"snapshot": d})
}
func (a *api) snapshot(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Snapshot(r.Context(), r.PathValue("id"))
	reply(w, map[string]any{"snapshot": d}, err)
}
