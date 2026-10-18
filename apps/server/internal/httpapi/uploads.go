package httpapi

import (
	"io"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func (a *API) importFiles(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20+64<<10)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Use multipart/form-data with files and parentId fields.")
		return
	}
	parentID := "documents"
	items := []storage.ImportFile{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_upload", "The upload could not be read.")
			return
		}
		if part.FileName() == "" {
			data, err := io.ReadAll(io.LimitReader(part, 1025))
			part.Close()
			if err != nil || len(data) > 1024 {
				writeError(w, http.StatusBadRequest, "invalid_upload", "Invalid upload field.")
				return
			}
			if part.FormName() == "parentId" {
				parentID = string(data)
			}
			continue
		}
		if len(items) >= 10 {
			part.Close()
			writeError(w, http.StatusRequestEntityTooLarge, "too_many_files", "Import up to 10 files at a time.")
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, storage.MaxFileSize+1))
		name := part.FileName()
		part.Close()
		if err != nil {
			fail(w, err)
			return
		}
		if len(data) > storage.MaxFileSize {
			writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "Files must be no larger than 10 MiB.")
			return
		}
		items = append(items, storage.ImportFile{Name: name, Data: data})
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "empty_upload", "Choose at least one file to import.")
		return
	}
	entries, err := a.store.ImportMany(r.Context(), parentID, items)
	if err != nil {
		fail(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"entries": entries})
}
