package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/compute/control"
)

func registerImages(mux *http.ServeMux, service *control.Service) {
	mux.HandleFunc("GET /v1/images", func(w http.ResponseWriter, r *http.Request) {
		images, err := service.Images(r.Context())
		reply(w, map[string]any{"images": images}, err)
	})
	mux.HandleFunc("POST /v1/images", func(w http.ResponseWriter, r *http.Request) {
		var req compute.ImageRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		result, err := service.CreateImage(r.Context(), req, "")
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, result)
	})
	mux.HandleFunc("POST /v1/images/import", func(w http.ResponseWriter, r *http.Request) { importImage(service, w, r) })
	mux.HandleFunc("POST /v1/images/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RequestID string `json:"requestId"`
			Revision  int64  `json:"revision"`
		}
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		op, err := service.DeleteImage(r.Context(), r.PathValue("id"), req.RequestID, req.Revision)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, http.StatusAccepted, op)
	})
}

func importImage(service *control.Service, w http.ResponseWriter, r *http.Request) {
	if err := service.RequireImageImport(); err != nil {
		failure(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, compute.MaxImageBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		failure(w, compute.Problem("invalid_request", "Send image metadata and one file as multipart form data."))
		return
	}
	metadata, err := reader.NextPart()
	if err != nil || metadata.FormName() != "metadata" {
		failure(w, compute.Problem("invalid_request", "The first part must contain image metadata."))
		return
	}
	var req compute.ImageRequest
	decoder := json.NewDecoder(io.LimitReader(metadata, 64<<10))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&req); err != nil {
		failure(w, compute.Problem("invalid_request", "Invalid image metadata."))
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		failure(w, compute.Problem("invalid_request", "Expected one image metadata object."))
		return
	}
	metadata.Close()
	if err = compute.ValidateImageRequest(req, true); err != nil {
		failure(w, err)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" {
		failure(w, compute.Problem("invalid_request", "Select one Linux cloud image file."))
		return
	}
	defer part.Close()
	f, err := service.NewUpload()
	if err != nil {
		failure(w, err)
		return
	}
	defer f.Close()
	submitted := false
	defer func() {
		if !submitted {
			_ = service.RemoveUpload(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, compute.MaxImageBytes+1))
	if err != nil {
		failure(w, compute.Problem("invalid_request", "Image upload was interrupted or storage is full."))
		return
	}
	if n > compute.MaxImageBytes {
		failure(w, compute.Problem("invalid_request", "Image upload exceeds the 8 GiB limit."))
		return
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(req.SHA256) {
		failure(w, compute.Problem("invalid_request", "Image SHA-256 does not match the uploaded file."))
		return
	}
	if _, err = reader.NextPart(); err != io.EOF {
		failure(w, compute.Problem("invalid_request", "Upload exactly one image file."))
		return
	}
	if err = f.Sync(); err != nil {
		failure(w, err)
		return
	}
	if err = f.Close(); err != nil {
		failure(w, err)
		return
	}
	// The catalog takes durable ownership before a worker can move the upload.
	submitted = true
	result, err := service.CreateImage(r.Context(), req, f.Name())
	_ = service.RemoveUnusedUpload(r.Context(), f.Name())
	if err != nil {
		failure(w, err)
		return
	}
	write(w, http.StatusAccepted, result)
}
