package httpapi

import (
	"log"
	"mime"
	"net/http"
	"strconv"
)

func (a *api) projects(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Projects(r.Context())
	reply(w, map[string]any{"projects": items}, err)
}

func (a *api) registerProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		RootPath string `json:"rootPath"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := a.service.Register(r.Context(), in.Name, in.RootPath)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 201, map[string]any{"project": p})
}

func (a *api) files(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "path", "cursor", "limit"); err != nil {
		fail(w, err)
		return
	}
	q := r.URL.Query()
	limit := 100
	if q.Has("limit") {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			limit = 0
		}
	}
	result, err := a.service.List(r.Context(), r.PathValue("id"), q.Get("path"), q.Get("cursor"), limit)
	reply(w, result, err)
}

func (a *api) file(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "path"); err != nil {
		fail(w, err)
		return
	}
	result, err := a.service.Read(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	reply(w, result, err)
}

func (a *api) download(w http.ResponseWriter, r *http.Request) {
	if err := query(r, "path"); err != nil {
		fail(w, err)
		return
	}
	name, data, err := a.service.Download(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err = w.Write(data); err != nil {
		log.Printf("project download response: %v", err)
	}
}
