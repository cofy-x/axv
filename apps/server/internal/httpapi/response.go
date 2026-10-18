package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"

	"github.com/cofy-x/axv/apps/server/internal/storage"
)

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]string{"error": message, "code": code})
}

func fail(w http.ResponseWriter, err error) {
	var e *storage.Error
	if errors.As(err, &e) {
		if status := storageErrorStatus(e.Code); status != http.StatusInternalServerError {
			writeError(w, status, e.Code, e.Message)
			return
		}
	}
	log.Printf("request failed: %v", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be saved. Please try again.")
}

// storageErrorStatus keeps protocol choices at the HTTP boundary. Unknown errors
// remain internal failures until their public response has been defined here.
func storageErrorStatus(code string) int {
	switch code {
	case "invalid_name", "invalid_parent", "invalid_kind", "invalid_content",
		"folder_cycle", "not_editable", "invalid_text", "invalid_wallpaper",
		"invalid_client", "invalid_layout":
		return http.StatusBadRequest
	case "not_found":
		return http.StatusNotFound
	case "name_conflict", "unavailable", "revision_conflict", "system_entry", "not_trashed":
		return http.StatusConflict
	case "text_too_large", "file_too_large":
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	write(w, http.StatusOK, value)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Use application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "Send one JSON object.")
		return false
	}
	return true
}
