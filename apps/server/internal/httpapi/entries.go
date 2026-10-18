package httpapi

import (
	"bytes"
	"mime"
	"net/http"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in storage.CreateInput
	if !decode(w, r, &in) {
		return
	}
	e, err := a.store.Create(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"entry": e})
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	e, content, err := a.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	value := map[string]any{"entry": e}
	if e.Editable {
		value["content"] = string(content)
	}
	write(w, http.StatusOK, value)
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	var in storage.UpdateInput
	if !decode(w, r, &in) {
		return
	}
	e, err := a.store.Update(r.Context(), r.PathValue("id"), in)
	respond(w, map[string]any{"entry": e}, err)
}

func (a *API) duplicate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ParentID string `json:"parentId"`
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
	}
	e, err := a.store.Duplicate(r.Context(), r.PathValue("id"), in.ParentID)
	respond(w, map[string]any{"entry": e}, err)
}

func (a *API) content(w http.ResponseWriter, r *http.Request) {
	e, content, err := a.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if e.Kind != "file" {
		writeError(w, http.StatusBadRequest, "not_file", "Folders have no downloadable content.")
		return
	}
	disposition := "attachment"
	if e.Previewable && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", e.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": e.Name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	modified, _ := time.Parse(time.RFC3339Nano, e.UpdatedAt)
	http.ServeContent(w, r, e.Name, modified, bytes.NewReader(content))
}
