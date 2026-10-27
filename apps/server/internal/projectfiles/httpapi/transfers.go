package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/projectfiles"
)

func receipt(w http.ResponseWriter, result json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write(result)
}

func (a *api) importFile(w http.ResponseWriter, r *http.Request) {
	var in projectfiles.ImportRequest
	if !decode(w, r, &in) {
		return
	}
	result, err := a.service.Import(r.Context(), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	receipt(w, result)
}

func (a *api) exportSnapshot(w http.ResponseWriter, r *http.Request) {
	var in projectfiles.ExportRequest
	if !decode(w, r, &in) {
		return
	}
	result, err := a.service.Export(r.Context(), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	receipt(w, result)
}
